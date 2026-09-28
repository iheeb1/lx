package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/runner"
	"github.com/iheeb1/lx/internal/tee"
	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
)

const (
	defaultHeartbeat  = 30 * time.Second
	defaultPromptIdle = 2 * time.Second

	heartbeatTail = 40

	errScanWindow = 1 << 20
)

type liveRun struct {
	argv   []string
	cwd    string
	home   string
	opts   runOpts
	start  time.Time
	stdout io.Writer
	stderr io.Writer

	spool     *tee.Spool
	hbPrinted bool

	partialShown  bool
	partialRaw    string
	partialSig    string
	partialFilter string
	partialOut    int
	partialChars  int
}

func newLiveRun(argv []string, cwd, home string, o runOpts) *liveRun {
	return &liveRun{argv: argv, cwd: cwd, home: home, opts: o, start: time.Now(), stdout: os.Stdout, stderr: os.Stderr}
}

func (l *liveRun) options(stdin io.Reader) runner.Options {
	return runner.Options{
		Stdin:       stdin,
		Heartbeat:   envDuration("LX_HEARTBEAT", defaultHeartbeat),
		OnHeartbeat: l.heartbeat,
		PromptIdle:  envDuration("LX_PROMPT_IDLE", defaultPromptIdle),
		OnPrompt:    l.prompt,
		OnInterrupt: l.interrupt,
	}
}

func (l *liveRun) reserve(sofar []byte) *tee.Spool {
	if l.spool != nil {
		return nil
	}
	sp, err := tee.Reserve(tee.Meta{Argv: l.argv, Cwd: l.cwd, Time: l.start})
	if err != nil {
		return nil
	}
	if len(sofar) > 0 {
		_, _ = sp.Write(sofar)
	}
	l.spool = sp
	return sp
}

func (l *liveRun) id() string {
	if l.spool == nil {
		return ""
	}
	return strconv.Itoa(l.spool.ID())
}

func (l *liveRun) heartbeat(elapsed time.Duration, sofar []byte) io.Writer {
	sp := l.reserve(sofar)
	var b strings.Builder
	fmt.Fprintf(&b, "[lx: still running after %s · %s so far", fmtElapsed(elapsed), plural(countLines(sofar), "line", "lines"))
	if n, windowed := countErrorLines(sofar); n > 0 {
		fmt.Fprintf(&b, " (%s", plural(n, "error line", "error lines"))
		if windowed {
			b.WriteString(" in the last 1 MB")
		}
		b.WriteString(")")
	}
	b.WriteString(" · this is not the result")
	if id := l.id(); id != "" {
		fmt.Fprintf(&b, " · output so far: lx show %s --tail %d", id, heartbeatTail)
	}
	b.WriteString("]\n")
	io.WriteString(l.stderr, b.String())
	l.hbPrinted = true
	if sp == nil {
		return nil
	}
	return sp
}

func (l *liveRun) prompt(line string) {
	msg := "[lx: the command may be waiting for input: " + strconv.Quote(line)
	if !stdinIsTerminal() {
		msg += " — nothing is answering it"
	}
	io.WriteString(l.stderr, msg+"]\n")
}

func (l *liveRun) interrupt(sig os.Signal, elapsed time.Duration, sofar []byte) io.Writer {
	sp := l.reserve(sofar)
	name := runner.SignalName(sig)
	raw := string(sofar)
	c := &engine.Context{Argv: l.argv, Cwd: l.cwd, Home: l.home, Exit: 128 + runner.SignalNumber(sig)}
	pr := processView(c, raw, l.partialOptions())

	var b strings.Builder
	if pr.Output != "" {
		b.WriteString(pr.Output)
		if !strings.HasSuffix(pr.Output, "\n") {
			b.WriteByte('\n')
		}
	}
	b.WriteString(interruptedLine(name, elapsed, countLines(sofar), l.id()))
	io.WriteString(l.stdout, b.String())
	l.partialShown, l.partialRaw, l.partialSig, l.partialFilter = true, raw, name, pr.Filter
	l.partialOut, l.partialChars = tokens.Count(b.String()), b.Len()
	if sp == nil {
		return nil
	}
	return sp
}

func interruptedLine(sig string, elapsed time.Duration, lines int, id string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[lx: interrupted by %s after %s; the command is still stopping", sig, fmtElapsed(elapsed))
	if lines > 0 {
		fmt.Fprintf(&b, " · partial view of %s", plural(lines, "line", "lines"))
	} else {
		b.WriteString(" · no output so far")
	}
	if id != "" {
		b.WriteString(" · output so far: lx show " + id)
	}
	b.WriteString("]\n")
	return b.String()
}

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if dn, err := os.Stat(os.DevNull); err == nil && os.SameFile(fi, dn) {
		return false
	}
	return true
}

func envDuration(name string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	switch strings.ToLower(v) {
	case "":
		return def
	case "0", "off", "false", "no":
		return 0
	}
	if d, err := time.ParseDuration(v); err == nil {
		return max(d, 0)
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil && n >= 0 && n < 1e6 {
		return time.Duration(n * float64(time.Second))
	}
	return def
}

func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}

func countErrorLines(b []byte) (n int, windowed bool) {
	if len(b) > errScanWindow {
		b = b[len(b)-errScanWindow:]
		if i := bytes.IndexByte(b, '\n'); i >= 0 {
			b = b[i+1:]
		}
		windowed = true
	}
	for _, ln := range strings.Split(textutil.Clean(string(b)), "\n") {
		if engine.Classify(ln) == engine.Err {
			n++
		}
	}
	return n, windowed
}

func fmtElapsed(d time.Duration) string {
	if d < 10*time.Second {
		return strconv.FormatFloat(max(d, 0).Seconds(), 'f', 1, 64) + "s"
	}
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d/time.Second)) + "s"
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
	}
	d = d.Round(time.Minute)
	return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return groupDigits(n) + " " + many
}

func groupDigits(n int) string {
	s := strconv.Itoa(n)
	if n < 1000 {
		return s
	}
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	return b.String()
}
