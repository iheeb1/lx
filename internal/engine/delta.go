package engine

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/iheeb1/lx/internal/lazyre"
	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
)

type Item struct {
	Key   string
	Block string
	Loc   string
}

type Identities interface {
	Items(c *Context, out string) []Item
}

type Prev struct {
	Ref      string
	TurnsAgo int
	Exit     int
	Raw      string
	Items    []Item
	MaxChars int

	shown func() string
}

const (
	deltaMaxFresh = 0.5
	deltaMinGain  = 0.3
	deltaMaxFixed = 30
	deltaMaxStill = 25
	deltaMaxLocs  = 3
)

var (
	deltaSummaryRe = lazyre.New(`^(?:ok  |FAIL)\t\S|^\[\d[\d,]* passed\b` +
		`|^Test Suites: |^Tests: +\d|^ *(?:Test Files|Tests|Errors) {2,}\d|^ ❯ \S.* \(\d+ tests?\b.*\)` +
		`|^ {2}\d+ (?:passing|failing|pending)\b` +
		`|^=+ .*\bin \d+(?:\.\d+)?s\b.*=+$|^\d+ (?:passed|failed|errors?|skipped|xfailed|xpassed|deselected)\b.*\bin \d+(?:\.\d+)?s\b` +
		`|^Found \d+ errors?\b|^✖ \d+ problems?\b` +
		"|^test result: |^error: (?:test failed|could not compile)\\b|^warning: `[^`]+` \\(.*\\) generated \\d+ warnings?" +
		`|^\[lx: .*\bexit(?:ed|s)?\b`)

	deltaTimingRe = lazyre.New(`\b\d+(?:\.\d+)?(?:ns|µs|us|ms|s|m|h)(?:\d+(?:\.\d+)?(?:ns|µs|us|ms|s|m|h))*\b` +
		`|\b\d+(?:\.\d+)? (?:ms|s|secs?|seconds?)\b` +
		`|\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2}(?:[.,]\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?|\b\d{1,2}:\d{2}:\d{2}(?:[.,]\d+)?\b`)
	deltaRunnerTimeRe = lazyre.New(`\(\d+(?:\.\d+)? ?(?:ns|µs|us|ms|s)\)|\bin \d+(?:\.\d+)?s\b|\t\d+(?:\.\d+)?s$|\(cached\)`)
	deltaAssertRe     = lazyre.New(`(?i)\b(?:want(?:ed)?|got|expect(?:ed)?|received|actual|assert\w*|should|must)\b`)
	deltaLocRe        = lazyre.New(`[\w./-]+\.\w+[:(]\d+`)
	deltaNoiseRe      = lazyre.New(`\b0x[0-9a-fA-F]+\b|\b(?:goroutine|node|pid|PID) ?[:=]?\d+\b`)
	deltaWhereRe      = lazyre.New(`(\.(?:go|ts|tsx|js|jsx|mjs|cjs|py|rs|rb|java|kt|c|h|cc|cpp|cs|php|swift|vue|svelte))(?::\d+(?::\d+)?|\(\d+(?:,\d+)?\))` +
		`|^(>? ?)\d+( ?\|)|^\d+\) `)
)

func ItemsOf(c *Context, clean string) (items []Item, ok bool) {
	defer func() {
		if recover() != nil {
			items, ok = nil, false
		}
	}()
	if c == nil {
		return nil, false
	}
	f, fc := Resolve(c)
	id, ok := f.(Identities)
	if !ok {
		return nil, false
	}
	return id.Items(fc, clean), true
}

func Rerun(c *Context, prev Prev, raw, view string) (string, bool) {
	low := seenBudget(c, prev.Exit)
	prev.shown = sync.OnceValue(func() string {
		sc := *c
		sc.Exit, sc.Budget, sc.Mode, sc.fm = prev.Exit, low, ModeAuto, nil
		clean := textutil.Clean(prev.Raw)
		if f, fc := Resolve(&sc); f != nil {
			if v, ok, _ := safeApply(f, fc, clean); ok && tokens.Count(v) <= low && (prev.MaxChars <= 0 || len(v) <= prev.MaxChars) {
				return v
			}
		}
		return Process(&sc, clean, Options{Budget: low, MaxChars: prev.MaxChars, Focus: c.Focus}).Output
	})
	if prev.Exit == c.Exit && (prev.Raw == raw || SameExceptTimings(prev.Raw, raw)) {
		out, ok := Same(prev, view, prev.Raw != raw)
		if !ok || (prev.Raw != raw || low != c.Budget) && unseen(view, deltaSummary(view), nil, nil, prev.seen) {
			return "", false
		}
		return out, true
	}
	clean := textutil.Clean(raw)
	cur, ok := ItemsOf(c, clean)
	if !ok || c.Failed() && len(cur) == 0 {
		return "", false
	}
	pc := *c
	pc.Exit = prev.Exit
	if prev.Items, ok = ItemsOf(&pc, textutil.Clean(prev.Raw)); !ok || refuted(prev.Items, cur, clean) {
		return "", false
	}
	return Delta(prev, cur, view)
}

