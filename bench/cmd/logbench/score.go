//go:build unix

package main

import (
	"slices"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

// <*> reads as .+?; a match ends where the line does, as loghub puts the message last
type pattern struct {
	segs        []string
	lead, trail bool
	consts      int
	glob        string
}

func compile(tmpl string) pattern {
	parts := strings.Split(tmpl, "<*>")
	p := pattern{lead: strings.TrimSpace(parts[0]) == "", trail: strings.TrimSpace(parts[len(parts)-1]) == ""}
	for i, s := range parts {
		s = squeeze(s)
		if i == 0 {
			s = strings.TrimLeft(s, " ")
		}
		if i == len(parts)-1 {
			s = strings.TrimRight(s, " ")
		}
		if strings.TrimSpace(s) == "" {
			continue
		}
		p.segs = append(p.segs, s)
		p.consts += len(strings.ReplaceAll(s, " ", ""))
	}
	p.glob = string(star) + strings.TrimSpace(squeeze(strings.ReplaceAll(tmpl, "<*>", string(star))))
	return p
}

func (p pattern) trivial() bool { return p.consts < 4 }

func (p pattern) match(t string) bool {
	if len(p.segs) == 0 {
		return false
	}
	pos := 0
	for i, s := range p.segs {
		lo := pos
		if i > 0 || p.lead {
			lo++
		}
		if lo > len(t) {
			return false
		}
		if i == len(p.segs)-1 && !p.trail {
			return len(t)-len(s) >= lo && strings.HasSuffix(t, s)
		}
		j := strings.Index(t[lo:], s)
		if j < 0 {
			return false
		}
		pos = lo + j + len(s)
	}
	return pos < len(t)
}

// constant bytes a cut-short line shows, if it starts some line of the template
func (p pattern) prefix(t string) int {
	best, pos, got := 0, 0, 0
	for i, s := range p.segs {
		lo := pos
		if i > 0 || p.lead {
			lo++
		}
		if lo > len(t) {
			break
		}
		for k := min(len(s)-1, len(t)-lo); k > 0; k-- {
			if t[len(t)-k:] == s[:k] {
				best = max(best, got+k)
				break
			}
		}
		if i == len(p.segs)-1 && !p.trail {
			if len(t)-len(s) >= lo && strings.HasSuffix(t, s) {
				best = max(best, got+len(s))
			}
			break
		}
		j := strings.Index(t[lo:], s)
		if j < 0 {
			break
		}
		pos, got = lo+j+len(s), got+len(s)
		best = max(best, got)
	}
	return best
}

const prefixEvidence = 16

func (p pattern) shows(whole, cut string) bool {
	return p.match(whole) || cut != "" && p.prefix(cut) >= min(prefixEvidence, p.consts)
}

const star = '\x00'

// a count line with placeholders of its own, like laya's routine lines
var placeholder = lazyre.New(`<(?:\*|N|H|TS|DATE|TIME|UUID|IP|HEX|DUR|SIZE)>`)

func globOf(whole, cut string) string {
	if !strings.Contains(whole, "<") || !placeholder.MatchString(whole) {
		return ""
	}
	if cut != "" {
		return placeholder.ReplaceAllString(cut, string(star)) + string(star)
	}
	return placeholder.ReplaceAllString(whole, string(star))
}

func (p pattern) names(glob string) bool {
	need := min(prefixEvidence, p.consts)
	lit := 0
	for _, w := range strings.Fields(strings.ReplaceAll(glob, string(star), " ")) {
		for _, s := range p.segs {
			if strings.Contains(s, w) {
				lit += len(w)
				break
			}
		}
	}
	return lit >= need && overlap(p.glob, glob) >= need
}

// most non-space literal bytes two globs line up in a common match, -1 if none
func overlap(a, b string) int {
	const none = -1 << 30
	n, m := len(a), len(b)
	next := make([]int, m+1)
	cur := make([]int, m+1)
	for j := range next {
		next[j] = none
	}
	for i := n; i >= 0; i-- {
		for j := m; j >= 0; j-- {
			best := none
			if i == n && j == m {
				best = 0
			}
			if i < n && a[i] == star {
				best = max(best, next[j])
				if j < m {
					best = max(best, cur[j+1])
				}
			}
			if j < m && b[j] == star {
				best = max(best, cur[j+1])
				if i < n {
					best = max(best, next[j])
				}
			}
			if i < n && j < m && a[i] != star && a[i] == b[j] {
				gain := 1
				if a[i] == ' ' {
					gain = 0
				}
				best = max(best, next[j+1]+gain)
			}
			cur[j] = best
		}
		next, cur = cur, next
	}
	return max(next[0], -1)
}

var (
	countHead = lazyre.New(`^\[×[\d,]+\]\s*`)
	countTail = lazyre.New(`\s*\[×[\d,]+(?:, last [^\]]*)?\]$`)
)

// cut drops a trailing ... or …, which a log line can also have for real
func viewLine(ln string) (whole, cut string) {
	s := strings.TrimSpace(ln)
	s = countHead.ReplaceAllString(s, "")
	s = countTail.ReplaceAllString(s, "")
	s = squeeze(strings.TrimSuffix(s, " (routine)"))
	c, ok := s, false
	if i := strings.Index(c, " …[+"); i >= 0 {
		c, ok = c[:i], true
	}
	for _, m := range []string{"...", "…"} {
		if strings.HasSuffix(c, m) {
			c, ok = strings.TrimSuffix(c, m), true
		}
	}
	if !ok {
		return s, ""
	}
	return s, strings.TrimSpace(c)
}

func squeeze(s string) string {
	if !strings.ContainsAny(s, "\t\r\n\v\f") && !strings.Contains(s, "  ") {
		return s
	}
	var b strings.Builder
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' || r == '\v' || r == '\f' {
			space = true
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	if space {
		b.WriteByte(' ')
	}
	return b.String()
}

type event struct {
	ID       string
	Template string
	Count    int
	pat      pattern
}

type truth struct {
	events []*event
	line   []*event
	text   []string
	exact  map[string][]*event
}

func (tr *truth) index(lines []string) {
	tr.text, tr.exact = nil, map[string][]*event{}
	for i, ln := range lines {
		w, _ := viewLine(ln)
		tr.text = append(tr.text, w)
		if e := tr.line[i]; !slices.Contains(tr.exact[w], e) {
			tr.exact[w] = append(tr.exact[w], e)
		}
	}
}

func (tr *truth) cutFrom(cut string) []*event {
	var evs []*event
	for i, t := range tr.text {
		if strings.HasPrefix(t, cut) && !slices.Contains(evs, tr.line[i]) {
			evs = append(evs, tr.line[i])
		}
	}
	return evs
}

type viewLines struct {
	whole, cut, glob []string
	flat             string
}

func splitView(view string) viewLines {
	var v viewLines
	var flat strings.Builder
	for _, ln := range strings.Split(view, "\n") {
		w, c := viewLine(ln)
		if w == "" {
			continue
		}
		v.whole = append(v.whole, w)
		v.cut = append(v.cut, c)
		v.glob = append(v.glob, globOf(w, c))
		flat.WriteString(w)
		flat.WriteByte('\n')
	}
	v.flat = flat.String()
	return v
}

// A line copied from the log shows its own template, not every template it matches.
func (tr *truth) covered(v viewLines, only map[*event]bool) map[*event]bool {
	got := map[*event]bool{}
	for i, w := range v.whole {
		c := v.cut[i]
		if evs, ok := tr.exact[w]; ok {
			for _, e := range evs {
				if only[e] {
					got[e] = true
				}
			}
			continue
		}
		if c != "" {
			if evs := tr.cutFrom(c); len(evs) > 0 {
				if e := evs[0]; len(evs) == 1 && only[e] && e.pat.prefix(c) >= min(prefixEvidence, e.pat.consts) {
					got[e] = true
				}
				continue
			}
		}
		for _, e := range tr.events {
			if only[e] && !got[e] && (e.pat.shows(w, c) || v.glob[i] != "" && e.pat.names(v.glob[i])) {
				got[e] = true
			}
		}
	}
	return got
}

type score struct {
	Templates int      `json:"templates_covered"`
	ErrKinds  int      `json:"error_kinds_kept"`
	ErrLines  int      `json:"error_lines_verbatim"`
	Missing   []string `json:"templates_missing,omitempty"`
}

type scorer struct {
	tr       *truth
	eligible map[*event]bool
	errLines []string
	errKinds []string
	errEvent map[*event]bool
}

func newScorer(raw string, tr *truth, isErr []bool) *scorer {
	s := &scorer{tr: tr}
	lines := strings.Split(strings.TrimRight(raw, "\n"), "\n")
	seen := map[string]bool{}
	for i, ln := range lines {
		if i >= len(isErr) || !isErr[i] {
			continue
		}
		t := squeeze(strings.TrimSpace(ln))
		if t == "" {
			continue
		}
		s.errLines = append(s.errLines, t)
		if tr == nil {
			if m := messageOf(t); m != "" && !seen[m] {
				seen[m] = true
				s.errKinds = append(s.errKinds, m)
			}
		}
	}
	if tr == nil {
		return s
	}
	tr.index(lines)
	s.eligible = map[*event]bool{}
	for i, e := range tr.line {
		if !s.eligible[e] && !e.pat.trivial() && e.pat.shows(viewLine(lines[i])) {
			s.eligible[e] = true
		}
	}
	s.errEvent = map[*event]bool{}
	for i, e := range tr.line {
		if e != nil && i < len(isErr) && isErr[i] && s.eligible[e] {
			s.errEvent[e] = true
		}
	}
	return s
}

func (s *scorer) score(view string) score {
	v := splitView(view)
	var sc score
	for _, ln := range s.errLines {
		if strings.Contains(v.flat, ln) {
			sc.ErrLines++
		}
	}
	if s.tr == nil {
		var norm strings.Builder
		var cut []string
		for i, w := range v.whole {
			norm.WriteString(messageOf(w))
			norm.WriteByte('\n')
			if c := v.cut[i]; c != "" {
				// the last word may be cut in half
				if m := messageOf(c[:max(0, strings.LastIndexByte(c, ' '))]); len(m) >= prefixEvidence {
					cut = append(cut, m)
				}
			}
		}
		have := norm.String()
		for _, m := range s.errKinds {
			kept := strings.Contains(have, m)
			for _, c := range cut {
				kept = kept || strings.HasPrefix(m, c)
			}
			if kept {
				sc.ErrKinds++
			}
		}
		return sc
	}
	got := s.tr.covered(v, s.eligible)
	sc.Templates = len(got)
	for e := range s.errEvent {
		if got[e] {
			sc.ErrKinds++
		}
	}
	for _, e := range s.tr.events {
		if s.eligible[e] && !got[e] {
			sc.Missing = append(sc.Missing, e.ID)
		}
	}
	return sc
}

// The corpus benchmark's message: a line's words without any that hold a digit.
func messageOf(line string) string {
	f := strings.Fields(line)
	out := f[:0]
	for _, w := range f {
		if !strings.ContainsAny(w, "0123456789") {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}

var alarmLevels = map[string]bool{
	"WARN": true, "WARNING": true, "ERROR": true, "ERR": true, "FATAL": true, "SEVERE": true,
	"CRITICAL": true, "CRIT": true, "ALERT": true, "EMERG": true, "EMERGENCY": true, "PANIC": true,
	"W": true, "E": true, "F": true,
}

var quietLevels = map[string]bool{
	"INFO": true, "DEBUG": true, "TRACE": true, "NOTICE": true, "VERBOSE": true, "FINE": true,
	"V": true, "D": true, "I": true,
}

// Linux's Level column holds a host name
func levelErrors(levels, lines []string) (isErr []bool, from string) {
	known := 0
	for _, l := range levels {
		u := strings.ToUpper(strings.TrimSpace(l))
		if alarmLevels[u] || quietLevels[u] {
			known++
		}
	}
	isErr = make([]bool, len(lines))
	if len(levels) == len(lines) && known*10 >= len(levels)*9 {
		for i, l := range levels {
			isErr[i] = alarmLevels[strings.ToUpper(strings.TrimSpace(l))]
		}
		return isErr, "level column"
	}
	for i, ln := range lines {
		isErr[i] = engine.Classify(ln) != engine.Normal
	}
	return isErr, "lx classifier"
}
