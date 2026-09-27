package discover

// Acted-on fidelity: transcripts hold the counterfactual. They record each
// command's full output, which is what lx would have condensed, and the
// agent's next moves. When the agent opens or edits a file that a command's
// output named at file:line within a few tool calls, that reference
// mattered. The question is whether lx's view of the output showed it, and
// whether a blind head+tail cut of the same size would have.
//
// Only outputs discover can replay exactly are scored: one rewritten
// command whose whole output the transcript holds. A command piped into
// head/tail is scored through the same cut, and only when the transcript
// shows the cut kept everything; otherwise lx would have condensed an
// output the transcript lacks. Outputs that can't be replayed are counted
// apart, by cause (Unreplayable), never as kept or missed.
//
// This is correlational. A reference the view lacks is one the agent
// reached anyway (from the receipt's `lx show`, another command, or its own
// knowledge). It is not an error lx hid, and the report says so.

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/baseline"
	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tokens"
)

// budgetMarker ends the line lx's budget stage puts where it removed lines
// ("… 120 lines omitted …").
const budgetMarker = " lines omitted …"

const (
	actWindow   = 3  // tool calls after a command during which a touch counts
	maxExamples = 20 // misses listed by --examples
	topActed    = 15 // commands listed in by_command (Total counts them all)
)

// ActedOn is the acted-on fidelity section of a Report.
type ActedOn struct {
	Window    int         `json:"window"` // tool calls
	ByCommand []ActedStat `json:"by_command"`
	Total     ActedStat   `json:"total"`
	// Ambiguous: touches that matched references in different
	// directories of one output; left out of the stats.
	Ambiguous int `json:"ambiguous_refs"`
	// Unmeasured: references the agent acted on in outputs discover
	// cannot replay exactly, by cause. They are left out of the stats:
	// scoring them would score a view lx never printed.
	Unmeasured   int          `json:"unmeasured_refs"`
	UnmeasuredBy Unreplayable `json:"unmeasured_by_cause"`
	// LxViews: outputs that already were lx views (the hook rewrote the
	// command, so the transcript lacks the raw output); not measured.
	LxViews  int         `json:"lx_views_skipped"`
	Examples []ActedMiss `json:"examples,omitempty"` // Options.Examples only: holds paths
}

// Unreplayable counts references in outputs discover cannot replay
// exactly, by why.
type Unreplayable struct {
	// HeadTail: the agent's head/tail kept part of the output, and lx
	// would have condensed the whole of it, which the transcript lacks.
	HeadTail int `json:"cut_by_head_tail"`
	// Several: the line ran several rewritten commands; lx condenses each
	// one's output apart, the transcript holds them together.
	Several int `json:"several_commands"`
	// Spill: the host spilled the output and its saved copy is gone.
	Spill int `json:"spill_without_saved_output"`
	// LxView: the output already was an lx view.
	LxView int `json:"already_lx_view"`
}

func (u *Unreplayable) add(why string, n int) {
	switch why {
	case whyHeadTail:
		u.HeadTail += n
	case whySeveral:
		u.Several += n
	case whySpill:
		u.Spill += n
	default:
		u.LxView += n
	}
}

// Why discover cannot replay an output exactly ("" = it can).
const (
	whyHeadTail = "head/tail"
	whySeveral  = "several"
	whySpill    = "spill"
	whyLxView   = "lx view"
)

// ActedStat counts the references the agent acted on for one command key.
type ActedStat struct {
	Command    string `json:"command,omitempty"`
	Refs       int    `json:"refs"`
	FileInView int    `json:"file_in_view"`      // lx's view names the file
	LocInView  int    `json:"loc_in_view"`       // lx's view shows the file:line
	FileInHT   int    `json:"file_in_head_tail"` // same, for head+tail cut to the view's size
	LocInHT    int    `json:"loc_in_head_tail"`
	Misses     Misses `json:"misses"` // references whose file:line lx's view does not show
}

// Misses splits the references lx's view does not show by cause: the
// agent's own head/tail after lx (the view had it, the cut dropped it),
// the budget stage ("… N lines omitted …" in the view), else the filter.
type Misses struct {
	Budget int `json:"budget"`
	Filter int `json:"filter"`
	Cut    int `json:"cut"`
}