// The earlier view may have been cut harder than this one: another mode, or verify on a passing run.
func seenBudget(c *Context, prevExit int) int {
	b := c.Budget
	if b <= 0 {
		b = DefaultBudget
	}
	if k := c.Mode.knobs(); k.num > k.den {
		b = b * k.den / k.num
	}
	if prevExit == 0 && c.Mode != ModeVerify && c.Mode != ModeMinimal {
		b = ModeVerify.knobs().budget(b)
	}
	return b
}

func Delta(prev Prev, cur []Item, view string) (string, bool) {
	old, cur := uniqItems(prev.Items), uniqItems(cur)
	if len(old) == 0 && len(cur) == 0 {
		return "", false
	}
	oldKeys, oldBy := groupItems(old)
	curKeys, curBy := groupItems(cur)
	var fresh, changed []Item
	var still []stillLine
	var fixed []string
	for _, k := range curKeys {
		c, p := curBy[k], oldBy[k]
		switch {
		case len(p) == 0:
			fresh = append(fresh, c...)
		case len(c) > len(p) || !sameBlocks(p, c):
			changed = append(changed, c...)
		default:
			locs := itemLocs(c)
			still = append(still, stillLine{c, k + locText(k, len(c), locs), strings.Join(locs, " ") != strings.Join(itemLocs(p), " ")})
			if n := len(p) - len(c); n > 0 {
				fixed = append(fixed, fmt.Sprintf("%s (%d of %d)", k, n, len(p)))
			}
		}
	}
	for _, k := range oldKeys {
		if len(curBy[k]) == 0 {
			fixed = append(fixed, k)
		}
	}
	if float64(len(fresh)+len(changed)) > deltaMaxFresh*float64(len(cur)) {
		return "", false
	}

	var b strings.Builder
	b.WriteString(deltaHeader(prev, "same command as", "", " — only what changed:"))
	writeBlocks(&b, "new:", fresh)
	writeBlocks(&b, "changed:", changed)
	if len(fixed) > 0 {
		if len(fixed) > deltaMaxFixed {
			fixed = append(fixed[:deltaMaxFixed], fmt.Sprintf("… +%d more", len(fixed)-deltaMaxFixed))
		}
		b.WriteString("fixed: " + strings.Join(fixed, ", ") + "\n")
	}
	listed := writeStill(&b, still)
	sum := deltaSummary(view)
	for _, s := range sum {
		b.WriteString(s + "\n")
	}
	out, ok := shrinks(b.String(), view)
	if !ok || unseen(view, sum, listed, slices.Concat(fresh, changed), prev.seen) {
		return "", false
	}
	return out, true
}

func (p Prev) seen() string {
	if p.shown != nil {
		return p.shown()
	}
	return p.Raw
}

func Same(prev Prev, view string, timings bool) (string, bool) {
	what := ""
	if timings {
		what = " apart from timings"
	}
	var b strings.Builder
	b.WriteString(deltaHeader(prev, "output identical to", what, ""))
	for _, s := range deltaSummary(view) {
		b.WriteString(s + "\n")
	}
	return shrinks(b.String(), view)
}

func SameExceptTimings(a, b string) bool {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	if len(al) != len(bl) {
		return false
	}
	for i := range al {
		if al[i] == bl[i] {
			continue
		}
		ra, rb := deltaRunnerTimeRe.ReplaceAllString(al[i], "<t>"), deltaRunnerTimeRe.ReplaceAllString(bl[i], "<t>")
		if ra == rb {
			continue
		}
		if !timingOnly(ra) || !timingOnly(rb) ||
			deltaTimingRe.ReplaceAllString(al[i], "<t>") != deltaTimingRe.ReplaceAllString(bl[i], "<t>") {
			return false
		}
	}
	return true
}

