package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tee"
	"github.com/iheeb1/lx/internal/tokens"
	"github.com/iheeb1/lx/internal/track"
)

const tuneMaxBudget = 24000

const tuneWhole = 2.0

var (
	tuneLoad    = track.LoadTune
	tuneLoadFor = track.LoadTuneFor
	tuneUpdate  = track.UpdateTune
)

func tuneRecover() { _ = recover() }

type tuneRun struct {
	on   bool
	argv []string
	cwd  string
	key  string
	root string
	lv   track.TuneLevel
}

func tuneLookup(argv []string, cwd string, now time.Time) (tn tuneRun) {
	defer func() {
		if recover() != nil {
			tn = tuneRun{}
		}
	}()
	if len(argv) == 0 || cwd == "" || !track.TuneEnabled() {
		return tuneRun{}
	}
	t := tuneRun{on: true, argv: argv, cwd: filepath.Clean(cwd), key: cmdKey(argv)}
	if t.key == "" {
		return tuneRun{}
	}
	st := tuneLoadFor(t.key)
	if !st.HasCmd(t.key) {
		return t
	}
	t.root = tuneProjectRoot(t.cwd)
	if e := st.Entry(st.Hash("project", t.root), t.key); e != nil {
		t.lv = e.Evaluate(now)
	}
	return t
}

func (t tuneRun) level() int { return t.lv.Level }

func (t tuneRun) budget(b int) int {
	if t.level() == track.LevelNormal {
		return b
	}
	base := b
	if base <= 0 {
		base = engine.DefaultBudget
	}
	if t.level() >= track.LevelRaw {
		return max(base, tuneMaxBudget)
	}
	return max(base, min(2*base, tuneMaxBudget))
}

func (t tuneRun) process(c *engine.Context, raw string, o engine.Options) engine.Result {
	return processView(c, raw, t.options(raw, o))
}

func (t tuneRun) options(raw string, o engine.Options) (tuned engine.Options) {
	if t.level() == track.LevelNormal {
		return o
	}
	defer func() {
		if recover() != nil {
			tuned = o
		}
	}()
	tuned = o
	tuned.Budget = t.budget(o.Budget)

	if t.level() >= track.LevelRaw || tokens.Count(raw) <= tuned.Budget {
		tuned.MinSavings = tuneWhole
	}
	return tuned
}

func (t tuneRun) note(self string, short bool) string {
	why := ""
	if !short {
		why = " after " + track.SignalCounts(t.lv.Full, t.lv.Raw)
	}
	switch t.level() {
	case track.LevelLoosen:
		return "loosened" + why + ": " + self + " tune"
	case track.LevelRaw:
		return "tuned to raw" + why + ", but over the output limit: " + self + " tune"
	}
	return ""
}

func (t tuneRun) receipt(r string) (out string) {
	if t.level() == track.LevelNormal || !strings.HasSuffix(r, "]") {
		return r
	}
	defer func() {
		if recover() != nil {
			out = r
		}
	}()
	self := selfCommand()
	for _, short := range []bool{false, true} {
		if s := strings.TrimSuffix(r, "]") + " · " + t.note(self, short) + "]"; len(s) < receiptRoom {
			return s
		}
	}
	return r
}

func (t tuneRun) recordCondensed(id string, exit int, now time.Time) {
	if !t.on {
		return
	}
	defer tuneRecover()
	root := t.root
	if root == "" {
		root = tuneProjectRoot(t.cwd)
	}
	n, _ := strconv.Atoi(id)
	_ = tuneUpdate(now, func(st *track.TuneState) bool {
		p := st.Hash("project", root)
		st.AddRecent(track.TuneRun{Time: now.Unix(), Run: max(n, 0), Project: p, Cmd: t.key, Argv: tuneArgvHash(st, t.cwd, t.argv)})
		if exit == 0 {
			if e := st.Entry(p, t.key); e != nil {
				e.Clean++
			}
		}
		return true
	})
}