// ActedMiss is one reference lx's view did not show (--examples).
type ActedMiss struct {
	Command    string `json:"command"`
	Location   string `json:"location"` // as the output printed it
	Reason     string `json:"reason"`   // "budget", "filter" or "cut"
	FileInView bool   `json:"file_in_view"`
}

type fidelity struct {
	examples   bool
	ring       []*actEntry // recent candidates with references, oldest first
	stats      map[string]*ActedStat
	ambiguous  int
	unmeasured Unreplayable
	lxViews    int
	misses     []ActedMiss
}

// entryKind says what a touch matching an entry's references counts as.
// Every kind takes part in matching: a touch belongs to the newest output
// that names the file, whatever that output's kind.
type entryKind uint8

const (
	measured   entryKind = iota // lx's view differs from what the agent read: scored
	same                        // the agent would read the same text with lx: not scored
	unmeasured                  // lx's view can't be replayed exactly: counted apart
)

type actEntry struct {
	cmd       string // command key
	cwd       string
	kind      entryKind
	why       string // unmeasured: why discover cannot replay the output
	refs      []actRef
	byBase    map[string][]int // refs by file base name: a touch can only match its own
	callsLeft int
}

type actRef struct {
	loc                   string // the location as printed
	path                  string // its path, cleaned
	line                  int
	fileInView, locInView bool
	fileInHT, locInHT     bool
	miss                  string // why the view lacks the location: "cut", "budget" or "filter"
	done                  bool   // credited, or ruled ambiguous
}

// touch is a file a tool call opened or edited.
type touch struct {
	path     string // cleaned; absolute when the call's cwd was known
	from, to int    // the lines read, when the call says (0, 0 = whole file)
}

func newFidelity(examples bool) *fidelity {
	return &fidelity{examples: examples, stats: map[string]*ActedStat{}}
}

// newFile starts a transcript: windows never span files.
func (f *fidelity) newFile() { f.ring = f.ring[:0] }

// toolUse credits the references a new tool call touched, then advances
// every window by one call.
func (f *fidelity) toolUse(ts []touch) {
	for _, t := range ts {
		f.match(t)
	}
	keep := f.ring[:0]
	for _, e := range f.ring {
		if e.callsLeft--; e.callsLeft > 0 {
			keep = append(keep, e)
		}
	}
	clear(f.ring[len(keep):])
	f.ring = keep
}

// match credits t to the newest output that names it (whatever its kind:
// an unscored output still claims the touch). Each reference is credited
// once; a touch that matches references in different directories of one
// output is ambiguous and not credited.
func (f *fidelity) match(t touch) {
	for i := len(f.ring) - 1; i >= 0; i-- {
		e := f.ring[i]
		var open []int
		named := false
		for _, j := range e.byBase[filepath.Base(t.path)] {
			if refMatches(&e.refs[j], t, e.cwd) {
				named = true
				if !e.refs[j].done {
					open = append(open, j)
				}
			}
		}
		if !named {
			continue
		}
		if len(open) == 0 {
			return // already credited to the newest output that names it
		}
		if e.kind != measured {
			for _, j := range open {
				e.refs[j].done = true
			}
			if e.kind == unmeasured {
				f.unmeasured.add(e.why, len(open))
			}
			return
		}
		dir := filepath.Dir(e.refs[open[0]].path)
		for _, j := range open[1:] {
			if filepath.Dir(e.refs[j].path) != dir {
				f.ambiguous++
				for _, j := range open {
					e.refs[j].done = true
				}
				return
			}
		}
		for _, j := range open {
			f.credit(e, &e.refs[j])
		}
		return
	}
}

// refMatches: the touched path is the reference's file (absolute reference:
// equal; relative: a path suffix, or the reference joined to the command's
// cwd), and within the lines the call read, when it says. Each rule implies
// the same base name, which match uses as an index.
func refMatches(r *actRef, t touch, cwd string) bool {
	if t.to > 0 && (r.line < t.from || r.line > t.to) {
		return false
	}
	if filepath.IsAbs(r.path) {
		return t.path == r.path
	}
	if t.path == r.path || strings.HasSuffix(t.path, "/"+r.path) {
		return true
	}
	return cwd != "" && filepath.Join(cwd, r.path) == t.path
}