// timingOnly: a line whose numbers may differ between runs only as
// timings. Error lines, lines with a source location and assertion
// messages ("want 30s, got 45s") must match exactly.
func timingOnly(ln string) bool {
	return !IsError(ln) && !deltaAssertRe.MatchString(ln) && !deltaLocRe.MatchString(ln)
}

func deltaHeader(prev Prev, lead, what, tail string) string {
	return fmt.Sprintf("[lx: %s %s%s (%s, still in your context)%s]\n", lead, prev.Ref, what, turnsAgo(prev.TurnsAgo), tail)
}

func turnsAgo(n int) string {
	switch {
	case n <= 0:
		return "earlier this turn"
	case n == 1:
		return "1 turn ago"
	}
	return strconv.Itoa(n) + " turns ago"
}

func shrinks(out, view string) (string, bool) {
	out = strings.TrimRight(out, "\n")
	if float64(tokens.Count(out)) > (1-deltaMinGain)*float64(tokens.Count(view)) {
		return "", false
	}
	return out, true
}

func writeBlocks(b *strings.Builder, label string, items []Item) {
	if len(items) == 0 {
		return
	}
	b.WriteString(label + "\n")
	for _, it := range items {
		b.WriteString(strings.TrimRight(it.Block, "\n") + "\n")
	}
}

func uniqItems(items []Item) []Item {
	seen := make(map[Item]bool, len(items))
	out := items[:0:0]
	for _, it := range items {
		if it.Key == "" || seen[it] {
			continue
		}
		seen[it] = true
		out = append(out, it)
	}
	return out
}

func groupItems(items []Item) ([]string, map[string][]Item) {
	var keys []string
	by := make(map[string][]Item, len(items))
	for _, it := range items {
		if by[it.Key] == nil {
			keys = append(keys, it.Key)
		}
		by[it.Key] = append(by[it.Key], it)
	}
	return keys, by
}

func sameBlocks(prev, cur []Item) bool {
	return sameBy(prev, cur, func(s string) string { return s }) || sameBy(prev, cur, normBlock)
}

func sameBy(prev, cur []Item, key func(string) string) bool {
	left := make(map[string]int, len(prev))
	for _, it := range prev {
		left[key(it.Block)]++
	}
	for _, it := range cur {
		k := key(it.Block)
		if left[k] == 0 {
			return false
		}
		left[k]--
	}
	return true
}

func normBlock(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, ln := range lines {
		lines[i] = normLine(ln)
	}
	return strings.Join(lines, "\n")
}

func normLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if !hasDigit(s) {
		return s
	}
	s = deltaTimingRe.ReplaceAllString(s, "<t>")
	s = deltaNoiseRe.ReplaceAllString(s, "<n>")
	return deltaWhereRe.ReplaceAllString(s, "$1$2<l>$3")
}

type stillLine struct {
	items []Item
	text  string
	moved bool
}

func writeStill(b *strings.Builder, still []stillLine) (listed []Item) {
	if len(still) == 0 {
		return nil
	}
	b.WriteString("still failing:\n")
	shown, moved := still, 0
	if len(still) > deltaMaxStill {
		shown = nil
		for _, s := range still {
			if s.moved {
				moved++
				if len(shown) < deltaMaxStill {
					shown = append(shown, s)
				}
			}
		}
	}
	for _, s := range shown {
		b.WriteString("  " + s.text + "\n")
		listed = append(listed, s.items...)
	}
	switch rest := len(still) - len(shown); {
	case rest > 0 && moved > len(shown):
		fmt.Fprintf(b, "  … +%d more, some at new lines (all in the full output)\n", rest)
	case rest > 0:
		fmt.Fprintf(b, "  … +%d more, at the same lines as before\n", rest)
	}
	return listed
}

func itemLocs(items []Item) []string {
	var locs []string
	seen := map[string]bool{}
	for _, it := range items {
		l := it.Loc
		if l == "" {
			if all := AppLocations(it.Block); len(all) > 0 {
				l = strings.Replace(all[0], "(", ":", 1)
			}
		}
		if l != "" && !seen[l] {
			seen[l] = true
			locs = append(locs, l)
		}
	}
	return locs
}