func tuneRecordRaw(argv []string, cwd string, now time.Time) {
	if len(argv) == 0 || cwd == "" || !track.TuneEnabled() {
		return
	}
	defer tuneRecover()
	cwd = filepath.Clean(cwd)
	if st := tuneLoad(); !st.HasRecent(now) || st.LatestRun(tuneArgvHash(st, cwd, argv), now) == nil {
		return
	}
	_ = tuneUpdate(now, func(st *track.TuneState) bool {
		r := st.LatestRun(tuneArgvHash(st, cwd, argv), now)
		if r == nil || r.Regretted {
			return false
		}
		r.Regretted = true
		st.AddRegret(r.Project, r.Cmd, track.Regret{Time: now.Unix(), Run: r.Run, Signal: track.SignalRaw})
		return true
	})
}

func tuneRecordShow(id int, meta tee.Meta, sel showSel, full, piped bool, now time.Time) {
	if id <= 0 || piped || sel.numbered() || meta.State != tee.StateDone || !track.TuneEnabled() {
		return
	}
	defer tuneRecover()
	sig := track.SignalShow
	if full {
		sig = track.SignalFull
	}
	_ = tuneUpdate(now, func(st *track.TuneState) bool {
		var p, k string
		var at time.Time
		r := st.RecentRun(id)
		if r != nil {
			if r.Regretted {
				return false
			}
			p, k, at = r.Project, r.Cmd, time.Unix(r.Time, 0)
		} else {

			if !tuneLossyFilter(meta.Filter) || len(meta.Argv) == 0 || meta.Cwd == "" {
				return false
			}
			p, k, at = st.Hash("project", tuneProjectRoot(meta.Cwd)), cmdKey(meta.Argv), meta.Time
		}
		if sig == track.SignalShow && now.Sub(at) > track.RegretWindow {
			return false
		}
		if !st.AddRegret(p, k, track.Regret{Time: now.Unix(), Run: id, Signal: sig}) {
			return false
		}
		if r != nil {
			r.Regretted = true
		}
		return true
	})
}

func tuneLossyFilter(f string) bool {
	return f != "" && f != "passthrough" && f != "normalize"
}

func tuneProjectRoot(cwd string) string {
	p := showFindProject(cwd)
	if p.real != "" {
		return p.real
	}
	return p.root
}

func tuneArgvHash(st *track.TuneState, cwd string, argv []string) string {
	return st.Hash("argv", append([]string{cwd}, argv...)...)
}

type tuneEnv struct {
	stdout, stderr io.Writer
	cwd, home      string
	now            time.Time
}

func cmdTune(args []string) int {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	return runTune(args, tuneEnv{stdout: os.Stdout, stderr: os.Stderr, cwd: cwd, home: home, now: time.Now()})
}

type tuneRow struct {
	Project     string     `json:"project"`
	Current     bool       `json:"current"`
	Cmd         string     `json:"cmd"`
	Level       string     `json:"level"`
	BaseLevel   string     `json:"base_level"`
	Regrets     int        `json:"regrets"`
	FullRecalls int        `json:"full_recalls"`
	RawReruns   int        `json:"raw_reruns"`
	Regrets7d   int        `json:"regrets_7d"`
	Regrets30d  int        `json:"regrets_30d"`
	Latest      time.Time  `json:"latest"`
	Until       *time.Time `json:"until,omitempty"`
	CleanRuns   int        `json:"clean_runs"`
	lv          track.TuneLevel
}

