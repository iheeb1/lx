// Package runner executes the wrapped command and captures its combined
// output while preserving the exit status exactly.
package runner

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// MaxCapture bounds how much output is held in memory. Past it, lx keeps the
// head, keeps a rolling tail, and reports how much was elided.
const MaxCapture = 64 << 20

const tailKeep = 4 << 20

// DefaultGrace is how long lx waits, after forwarding the first signal, for
// the command to stop before it shows what it has so far.
const DefaultGrace = 2 * time.Second

// Result of a finished command.
type Result struct {
	Output   string
	ExitCode int
	Duration time.Duration
	Elided   int64 // bytes dropped from the middle because of MaxCapture
	NotFound bool
	// Interrupted names the first signal (SIGINT, SIGTERM, SIGHUP) lx
	// received and forwarded while the command ran; "" when there was none.
	Interrupted string

	chunks []chunk
}

// Options tune RunWith. The zero value is Run's behavior: no heartbeat, no
// prompt detection, signals forwarded, a DefaultGrace grace period.
//
// Callbacks run one at a time on a single goroutine (never concurrently with
// each other) and all of them have returned by the time RunWith returns.
type Options struct {
	Stdin io.Reader

	// Heartbeat > 0 calls OnHeartbeat once, that long after the start, if
	// the command is still running. sofar is the capture up to then. A
	// non-nil writer it returns (a spool) receives every byte captured
	// afterwards, in order and with nothing missing; a write error drops
	// the spool silently.
	Heartbeat   time.Duration
	OnHeartbeat func(elapsed time.Duration, sofar []byte) io.Writer

	// PromptIdle > 0 calls OnPrompt at most once, when the command has been
	// silent for PromptIdle and its output ends in an unterminated line that
	// looks like a question to the user ("Proceed? [y/N] ", "Password:").
	PromptIdle time.Duration
	OnPrompt   func(line string)

	// Grace is how long after the first forwarded signal the command may
	// take to exit before OnInterrupt is called with the capture so far
	// (0 = DefaultGrace). lx never kills the command: it keeps waiting, and
	// the Result is the command's real status. OnInterrupt is called at most
	// once; like OnHeartbeat, a non-nil writer it returns becomes the spool
	// for later bytes when no spool is attached yet.
	Grace       time.Duration
	OnInterrupt func(sig os.Signal, elapsed time.Duration, sofar []byte) io.Writer
}

// notifySignals subscribes ch to the signals lx forwards to the command. It
// is a variable so tests can deliver signals without real OS signals.
var notifySignals = func(ch chan<- os.Signal) (stop func()) {
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	return func() { signal.Stop(ch) }
}

// testHookCapture, when set by tests, receives the live capture.
var testHookCapture func(*capture)

// Run executes argv with stdin inherited and stdout+stderr interleaved into a
// single capture, in the order the child wrote them.
func Run(argv []string, stdin io.Reader) Result {
	return RunWith(argv, Options{Stdin: stdin})
}

// RunWith is Run with a heartbeat, prompt detection and a bounded wait after
// a signal (see Options).
func RunWith(argv []string, o Options) Result {
	start := time.Now()
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return Result{
			Output:   "lx: command not found: " + argv[0],
			ExitCode: 127,
			NotFound: true,
		}
	}
	cmd := exec.Command(path, argv[1:]...)
	cmd.Args[0] = argv[0]
	cmd.Stdin = o.Stdin
	buf := &capture{}
	cmd.Stdout = stream{buf, false}
	cmd.Stderr = stream{buf, true}
	if testHookCapture != nil {
		testHookCapture(buf)
	}

	// The child shares our process group, so the terminal delivers ^C to it
	// directly. lx must survive long enough to report the child's status.
	sigs := make(chan os.Signal, 4)
	stop := notifySignals(sigs)
	defer stop()

	if err := cmd.Start(); err != nil {
		return Result{Output: "lx: " + err.Error(), ExitCode: 126, Duration: time.Since(start)}
	}

	var (
		sigMu       sync.Mutex
		interrupted os.Signal
		closed      bool
	)
	exited := make(chan struct{})
	firstSig := make(chan os.Signal, 1)
	go func() {
		for {
			select {
			case s := <-sigs:
				sigMu.Lock()
				first := interrupted == nil && !closed
				if first {
					interrupted = s
				}
				sigMu.Unlock()
				if first {
					firstSig <- s
				}
				_ = cmd.Process.Signal(s)
			case <-exited:
				return
			}
		}
	}()

	mon := monitor{o: o, c: buf, start: start, exited: exited, firstSig: firstSig}
	monDone := make(chan struct{})
	if mon.needed() {
		go func() { mon.run(); close(monDone) }()
	} else {
		close(monDone)
	}

	err = cmd.Wait()
	sigMu.Lock()
	closed = true
	sig := interrupted
	sigMu.Unlock()
	close(exited)
	<-monDone // every callback has returned

	res := Result{
		Output:   buf.String(),
		ExitCode: exitCode(err),
		Duration: time.Since(start),
		Elided:   buf.elided,
	}
	if sig != nil {
		res.Interrupted = SignalName(sig)
	}
	if !buf.noRepl {
		res.chunks = buf.chunks
	}
	return res
}

