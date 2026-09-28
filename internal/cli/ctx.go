package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/agentctx"
	"github.com/iheeb1/lx/internal/hook"
)

const (
	ctxUsage   = "lx ctx [--json]"
	ctxMaxRuns = 10
)

var ctxRetries, ctxPause = 8, 25 * time.Millisecond

func cmdCtx(args []string) int {
	cwd, _ := os.Getwd()
	return runCtx(args, os.Stdout, os.Stderr, os.Getenv, cwd, time.Now())
}

type ctxTerm struct {
	Text   string  `json:"text"`
	Weight float64 `json:"weight"`
}

type ctxFile struct {
	Path     string `json:"path"`
	Op       string `json:"op"`
	TurnsAgo int    `json:"turns_ago"`
}

type ctxRun struct {
	Command   string `json:"command"`
	LxID      int    `json:"lx_id,omitempty"`
	TurnsAgo  int    `json:"turns_ago"`
	InContext bool   `json:"in_context"`
	Failed    bool   `json:"failed,omitempty"`
}

type ctxView struct {
	Agent       string     `json:"agent"`
	Session     string     `json:"session"`
	Subagent    string     `json:"subagent,omitempty"`
	Caller      bool       `json:"caller"`
	Transcript  string     `json:"transcript"`
	BytesRead   int        `json:"bytes_read"`
	TurnsRead   int        `json:"turns_read"`
	Model       string     `json:"model,omitempty"`
	Used        int        `json:"context_used"`
	Window      int        `json:"context_window"`
	Percent     float64    `json:"context_pct"`
	WindowFrom  string     `json:"window_from"`
	Compactions int        `json:"compactions"`
	CompactedAt *time.Time `json:"compacted_at,omitempty"`
	Title       string     `json:"title,omitempty"`
	Mode        string     `json:"mode"`
	ModeEnv     string     `json:"lx_mode,omitempty"`
	Terms       []ctxTerm  `json:"focus_terms"`
	Files       []ctxFile  `json:"files"`
	Runs        []ctxRun   `json:"runs"`
}