func runTune(args []string, env tuneEnv) int {
	fs := flag.NewFlagSet("tune", flag.ContinueOnError)
	fs.SetOutput(env.stderr)
	fs.Usage = func() {
		fmt.Fprintln(env.stderr, "usage: lx tune [--json] [--all] [--reset] [CMD…]")
		fs.PrintDefaults()
	}
	asJSON := fs.Bool("json", false, "machine-readable output")
	all := fs.Bool("all", false, "every project, not just the current one")
	reset := fs.Bool("reset", false, "forget the regrets of CMD (default: every command) in this project (--all: everywhere)")

	var rest []string
	for ; len(args) > 0; args = args[1:] {
		a := args[0]
		switch {
		case a == "--":
			rest = append(rest, args[1:]...)
			args = args[:1]
		case tuneFlag(fs, a) || (len(rest) == 0 && strings.HasPrefix(a, "-") && a != "-"):
			if err := fs.Parse([]string{a}); err != nil {
				if errors.Is(err, flag.ErrHelp) {
					return 0
				}
				return 2
			}
		default:
			rest = append(rest, a)
		}
	}
	key := ""
	if len(rest) > 0 {
		if key = cmdKey(strings.Fields(strings.Join(rest, " "))); key == "" {
			fmt.Fprintf(env.stderr, "lx tune: no command in %q\n", strings.Join(rest, " "))
			return 2
		}
	}
	root := ""
	if env.cwd != "" {
		root = tuneProjectRoot(env.cwd)
	}
	if *reset {
		return tuneReset(env, root, key, *all, *asJSON)
	}

	st := track.LoadTune()
	cur := ""
	if root != "" {
		cur = st.Hash("project", root)
	}
	var rows []tuneRow
	for _, e := range st.Entries {
		if (!*all && e.Project != cur) || (key != "" && e.Cmd != key) {
			continue
		}
		lv := e.Evaluate(env.now)
		r := tuneRow{Project: e.Project, Current: e.Project == cur, Cmd: e.Cmd, Level: track.LevelName(lv.Level),
			BaseLevel: track.LevelName(lv.Base), Regrets: lv.Count, FullRecalls: lv.Full, RawReruns: lv.Raw,
			Regrets7d: lv.Week, Regrets30d: lv.Total, Latest: lv.Latest.In(env.now.Location()), CleanRuns: lv.Clean, lv: lv}
		if !lv.Until.IsZero() {
			u := lv.Until.In(env.now.Location())
			r.Until = &u
		}
		rows = append(rows, r)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch {
		case a.Current != b.Current:
			return a.Current
		case a.Project != b.Project:
			return a.Project < b.Project
		case a.lv.Level != b.lv.Level:
			return a.lv.Level > b.lv.Level
		case !a.Latest.Equal(b.Latest):
			return a.Latest.After(b.Latest)
		}
		return a.Cmd < b.Cmd
	})

	if *asJSON {
		out := struct {
			Enabled  bool      `json:"enabled"`
			Project  string    `json:"project"`
			Commands []tuneRow `json:"commands"`
		}{track.TuneEnabled(), root, rows}
		if out.Commands == nil {
			out.Commands = []tuneRow{}
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintln(env.stdout, string(b))
		return 0
	}
	tuneText(env, rows, root, key, *all)
	return 0
}

func tuneFlag(fs *flag.FlagSet, a string) bool {
	name, ok := strings.CutPrefix(a, "-")
	if !ok {
		return false
	}
	name = strings.TrimPrefix(name, "-")
	name, _, _ = strings.Cut(name, "=")
	return name == "h" || name == "help" || (name != "" && fs.Lookup(name) != nil)
}

