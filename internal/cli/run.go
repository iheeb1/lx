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
	"time"

	"github.com/iheeb1/lx/internal/agentctx"
	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/laya"
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

	fit    int
	fitCut engine.Cut

	mode    engine.Mode
	modeSet bool
}

func parseRunFlags(args []string) (runOpts, []string, error) {
	o := runOpts{raw: os.Getenv("LX_RAW") == "1" || os.Getenv("LX_OFF") == "1"}
	if b, err := strconv.Atoi(os.Getenv("LX_BUDGET")); err == nil && b > 0 {
		o.budget = b
	}
	m, err := envMode()
	if err != nil {
		return o, nil, err
	}
	o.mode, o.modeSet = m, os.Getenv("LX_MODE") != ""
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
		case a == "--fit":
			if i+1 >= len(args) {
				return o, nil, errors.New("--fit needs a number of lines")
			}
			i++
			n, cut, ok := engine.ParseFit(args[i])
			if !ok {
				return o, nil, fmt.Errorf("bad --fit %q (a number of lines, at least 1, as in 40, head:40 or tail:40)", args[i])
			}
			o.fit, o.fitCut = n, cut
		case strings.HasPrefix(a, "--fit="):
			n, cut, ok := engine.ParseFit(strings.TrimPrefix(a, "--fit="))
			if !ok {
				return o, nil, fmt.Errorf("bad %s (a number of lines, at least 1, as in 40, head:40 or tail:40)", a)
			}
			o.fit, o.fitCut = n, cut
		case a == "-m" || a == "--mode":
			if i+1 >= len(args) {
				return o, nil, fmt.Errorf("%s needs a mode (%s)", a, engine.ModeList)
			}
			i++
			m, ok := engine.ParseMode(args[i])
			if !ok {
				return o, nil, fmt.Errorf("unknown mode %q for %s (the modes are %s)", args[i], a, engine.ModeList)
			}
			o.mode, o.modeSet = m, true
		case strings.HasPrefix(a, "--mode="):
			m, ok := engine.ParseMode(strings.TrimPrefix(a, "--mode="))
			if !ok {
				return o, nil, fmt.Errorf("bad %s (the modes are %s)", a, engine.ModeList)
			}
			o.mode, o.modeSet = m, true
		case strings.HasPrefix(a, "-") && len(a) > 1:
			return o, nil, fmt.Errorf("unknown lx flag %s (put lx flags before the command)", a)
		default:
			return o, args[i:], nil
		}
	}
	return o, nil, nil
}

func envMode() (engine.Mode, error) {
	v := os.Getenv("LX_MODE")
	if v == "" {
		return engine.ModeAuto, nil
	}
	m, ok := engine.ParseMode(v)
	if !ok {
		return engine.ModeAuto, fmt.Errorf("unknown LX_MODE %q (the modes are %s)", v, engine.ModeList)
	}
	return m, nil
}

func modeStat(pr engine.Result) string {
	if pr.Mode == engine.ModeAuto {
		return ""
	}
	return " mode=" + pr.ViewMode.String()
}

func (o runOpts) engineOptions() engine.Options {
	return engine.Options{Budget: o.budget, MaxChars: hostCharCapFor(exitUnknown), MaxLines: o.fit, Cut: o.fitCut, Mode: o.mode}
}

func fitRoom(fit, used int) int {
	if fit <= 0 {
		return 0
	}
	return max(fit-used, 1)
}

type noticeCounter struct {
	w     io.Writer
	lines int
}

func (c *noticeCounter) Write(p []byte) (int, error) {
	c.lines += strings.Count(string(p), "\n")
	return c.w.Write(p)
}

