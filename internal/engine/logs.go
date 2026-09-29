package engine

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/iheeb1/lx/internal/lazyre"
	"github.com/iheeb1/lx/internal/tokens"
)

var (
	logTSRe = lazyre.New(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2}\b| UTC\b)?` +
		`|\d{4}/\d{2}/\d{2}(?:[T ]| - )\d{2}:\d{2}:\d{2}(?:[.,]\d+)?` +
		`|\d{1,2}/(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)/\d{4}:\d{2}:\d{2}:\d{2}(?: [+-]\d{4})?` +
		`|\b(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) {1,2}\d{1,2} \d{2}:\d{2}:\d{2}` +
		`|^\d{6} \d{6}\b` +
		`|^\d{8}-\d{2}:\d{2}:\d{2}(?:[:.,]\d+)?\b` +
		`|^\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:[.,]\d+)?\b` +
		`|^\d{2}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}\b` +
		`|^\[?\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\b` +
		`|^(?:- )?\[?1\d{9}(?:\.\d+)?\b` +
		`|^\S+(?: \S+){1,4} 1\d{9} `)

	logLevelRe = lazyre.New(`\b(TRACE|DEBUG|INFO|NOTICE|WARN|WARNING|ERROR|FATAL|CRITICAL|SEVERE)\b` +
		`|\[([TDIWEF]|trace|debug|info|warn|warning|error|fatal)\]` +
		`|^([VDIWEF])/\S|^\d{2}-\d{2} [\d:.]+ +\d+ +\d+ ([VDIWEF]) ` +
		`|\blevel=(\w+)` +
		`|"(?:level|severity|lvl)"\s*:\s*"(\w+)"`)
)

const (
	minLogLines      = 40
	logSimThreshold  = 0.5
	maxLogScan       = 200
	maxLogTemplates  = 2000
	maxVarSlots      = 4
	maxDistinctTrack = 64
	maxCategorical   = 4
	maxErrVars       = 3
	maxWordValues    = 8
	maxTSScan        = 100
	maxMaskCache     = 1 << 16
	parallelClassify = 128
	logMoreReserve   = 24
)

const logPrefix = 256

func isLogLine(ln string) bool {
	if len(ln) > logPrefix {
		ln = ln[:logPrefix]
	}
	if hasDigit(ln) && logTSRe.MatchString(ln) {
		return true
	}
	return mayHaveLevel(ln) && logLevelRe.MatchString(ln)
}

var levelWords = []string{"TRACE", "DEBUG", "INFO", "NOTICE", "WARN", "ERROR", "FATAL", "CRITICAL", "SEVERE", "[", "level", "lvl", "severity"}

func mayHaveLevel(ln string) bool {
	if len(ln) > 1 && ln[1] == '/' || len(ln) > 5 && ln[2] == '-' && ln[5] == ' ' {
		return true
	}
	for _, w := range levelWords {
		if strings.Contains(ln, w) {
			return true
		}
	}
	return false
}

func logLevel(ln string) string {
	lvl, _ := logLevelAt(ln)
	return lvl
}

func logLevelAt(ln string) (string, int) {
	if !mayHaveLevel(ln) {
		return "", 0
	}
	m := logLevelRe.FindStringSubmatchIndex(ln)
	if m == nil {
		return "", 0
	}
	for g := 2; g+1 < len(m); g += 2 {
		if m[g] >= 0 {
			return strings.ToLower(ln[m[g]:m[g+1]]), m[1]
		}
	}
	return "", 0
}

type slotStat struct {
	counts   map[string]int
	order    []string
	overflow bool
	numeric  bool
	min, max float64
	minS     string
	maxS     string
}

func (s *slotStat) add(v string) {
	if s.counts == nil {
		s.counts = map[string]int{}
		s.numeric = true
	}
	if _, ok := s.counts[v]; ok {
		s.counts[v]++
	} else if len(s.counts) < maxDistinctTrack {
		s.counts[v] = 1
		s.order = append(s.order, v)
	} else {
		s.overflow = true
	}
	if s.numeric {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			s.numeric = false
		} else if s.minS == "" || f < s.min {
			s.min, s.minS = f, v
		}
		if err == nil && (s.maxS == "" || f > s.max) {
			s.max, s.maxS = f, v
		}
	}
}

type logTemplate struct {
	toks    []string
	labels  []string
	count   int
	first   int
	example string
	slots   []slotStat

	isErr    bool
	head     int
	exRaw    []string
	word     []bool
	wordSlot []bool
	record   []string
	firstTS  string
	lastTS   string
}

type logStyle int

const (
	styleCount logStyle = iota
	styleExample
	styleRoutine
	styleBare
	styleTight
)

func TemplateLogs(lines []string) ([]string, bool) { return TemplateLogsFor(nil, lines) }

func TemplateLogsFor(c *Context, lines []string) ([]string, bool) {
	nonEmpty, logLike := 0, 0
	isLog := make([]bool, len(lines))
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		nonEmpty++
		if isLog[i] = isLogLine(ln); isLog[i] {
			logLike++
		} else if (nonEmpty-logLike)*10 > len(lines)*4 {
			return nil, false
		}
	}
	if nonEmpty < minLogLines || logLike*10 < nonEmpty*6 {
		return nil, false
	}
	blame := 0
	for _, ln := range lines {
		if blameRe.MatchString(ln) {
			blame++
		}
	}
	if blame*2 > nonEmpty {
		return nil, false
	}

	type record struct{ start, end int }
	var recs []record
	for i := range lines {
		if len(recs) == 0 || isLog[i] {
			recs = append(recs, record{i, i + 1})
			continue
		}
		recs[len(recs)-1].end = i + 1
	}

	var tmpls []*logTemplate
	groups := map[string][]*logTemplate{}
	exact := map[string]*logTemplate{}
	var owner []*logTemplate
	if c.judging() != nil || c.focus() != nil {
		owner = make([]*logTemplate, len(lines))
	}

	masks := map[string]string{}
	add := func(i int, rest []string, isErr bool, cls Level) *logTemplate {
		ln := lines[i]
		raw, labels := logTokens(ln)
		head := len(raw)
		json := isJSONTokens(raw)
		var widths []int
		for _, r := range rest {
			rt, rl := logTokens(r)
			raw, labels = append(raw, rt...), append(labels, rl...)
			widths = append(widths, len(rt))
		}
		masked := make([]string, len(raw))
		for k, t := range raw {
			m, ok := masks[t]
			if !ok {
				if m = maskTok(t); len(masks) < maxMaskCache {
					masks[t] = m
				}
			}
			masked[k] = m
		}
		word := make([]bool, len(masked))
		lvl, lvlEnd := logLevelAt(ln)
		msg := 0
		if lvlEnd > 0 && !json {
			msg = len(strings.Fields(ln[:lvlEnd]))
			if lvlEnd < len(ln) && !isSpace(ln[lvlEnd]) && !isSpace(ln[lvlEnd-1]) {
				msg--
			}
		}
		if isErr {
			msg = max(msg, leadTimeFields(ln))
		}
		key := fmt.Sprintf("%s\x00%d\x00%d\x00%d", lvl, cls, head, msg)
		lead := 0
		for k := 0; k < head; k++ {
			if word[k] = k >= msg && wordTok(masked[k]); word[k] && lead < 2 {
				key += "\x00" + masked[k]
				lead++
			}
		}
		if json {
			key += "\x00" + strings.Join(labels[:head], "\x01")
		}
		if isErr {
			parts, off := make([]string, len(widths)), head
			for j, n := range widths {
				parts[j] = strings.Join(masked[off:off+n], "\x02")
				off += n
			}
			key = "E" + key + "\x00" + strings.Join(parts, "\x01")
		}
		ek := key + "\x00" + strings.Join(masked[:head], "\x01")
		t := exact[ek]
		if t == nil {
			g := groups[key]
			if t = closestTemplate(g, masked, raw, word, head, msg, isErr); t == nil {
				if len(tmpls) >= maxLogTemplates {
					return nil
				}
				t = &logTemplate{
					toks:     append([]string(nil), masked...),
					exRaw:    raw[:head:head],
					labels:   labels,
					first:    i,
					example:  ln,
					slots:    make([]slotStat, len(masked)),
					isErr:    isErr,
					head:     head,
					word:     word,
					wordSlot: make([]bool, len(masked)),
				}
				groups[key] = append(g, t)
				tmpls = append(tmpls, t)
			} else {
				for k := 0; k < head; k++ {
					if t.toks[k] != masked[k] {
						if t.word[k] || word[k] {
							t.wordSlot[k] = true
						}
						t.toks[k], t.word[k] = "<*>", false
					}
				}
			}
			exact[ek] = t
		}
		t.count++
		for k, v := range raw {
			t.slots[k].add(slotValue(v))
		}
		return t
	}

	errRecords := 0
	cls := classifyLines(lines)
	for _, r := range recs {
		end := r.end
		for end > r.start && strings.TrimSpace(lines[end-1]) == "" {
			end--
		}
		isErr := false
		for i := r.start; i < end; i++ {
			if cls[i] == Err {
				isErr = true
			}
		}
		if isErr {
			t := add(r.start, lines[r.start+1:end], true, cls[r.start])
			if t == nil {
				return nil, false
			}
			if t.record == nil {
				t.record = FoldStacks(nil, lines[r.start:end])
			}
			ts := recordTime(lines[r.start])
			if t.count == 1 {
				t.firstTS = ts
			}
			if ts != "" {
				t.lastTS = ts
			}
			errRecords++
			for i := r.start; owner != nil && i < end; i++ {
				owner[i] = t
			}
			continue
		}
		for i := r.start; i < r.end; i++ {
			if strings.TrimSpace(lines[i]) == "" {
				continue
			}
			t := add(i, nil, false, cls[i])
			if t == nil {
				return nil, false
			}
			if owner != nil {
				owner[i] = t
			}
		}
	}

	style := map[*logTemplate]logStyle{}
	for _, t := range tmpls {
		if !t.isErr && t.count > 1 && t.alarming() {
			style[t] = styleExample
		}
	}
	full := renderLogView(tmpls, style, nil)
	if len(full)*4 > nonEmpty*3 {
		return nil, false
	}

	focused := c.focusTemplates(owner, lines)
	for t := range focused {
		style[t] = styleExample
	}
	keep, noise := c.judgeTemplates(tmpls, owner, lines, full)
	for t := range keep {
		style[t] = styleExample
	}
	for t := range noise {
		style[t] = styleRoutine
	}

	errKinds := 0
	for _, t := range tmpls {
		if t.isErr {
			errKinds++
		}
	}
	hdr := fmt.Sprintf("[log: %s lines, %s", commaInt(nonEmpty), Plural(len(tmpls)-errKinds, "template", "templates"))
	switch {
	case errRecords > errKinds:
		hdr += fmt.Sprintf(", %s error records of %s", commaInt(errRecords), Plural(errKinds, "kind", "kinds"))
	case errRecords > 0:
		hdr += ", " + Plural(errRecords, "error record", "error records")
	}
	firstTS, lastTS := "", ""
	for i := 0; i < min(len(lines), maxTSScan) && firstTS == ""; i++ {
		firstTS = recordTime(lines[i])
	}
	for i := len(lines) - 1; i >= max(0, len(lines)-maxTSScan) && lastTS == ""; i-- {
		lastTS = recordTime(lines[i])
	}
	if firstTS != "" && lastTS != "" {
		hdr += ", " + firstTS + " … " + lastTS
	}
	hdr += "]"

	var pick map[*logTemplate]bool
	if c != nil && c.Budget > 0 {
		pick = pickTemplates(tmpls, style, focused, c.Budget-tokens.Count(hdr)-1)
	}
	return append([]string{hdr}, renderLogView(tmpls, style, pick)...), true
}

func maskTok(t string) string {
	if !hasDigit(t) {
		return t
	}
	if isAllDigits(t) {
		if len(t) > 1 {
			return "<N>"
		}
		return t
	}
	return Mask(t)
}

func classifyLines(lines []string) []Level {
	keys := make([]string, len(lines))
	first := map[string]int{}
	var uniq []int
	for i, ln := range lines {
		keys[i] = "s" + digitShape(ln)
		if strings.IndexByte(ln, 0) >= 0 {
			keys[i] = "r" + ln
		}
		if _, ok := first[keys[i]]; !ok {
			first[keys[i]] = len(uniq)
			uniq = append(uniq, i)
		}
	}
	got := make([]Level, len(uniq))
	workers := min(runtime.GOMAXPROCS(0), len(uniq)/parallelClassify)
	if workers < 2 {
		for u, i := range uniq {
			got[u] = Classify(lines[i])
		}
	} else {
		var wg sync.WaitGroup
		for w := range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for u := w; u < len(uniq); u += workers {
					got[u] = Classify(lines[uniq[u]])
				}
			}()
		}
		wg.Wait()
	}
	cls := make([]Level, len(lines))
	for i, k := range keys {
		cls[i] = got[first[k]]
	}
	return cls
}

// Classify reads words, not the numbers standing between them: lines that
// differ only there share a class.
func digitShape(ln string) string {
	var b []byte
	for i := 0; i < len(ln); {
		c := ln[i]
		if c < '0' || c > '9' {
			if b != nil {
				b = append(b, c)
			}
			i++
			continue
		}
		j := i
		for j < len(ln) && ln[j] >= '0' && ln[j] <= '9' {
			j++
		}
		free := j-i > 1 && (i == 0 || !isWordByte(ln[i-1])) && (j == len(ln) || !isWordByte(ln[j]))
		if free && b == nil {
			b = append(make([]byte, 0, len(ln)), ln[:i]...)
		}
		switch {
		case free:
			b = append(b, 0)
		case b != nil:
			b = append(b, ln[i:j]...)
		}
		i = j
	}
	if b == nil {
		return ln
	}
	return string(b)
}

func maskLines(ls []string) string {
	if len(ls) == 0 {
		return ""
	}
	m := make([]string, len(ls))
	for i, ln := range ls {
		m[i] = Mask(strings.TrimSpace(ln))
	}
	return strings.Join(m, "\x01")
}

func wordTok(tok string) bool {
	letter := false
	for i := 0; i < len(tok); i++ {
		switch c := tok[i]; {
		case c == '<' || c >= '0' && c <= '9':
			return false
		case c|0x20 >= 'a' && c|0x20 <= 'z':
			letter = true
		}
	}
	return letter
}

func closestTemplate(g []*logTemplate, masked, raw []string, word []bool, head, msg int, strict bool) *logTemplate {
	best, bestSim := (*logTemplate)(nil), -1.0
	for _, t := range g[:min(len(g), maxLogScan)] {
		sim, ok := t.similar(masked, word, head)
		if strict {
			sim, ok = t.sameError(masked, raw, word, head, msg)
		}
		if ok && sim > bestSim {
			best, bestSim = t, sim
		}
	}
	return best
}

func (t *logTemplate) similar(masked []string, word []bool, head int) (float64, bool) {
	n, eq := 0, 0
	for k := 0; k < head; k++ {
		if !word[k] && !t.word[k] {
			continue
		}
		n++
		if t.toks[k] == masked[k] {
			eq++
		}
	}
	sim, need := 1.0, logSimThreshold
	if n <= 2 {
		need = 1
	}
	if n > 0 {
		sim = float64(eq) / float64(n)
	}
	return sim, sim >= need
}

func (t *logTemplate) sameError(masked, raw []string, word []bool, head, msg int) (float64, bool) {
	n, diff, words, same := 0, 0, 0, 0
	for k := msg; k < head; k++ {
		n++
		w := word[k] || t.word[k] || t.wordSlot[k]
		if w {
			words++
		}
		switch {
		case t.toks[k] == masked[k]:
			if w {
				same++
			}
			continue
		}
		diff++
		if strings.ContainsAny(raw[k], " \t") && noDigits(raw[k]) != noDigits(t.exRaw[k]) {
			return 0, false
		}
	}
	if diff > 0 && (n <= 2 || diff > max(1, n/5)) || words > 0 && same == 0 {
		return 0, false
	}
	if n == 0 {
		return 1, true
	}
	return float64(n-diff) / float64(n), true
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' }

func leadTimeFields(ln string) int {
	if len(ln) > logPrefix {
		ln = ln[:logPrefix]
	}
	if loc := logTSRe.FindStringIndex(ln); loc != nil && loc[0] <= 1 {
		return len(strings.Fields(ln[:loc[1]]))
	}
	return 0
}

func noDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return -1
		}
		return r
	}, s)
}

func recordTime(ln string) string {
	if len(ln) > logPrefix {
		ln = ln[:logPrefix]
	}
	if !hasDigit(ln) {
		return ""
	}
	return trimTS(logTSRe.FindString(ln))
}

func trimTS(ts string) string {
	return strings.TrimPrefix(strings.TrimPrefix(ts, "- "), "[")
}

func (c *Context) focusTemplates(owner []*logTemplate, lines []string) map[*logTemplate]bool {
	fm := c.focus()
	if fm == nil || owner == nil {
		return nil
	}
	var out map[*logTemplate]bool
	for _, fl := range fm.rankLines(strings.Join(lines, "\n")) {
		if fl.i < len(owner) && owner[fl.i] != nil && !owner[fl.i].isErr {
			if out == nil {
				out = map[*logTemplate]bool{}
			}
			out[owner[fl.i]] = true
		}
	}
	return out
}

func (t *logTemplate) render(st logStyle) []string {
	switch {
	case t.isErr && st == styleTight && strings.TrimSpace(t.example) != "":
		ln := t.countLine()
		if t.count == 1 {
			ln = strings.TrimPrefix(ln, "[×1] ")
		}
		ls := []string{ln}
		for i := 1; i < len(t.record); {
			if f, ok := parseFrame(t.record, i); ok && f.end > i {
				i = f.end
				continue
			}
			if r := strings.TrimSpace(t.record[i]); r != "" && !strings.HasPrefix(r, "… ") {
				ls = append(ls, t.record[i])
			}
			i++
		}
		if v := t.varSummary(true); v != "" {
			ls = append(ls, "    vars: "+v)
		}
		return ls
	case t.isErr:
		ls := append([]string(nil), t.record...)
		if t.count > 1 {
			sfx := " [×" + commaInt(t.count)
			if t.lastTS != "" && t.lastTS != t.firstTS {
				sfx += ", last " + t.lastTS
			}
			ls[0] += sfx + "]"
			if v := t.varSummary(st == styleBare || st == styleTight); v != "" {
				ls = append(ls, "    vars: "+v)
			}
		}
		return ls
	case t.count == 1:
		return []string{shortRoutine(t.example)}
	case st == styleExample:
		ls := []string{shortRoutine(t.example) + " [×" + commaInt(t.count) + "]"}
		if t.count >= 3 {
			if v := t.varSummary(false); v != "" {
				ls = append(ls, "    vars: "+v)
			}
		}
		return ls
	}
	ln := t.countLine()
	if st == styleRoutine {
		ln += " (routine)"
	}
	ls := []string{ln}
	if v := t.categorical(); v != "" {
		ls = append(ls, "    vars: "+v)
	}
	return ls
}

func shortRoutine(ln string) string {
	if Classify(ln) == Normal {
		return ShortenLine(ln, 400)
	}
	return ln
}

func renderLogView(tmpls []*logTemplate, style map[*logTemplate]logStyle, pick map[*logTemplate]bool) []string {
	var body []string
	hidden, hiddenLines := 0, 0
	for _, t := range tmpls {
		if pick != nil && !pick[t] {
			hidden++
			hiddenLines += t.count
			continue
		}
		body = append(body, t.render(style[t])...)
	}
	if hidden > 0 {
		body = append(body, fmt.Sprintf("… %s (%s) omitted …", Plural(hidden, "more template", "more templates"), Plural(hiddenLines, "line", "lines")))
	}
	return body
}

func pickTemplates(tmpls []*logTemplate, style map[*logTemplate]logStyle, focused map[*logTemplate]bool, budget int) map[*logTemplate]bool {
	costAs := func(t *logTemplate, st logStyle) int {
		n := 0
		for _, ln := range t.render(st) {
			n += tokens.Count(ln) + 1
		}
		return n
	}
	cost := func(t *logTemplate) int { return costAs(t, style[t]) }
	pick := map[*logTemplate]bool{}
	used := logMoreReserve
	var errs []*logTemplate
	full, bare := 0, 0
	for _, t := range tmpls {
		if t.isErr {
			errs = append(errs, t)
			full += cost(t)
			bare += costAs(t, styleBare)
		}
	}
	switch {
	case full*2 <= budget:
	case bare <= budget-used:
		for _, t := range errs {
			style[t] = styleBare
		}
	default:
		total := 0
		for _, t := range errs {
			style[t] = styleTight
			total += cost(t)
		}
		for _, t := range errs {
			if up := costAs(t, styleBare) - cost(t); total+up <= budget-used {
				style[t] = styleBare
				total += up
			}
		}
	}
	var rest, last []*logTemplate
	for _, t := range tmpls {
		switch {
		case t.isErr:
			pick[t] = true
			used += cost(t)
		case style[t] == styleRoutine:
			last = append(last, t)
		default:
			rest = append(rest, t)
		}
	}
	if len(rest)+len(last) == 0 {
		return nil
	}
	costs := map[*logTemplate]int{}
	for _, t := range append(append([]*logTemplate(nil), rest...), last...) {
		costs[t] = cost(t)
	}
	take := func(t *logTemplate) {
		if !pick[t] && used+costs[t] <= budget {
			pick[t] = true
			used += costs[t]
		}
	}
	for _, t := range rest {
		if focused[t] || style[t] == styleExample {
			take(t)
		}
	}
	for _, t := range rest {
		if t.alarming() {
			take(t)
		}
	}
	freq := append([]*logTemplate(nil), rest...)
	sort.SliceStable(freq, func(a, b int) bool { return freq[a].count > freq[b].count })
	rare := append([]*logTemplate(nil), rest...)
	sort.SliceStable(rare, func(a, b int) bool {
		if rare[a].count != rare[b].count {
			return rare[a].count < rare[b].count
		}
		return costs[rare[a]] < costs[rare[b]]
	})
	for i := range freq {
		take(freq[i])
		take(rare[i])
	}
	for _, t := range rest {
		if !pick[t] {
			return pick
		}
	}
	sort.SliceStable(last, func(a, b int) bool { return last[a].count > last[b].count })
	for _, t := range last {
		take(t)
	}
	if len(pick) == len(tmpls) {
		return nil
	}
	return pick
}

func isJSONTokens(raw []string) bool {
	return len(raw) > 0 && strings.HasPrefix(raw[0], `"`) && strings.Contains(raw[0], `":`)
}

var blameRe = lazyre.New(`^\^?[0-9a-f]{6,40} (?:\S+ +)?\(.*\d{4}-\d{2}-\d{2} .*\d+\) `)

func logTokens(ln string) (toks, labels []string) {
	if t := strings.TrimSpace(ln); strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") {
		if toks, labels, ok := jsonLogTokens(t); ok {
			return toks, labels
		}
	}
	toks = strings.Fields(ln)
	labels = make([]string, len(toks))
	for i, t := range toks {
		if k, _, ok := strings.Cut(t, "="); ok && k != "" && isWordish(k) {
			labels[i] = k
			continue
		}
		if i > 0 {
			p := strings.TrimRight(toks[i-1], ":=")
			if p == "" && i > 1 {
				p = strings.TrimRight(toks[i-2], ":=")
			}
			if isWordish(p) {
				labels[i] = p
			}
		}
	}
	return toks, labels
}

func jsonLogTokens(t string) (toks, labels []string, ok bool) {
	dec := json.NewDecoder(strings.NewReader(t))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, nil, false
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, nil, false
		}
		k, _ := kt.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, nil, false
		}
		toks = append(toks, quoteJSON(k)+":"+string(raw))
		labels = append(labels, k)
	}
	return toks, labels, true
}

func slotValue(tok string) string {
	if strings.HasPrefix(tok, `"`) {
		if i := strings.Index(tok, `":`); i > 0 {
			v := tok[i+2:]
			if uq, err := strconv.Unquote(v); err == nil {
				return uq
			}
			return v
		}
	}
	if k, v, ok := strings.Cut(tok, "="); ok && isWordish(k) {
		return strings.Trim(v, `"`)
	}
	return strings.Trim(tok, `",;()[]`)
}