func locText(key string, n int, locs []string) string {
	s := ""
	if n > 1 {
		s = fmt.Sprintf(" ×%d", n)
	}
	if len(locs) == 0 {
		return s
	}
	lines := make([]string, 0, len(locs))
	for _, l := range locs {
		if path, line, ok := SplitLoc(l); ok && strings.HasPrefix(key, path) {
			lines = append(lines, line)
		}
	}
	short := len(lines) == len(locs)
	if short {
		locs = lines
	}
	if len(locs) > deltaMaxLocs {
		locs = append(locs[:deltaMaxLocs], "…")
	}
	switch {
	case short && len(lines) == 1:
		return s + " (line " + locs[0] + ")"
	case short:
		return s + " (lines " + strings.Join(locs, ", ") + ")"
	}
	return s + " (" + strings.Join(locs, ", ") + ")"
}

func deltaSummary(view string) []string {
	var out []string
	for _, ln := range strings.Split(view, "\n") {
		t := strings.TrimLeft(ln, " ")
		if t != "" && strings.IndexByte("oF[TE=0123456789tew\xe2", t[0]) >= 0 && deltaSummaryRe.MatchString(ln) {
			out = append(out, ln)
		}
	}
	return out
}

func unseen(view string, sum []string, listed, printed []Item, shown func() string) bool {
	done := make(map[string]bool, len(sum))
	for _, ln := range sum {
		done[ln] = true
	}
	before := strings.Split(shown(), "\n")
	for _, ln := range before {
		done[ln] = true
	}
	var loose []string
	for _, ln := range strings.Split(view, "\n") {
		if done[ln] || strings.TrimSpace(ln) == "" || strings.HasPrefix(ln, "[lx: ") {
			continue
		}
		done[ln] = true
		loose = append(loose, looseKey(ln))
	}
	if len(loose) == 0 {
		return false
	}
	in := map[string]bool{}
	for _, it := range printed {
		for _, ln := range strings.Split(it.Block, "\n") {
			in[looseKey(ln)] = true
		}
	}
	loose = slices.DeleteFunc(loose, func(k string) bool {
		return in[k] || namesItem(k, printed, 3) || namesItem(k, listed, 8)
	})
	if len(loose) == 0 {
		return false
	}
	seen := linesLike(before, loose, looseKey)
	return slices.ContainsFunc(loose, func(k string) bool { return !seen[k] })
}

func looseKey(ln string) string {
	k := normLine(ln)
	if Classify(ln) != Normal || !hasDigit(k) {
		return k
	}
	var b strings.Builder
	num := false
	for _, r := range k {
		if r >= '0' && r <= '9' {
			if !num {
				b.WriteByte('#')
			}
			num = true
			continue
		}
		num = false
		b.WriteRune(r)
	}
	return b.String()
}

func refuted(prev, cur []Item, raw string) bool {
	now := make(map[string]bool, len(cur))
	for _, it := range cur {
		now[it.Key] = true
	}
	var want []string
	for _, it := range prev {
		name := keyName(it.Key)
		if now[it.Key] || len(name) < 3 {
			continue
		}
		for _, ln := range strings.Split(it.Block, "\n") {
			if strings.Contains(ln, name) {
				want = append(want, normLine(ln))
			}
		}
	}
	if len(want) == 0 {
		return false
	}
	have := linesLike(strings.Split(raw, "\n"), want, normLine)
	return slices.ContainsFunc(want, func(k string) bool { return have[k] })
}

func linesLike(lines, want []string, norm func(string) string) map[string]bool {
	var anchors []string
	for _, k := range want {
		a := ""
		for _, w := range strings.Fields(k) {
			if len(w) > len(a) && !strings.ContainsAny(w, "0123456789<#") {
				a = w
			}
		}
		if len(a) < 3 || len(anchors) == 64 {
			anchors = nil
			break
		}
		anchors = append(anchors, a)
	}
	set := map[string]bool{}
	for _, ln := range lines {
		if anchors == nil || slices.ContainsFunc(anchors, func(a string) bool { return strings.Contains(ln, a) }) {
			set[norm(ln)] = true
		}
	}
	return set
}

func namesItem(line string, items []Item, least int) bool {
	for _, it := range items {
		if name := keyName(it.Key); len(name) >= least && strings.Contains(line, name) {
			return true
		}
	}
	return false
}

func keyName(key string) string {
	key = strings.Join(strings.Fields(key), " ")
	cut := -1
	for _, sep := range []string{" › ", " > ", "::", ": "} {
		if i := strings.LastIndex(key, sep); i >= 0 && i+len(sep) > cut {
			cut = i + len(sep)
		}
	}
	if cut < 0 {
		cut = strings.LastIndexByte(key, ' ') + 1
	}
	return strings.TrimSpace(key[cut:])
}