func (f *fidelity) credit(e *actEntry, r *actRef) {
	r.done = true
	st := f.stats[e.cmd]
	if st == nil {
		st = &ActedStat{Command: e.cmd}
		f.stats[e.cmd] = st
	}
	st.add(r)
	if r.locInView {
		return
	}
	switch r.miss {
	case "cut":
		st.Misses.Cut++
	case "budget":
		st.Misses.Budget++
	default:
		st.Misses.Filter++
	}
	if f.examples && len(f.misses) < maxExamples {
		f.misses = append(f.misses, ActedMiss{Command: e.cmd, Location: r.loc, Reason: r.miss, FileInView: r.fileInView})
	}
}

func (st *ActedStat) add(r *actRef) {
	st.Refs++
	st.FileInView += b2i(r.fileInView)
	st.LocInView += b2i(r.locInView)
	st.FileInHT += b2i(r.fileInHT)
	st.LocInHT += b2i(r.locInHT)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// candidate records the application file:line references of a rewritten
// command's output, so the agent's next calls can be matched against them.
//
// why is "" when discover can replay exactly what lx would have printed:
// one rewritten command, its whole output recorded, and any head/tail
// after it replayable (sliceOf). Else it says why not (whyHeadTail, …).
// Only an exact output is scored — whether lx's view, through the agent's
// own head/tail, and a blind head+tail cut of the same size show each
// reference — and only when lx changes what the agent reads. Other outputs
// still take part in matching (a touch belongs to the newest output that
// names the file) but are never scored: a reference in an output lx leaves
// as is says nothing about lx, and one in an output discover cannot replay
// would score a view lx never printed.
func (f *fidelity) candidate(key, cwd string, v *view, why string, sl slicing) {
	clean := v.cleanRaw()
	lxView := isLxView(clean)
	if lxView {
		f.lxViews++
	}
	locs := engine.AppLocations(clean)
	if len(locs) == 0 {
		return
	}
	if lxView {
		why = whyLxView
	}
	e := &actEntry{cmd: key, cwd: cwd, why: why, callsLeft: actWindow}
	if why != "" {
		e.kind, e.refs = unmeasured, plainRefs(locs)
	} else {
		e.kind, e.refs = scoreView(locs, v, sl)
	}
	if len(e.refs) == 0 {
		return
	}
	e.byBase = make(map[string][]int, len(e.refs))
	for j := range e.refs {
		b := filepath.Base(e.refs[j].path)
		e.byBase[b] = append(e.byBase[b], j)
	}
	f.ring = append(f.ring, e)
	if len(f.ring) > actWindow {
		clear(f.ring[:len(f.ring)-actWindow])
		f.ring = f.ring[len(f.ring)-actWindow:]
	}
}

// scoreView builds the references for locs (engine.AppLocations of the
// replayed output) from what the agent would read with lx: the view and
// receipt, cut by the agent's head/tail when the command line has one.
// kind is same when that text is what the agent read without lx.
func scoreView(locs []string, v *view, sl slicing) (entryKind, []actRef) {
	clean := v.cleanRaw()
	budget := strings.Contains(v.res.Output, budgetMarker)
	if !sl.sliced {
		if v.res.Filter == "passthrough" || v.res.Output == clean {
			return same, plainRefs(locs)
		}
		return measured, refsOf(locs, v.res.Output+"\n"+v.receipt, "", baseline.HeadTail(clean, v.after), budget)
	}
	if streamsKept(v) {
		return same, plainRefs(locs)
	}
	lines, _, lo, hi := sl.shown(v)
	shown := strings.Join(lines[lo:hi], "\n")
	cut := lo > 0 || hi < len(lines)
	if !cut && v.res.Output == clean {
		return same, plainRefs(locs)
	}
	whole := ""
	if cut {
		whole = strings.Join(lines, "\n")
	}
	// The blind cut gets as many tokens as the agent reads of lx's output.
	size := min(tokens.Count(shown), v.res.RawTokens)
	return measured, refsOf(locs, shown, whole, baseline.HeadTail(clean, size), budget)
}

// maxName is the longest file name (one path component) macOS and Linux
// allow, in bytes. A longer "name" in an output is not a file the agent
// could open, and bounding names keeps names() linear.
const maxName = 255

// refsOf builds the references for locs: whether shown (what the agent
// reads of lx's output) and the head+tail cut ht name each file and show
// each file:line. whole is lx's entire output when the agent's head/tail
// cut it to shown ("" otherwise): a location it has and shown lacks is a
// "cut" miss. Its cost is linear in the texts and the number of
// references.
func refsOf(locs []string, shown, whole, ht string, budget bool) []actRef {
	viewKeys, viewNames := engine.ViewLocKeys(shown), names(shown)
	htKeys, htNames := engine.ViewLocKeys(ht), names(ht)
	var wholeKeys map[string]bool
	if whole != "" {
		wholeKeys = engine.ViewLocKeys(whole)
	}
	refs := plainRefs(locs)
	for i := range refs {
		r := &refs[i]
		k, base := engine.LocKey(r.loc), filepath.Base(r.path)
		r.fileInView, r.locInView = viewNames[base], viewKeys[k]
		r.fileInHT, r.locInHT = htNames[base], htKeys[k]
		switch {
		case r.locInView:
		case wholeKeys[k]:
			r.miss = "cut"
		case budget:
			r.miss = "budget"
		default:
			r.miss = "filter"
		}
	}
	return refs
}

// plainRefs builds the references for locs with their paths and lines
// only: enough to match the agent's calls against.
func plainRefs(locs []string) []actRef {
	refs := make([]actRef, 0, len(locs))
	for _, m := range locs {
		path, line, ok := engine.SplitLoc(m)
		if !ok || len(filepath.Base(path)) > maxName {
			continue
		}
		n, _ := strconv.Atoi(line)
		refs = append(refs, actRef{loc: m, path: filepath.Clean(path), line: n})
	}
	return refs
}

// isLxView: the output ends with an lx receipt, so it already is an lx
// view (a hook rewrote the command) and the raw output is not recorded.
func isLxView(s string) bool {
	s = strings.TrimRight(s, " \t\n")
	last := s[strings.LastIndexByte(s, '\n')+1:]
	return strings.HasPrefix(last, "[lx: ") && strings.Contains(last, " lines (−") && strings.HasSuffix(last, "]")
}

// names returns the file names text mentions as whole names: every
// substring that starts after a character that cannot be part of a name
// and ends before one that cannot continue a word. So "pkg/a.go:12"
// names "a.go" (and "pkg", "a", "12") but "data.go" does not name "a.go",
// and "a.go.orig" names "a.go". One pass; names(text)[n] is hasName(text, n)
// for every n of at most maxName bytes (longer ones are never looked up),
// which keeps a long run of dots from costing quadratic time.
func names(text string) map[string]bool {
	set := map[string]bool{}
	for i := 0; i < len(text); {
		if !nameByte(text[i]) {
			i++
			continue
		}
		j := i
		for j < len(text) && nameByte(text[j]) {
			j++
		}
		// text[i:j] is a maximal run; a name ends at j or before a '.' or '@'.
		for k := i + 1; k <= j && k-i <= maxName; k++ {
			if k == j || !wordByte(text[k]) {
				set[text[i:k]] = true
			}
		}
		i = j
	}
	return set
}

func wordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-'
}