func isWordish(s string) bool {
	if s == "" || len(s) > 24 {
		return false
	}
	for _, r := range s {
		if !(r == '_' || r == '-' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return !isAllDigits(s)
}

func (t *logTemplate) tsTokens() int {
	if loc := logTSRe.FindStringIndex(t.example); loc != nil && loc[0] <= 1 {
		return len(strings.Fields(t.example[:loc[1]]))
	}
	return 0
}

func timeTok(tok string) bool {
	return strings.Contains(tok, "<TS>") || strings.Contains(tok, "<TIME>") || strings.Contains(tok, "<DATE>")
}

func (t *logTemplate) slotLabel(k int) string {
	if t.labels[k] != "" {
		return t.labels[k]
	}
	return "#" + strconv.Itoa(k+1)
}

func (st *slotStat) values(n int) string {
	vals := append([]string(nil), st.order...)
	sort.SliceStable(vals, func(a, b int) bool { return st.counts[vals[a]] > st.counts[vals[b]] })
	vals = vals[:min(n, len(vals))]
	vs := make([]string, len(vals))
	for i, v := range vals {
		vs[i] = fmt.Sprintf("%s ×%d", cutRunes(cellEscaper.Replace(v), 32), st.counts[v])
	}
	return strings.Join(vs, ", ")
}

func (t *logTemplate) categorical() string {
	var parts []string
	skip := t.tsTokens()
	for k := range t.slots {
		st := &t.slots[k]
		if len(parts) >= maxVarSlots {
			break
		}
		if k < skip || st.overflow || len(st.counts) < 2 || len(st.counts) > maxCategorical || len(st.counts)*2 > t.count || timeTok(t.toks[k]) {
			continue
		}
		parts = append(parts, t.slotLabel(k)+" "+st.values(maxCategorical))
	}
	return strings.Join(parts, "; ")
}

func (t *logTemplate) varSummary(wordsOnly bool) string {
	type part struct {
		rank int
		k    int
		text string
	}
	var parts []part
	skip := t.tsTokens()
	for k := range t.slots {
		st := &t.slots[k]
		if k < skip && k < t.head || len(st.counts) < 2 && !st.overflow || timeTok(t.toks[k]) {
			continue
		}
		label := t.slotLabel(k)
		distinct := strconv.Itoa(len(st.counts))
		if st.overflow {
			distinct = strconv.Itoa(maxDistinctTrack) + "+"
		}
		switch ws := t.wordSlot[k]; {
		case ws && !st.overflow && len(st.counts) <= maxWordValues:
			parts = append(parts, part{-1, k, label + " " + st.values(len(st.counts))})
		case ws:
			parts = append(parts, part{-1, k, fmt.Sprintf("%s ×%s distinct: %s, …", label, distinct, st.values(3))})
		case wordsOnly:
		case !st.overflow && len(st.counts) <= 3:
			parts = append(parts, part{0, k, label + " " + st.values(3)})
		case st.numeric:
			parts = append(parts, part{1, k, fmt.Sprintf("%s %s–%s", label, st.minS, st.maxS)})
		default:
			parts = append(parts, part{2, k, fmt.Sprintf("%s ×%s distinct", label, distinct)})
		}
	}
	sort.SliceStable(parts, func(a, b int) bool { return parts[a].rank < parts[b].rank })
	keep := 0
	for keep < len(parts) && (parts[keep].rank < 0 || keep < maxErrVars) {
		keep++
	}
	parts = parts[:keep]
	sort.SliceStable(parts, func(a, b int) bool { return parts[a].k < parts[b].k })
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = p.text
	}
	return strings.Join(out, "; ")
}
