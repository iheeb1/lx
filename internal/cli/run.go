package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/runner"
	"github.com/iheeb1/lx/internal/tee"
	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
	"github.com/iheeb1/lx/internal/track"
)

type runOpts struct {
	raw     bool
	budget  int
	verbose bool
}

func parseRunFlags(args []string) (runOpts, []string, error) {
	o := runOpts{raw: os.Getenv("LX_RAW") == "1" || os.Getenv("LX_OFF") == "1"}
	if b, err := strconv.Atoi(os.Getenv("LX_BUDGET")); err == nil && b > 0 {
		o.budget = b
	}
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return o, args[i+1:], nil
		case a == "-r" || a == "--raw":
			o.raw = true
		case a == "-v" || a == "--verbose":
			o.verbose = true
		case a == "-b" || a == "--budget":
			if i+1 >= len(args) {
				return o, nil, errors.New("--budget needs a value")
			}
			i++
			b, err := strconv.Atoi(args[i])
			if err != nil || b <= 0 {
				return o, nil, fmt.Errorf("bad --budget %q", args[i])
			}
			o.budget = b
		case strings.HasPrefix(a, "--budget="):
			b, err := strconv.Atoi(strings.TrimPrefix(a, "--budget="))
			if err != nil || b <= 0 {
				return o, nil, fmt.Errorf("bad %s", a)
			}
			o.budget = b
		case strings.HasPrefix(a, "-") && len(a) > 1:
			return o, nil, fmt.Errorf("unknown lx flag %s (put lx flags before the command)", a)
		default:
			return o, args[i:], nil
		}
	}
	return o, nil, nil
}

// engineOptions are the engine options for every view of this run (the
// final view and a partial one after a signal): the token budget and the
// host's output cap (hostcap.go).
func (o runOpts) engineOptions() engine.Options {
	return engine.Options{Budget: o.budget, MaxChars: hostCharCap()}
}

func cmdRun(args []string) int {
	o, argv, err := parseRunFlags(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "lx:", err)
		return 2
	}
	if len(argv) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	c := &engine.Context{Argv: argv, Cwd: cwd, Home: home}
	if o.raw || ShouldStream(argv) || engine.MachineReadableAny(c) || filterStreams(c) {
		return runner.Passthrough(argv)
	}

	l := newLiveRun(argv, cwd, home, o)
	res := runner.RunWith(argv, l.options(os.Stdin))
	if res.NotFound {
		fmt.Fprintln(os.Stderr, res.Output)
		return res.ExitCode
	}
	c.Exit = res.ExitCode
	if l.partialShown {
		// The agent already has a partial view: finish quickly (whatever
		// stopped the command may stop lx next).
		out := l.finishAfterPartial(c, res)
		_ = track.Add(track.Record{
			Cmd: cmdKey(argv), Filter: l.partialFilter, Raw: tokens.Count(res.Output), Out: out,
			Ms: res.Duration.Milliseconds(), Exit: res.ExitCode, Lossy: true,
		})
		return res.ExitCode
	}
	pr := processView(c, res.Output, o.engineOptions())

	switch {
	case pr.Filter == "passthrough":
		l.store(res, pr.Filter, false)
		if !res.Replay(os.Stdout, os.Stderr) {
			writeOut(res.Output)
		}
	case !pr.Lossy:
		l.store(res, pr.Filter, false)
		writeOut(pr.Output)
	default:
		ids := l.store(res, pr.Filter, true)
		receipt := engine.Receipt(pr, ids)
		if self := selfCommand(); self != "lx" {
			receipt = strings.Replace(receipt, "lx show ", self+" show ", 1)
		}
		if res.Interrupted != "" {
			receipt = strings.TrimSuffix(receipt, "]") + " · interrupted by " + res.Interrupted + "]"
		}
		writeOut(pr.Output + "\n" + receipt)
	}
	if o.verbose {
		fmt.Fprintf(os.Stderr, "lx: filter=%s raw=%d out=%d saved=%.1f%% guard=%d %s\n",
			pr.Filter, pr.RawTokens, pr.OutTokens, 100*pr.Saved(), pr.GuardAdded, pr.FilterPanic)
	}
	_ = track.Add(track.Record{
		Cmd: cmdKey(argv), Filter: pr.Filter, Raw: pr.RawTokens, Out: pr.OutTokens,
		Ms: res.Duration.Milliseconds(), Exit: res.ExitCode, Lossy: pr.Lossy,
	})
	return res.ExitCode
}

// process is the view pipeline (tests replace it to reach the recovery in
// processView).
var process = engine.Process