// nameByte: a character of a file name as LocRe matches it ([\w.@-]).
func nameByte(c byte) bool { return wordByte(c) || c == '.' || c == '@' }

// ---- what a tool call touched ----

// touchedPaths returns the file a non-Bash tool call opened or edited.
func touchedPaths(name string, input json.RawMessage, cwd string) []touch {
	var in struct {
		FilePath     string          `json:"file_path"`
		NotebookPath string          `json:"notebook_path"`
		Path         string          `json:"path"`
		Offset       json.RawMessage `json:"offset"`
		Limit        json.RawMessage `json:"limit"`
	}
	if len(input) == 0 || json.Unmarshal(input, &in) != nil {
		return nil
	}
	t := touch{}
	switch name {
	case "Read":
		t.path = in.FilePath
		off, lim := min(jsonInt(in.Offset), 1<<30), min(jsonInt(in.Limit), 1<<30)
		if off > 0 || lim > 0 {
			if lim <= 0 {
				lim = 2000 // Claude Code's default
			}
			start := max(off, 1)
			// offset may count from 0 or 1: allow one line of slack
			t.from, t.to = max(start-1, 1), start+lim
		}
	case "Edit", "MultiEdit", "Write":
		t.path = in.FilePath
	case "NotebookEdit":
		t.path = in.NotebookPath
	case "Grep", "Glob":
		if filepath.Ext(in.Path) != "" {
			t.path = in.Path
		}
	}
	if t.path = cleanPath(t.path, cwd); t.path == "" {
		return nil
	}
	return []touch{t}
}