// monitor fires the heartbeat, the prompt check and the post-signal grace
// timer from one goroutine, so callbacks never run concurrently.
type monitor struct {
	o        Options
	c        *capture
	start    time.Time
	exited   <-chan struct{}
	firstSig <-chan os.Signal
}

func (m *monitor) needed() bool {
	return m.o.Heartbeat > 0 && m.o.OnHeartbeat != nil ||
		m.o.PromptIdle > 0 && m.o.OnPrompt != nil ||
		m.o.OnInterrupt != nil
}

// promptTick is how often the prompt detector looks at the capture.
func promptTick(idle time.Duration) time.Duration {
	return max(min(500*time.Millisecond, idle/4), 10*time.Millisecond)
}

func (m *monitor) run() {
	var hbC, tickC, graceC <-chan time.Time
	if m.o.Heartbeat > 0 && m.o.OnHeartbeat != nil {
		t := time.NewTimer(m.o.Heartbeat)
		defer t.Stop()
		hbC = t.C
	}
	if m.o.PromptIdle > 0 && m.o.OnPrompt != nil {
		t := time.NewTicker(promptTick(m.o.PromptIdle))
		defer t.Stop()
		tickC = t.C
	}
	sigC := m.firstSig
	if m.o.OnInterrupt == nil {
		sigC = nil
	}
	var sig os.Signal
	for {
		select {
		case <-m.exited:
			return
		default:
		}
		select {
		case <-m.exited:
			return
		case <-hbC:
			hbC = nil
			if m.alive() {
				m.tap(func(sofar []byte) io.Writer {
					return m.o.OnHeartbeat(time.Since(m.start), sofar)
				})
			}
		case <-tickC:
			if line, ok := m.c.promptLine(m.o.PromptIdle); ok && m.alive() {
				tickC = nil
				safely(func() { m.o.OnPrompt(line) })
			}
		case sig = <-sigC:
			sigC = nil
			grace := m.o.Grace
			if grace <= 0 {
				grace = DefaultGrace
			}
			t := time.NewTimer(grace)
			defer t.Stop()
			graceC = t.C
		case <-graceC:
			graceC = nil
			if m.alive() {
				m.tap(func(sofar []byte) io.Writer {
					return m.o.OnInterrupt(sig, time.Since(m.start), sofar)
				})
			}
		}
	}
}

// alive reports that the command has not exited yet (a timer that fires as
// the command exits must not report on a finished run).
func (m *monitor) alive() bool {
	select {
	case <-m.exited:
		return false
	default:
		return true
	}
}

// tap snapshots the capture and calls fn with it outside the capture lock,
// so the command never blocks on lx's callback. Bytes that arrive meanwhile
// are held and handed to the writer fn returns, ahead of every later byte.
func (m *monitor) tap(fn func(sofar []byte) io.Writer) {
	c := m.c
	c.mu.Lock()
	sofar := c.snapshotLocked()
	tapping := c.spool == nil
	if tapping {
		c.tapping, c.pending, c.pendingLost = true, nil, false
	}
	c.mu.Unlock()

	var w io.Writer
	safely(func() { w = fn(sofar) })

	if !tapping {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if w != nil {
		ok := len(c.pending) == 0 || spoolWrite(w, c.pending)
		if ok && c.pendingLost {
			ok = spoolWrite(w, []byte("\n[lx: output elided here while lx was saving it]\n"))
		}
		if ok {
			c.spool = w
		}
	}
	c.tapping, c.pending, c.pendingLost = false, nil, false
}

// safely runs a callback so that a bug in it can never take lx down while
// the command is still running: lx must survive to report its exit status.
func safely(fn func()) {
	defer func() { _ = recover() }()
	fn()
}

// spoolWrite writes p to a spool, reporting false on any failure (a panic
// included: the spool is best effort, capturing the command is not).
func spoolWrite(w io.Writer, p []byte) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	n, err := w.Write(p)
	return err == nil && n == len(p)
}

// SignalName is the conventional name of a forwarded signal ("SIGTERM").
func SignalName(s os.Signal) string {
	switch s {
	case os.Interrupt:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGHUP:
		return "SIGHUP"
	}
	return s.String()
}

// SignalNumber is s's number (the N in exit status 128+N), or 0.
func SignalNumber(s os.Signal) int {
	if n, ok := s.(syscall.Signal); ok {
		return int(n)
	}
	return 0
}

