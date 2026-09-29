// Package engine turns raw command output into a condensed view.
package engine

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
)

type Context struct {
	Argv   []string
	Exit   int
	Cwd    string
	Home   string
	Budget int
	Mode   Mode
	Focus  *Focus

	fm *focusMatcher
	jr *judgeRun
}

func (c *Context) focus() *focusMatcher {
	if c == nil || c.Focus.Empty() {
		return nil
	}
	if c.fm == nil {
		c.fm = newFocusMatcher(c.Focus)
	}
	return c.fm
}

func (c *Context) Name() string {
	if len(c.Argv) == 0 {
		return ""
	}
	return filepath.Base(c.Argv[0])
}

func (c *Context) Args() []string {
	if len(c.Argv) < 2 {
		return nil
	}
	return c.Argv[1:]
}

func (c *Context) Sub() string {
	args := c.Args()
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" || a == "--prefix" ||
			a == "--namespace" || a == "--config-env" || a == "--exec-path" {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}

func (c *Context) HasFlag(names ...string) bool {
	for _, a := range c.Args() {
		if a == "--" {
			return false
		}
		for _, n := range names {
			if a == n || (strings.HasPrefix(n, "--") && strings.HasPrefix(a, n+"=")) {
				return true
			}
		}
	}
	return false
}

func (c *Context) Failed() bool { return c.Exit != 0 }

type Filter interface {
	Name() string
	Match(c *Context) bool
	Apply(c *Context, out string) (result string, ok bool)
}

type Faithful interface {
	Faithful(c *Context) bool
}

type Streamer interface {
	Stream(c *Context) bool
}

var (
	regMu    sync.RWMutex
	registry []Filter
)

func Register(f Filter) {
	regMu.Lock()
	defer regMu.Unlock()
	registry = append(registry, f)
}

func Filters() []Filter {
	regMu.RLock()
	defer regMu.RUnlock()
	out := append([]Filter(nil), registry...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

func Find(c *Context) Filter {
	regMu.RLock()
	defer regMu.RUnlock()
	for _, f := range registry {
		if f.Match(c) {
			return f
		}
	}
	return nil
}

type Options struct {
	Budget     int
	MinSavings float64
	NoGuard    bool
	MaxChars   int
	MaxLines   int
	Cut        Cut
	Mode       Mode
	Focus      *Focus
	Pressure   Pressure

	Judge        Judge
	Task         string
	JudgeTimeout time.Duration

	jr *judgeRun
}

type Cut uint8

const (
	CutEither Cut = iota
	CutHead
	CutTail
)

func FitArg(n int, cut Cut) string {
	s := strconv.Itoa(n)
	switch cut {
	case CutHead:
		return "head:" + s
	case CutTail:
		return "tail:" + s
	}
	return s
}

func ParseFit(s string) (n int, cut Cut, ok bool) {
	switch {
	case strings.HasPrefix(s, "head:"):
		s, cut = s[len("head:"):], CutHead
	case strings.HasPrefix(s, "tail:"):
		s, cut = s[len("tail:"):], CutTail
	}
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, CutEither, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, CutEither, false
	}
	return n, cut, true
}

const (
	DefaultBudget     = 8000
	DefaultMinSavings = 0.10
	SmallOutput       = 150
	MinFitLines       = 5
)

type Result struct {
	Output         string
	Filter         string
	RawTokens      int
	OutTokens      int
	RawLines       int
	OutLines       int
	Lossy          bool
	GuardAdded     int
	FilterPanic    string
	Mode, ViewMode Mode
	Notes          []string
}

func (r Result) Saved() float64 {
	if r.RawTokens == 0 {
		return 0
	}
	return 1 - float64(r.OutTokens)/float64(r.RawTokens)
}

func Process(c *Context, raw string, opt Options) Result {
	if opt.jr == nil {
		opt.jr = newJudgeRun(c, opt)
	}
	res := process(c, raw, opt)
	if opt.Focus.Empty() || !res.Lossy {
		return res
	}
	// Focus may reorder what a cut keeps, never cost error lines.
	pc := *c
	popt := opt
	popt.Focus = nil
	plain := process(&pc, raw, popt)
	clean := textutil.Clean(raw)
	if losesErrors(clean, res.Output, plain.Output) {
		c.Focus, c.fm = nil, nil
		return plain
	}
	if !hasFocusNote(res.Notes) {
		if n := c.fm.note(res.Output, c.fm.gain(res.Output, plain.Output)); n != "" {
			res.Notes = append([]string{n}, res.Notes...)
		}
	}
	return res
}

// losesErrors: some error line the plain view shows is missing from the
// focused one.
func losesErrors(clean, focused, plain string) bool {
	gone := map[string]bool{}
	for _, ln := range missingErrorLines(clean, plain, -1) {
		gone[strings.Join(strings.Fields(ln), " ")] = true
	}
	for _, ln := range missingErrorLines(clean, focused, -1) {
		if !gone[strings.Join(strings.Fields(ln), " ")] {
			return true
		}
	}
	return false
}

func hasFocusNote(notes []string) bool {
	for _, n := range notes {
		if strings.HasPrefix(n, "focus: ") {
			return true
		}
	}
	return false
}

func process(c *Context, raw string, opt Options) (res Result) {
	res.Mode, res.ViewMode = opt.Mode, opt.Mode.View(c.Failed())
	knobs := res.ViewMode.knobs()
	opt.Budget = opt.Mode.Budget(opt.Budget, c.Failed())
	modeBudget := opt.Budget
	opt.Budget = opt.Pressure.Budget(opt.Budget)
	receiptGate := false
	if opt.MinSavings == 0 {
		opt.MinSavings = DefaultMinSavings
		if knobs.receiptGate {
			opt.MinSavings, receiptGate = 0, true
		}
	}
	c.Budget, c.Mode, c.Focus = opt.Budget, res.ViewMode, opt.Focus
	c.fm = newFocusMatcher(opt.Focus)
	c.jr = opt.jr
	res.RawTokens = tokens.Count(raw)
	res.RawLines = countLines(raw)
	clean := textutil.Clean(raw)
	cleanTokens := res.RawTokens
	if clean != raw {
		cleanTokens = tokens.Count(clean)
	}

	finish := func(out, name string, lossy bool) Result {
		res.Output, res.Filter, res.Lossy = out, name, lossy
		if !lossy {
			res.Notes = nil
		}
		res.OutTokens = tokens.Count(out)
		res.OutLines = countLines(out)
		if name == "passthrough" {
			res.OutTokens, res.OutLines = res.RawTokens, res.RawLines
		}
		return res
	}
	natural := clean == textutil.TrimTrailingSpace(raw)

	if MachineReadableAny(c) {
		return finish(strings.TrimRight(raw, "\n"), "passthrough", false)
	}

	overCap := opt.MaxChars > 0 && len(clean) > opt.MaxChars
	if natural && opt.MaxChars > 0 && len(raw) > opt.MaxChars {
		natural = false
	}

	whole := func() Result {
		if natural {
			return finish(clean, "passthrough", false)
		}
		return finish(clean, "normalize", false)
	}
	fit := max(opt.MaxLines, 0)
	if fit > 0 && fit < MinFitLines {
		if !overCap {
			return whole()
		}
		fit = 0
	}

	overLines := fit > 0 && countLines(clean) > fit
	if cleanTokens <= SmallOutput && !overCap && !overLines {
		return whole()
	}

	out, name := clean, "generic"
	guard, errorsFirst, faithful := !opt.NoGuard, true, false
	if f, fc := Resolve(c); f != nil {
		h0 := c.fm.mark()
		if r, ok, perr := safeApply(f, fc, clean); perr != "" || !ok {
			res.FilterPanic = perr
			c.fm.rollback(h0)
		} else {
			out, name = r, f.Name()
			if fa, ok := f.(Faithful); ok && fa.Faithful(fc) {
				faithful = true
			}
			if g, ok := f.(Guarded); ok && g.GuardsErrors() {
				guard = false
			}
			if ct, ok := f.(Content); ok && ct.IsContent() {
				guard, errorsFirst = false, false
			}
		}
	}
	if name == "generic" {
		var shape string
		out, shape = GenericShape(c, clean)
		if shape == "json" || shape == "paths" {
			guard, errorsFirst = false, false
		}
		if f := DetectedFilter(shape); f != nil {
			name = shape
			if g, ok := f.(Guarded); ok && g.GuardsErrors() {
				guard = false
			}
			if ct, ok := f.(Content); ok && ct.IsContent() {
				guard, errorsFirst = false, false
			}
		}
	}
	if guard {
		var added int
		out, added = Guard(clean, out)
		res.GuardAdded = added
	}
	before := out
	maxLines := 0
	if overLines {
		maxLines = fit - 1
	}
	budget, pressured := modeBudget, false
	var errs []string
	if b := opt.Budget; b < modeBudget && len(before) > b && tokens.Count(before) > b {
		if errorsFirst {
			var need int
			need, errs = errorNeed(before, knobs.errs)
			b = max(b, min(modeBudget, need))
		}
		budget, pressured = b, b < modeBudget
	}
	pressed := false
	var hits []focusHit
	view := func(lines int, cut Cut) string {
		cutTo := func(b int) (string, []focusHit) {
			return budgetFocus(before, b, opt.MaxChars, lines, cut, c.Failed(), errorsFirst, knobs.errs, c.fm)
		}
		v, h := cutTo(budget)
		pressed = false
		if pressured && v != before {
			full, fh := cutTo(modeBudget)
			if lostErrors(errs, v, full) {
				v, h = full, fh
				if mid := (budget + modeBudget) / 2; mid > budget {
					if mv, mh := cutTo(mid); !lostErrors(errs, mv, full) {
						v, h = mv, mh
					}
				}
			}
			pressed = v != full
		}
		hits = h
		return v
	}
	out = view(maxLines, opt.Cut)
	lossy := !faithful || out != before || res.GuardAdded > 0

	if fit > 0 && !overLines && viewLines(out, lossy) > fit {
		if !overCap {
			return whole()
		}

		out = view(fit-1, CutEither)
		lossy = !faithful || out != before || res.GuardAdded > 0
	}
	if lossy {
		if n := c.fm.note(out, hits); n != "" {
			res.Notes = append(res.Notes, n)
		}
		if pressed {
			res.Notes = append(res.Notes, opt.Pressure.note())
		}
		if n := c.jr.note(out); n != "" {
			res.Notes = append(res.Notes, n)
		}
	}

	if !overCap && !overLines {
		outTokens := tokens.Count(out)
		worse := float64(outTokens) > float64(cleanTokens)*(1-opt.MinSavings)
		if receiptGate && lossy {
			worse = outTokens+receiptTokens(res, out, outTokens)+notesTokens(res.Notes) >= cleanTokens
		}
		if worse {
			return whole()
		}
	}
	return finish(out, name, lossy)
}

func safeApply(f Filter, c *Context, in string) (out string, ok bool, panicMsg string) {
	defer func() {
		if r := recover(); r != nil {
			out, ok, panicMsg = "", false, fmt.Sprint(r)
		}
	}()
	out, ok = f.Apply(c, in)
	return out, ok, ""
}

func Receipt(r Result, id string) string {
	pct := int(r.Saved()*100 + 0.5)
	s := fmt.Sprintf("[lx: %s→%s lines (−%d%%)", humanInt(r.RawLines), humanInt(r.OutLines), pct)
	if note := modeNote(r.Mode, r.ViewMode); note != "" {
		s += " · " + note
	}
	if id != "" {
		s += " · full output: lx show " + id
	}
	return s + "]"
}

func notesTokens(notes []string) int {
	if len(notes) == 0 {
		return 0
	}
	return tokens.Count(" · " + strings.Join(notes, " · "))
}

func WithNotes(receipt string, notes []string, room int) string {
	for _, n := range notes {
		s := strings.TrimSuffix(receipt, "]") + " · " + n + "]"
		if room > 0 && len(s) >= room {
			continue
		}
		receipt = s
	}
	return receipt
}

func viewLines(out string, lossy bool) int {
	if lossy {
		return countLines(out) + 1
	}
	return countLines(out)
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func humanInt(n int) string {
	s := fmt.Sprint(n)
	if n < 10000 {
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