func runCtx(args []string, stdout, stderr io.Writer, env func(string) string, cwd string, now time.Time) int {
	fs := flag.NewFlagSet("ctx", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "lx ctx: unexpected argument %q\nusage: %s\n", fs.Arg(0), ctxUsage)
		return 2
	}
	src, err := agentctx.Locate(env, cwd)
	if err != nil {
		fmt.Fprintln(stderr, "lx ctx: no session:", err)
		return 1
	}
	src.Argv = []string{"lx", "ctx"}
	snap, err := agentctx.Load(src, agentctx.DefaultTail)
	for i := 0; err == nil && !snap.Caller && i < ctxRetries; i++ {
		time.Sleep(ctxPause)
		snap, err = agentctx.Load(src, agentctx.DefaultTail)
	}
	if err != nil {
		fmt.Fprintln(stderr, "lx ctx: can't read the transcript:", err)
		return 1
	}
	home := env("HOME")
	v := ctxView{
		Agent: snap.Agent, Session: snap.SessionID, Subagent: snap.Subagent, Caller: snap.Caller,
		Transcript: ctxPath(snap.Path, "", home), BytesRead: snap.Bytes, TurnsRead: snap.Turns,
		Model: snap.Model, Used: snap.Pressure.Used, Window: snap.Pressure.Window, WindowFrom: snap.WindowFrom,
		Percent: float64(int(snap.Pressure.Fraction()*1000+0.5)) / 10, Compactions: snap.Compactions,
		Title: snap.Title, Mode: snap.InferMode().String(), ModeEnv: env("LX_MODE"),
		Terms: []ctxTerm{}, Files: []ctxFile{}, Runs: []ctxRun{},
	}
	if !snap.CompactedAt.IsZero() {
		t := snap.CompactedAt
		v.CompactedAt = &t
	}
	if f := snap.Focus(); f != nil {
		for _, t := range f.Terms {
			v.Terms = append(v.Terms, ctxTerm{t.Text, t.Weight})
		}
	}
	for _, f := range snap.Files {
		v.Files = append(v.Files, ctxFile{ctxPath(f.Path, cwd, home), f.Op, f.TurnsAgo})
	}
	for _, r := range snap.Runs {
		if !r.Pending {
			v.Runs = append(v.Runs, ctxRun{ctxRunKey(r.Command), r.LxID, r.TurnsAgo, r.InContext, r.Failed})
		}
	}
	if *asJSON {
		b, _ := json.MarshalIndent(v, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	v.text(stdout, now)
	return 0
}

func (v ctxView) text(w io.Writer, now time.Time) {
	row := func(k, format string, a ...any) { fmt.Fprintf(w, "%-11s %s\n", k, fmt.Sprintf(format, a...)) }
	who := v.Agent + ", session " + v.Session
	if v.Subagent != "" {
		who += " (subagent " + v.Subagent + ")"
	}
	row("agent", "%s", who)
	if v.Caller {
		row("caller", "confirmed")
	} else {
		row("caller", "not confirmed: this command isn't in the transcript yet, so no run counts as in context")
	}
	row("transcript", "%s (last %s read, %d turns)", v.Transcript, ctxBytes(v.BytesRead), v.TurnsRead)
	row("model", "%s", ctxOr(v.Model, "unknown"))
	switch {
	case v.Used > 0:
		row("context", "%s of %s tokens (%.1f%%), window from %s", ctxInt(v.Used), ctxInt(v.Window), v.Percent, ctxWindowFrom(v.WindowFrom))
	default:
		row("context", "unknown (no usage recorded yet), window %s tokens from %s", ctxInt(v.Window), ctxWindowFrom(v.WindowFrom))
	}
	switch {
	case v.Compactions == 0:
		row("compacted", "not in the part read")
	case v.CompactedAt != nil:
		row("compacted", "%d× in the part read, last %s ago", v.Compactions, ctxAgo(now.Sub(*v.CompactedAt)))
	default:
		row("compacted", "%d× in the part read", v.Compactions)
	}
	if v.Title != "" {
		row("title", "%s", v.Title)
	}
	mode := v.Mode + ", inferred from the latest assistant message"
	if v.ModeEnv != "" {
		mode += " (LX_MODE=" + v.ModeEnv + " is set)"
	}
	row("mode", "%s", mode)
	var terms []string
	for _, t := range v.Terms {
		terms = append(terms, fmt.Sprintf("%s %.1f", t.Text, t.Weight))
	}
	row("focus", "%s", ctxOr(strings.Join(terms, ", "), "none"))
	var files []string
	for _, f := range v.Files {
		files = append(files, fmt.Sprintf("%s (%s, %s)", f.Path, f.Op, ctxTurns(f.TurnsAgo)))
	}
	row("files", "%s", ctxOr(strings.Join(files, "; "), "none"))
	var runs []string
	gone, more := 0, 0
	for _, r := range v.Runs {
		if !r.InContext {
			gone++
			continue
		}
		if len(runs) == ctxMaxRuns {
			more++
			continue
		}
		s := r.Command
		if r.LxID > 0 {
			s += ": lx show " + fmt.Sprint(r.LxID)
		}
		if r.Failed {
			s += ", failed"
		}
		runs = append(runs, s+", "+ctxTurns(r.TurnsAgo))
	}
	line := ctxOr(strings.Join(runs, "; "), "none")
	if more > 0 {
		line += fmt.Sprintf("; %d more", more)
	}
	if gone > 0 {
		line += fmt.Sprintf(" (+%d not in context)", gone)
	}
	row("runs", "%s", line)
}

func ctxRunKey(cmd string) string {
	if in := hook.Inspect(cmd); len(in.Targets) > 0 {
		return cmdKey(in.Targets[0])
	}
	r := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n")
	for _, seg := range strings.Split(r.Replace(cmd), "\n") {
		f := strings.Fields(seg)
		for len(f) > 0 && strings.Contains(f[0], "=") && !strings.HasPrefix(f[0], "=") {
			f = f[1:]
		}
		if len(f) == 0 || f[0] == "cd" || f[0] == "export" {
			continue
		}
		return cmdKey(f)
	}
	return "sh"
}

func ctxPath(p, cwd, home string) string {
	if cwd != "" {
		if rel, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel) {
			return rel
		}
	}
	if home != "" && strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

func ctxWindowFrom(s string) string {
	switch s {
	case "model":
		return "the model table"
	case "default":
		return "the default (unknown model)"
	case "usage":
		return "usage above the model's window"
	case "transcript":
		return "the transcript"
	}
	return s
}

func ctxTurns(n int) string {
	switch n {
	case 0:
		return "this turn"
	case 1:
		return "1 turn ago"
	}
	return fmt.Sprintf("%d turns ago", n)
}

func ctxAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", max(int(d/time.Second), 0))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d/time.Hour))
	}
	return fmt.Sprintf("%dd", int(d/(24*time.Hour)))
}

func ctxBytes(n int) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KiB", (n+1023)/1024)
}

func ctxInt(n int) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func ctxOr(s, none string) string {
	if s == "" {
		return none
	}
	return s
}