// Replay writes the captured output to stdout/stderr exactly as the child
// wrote it, preserving which stream each byte went to. It reports false when
// the output was too large to keep per-stream (callers then print Output).
func (r Result) Replay(stdout, stderr io.Writer) bool {
	if r.chunks == nil && r.Output != "" {
		return false
	}
	for _, ch := range r.chunks {
		w := stdout
		if ch.stderr {
			w = stderr
		}
		if _, err := w.Write(ch.data); err != nil {
			return true
		}
	}
	return true
}

// Passthrough runs argv wired straight to the terminal, used for raw mode and
// for commands lx must not buffer (watchers, servers, interactive tools).
func Passthrough(argv []string) int {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		os.Stderr.WriteString("lx: command not found: " + argv[0] + "\n")
		return 127
	}
	cmd := exec.Command(path, argv[1:]...)
	cmd.Args[0] = argv[0]
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)
	if err := cmd.Start(); err != nil {
		os.Stderr.WriteString("lx: " + err.Error() + "\n")
		return 126
	}
	go func() {
		for s := range sigs {
			_ = cmd.Process.Signal(s)
		}
	}()
	return exitCode(cmd.Wait())
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 1
}

// capture is shared by stdout and stderr. It records the interleaved byte
// stream (for filtering) and, while small, the per-stream chunks in order so
// unfiltered output can be replayed to the right file descriptors.
type capture struct {
	mu     sync.Mutex
	head   bytes.Buffer
	tail   []byte
	elided int64
	chunks []chunk
	noRepl bool // chunks abandoned (too large to be worth replaying)

	total int64     // bytes written so far
	last  time.Time // when the last bytes arrived

	spool       io.Writer // receives every byte once attached (heartbeat)
	tapping     bool      // a callback is choosing a spool: hold new bytes
	pending     []byte
	pendingLost bool
}

// pendingMax bounds the bytes held while a callback runs.
const pendingMax = 32 << 20

type chunk struct {
	stderr bool
	data   []byte
}

const replayLimit = 1 << 20

type stream struct {
	c      *capture
	stderr bool
}

func (w stream) Write(p []byte) (int, error) { return w.c.write(p, w.stderr) }

func (c *capture) write(p []byte, stderr bool) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	c.total += int64(n)
	c.last = time.Now()
	if c.spool != nil {
		if !spoolWrite(c.spool, p) {
			c.spool = nil // the spool is best effort; the capture is not
		}
	} else if c.tapping {
		if len(c.pending)+len(p) <= pendingMax {
			c.pending = append(c.pending, p...)
		} else {
			c.pendingLost = true
		}
	}
	if !c.noRepl {
		if c.head.Len()+len(c.tail)+len(p) > replayLimit {
			c.noRepl, c.chunks = true, nil
		} else if k := len(c.chunks); k > 0 && c.chunks[k-1].stderr == stderr {
			c.chunks[k-1].data = append(c.chunks[k-1].data, p...)
		} else {
			c.chunks = append(c.chunks, chunk{stderr, append([]byte(nil), p...)})
		}
	}
	if room := MaxCapture - tailKeep - c.head.Len(); room > 0 {
		k := min(room, len(p))
		c.head.Write(p[:k])
		p = p[k:]
	}
	if len(p) > 0 {
		c.tail = append(c.tail, p...)
		if over := len(c.tail) - tailKeep; over > 0 {
			c.elided += int64(over)
			c.tail = append(c.tail[:0], c.tail[over:]...)
		}
	}
	return n, nil
}

const elisionMarker = "\n[lx: output exceeded capture limit; middle elided]\n"

func (c *capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.snapshotLocked())
}

// snapshotLocked copies the capture as String renders it. c.mu must be held.
func (c *capture) snapshotLocked() []byte {
	out := make([]byte, 0, c.head.Len()+len(elisionMarker)+len(c.tail))
	out = append(out, c.head.Bytes()...)
	if c.elided > 0 {
		out = append(out, elisionMarker...)
	}
	return append(out, c.tail...)
}

// lastBytes returns up to n of the most recently captured bytes.
func (c *capture) lastBytesLocked(n int) []byte {
	if len(c.tail) >= n {
		return c.tail[len(c.tail)-n:]
	}
	h := c.head.Bytes()
	k := min(n-len(c.tail), len(h))
	out := make([]byte, 0, k+len(c.tail))
	out = append(out, h[len(h)-k:]...)
	return append(out, c.tail...)
}

// promptLine reports the capture's unterminated last line when the command
// has been silent for idle and that line looks like a prompt.
func (c *capture) promptLine(idle time.Duration) (string, bool) {
	c.mu.Lock()
	if c.total == 0 || time.Since(c.last) < idle {
		c.mu.Unlock()
		return "", false
	}
	tail := string(c.lastBytesLocked(promptWindow))
	whole := c.total <= promptWindow
	c.mu.Unlock()
	return promptLine(tail, whole)
}
