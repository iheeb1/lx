package discover

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

const budgetMarker = " lines omitted …"

const (
	actWindow   = 3
	maxExamples = 20
	topActed    = 15
)

type ActedOn struct {
	Window    int         `json:"window"`
	ByCommand []ActedStat `json:"by_command"`
	Total     ActedStat   `json:"total"`

	Ambiguous int `json:"ambiguous_refs"`

	Unmeasured   int          `json:"unmeasured_refs"`
	UnmeasuredBy Unreplayable `json:"unmeasured_by_cause"`

	LxViews  int         `json:"lx_views_skipped"`
	Examples []ActedMiss `json:"examples,omitempty"`
}

type Unreplayable struct {
	HeadTail int `json:"cut_by_head_tail"`

	Several int `json:"several_commands"`

	Spill int `json:"spill_without_saved_output"`

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

const (
	whyHeadTail = "head/tail"
	whySeveral  = "several"
	whySpill    = "spill"
	whyLxView   = "lx view"
)

type ActedStat struct {
	Command    string `json:"command,omitempty"`
	Refs       int    `json:"refs"`
	FileInView int    `json:"file_in_view"`
	LocInView  int    `json:"loc_in_view"`
	FileInHT   int    `json:"file_in_head_tail"`
	LocInHT    int    `json:"loc_in_head_tail"`
	Misses     Misses `json:"misses"`
}

type Misses struct {
	Budget int `json:"budget"`
	Filter int `json:"filter"`
	Cut    int `json:"cut"`
}

type ActedMiss struct {
	Command    string `json:"command"`
	Location   string `json:"location"`
	Reason     string `json:"reason"`
	FileInView bool   `json:"file_in_view"`
}

type fidelity struct {
	examples   bool
	ring       []*actEntry
	stats      map[string]*ActedStat
	ambiguous  int
	unmeasured Unreplayable
	lxViews    int
	misses     []ActedMiss
}

type entryKind uint8

const (
	measured entryKind = iota
	same
	unmeasured
)

type actEntry struct {
	cmd       string
	cwd       string
	kind      entryKind
	why       string
	refs      []actRef
	byBase    map[string][]int
	callsLeft int
}

type actRef struct {
	loc                   string
	path                  string
	line                  int
	fileInView, locInView bool
	fileInHT, locInHT     bool
	miss                  string
	done                  bool
}

type touch struct {
	path     string
	from, to int
}

func newFidelity(examples bool) *fidelity {
	return &fidelity{examples: examples, stats: map[string]*ActedStat{}}
}

func (f *fidelity) newFile() { f.ring = f.ring[:0] }

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
			return
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

	size := min(tokens.Count(shown), v.res.RawTokens)
	return measured, refsOf(locs, shown, whole, baseline.HeadTail(clean, size), budget)
}

const maxName = 255

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

func isLxView(s string) bool {
	s = strings.TrimRight(s, " \t\n")
	last := s[strings.LastIndexByte(s, '\n')+1:]
	return strings.HasPrefix(last, "[lx: ") && strings.Contains(last, " lines (−") && strings.HasSuffix(last, "]")
}

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

func nameByte(c byte) bool { return wordByte(c) || c == '.' || c == '@' }

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
				lim = 2000
			}
			start := max(off, 1)

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

func fileReader(name string) bool {
	switch name {
	case "cat", "head", "tail", "sed", "bat", "nl", "wc":
		return true
	}
	return false
}

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