func jsonInt(raw json.RawMessage) int {
	n, err := strconv.Atoi(strings.Trim(strings.TrimSpace(string(raw)), `"`))
	if err != nil {
		return 0
	}
	return n
}

// fileReader: a Bash command whose file operands count as opening the file.
func fileReader(name string) bool {
	switch name {
	case "cat", "head", "tail", "sed", "bat", "nl", "wc":
		return true
	}
	return false
}

// bashPaths returns the files a Bash command reads with cat, head, tail,
// sed, bat, nl or wc: operands with an extension that are not flags or
// globs. `cd DIR &&` before them is followed; after a cd whose target is
// unknown (`cd "$D"`, `cd -`, `cd ~/x`, a bare `cd`) relative operands stay
// relative, so they match only references printed the same way. `sed -n
// 'A,Bp' FILE` reads lines A to B.
func bashPaths(cmds [][]string, cwd string) []touch {
	var out []touch
	for _, argv := range cmds {
		if len(argv) == 0 {
			continue
		}
		base := filepath.Base(argv[0])
		if base == "cd" || base == "pushd" || base == "popd" {
			switch {
			case base == "cd" && len(argv) == 2 && argv[1] != "-" && !strings.ContainsAny(argv[1], "$~`*?[{"):
				if filepath.IsAbs(argv[1]) {
					cwd = filepath.Clean(argv[1])
				} else if cwd != "" {
					cwd = filepath.Join(cwd, argv[1])
				}
			default:
				cwd = ""
			}
			continue
		}
		if !fileReader(base) {
			continue
		}
		from, to := 0, 0
		if base == "sed" {
			from, to = sedRange(argv[1:])
		}
		for _, a := range argv[1:] {
			if strings.HasPrefix(a, "-") || filepath.Ext(a) == "" || strings.ContainsAny(a, "*?[{$`") {
				continue
			}
			if p := cleanPath(a, cwd); p != "" {
				out = append(out, touch{path: p, from: from, to: to})
			}
		}
	}
	return out
}

// sedRange parses `sed -n 'A,Bp'` / `sed -n 'Ap'`; (0, 0) otherwise.
func sedRange(args []string) (int, int) {
	quiet := false
	script := ""
	for _, a := range args {
		switch {
		case a == "-n" || a == "--quiet" || a == "--silent":
			quiet = true
		case !strings.HasPrefix(a, "-") && script == "":
			script = a
		}
	}
	if !quiet || !strings.HasSuffix(script, "p") {
		return 0, 0
	}
	a, b, found := strings.Cut(strings.TrimSuffix(script, "p"), ",")
	from, err := strconv.Atoi(a)
	if err != nil || from < 1 {
		return 0, 0
	}
	to := from
	if found {
		if to, err = strconv.Atoi(b); err != nil || to < from {
			return 0, 0
		}
	}
	return from, to
}

func cleanPath(p, cwd string) string {
	p = strings.TrimSpace(p)
	if p == "" || strings.ContainsRune(p, 0) {
		return ""
	}
	if !filepath.IsAbs(p) && cwd != "" {
		p = filepath.Join(cwd, p)
	}
	return filepath.Clean(p)
}

// ---- report ----

func (f *fidelity) report() *ActedOn {
	u := f.unmeasured
	a := &ActedOn{Window: actWindow, Ambiguous: f.ambiguous, Unmeasured: u.HeadTail + u.Several + u.Spill + u.LxView,
		UnmeasuredBy: u, LxViews: f.lxViews, Examples: f.misses, ByCommand: []ActedStat{}}
	for _, st := range f.stats {
		a.ByCommand = append(a.ByCommand, *st)
		a.Total.Refs += st.Refs
		a.Total.FileInView += st.FileInView
		a.Total.LocInView += st.LocInView
		a.Total.FileInHT += st.FileInHT
		a.Total.LocInHT += st.LocInHT
		a.Total.Misses.Budget += st.Misses.Budget
		a.Total.Misses.Filter += st.Misses.Filter
		a.Total.Misses.Cut += st.Misses.Cut
	}
	sort.Slice(a.ByCommand, func(i, j int) bool {
		if a.ByCommand[i].Refs != a.ByCommand[j].Refs {
			return a.ByCommand[i].Refs > a.ByCommand[j].Refs
		}
		return a.ByCommand[i].Command < a.ByCommand[j].Command
	})
	if len(a.ByCommand) > topActed {
		a.ByCommand = a.ByCommand[:topActed]
	}
	return a
}