func tuneReset(env tuneEnv, root, key string, all, asJSON bool) int {
	if root == "" && !all {
		fmt.Fprintln(env.stderr, "lx tune: can't tell the current directory (use --all)")
		return 1
	}
	n := 0
	err := track.UpdateTune(env.now, func(st *track.TuneState) bool {
		p := ""
		if !all {
			p = st.Hash("project", root)
		}
		n = st.Reset(p, key)
		return n > 0
	})
	if err != nil {
		fmt.Fprintln(env.stderr, "lx tune:", err)
		return 1
	}
	if asJSON {
		fmt.Fprintf(env.stdout, "{\"reset\": %d}\n", n)
		return 0
	}
	what, where := "every command", "in this project"
	if key != "" {
		what = key
	}
	if all {
		where = "in every project"
	}
	if n == 0 {
		fmt.Fprintf(env.stdout, "lx tune: nothing to forget for %s %s\n", what, where)
		return 0
	}
	whose := "its"
	if key == "" {
		what, whose = plural(n, "command", "commands"), "their"
		if all {
			what = plural(n, "entry", "entries")
		}
	}
	fmt.Fprintf(env.stdout, "lx tune: forgot %s %s; %s views are back to normal\n", what, where, whose)
	return 0
}

func tuneText(env tuneEnv, rows []tuneRow, root, key string, all bool) {
	w := env.stdout
	where := "this project"
	if root != "" {
		where += " (" + showTilde(root, env.home) + ")"
	}
	if all {
		where = "every project"
	}
	off := ""
	if !track.TuneEnabled() {
		off = " · off (LX_TUNE=0 or LX_TRACK=0): nothing is applied or learned"
	}
	if len(rows) == 0 {
		what := "anything"
		if key != "" {
			what = key
		}
		fmt.Fprintf(w, "lx tune: no regrets for %s in %s%s.\n", what, where, off)
		fmt.Fprintf(w, "lx loosens a command's views in a project after the agent reads %d of its condensed runs\n"+
			"back in full (lx show N without --errors/--grep/--lines/--head/--tail) or re-runs them with lx -r\n"+
			"within a week.\n", track.LoosenAt)
		return
	}
	fmt.Fprintf(w, "lx tune · %s%s\n\n", where, off)
	cw := len("command")
	for _, r := range rows {
		cw = max(cw, len(r.Cmd))
	}
	proj := ""
	for _, r := range rows {
		if all && r.Project != proj {
			proj = r.Project
			if r.Current {
				fmt.Fprintf(w, "  this project (%s)\n", showTilde(root, env.home))
			} else {
				fmt.Fprintf(w, "  project %s\n", r.Project[:8])
			}
		}
		fmt.Fprintf(w, "  %-*s  %-8s  %s\n", cw, r.Cmd, r.Level, tuneWhy(r, env.now))
	}
	fmt.Fprintf(w, "\n  loosened: twice the token budget, and the whole output when it fits in it\n"+
		"  raw: the whole output, condensed only past the agent's output limit\n"+
		"  %d clean runs (exit 0, not read back) lower a level · lx tune --reset [CMD] forgets\n", track.DecayRuns)
}

func tuneWhy(r tuneRow, now time.Time) string {
	lv := r.lv
	var b strings.Builder
	switch {
	case lv.Base > track.LevelNormal:
		fmt.Fprintf(&b, "%s in 7 days", track.SignalCounts(lv.Full, lv.Raw))
	case lv.Week > 0:
		fmt.Fprintf(&b, "%s in 7 days · loosens at %d", track.SignalCounts(lv.Full, lv.Raw), track.LoosenAt)
	default:
		fmt.Fprintf(&b, "%s, none in 7 days", plural(lv.Total, "regret", "regrets"))
	}
	if lv.Base > track.LevelNormal {
		switch {
		case lv.Level < lv.Base:
			fmt.Fprintf(&b, " · lowered by %s", plural(lv.Clean, "clean run", "clean runs"))
		case lv.Clean > 0:
			fmt.Fprintf(&b, " · %d of %d clean runs to lower it", lv.Clean%track.DecayRuns, track.DecayRuns)
		}
		if lv.Level > track.LevelNormal && !lv.Until.IsZero() {
			b.WriteString(" · until " + tuneWhen(lv.Until, now))
		}
	}
	return b.String()
}

func tuneWhen(t, now time.Time) string {
	return t.In(now.Location()).Format("Jan 2 15:04")
}
