package engine

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

type Focus struct {
	Terms []FocusTerm
	Files []string
}

type FocusTerm struct {
	Text   string
	Weight float64
}

func (f *Focus) Empty() bool { return f == nil || len(f.Terms) == 0 && len(f.Files) == 0 }

type Pressure struct {
	Used   int
	Window int
}

func (p Pressure) Known() bool { return p.Used > 0 && p.Window > 0 }

func (p Pressure) Fraction() float64 {
	if !p.Known() {
		return 0
	}
	return float64(p.Used) / float64(p.Window)
}

func (p Pressure) Budget(b int) int {
	if !p.Known() {
		return b
	}
	scale, floor := 1.0, 0
	switch f := p.Fraction(); {
	case f >= 0.9:
		scale, floor = 0.4, 1500
	case f >= 0.75:
		scale, floor = 0.6, 2500
	case f >= 0.5:
		scale = 0.8
	default:
		return b
	}
	return min(b, max(int(float64(b)*scale), floor, 1))
}

func (p Pressure) note() string {
	pct := min(int(p.Fraction()*100), 100)
	return fmt.Sprintf("context %d%% full", pct)
}

const (
	maxFocusPats   = 64
	minFocusTerm   = 3
	focusShare     = 0.75
	maxFocusNames  = 3
	maxFocusNote   = 60
	focusProbeLen  = 200
	focusSpreadDiv = 3
	focusSpreadMin = 10
)

type focusPat struct {
	text   string
	name   string
	weight float64
	fold   bool
	file   bool
	dirs   []string
}

type focusHit struct {
	probe string
	mask  uint64
}

type focusMatcher struct {
	pats []focusPat
	hits []focusHit

	rankedText string
	ranked     []focusLine
}

func newFocusMatcher(f *Focus) *focusMatcher {
	if f.Empty() {
		return nil
	}
	m := &focusMatcher{}
	seen, seenFile := map[string]bool{}, map[string]bool{}
	for _, t := range f.Terms {
		text := strings.TrimSpace(t.Text)
		if len(text) < minFocusTerm || seen[text] || strings.ContainsAny(text, "\r\n") {
			continue
		}
		seen[text] = true
		p := focusPat{text: text, name: text, weight: focusWeight(t.Weight), fold: plainWord(text)}
		if p.fold {
			p.text = asciiLower(text)
		}
		m.pats = append(m.pats, p)
	}
	for _, file := range f.Files {
		file = strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(file), "\\", "/"), "/")
		parts := strings.Split(file, "/")
		base := parts[len(parts)-1]
		if len(base) < 2 || base == ".." || seenFile[file] {
			continue
		}
		seenFile[file] = true
		var dirs []string
		for i := len(parts) - 2; i >= 0; i-- {
			d := parts[i]
			if d == ".." || d == "~" {
				break
			}
			if d != "" && d != "." {
				dirs = append(dirs, d)
			}
		}
		m.pats = append(m.pats, focusPat{text: base, name: base, weight: 1, file: true, dirs: dirs})
	}
	if len(m.pats) == 0 {
		return nil
	}
	sort.SliceStable(m.pats, func(i, j int) bool { return m.pats[i].weight > m.pats[j].weight })
	if len(m.pats) > maxFocusPats {
		m.pats = m.pats[:maxFocusPats]
	}
	return m
}

func focusWeight(w float64) float64 {
	if w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
		return 1
	}
	return w
}

func plainWord(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c == ' ', c == '-':
		case c >= 'A' && c <= 'Z' && i == 0:
		default:
			return false
		}
	}
	return true
}

func wordish(b byte) bool { return isWordByte(b) || b >= 0x80 }

func pathByte(b byte) bool {
	return b > ' ' && !strings.ContainsRune("/\\\"'`()[]{}<>:=,;|", rune(b))
}

func (p *focusPat) in(s string) bool {
	for off := 0; off < len(s); {
		i := strings.Index(s[off:], p.text)
		if i < 0 {
			return false
		}
		i += off
		if p.bounded(s, i, i+len(p.text)) {
			return true
		}
		off = i + 1
	}
	return false
}

func (p *focusPat) bounded(s string, i, j int) bool {
	if j < len(s) && wordish(s[j]) && wordish(p.text[len(p.text)-1]) {
		return false
	}
	if !p.file {
		return i == 0 || !wordish(s[i-1]) || !wordish(p.text[0])
	}
	if i > 0 && (wordish(s[i-1]) || s[i-1] == '.' || s[i-1] == '-') {
		return false
	}
	for _, d := range p.dirs {
		if i == 0 || s[i-1] != '/' && s[i-1] != '\\' {
			return true
		}
		start := i - 1
		for start > 0 && pathByte(s[start-1]) {
			start--
		}
		switch comp := s[start : i-1]; comp {
		case "", ".", "..", "~":
			return true
		case d:
			i = start
		default:
			return false
		}
	}
	return true
}