// text writes the acted-on section.
func (a *ActedOn) text(w io.Writer) {
	fmt.Fprintf(w, "\nActed-on fidelity (the agent opened or edited a file a rewritten command named, within %d tool calls)\n", a.Window)
	if a.Total.Refs == 0 {
		fmt.Fprintln(w, "  no such references found")
	} else {
		rows := make([][]string, 0, len(a.ByCommand)+1)
		for _, st := range a.ByCommand {
			rows = append(rows, st.row(st.Command, false))
		}
		rows = append(rows, a.Total.row("total", true))
		table(w, []string{"command", "refs", "file in lx view", "loc in lx view", "file in head+tail", "loc in head+tail", ""}, rows)
		fmt.Fprintln(w, "  head+tail: the raw output cut blind to the size of lx's view.")
	}
	if a.Unmeasured > 0 {
		u := a.UnmeasuredBy
		var why []string
		for _, c := range []struct {
			n    int
			what string
		}{{u.HeadTail, "a head/tail kept part of the output"}, {u.Several, "several rewritten commands on one line"},
			{u.Spill, "a spill whose saved output is gone"}, {u.LxView, "already an lx view"}} {
			if c.n > 0 {
				why = append(why, fmt.Sprintf("%s %s", c.what, num(c.n)))
			}
		}
		fmt.Fprintf(w, "  not counted: %s references in outputs discover can't replay exactly (%s)\n", num(a.Unmeasured), strings.Join(why, " · "))
	}
	if a.Ambiguous > 0 || a.LxViews > 0 {
		fmt.Fprintf(w, "  not counted: ambiguous references %s · outputs that already were lx views %s\n", num(a.Ambiguous), num(a.LxViews))
	}
	if miss := a.Total.Misses.Budget + a.Total.Misses.Filter + a.Total.Misses.Cut; miss > 0 {
		var parts []string
		listed := 0
		for _, st := range a.ByCommand {
			if st.Misses.Filter > 0 {
				parts = append(parts, fmt.Sprintf("%s %s (filter)", st.Command, num(st.Misses.Filter)))
			}
			if st.Misses.Budget > 0 {
				parts = append(parts, fmt.Sprintf("%s %s (budget)", st.Command, num(st.Misses.Budget)))
			}
			if st.Misses.Cut > 0 {
				parts = append(parts, fmt.Sprintf("%s %s (the agent's head/tail cut lx's view)", st.Command, num(st.Misses.Cut)))
			}
			listed += st.Misses.Filter + st.Misses.Budget + st.Misses.Cut
		}
		if listed < miss {
			parts = append(parts, fmt.Sprintf("other commands %s", num(miss-listed)))
		}
		fmt.Fprintf(w, "  locations not in lx's view: %s\n", strings.Join(parts, ", "))
		fmt.Fprintln(w, "  (correlational: references the agent reached anyway, not errors lx hid)")
		if a.Examples == nil {
			fmt.Fprintln(w, "  lx discover --fidelity --examples lists them (on this terminal only)")
		}
	}
	for _, m := range a.Examples {
		note := m.Reason
		if m.FileInView {
			note += "; file named in the view"
		}
		fmt.Fprintf(w, "  %s  %s  (%s)\n", m.Command, m.Location, note)
	}
}

func (st ActedStat) row(name string, total bool) []string {
	cell := func(n int) string {
		if total {
			return fmt.Sprintf("%s (%s)", num(n), pctFloor(n, st.Refs, 1))
		}
		return fmt.Sprintf("%s (%s)", num(n), pctFloor(n, st.Refs, 0))
	}
	return []string{name, num(st.Refs), cell(st.FileInView), cell(st.LocInView), cell(st.FileInHT), cell(st.LocInHT), ""}
}

// pctFloor is a/b as a percentage with the given decimals (0 or 1),
// rounded down: a share of references kept never shows as 100% unless it is.
func pctFloor(a, b, decimals int) string {
	if b == 0 {
		return "0%"
	}
	if decimals == 0 {
		return fmt.Sprintf("%d%%", a*100/b)
	}
	if a == b {
		return "100%"
	}
	t := a * 1000 / b
	return fmt.Sprintf("%d.%d%%", t/10, t%10)
}