func (l *liveRun) notices() int {
	if c, ok := l.stderr.(*noticeCounter); ok {
		return c.lines
	}
	return 0
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
	if o.raw {
		tuneRecordRaw(argv, cwd, time.Now())
		return runner.Passthrough(argv)
	}
	if ShouldStream(argv) || engine.MachineReadableAny(c) || filterStreams(c) {
		return runner.Passthrough(argv)
	}

	tn := tuneLookup(argv, cwd, time.Now())

	l := newLiveRun(argv, cwd, home, o)
	if o.fit > 0 {
		l.stderr = &noticeCounter{w: l.stderr}
	}
	res := runner.RunWith(argv, l.options(os.Stdin))
	if res.NotFound {
		fmt.Fprintln(os.Stderr, res.Output)
		return res.ExitCode
	}
	c.Exit = res.ExitCode
	if l.partialShown {
		out := l.finishAfterPartial(c, res)
		_ = track.Add(track.Record{
			Cmd: cmdKey(argv), Filter: l.partialFilter, Raw: tokens.Count(res.Output), Out: out,
			Ms: res.Duration.Milliseconds(), Exit: res.ExitCode, Lossy: true,
		})
		return res.ExitCode
	}
	eo := o.engineOptions()
	eo.MaxChars = hostCharCapFor(res.ExitCode)
	verboseLine := 0
	if o.verbose {
		verboseLine = 1
	}
	eo.MaxLines = fitRoom(eo.MaxLines, l.notices()+verboseLine)
	sess := openSession(argv, cwd, res.Output, o.fit)
	eo = sess.options(c, eo, o.modeSet, tn.level() != track.LevelNormal)
	if tn.level() == track.LevelNormal {
		if eo.Judge, eo.JudgeTimeout = judgeFor(res.Output); eo.Judge != nil {
			eo.Task = sess.task()
		}
	}
	pr := tn.process(c, res.Output, eo)
	if tn.level() == track.LevelNormal && !o.wantsMore() {
		pr = sess.delta(c, res.Output, pr, eo)
	}

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
		if ids == "" && tee.Enabled() {
			if !res.Replay(os.Stdout, os.Stderr) {
				writeOut(res.Output)
			}
			pr.Lossy, pr.OutTokens = false, pr.RawTokens
			break
		}
		receipt := engine.Receipt(pr, ids)
		if self := selfCommand(); self != "lx" {
			receipt = strings.Replace(receipt, "lx show ", self+" show ", 1)
		}
		if res.Interrupted != "" {
			receipt = strings.TrimSuffix(receipt, "]") + " · interrupted by " + res.Interrupted + "]"
		}
		receipt = tn.receipt(sess.receipt(receipt, pr))
		if pr.Output == "" {
			writeOut(receipt)
		} else {
			writeOut(pr.Output + "\n" + receipt)
		}
		tn.recordCondensed(ids, res.ExitCode, time.Now())
	}
	if o.verbose {
		fmt.Fprintf(os.Stderr, "lx: filter=%s raw=%d out=%d saved=%.1f%% guard=%d%s %s\n",
			pr.Filter, pr.RawTokens, pr.OutTokens, 100*pr.Saved(), pr.GuardAdded, modeStat(pr), pr.FilterPanic)
	}
	_ = track.Add(track.Record{
		Cmd: cmdKey(argv), Filter: pr.Filter, Raw: pr.RawTokens, Out: pr.OutTokens,
		Ms: res.Duration.Milliseconds(), Exit: res.ExitCode, Lossy: pr.Lossy,
	})
	return res.ExitCode
}

var process = engine.Process

type session struct {
	snap     *agentctx.Snapshot
	inferred bool
}

func openSession(argv []string, cwd, raw string, fit int) (s session) {
	if os.Getenv("LX_RAW") == "1" || os.Getenv("LX_OFF") == "1" || smallOutput(raw) && (fit <= 0 || strings.Count(raw, "\n") < fit) {
		return session{}
	}
	defer func() {
		if recover() != nil {
			s = session{}
		}
	}()
	return session{snap: agentctx.Current(os.Getenv, cwd, argv)}
}

func smallOutput(raw string) bool {
	return len(raw) <= 8*engine.SmallOutput && tokens.Count(raw) <= engine.SmallOutput
}

func (o runOpts) wantsMore() bool {
	return o.modeSet && (o.mode == engine.ModeError || o.mode == engine.ModeDebug) || o.budget > engine.DefaultBudget
}

// A tuned command only ever shows more: the session may reorder it, never shrink it.
func (s *session) options(c *engine.Context, eo engine.Options, modeSet, tuned bool) engine.Options {
	if s.snap == nil {
		return eo
	}
	eo.Focus = s.snap.Focus()
	if tuned {
		return eo
	}
	eo.Pressure = s.snap.Pressure
	if !modeSet {
		if m := s.snap.InferMode(); m == engine.ModeError || m == engine.ModeVerify && verdict(c) {
			eo.Mode, s.inferred = m, true
		}
	}
	return eo
}

// An inferred verify only shrinks a green run of a command that has a verdict, not a diff or a listing.
func verdict(c *engine.Context) bool {
	if c.Failed() {
		return true
	}
	f, _ := engine.Resolve(c)
	if g, ok := f.(engine.Guarded); ok && g.GuardsErrors() {
		return true
	}
	_, ok := f.(engine.Identities)
	return ok
}

func (s session) delta(c *engine.Context, raw string, pr engine.Result, eo engine.Options) engine.Result {
	if s.snap == nil || pr.FilterPanic != "" {
		return pr
	}
	v, ok := sessionDelta(s.snap, c, raw, pr.Output)
	lines := strings.Count(v, "\n") + 1
	if !ok || v == "" || eo.MaxLines > 0 && lines+1 > eo.MaxLines || eo.MaxChars > 0 && len(v) > eo.MaxChars {
		return pr
	}
	pr.Output, pr.Filter, pr.Lossy, pr.Notes = v, "delta", true, nil
	pr.OutTokens, pr.OutLines = tokens.Count(v), lines
	return pr
}