// processView renders a view and survives a bug in lx's own pipeline (only
// filters are guarded inside the engine): on a panic the view is the output
// itself, unfiltered and not lossy, so the agent still reads every line and
// lx still exits with the command's status.
func processView(c *engine.Context, raw string, o engine.Options) (pr engine.Result) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "lx: internal error while condensing (%v); the output follows unfiltered\n", r)
			pr = engine.Result{Output: raw, Filter: "passthrough", FilterPanic: fmt.Sprint(r)}
		}
	}()
	return process(c, raw, o)
}

// store saves the full output and returns its id ("" when nothing was
// stored). A run reserved by the heartbeat or the interrupt is always
// finished, because those lines promised `lx show <id>`; otherwise the run
// is stored only when the view is lossy.
func (l *liveRun) store(res runner.Result, filter string, lossy bool) string {
	m := tee.Meta{Argv: l.argv, Cwd: l.cwd, Exit: res.ExitCode, Filter: filter}
	if l.spool != nil {
		if l.spool.Finish(m, res.Output) != nil {
			return ""
		}
		return strconv.Itoa(l.spool.ID())
	}
	if !lossy {
		return ""
	}
	if n, err := tee.Save(m, res.Output); err == nil {
		return strconv.Itoa(n)
	}
	return ""
}

// liveLineRoom is kept, under the host's output cap, for lx's own lines
// around a view: the interrupted line after the partial view, and the
// header and exit line around the view after it (each well under 200
// bytes, even with 10-digit ids and 7-digit line counts).
const liveLineRoom = 200

// partialOptions size the partial view after a signal: it gets 3/4 of the
// host's output cap, less room for its trailing line, so what the command
// prints while stopping still fits in the same tool result.
func (l *liveRun) partialOptions() engine.Options {
	o := l.opts.engineOptions()
	if o.MaxChars > 0 {
		o.MaxChars = max(o.MaxChars*3/4-liveLineRoom, o.MaxChars/4)
	}
	return o
}

// restOptions size the view of what the command printed after the partial
// view: the agent reads both in one result, so together (with lx's lines)
// they stay within one run's budget and the host's cap. Floors keep the
// end from being cut to nothing under a tiny cap.
func (l *liveRun) restOptions() engine.Options {
	o := l.opts.engineOptions()
	if o.Budget <= 0 {
		o.Budget = engine.DefaultBudget
	}
	o.Budget = max(o.Budget-l.partialOut, 1000)
	if o.MaxChars > 0 {
		o.MaxChars = max(o.MaxChars-l.partialChars-liveLineRoom, o.MaxChars/8)
	}
	return o
}

// finishAfterPartial ends a run whose partial view was already printed: it
// shows only what the command printed after that view (so nothing it said
// while stopping is hidden), then the exit status and where the full
// output is. It returns the tokens lx printed for the whole run.
func (l *liveRun) finishAfterPartial(c *engine.Context, res runner.Result) int {
	ids := l.store(res, l.partialFilter, true)
	var b strings.Builder
	rest, header := res.Output, "[lx: the full view, now that the command has exited:]"
	if strings.HasPrefix(res.Output, l.partialRaw) {
		rest, header = res.Output[len(l.partialRaw):], "[lx: printed after the partial view:]"
	}
	if strings.TrimSpace(textutil.Clean(rest)) != "" {
		rpr := processView(c, rest, l.restOptions())
		if rpr.Lossy {
			// Condensed: say so even when nothing was stored (no receipt).
			header = strings.TrimSuffix(header, ":]") + " (" + groupDigits(countLines([]byte(rest))) +
				"→" + groupDigits(countLines([]byte(rpr.Output))) + " lines):]"
		}
		b.WriteString(header + "\n" + rpr.Output)
		if !strings.HasSuffix(rpr.Output, "\n") {
			b.WriteByte('\n')
		}
	}
	sig := res.Interrupted
	if sig == "" {
		sig = l.partialSig
	}
	b.WriteString(exitedLine(res.ExitCode, sig, ids))
	io.WriteString(l.stdout, b.String())
	return l.partialOut + tokens.Count(b.String())
}

// exitedLine ends a run whose partial view was printed: the command's real
// status and where its full output is (id "" when nothing was stored).
func exitedLine(code int, sig, id string) string {
	s := fmt.Sprintf("[lx: command exited %d after %s", code, sig)
	if id != "" {
		s += " · full output: lx show " + id
	}
	return s + "]\n"
}

// selfCommand is how the agent's shell can run this lx: "lx" when that name
// resolves on PATH, else this binary's absolute path. A hook installed with
// --prefix (lx not on the agent's PATH) would otherwise get receipts whose
// `lx show N` exits 127.
func selfCommand() string {
	if _, err := exec.LookPath("lx"); err == nil {
		return "lx"
	}
	if exe, err := os.Executable(); err == nil && filepath.IsAbs(exe) && !strings.ContainsAny(exe, " \t'\"$`\\") {
		return exe
	}
	return "lx"
}