func (m *focusMatcher) files(text string) uint64 {
	var mask uint64
	for k := range m.pats {
		if p := &m.pats[k]; p.file && p.in(text) {
			mask |= 1 << k
		}
	}
	return mask
}

func (m *focusMatcher) weight(mask uint64) float64 {
	w := 0.0
	for k := range m.pats {
		if mask&(1<<k) != 0 {
			w += m.pats[k].weight
		}
	}
	return w
}

type focusLine struct {
	i    int
	mask uint64
	w    float64
}

func (m *focusMatcher) rank(text string) []focusLine {
	if m == nil {
		return nil
	}
	if m.ranked == nil || text != m.rankedText {
		m.rankedText, m.ranked = text, m.rankLines(text)
	}
	return m.ranked
}

func (m *focusMatcher) rankLines(text string) []focusLine {
	var starts []int
	nonBlank := 0
	for off := 0; ; {
		starts = append(starts, off)
		j := strings.IndexByte(text[off:], '\n')
		end := len(text)
		if j >= 0 {
			end = off + j
		}
		if strings.TrimSpace(text[off:end]) != "" {
			nonBlank++
		}
		if j < 0 {
			break
		}
		off = end + 1
	}
	masks := make([]uint64, len(starts))
	counts := make([]int, len(m.pats))
	low, lowered := "", false
	for k := range m.pats {
		p := &m.pats[k]
		s := text
		if p.fold {
			if !lowered {
				low, lowered = asciiLower(text), true
			}
			s = low
		}
		for pos := 0; pos < len(s); {
			i := strings.Index(s[pos:], p.text)
			if i < 0 {
				break
			}
			i += pos
			if !p.bounded(s, i, i+len(p.text)) {
				pos = i + 1
				continue
			}
			ln := sort.SearchInts(starts, i+1) - 1
			masks[ln] |= 1 << k
			counts[k]++
			if ln+1 >= len(starts) {
				break
			}
			pos = starts[ln+1]
		}
	}
	var broad uint64
	for k, n := range counts {
		if n > focusSpreadMin && n*focusSpreadDiv > nonBlank {
			broad |= 1 << k
		}
	}
	out := []focusLine{}
	for i, mk := range masks {
		if mk &^= broad; mk != 0 {
			out = append(out, focusLine{i, mk, m.weight(mk)})
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].w > out[b].w })
	return out
}

func newFocusHit(line string, mask uint64) focusHit {
	p := strings.TrimSpace(line)
	if len(p) > focusProbeLen {
		p = p[:focusProbeLen]
	}
	return focusHit{probe: p, mask: mask}
}

func (m *focusMatcher) mark() int {
	if m == nil {
		return 0
	}
	return len(m.hits)
}

func (m *focusMatcher) rollback(n int) {
	if m != nil && len(m.hits) > n {
		m.hits = m.hits[:n]
	}
}

func (m *focusMatcher) note(out string, more []focusHit) string {
	if m == nil || len(m.hits)+len(more) == 0 {
		return ""
	}
	shown := map[string]bool{}
	for _, ln := range strings.Split(out, "\n") {
		shown[newFocusHit(ln, 0).probe] = true
	}
	var mask uint64
	for _, hs := range [][]focusHit{m.hits, more} {
		for _, h := range hs {
			if h.mask&^mask != 0 && h.probe != "" && shown[h.probe] {
				mask |= h.mask
			}
		}
	}
	if mask == 0 {
		return ""
	}
	var names []string
	extra, size := 0, 0
	for k := range m.pats {
		if mask&(1<<k) == 0 {
			continue
		}
		name := shortName(m.pats[k].name)
		if slices.Contains(names, name) {
			continue
		}
		if len(names) == maxFocusNames || len(names) > 0 && size+len(name) > maxFocusNote {
			extra++
			continue
		}
		names = append(names, name)
		size += len(name) + 2
	}
	s := "focus: " + strings.Join(names, ", ")
	if extra > 0 {
		s += fmt.Sprintf(" +%d", extra)
	}
	return s
}

func shortName(s string) string {
	if len(s) <= 32 {
		return s
	}
	i := 31
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i] + "…"
}

func (m *focusMatcher) gain(focused, plain string) []focusHit {
	if m == nil {
		return nil
	}
	had := map[string]bool{}
	for _, ln := range strings.Split(plain, "\n") {
		had[strings.TrimSpace(ln)] = true
	}
	lines := strings.Split(focused, "\n")
	var hits []focusHit
	for _, fl := range m.rankLines(focused) {
		if fl.i < len(lines) && !had[strings.TrimSpace(lines[fl.i])] {
			hits = append(hits, newFocusHit(lines[fl.i], fl.mask))
		}
	}
	return hits
}