func (s session) task() string {
	if s.snap == nil {
		return ""
	}
	var parts []string
	if t := strings.TrimRight(strings.TrimSpace(s.snap.Title), "."); t != "" {
		parts = append(parts, t)
	}
	if len(s.snap.Assistant) > 0 {
		if f := firstSentence(s.snap.Assistant[0]); f != "" {
			parts = append(parts, f)
		}
	}
	return strings.Join(parts, ": ")
}

const maxTaskRunes = 200

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	for _, end := range []string{". ", "? ", "! "} {
		if i := strings.Index(s, end); i >= 0 {
			s = s[:i+1]
		}
	}
	if r := []rune(s); len(r) > maxTaskRunes {
		s = string(r[:maxTaskRunes])
	}
	return strings.TrimSpace(s)
}

const layaMinRaw = 2048

var (
	layaAvailable = laya.Available
	layaJudge     = laya.Judge
)

func judgeFor(raw string) (engine.Judge, time.Duration) {
	if layaOff() || len(raw) < layaMinRaw && strings.Count(raw, "\n") < 80 || !layaAvailable() {
		return nil, 0
	}
	return layaClient{}, layaTimeout(os.Getenv("LX_LAYA_TIMEOUT"))
}

func layaOff() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LX_LAYA"))) {
	case "0", "off", "false", "no":
		return true
	}
	return false
}

func layaTimeout(v string) time.Duration {
	if ms, err := strconv.ParseInt(v, 10, 64); err == nil && ms > 0 && ms <= int64((1<<63-1)/time.Millisecond) {
		return time.Duration(ms) * time.Millisecond
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	return engine.DefaultJudgeTimeout
}

type layaClient struct{}

// min_conf at the daemon's floor: the engine applies its per-mode threshold
func (layaClient) Judge(family, task string, items []string, timeout time.Duration) ([]engine.JudgeVerdict, error) {
	req := laya.Request{Family: family, Task: task, Items: make([]laya.Item, len(items)), MinConf: 0.30}
	for i, it := range items {
		req.Items[i].Text = it
	}
	vs, err := layaJudge(req, timeout)
	if err != nil {
		return nil, err
	}
	out := make([]engine.JudgeVerdict, len(vs))
	for i, v := range vs {
		out[i] = engine.JudgeVerdict{Keep: v.Keep, Confidence: v.Confidence}
	}
	return out, nil
}

func (s session) receipt(r string, pr engine.Result) string {
	if s.inferred {
		r = inferredNote(r)
	}
	return engine.WithNotes(r, pr.Notes, receiptRoom)
}

func inferredNote(r string) string {
	i := strings.Index(r, " · mode ")
	if i < 0 {
		return r
	}
	i += len(" · mode ")
	j := strings.IndexAny(r[i:], " ]")
	if j < 0 {
		return r
	}
	return r[:i+j] + " (from your last message)" + r[i+j:]
}

func processView(c *engine.Context, raw string, o engine.Options) (pr engine.Result) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "lx: internal error while condensing (%v); the output follows unfiltered\n", r)
			pr = engine.Result{Output: raw, Filter: "passthrough", FilterPanic: fmt.Sprint(r)}
		}
	}()
	return process(c, raw, o)
}

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

const liveLineRoom = 200

func (l *liveRun) partialOptions() engine.Options {
	o := l.opts.engineOptions()
	if o.MaxChars > 0 {
		o.MaxChars = max(o.MaxChars*3/4-liveLineRoom, o.MaxChars/4)
	}
	o.MaxLines = fitRoom(o.MaxLines, l.notices()+1)
	return o
}

func (l *liveRun) restOptions() engine.Options {
	o := l.opts.engineOptions()
	if o.Budget <= 0 {
		o.Budget = engine.DefaultBudget
	}
	o.Budget = max(o.Budget-l.partialOut, 1000)
	if o.MaxChars > 0 {
		o.MaxChars = max(o.MaxChars-l.partialChars-liveLineRoom, o.MaxChars/8)
	}
	o.MaxLines = fitRoom(o.MaxLines, 2)
	return o
}

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

func exitedLine(code int, sig, id string) string {
	s := fmt.Sprintf("[lx: command exited %d after %s", code, sig)
	if id != "" {
		s += " · full output: lx show " + id
	}
	return s + "]\n"
}

func selfCommand() string {
	if _, err := exec.LookPath("lx"); err == nil {
		return "lx"
	}
	if exe, err := os.Executable(); err == nil && filepath.IsAbs(exe) && !strings.ContainsAny(exe, " \t'\"$`\\") {
		return exe
	}
	return "lx"
}
