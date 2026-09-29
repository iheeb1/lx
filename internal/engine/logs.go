package engine

import (
	"encoding/json"
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"sort"
	"strconv"
	"strings"
)

var (
	logTSRe = lazyre.New(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2}\b| UTC\b)?` +
		`|\d{4}/\d{2}/\d{2}(?:[T ]| - )\d{2}:\d{2}:\d{2}(?:[.,]\d+)?` +
		`|\d{1,2}/(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)/\d{4}:\d{2}:\d{2}:\d{2}(?: [+-]\d{4})?` +
		`|\b(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) [ \d]\d \d{2}:\d{2}:\d{2}` +
		`|^\d{6} \d{6}\b` +
		`|^\[?\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\b` +
		`|^\[?1\d{9}(?:\.\d+)?\b`)

	logLevelRe = lazyre.New(`\b(TRACE|DEBUG|INFO|NOTICE|WARN|WARNING|ERROR|FATAL|CRITICAL|SEVERE)\b` +
		`|\[([TDIWEF]|trace|debug|info|warn|warning|error|fatal)\]` +
		`|^([VDIWEF])/\S` +
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
	if len(ln) > 1 && ln[1] == '/' {
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
	m := logLevelRe.FindStringSubmatch(ln)
	if m == nil {
		return ""
	}
	for _, g := range m[1:] {
		if g != "" {
			return strings.ToLower(g)
		}
	}
	return ""
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
}

type logItem struct {
	first int
	tmpl  *logTemplate
	lines []string
	count int
}

func TemplateLogs(lines []string) ([]string, bool) { return TemplateLogsFor(nil, lines) }

func TemplateLogsFor(c *Context, lines []string) ([]string, bool) {
	nonEmpty, logLike := 0, 0
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		nonEmpty++
		if isLogLine(ln) {
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
	for i, ln := range lines {
		if len(recs) == 0 || isLogLine(ln) {
			recs = append(recs, record{i, i + 1})
			continue
		}
		recs[len(recs)-1].end = i + 1
	}

	var items []*logItem
	errByKey := map[string]*logItem{}
	groups := map[string][]*logTemplate{}
	exact := map[string]*logTemplate{}
	nTemplates := 0
	var owner []*logTemplate
	if c.judging() != nil {
		owner = make([]*logTemplate, len(lines))
	}

	addLine := func(i int) bool {
		ln := lines[i]
		raw, labels := logTokens(ln)
		masked := make([]string, len(raw))
		for k, t := range raw {
			masked[k] = Mask(t)
		}
		lvl := logLevel(ln)
		key := fmt.Sprintf("%s\x00%d\x00%d\x00", lvl, Classify(ln), len(masked))
		if len(masked) > 0 {
			key += masked[0]
		}
		if isJSONTokens(raw) {
			key += "\x00" + strings.Join(labels, "\x01")
		}
		ek := key + "\x00" + strings.Join(masked, "\x01")
		t := exact[ek]
		if t == nil {
			best, bestSim := (*logTemplate)(nil), -1.0
			g := groups[key]
			for _, c := range g[:min(len(g), maxLogScan)] {
				eq := 0
				for k, tok := range c.toks {
					if tok == masked[k] {
						eq++
					}
				}
				sim := 1.0
				if len(masked) > 0 {
					sim = float64(eq) / float64(len(masked))
				}
				need := logSimThreshold
				if len(masked) <= 2 {
					need = 1
				}
				if sim >= need && sim > bestSim {
					best, bestSim = c, sim
				}
			}
			if best == nil {
				if nTemplates >= maxLogTemplates {
					return false
				}
				nTemplates++
				best = &logTemplate{
					toks:    append([]string(nil), masked...),
					labels:  labels,
					first:   i,
					example: ln,
					slots:   make([]slotStat, len(masked)),
				}
				groups[key] = append(g, best)
				items = append(items, &logItem{first: i, tmpl: best})
			} else {
				for k, tok := range best.toks {
					if tok != masked[k] {
						best.toks[k] = "<*>"
					}
				}
			}
			t = best
			exact[ek] = t
		}
		t.count++
		if owner != nil {
			owner[i] = t
		}
		for k, v := range raw {
			t.slots[k].add(slotValue(v))
		}
		return true
	}

	for _, r := range recs {
		isErr := false
		for _, ln := range lines[r.start:r.end] {
			if IsError(ln) {
				isErr = true
				break
			}
		}
		if isErr {
			end := r.end
			for end > r.start && strings.TrimSpace(lines[end-1]) == "" {
				end--
			}
			k := strings.Join(lines[r.start:end], "\n")
			if it := errByKey[k]; it != nil {
				it.count++
				continue
			}
			it := &logItem{first: r.start, lines: FoldStacks(nil, lines[r.start:end]), count: 1}
			errByKey[k] = it
			items = append(items, it)
			continue
		}
		for i := r.start; i < r.end; i++ {
			if strings.TrimSpace(lines[i]) == "" {
				continue
			}
			if !addLine(i) {
				return nil, false
			}
		}
	}

	sort.SliceStable(items, func(a, b int) bool { return items[a].first < items[b].first })
	body := renderLogItems(items, nil)
	if len(body)*4 > nonEmpty*3 {
		return nil, false
	}
	if fold := c.judgeTemplates(items, owner, lines, body); fold != nil {
		body = renderLogItems(items, fold)
	}

	hdr := fmt.Sprintf("[log: %s lines, %s", commaInt(nonEmpty), Plural(nTemplates, "template", "templates"))
	if n := len(errByKey); n > 0 {
		hdr += fmt.Sprintf(", %s", Plural(n, "distinct error record", "distinct error records"))
	}
	firstTS, lastTS := "", ""
	for _, ln := range lines {
		if ts := logTSRe.FindString(ln); ts != "" {
			firstTS = ts
			break
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if ts := logTSRe.FindString(lines[i]); ts != "" {
			lastTS = ts
			break
		}
	}
	if firstTS != "" {
		hdr += ", " + strings.TrimPrefix(firstTS, "[") + " … " + strings.TrimPrefix(lastTS, "[")
	}
	return append([]string{hdr + "]"}, body...), true
}

func renderLogItems(items []*logItem, fold map[*logTemplate]string) []string {
	var body []string
	for _, it := range items {
		if it.tmpl == nil {
			ls := append([]string(nil), it.lines...)
			if it.count > 1 {
				ls[0] += fmt.Sprintf(" [×%d]", it.count)
			}
			body = append(body, ls...)
			continue
		}
		t := it.tmpl
		if ln, ok := fold[t]; ok {
			body = append(body, ln)
			continue
		}
		ln := t.example
		if t.count > 1 {
			ln += fmt.Sprintf(" [×%d]", t.count)
		}
		body = append(body, ln)
		if t.count >= 3 {
			if v := t.varSummary(); v != "" {
				body = append(body, "    vars: "+v)
			}
		}
	}
	return body
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

func (t *logTemplate) varSummary() string {
	var parts []string
	for k, st := range t.slots {
		if len(parts) >= maxVarSlots {
			break
		}
		if len(st.counts) < 2 && !st.overflow {
			continue
		}
		switch t.toks[k] {
		case "<TS>", "<DATE>", "<TIME>":
			continue
		}
		if strings.Contains(t.toks[k], "<TS>") || strings.Contains(t.toks[k], "<TIME>") {
			continue
		}
		label := t.labels[k]
		if label == "" {
			label = "#" + strconv.Itoa(k+1)
		}
		distinct := strconv.Itoa(len(st.counts))
		if st.overflow {
			distinct = strconv.Itoa(maxDistinctTrack) + "+"
		}
		switch {
		case st.numeric && (st.overflow || len(st.counts) > 3):
			parts = append(parts, fmt.Sprintf("%s %s–%s", label, st.minS, st.maxS))
		case !st.overflow && len(st.counts) <= 4:
			vals := append([]string(nil), st.order...)
			sort.SliceStable(vals, func(a, b int) bool { return st.counts[vals[a]] > st.counts[vals[b]] })
			var vs []string
			for _, v := range vals {
				vs = append(vs, fmt.Sprintf("%s ×%d", cutRunes(cellEscaper.Replace(v), 40), st.counts[v]))
			}
			parts = append(parts, label+" "+strings.Join(vs, ", "))
		default:
			parts = append(parts, fmt.Sprintf("%s ×%s distinct (%s, …)", label, distinct, cutRunes(cellEscaper.Replace(st.order[0]), 40)))
		}
	}
	return strings.Join(parts, "; ")
}
