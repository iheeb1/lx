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

// Result of a finished command.
type Result struct {
	Output   string
	ExitCode int
	Duration time.Duration
	Elided   int64 // bytes dropped from the middle because of MaxCapture
	NotFound bool

	chunks []chunk
}

// Run executes argv with stdin inherited and stdout+stderr interleaved into a
// single capture, in the order the child wrote them.
func Run(argv []string, stdin io.Reader) Result {
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
	cmd.Stdin = stdin
	buf := &capture{}
	cmd.Stdout = stream{buf, false}
	cmd.Stderr = stream{buf, true}

	// The child shares our process group, so the terminal delivers ^C to it
	// directly. lx must survive long enough to report the child's status.
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(sigs)

	if err := cmd.Start(); err != nil {
		return Result{Output: "lx: " + err.Error(), ExitCode: 126, Duration: time.Since(start)}
	}
	go func() {
		for s := range sigs {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(s)
			}
		}
	}()
	err = cmd.Wait()
	res := Result{
		Output:   buf.String(),
		ExitCode: exitCode(err),
		Duration: time.Since(start),
		Elided:   buf.elided,
	}
	if !buf.noRepl {
		res.chunks = buf.chunks
	}
	return res
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
}

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

func (c *capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.elided == 0 {
		return c.head.String() + string(c.tail)
	}
	return c.head.String() + "\n[lx: output exceeded capture limit; middle elided]\n" + string(c.tail)
}
