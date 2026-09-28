package jstools

import (
	"fmt"
	"slices"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/filters/focus"
	"github.com/iheeb1/lx/internal/lazyre"
	"github.com/iheeb1/lx/internal/tokens"
)

type eslint struct{}

func (eslint) Name() string { return "eslint" }

func (eslint) Match(c *engine.Context) bool {
	t, args := tool(c)
	if t != "eslint" {
		return false
	}
	if f, ok := argValue(args, "-f", "--format"); ok && f != "stylish" {
		return false
	}
	return !hasArg(args, "--version", "-v", "--help", "-h", "--init", "--print-config", "--env-info",
		"--inspect-config", "-o", "--output-file")
}

func (eslint) GuardsErrors() bool { return true }

func (eslint) Apply(c *engine.Context, s string) (string, bool) {
	if strings.Contains(s, "Oops! Something went wrong!") {
		return "", false
	}
	lines := strings.Split(s, "\n")
	var o out
	found := false
	for i := 0; i < len(lines); {
		if end, rendered := eslintRegion(c, lines, i); end > i {
			o.add(rendered...)
			found = true
			i = end
			continue
		}
		o.add(lines[i])
		i++
	}
	if !found {
		return "", false
	}
	if note := failNote(c, o.lines, eslintMaxWarnRe.MatchString); note != "" {
		o.add(note)
	}
	return o.String(), true
}

var (
	eslintSummaryRe = lazyre.New(`^✖ \d+ problems? \(\d+ errors?, \d+ warnings?\)$`)
	eslintFixRe     = lazyre.New("^\\s+\\d+ errors? and \\d+ warnings? potentially fixable with the `--fix` option\\.$")
	eslintMaxWarnRe = lazyre.New(`^ESLint found too many warnings \(maximum: \d+\)\.$`)
)

type esMsg struct {
	raw       string
	isErr     bool
	pos       string
	sev, text string
	rule      string
	extra     []string
}

func (m esMsg) key() string { return m.sev + "\x00" + m.text + "\x00" + m.rule }

type esFile struct {
	header string
	shown  string
	msgs   []esMsg
}

func parseESMsg(ln string) (esMsg, bool) {
	if !strings.Contains(ln, ":") {
		return esMsg{}, false
	}

	line, col, sev, rest, ok := scanESMsg(ln)
	if !ok {
		return esMsg{}, false
	}
	msg := esMsg{raw: ln, pos: line + ":" + col, sev: sev, text: rest, isErr: sev == "error"}
	if text, rule, ok := scanESRule(rest); ok {
		msg.text, msg.rule = text, rule
	}
	return msg, true
}

func isESHeader(lines []string, i int) bool {
	ln := lines[i]
	if ln == "" || ln[0] == ' ' || ln[0] == '\t' || i+1 >= len(lines) {
		return false
	}
	_, ok := parseESMsg(lines[i+1])
	return ok
}

const (
	eslintFactorMin    = 4
	eslintWarnKeepMax  = 60
	eslintWarnLocs     = 3
	eslintPosPerLine   = 24
	eslintFilesPerLine = 6
)

func eslintRegion(c *engine.Context, lines []string, i int) (int, []string) {
	if !isESHeader(lines, i) {
		return i, nil
	}
	var (
		files   []*esFile
		cur     *esFile
		summary []string
		n       = len(lines)
		j       = i
	)
	for j < n {
		ln := lines[j]
		switch {
		case ln == "":
			k := j
			for k < n && lines[k] == "" {
				k++
			}
			if k < n && (isESHeader(lines, k) || eslintSummaryRe.MatchString(lines[k])) {
				j = k
				cur = nil
				continue
			}
			goto done
		case isESHeader(lines, j):
			cur = &esFile{header: ln, shown: engine.Relativize(c, ln)}
			files = append(files, cur)
			j++
		case eslintSummaryRe.MatchString(ln):
			summary = append(summary, ln)
			j++
			for j < n && (eslintFixRe.MatchString(lines[j]) || eslintMaxWarnRe.MatchString(lines[j]) ||
				lines[j] == "" && j+1 < n && eslintMaxWarnRe.MatchString(lines[j+1])) {
				summary = append(summary, lines[j])
				j++
			}
			goto done
		case cur != nil:
			if m, ok := parseESMsg(ln); ok {
				cur.msgs = append(cur.msgs, m)
				j++
				continue
			}
			if (ln[0] == ' ' || ln[0] == '\t') && len(cur.msgs) > 0 {
				last := &cur.msgs[len(cur.msgs)-1]
				last.extra = append(last.extra, ln)
				j++
				continue
			}
			goto done
		default:
			goto done
		}
	}
done:

	memo := map[string]bool{}
	for _, f := range files {
		for k := range f.msgs {
			m := &f.msgs[k]
			if m.sev != "warning" {
				continue
			}
			key := m.text + "\x00" + m.rule
			isErr, ok := memo[key]
			if !ok {
				isErr = engine.IsError(m.raw)
				memo[key] = isErr
			}
			m.isErr = isErr
		}
	}
	files = focusFirst(focus.New(c.Focus), files, func(f *esFile) string { return f.header })
	return j, renderESLint(files, summary)
}

type esGroup struct {
	rule string
	msgs []string
	locs map[string][]string
	n    int
}

const eslintMaxTokens = 6000

func renderESLint(files []*esFile, summary []string) []string {
	r := renderESFiles(files, summary)
	if countUpTo(r, eslintMaxTokens) <= eslintMaxTokens {
		return r
	}
	all := [][]string{r}
	gr := groupES(files)
	for _, k := range []int{10, 3, 1, 0} {
		g := gr.render(summary, k)
		if countUpTo(g, eslintMaxTokens) <= eslintMaxTokens {
			return g
		}
		all = append(all, g)
	}

	best, bestN := all[0], byteLen(all[0])
	for _, g := range all[1:] {
		if n := byteLen(g); n < bestN {
			best, bestN = g, n
		}
	}
	return best
}

func byteLen(lines []string) int {
	n := 0
	for _, ln := range lines {
		n += len(ln) + 1
	}
	return n
}

func countUpTo(lines []string, limit int) int {
	n := 0
	for _, ln := range lines {
		n += tokens.Count(ln) + 1
		if n > limit {
			return n
		}
	}
	return n
}

type esWarnGroups struct {
	groups    map[string]*esGroup
	ruleOrder []string
	n         int
}

func (w *esWarnGroups) add(f *esFile, m esMsg) {
	if w.groups == nil {
		w.groups = map[string]*esGroup{}
	}
	g := w.groups[m.rule]
	if g == nil {
		g = &esGroup{rule: m.rule, locs: map[string][]string{}}
		w.groups[m.rule] = g
		w.ruleOrder = append(w.ruleOrder, m.rule)
	}
	if _, ok := g.locs[m.text]; !ok {
		g.msgs = append(g.msgs, m.text)
	}
	g.locs[m.text] = append(g.locs[m.text], f.shown+":"+m.pos)
	g.n++
	w.n++
}

func (w *esWarnGroups) render() []string {
	if w.n == 0 {
		return nil
	}
	r := []string{fmt.Sprintf("[%d warnings grouped by rule; first %d locations each]", w.n, eslintWarnLocs)}
	for _, rule := range w.ruleOrder {
		g := w.groups[rule]
		if len(g.msgs) == 1 {
			t := g.msgs[0]
			r = append(r, joinNonEmpty("  ", "warning", t, rule)+"  "+countLocs(g.locs[t]))
			continue
		}
		name := rule
		if name == "" {
			name = "(no rule)"
		}
		r = append(r, fmt.Sprintf("%s: %s", name, engine.Plural(g.n, "warning", "warnings")))
		for _, t := range g.msgs {
			r = append(r, "  warning  "+t+"  "+countLocs(g.locs[t]))
		}
	}
	return r
}

func groupedWarning(groupWarn bool, m esMsg) bool {
	return groupWarn && m.sev == "warning" && len(m.extra) == 0 && !m.isErr
}

func countWarnings(files []*esFile) int {
	n := 0
	for _, f := range files {
		for _, m := range f.msgs {
			if m.sev == "warning" {
				n++
			}
		}
	}
	return n
}

type esGrouped struct {
	w        esWarnGroups
	order    []string
	byKey    map[string]*esMsgGroup
	verbatim []string
	total    int
}

func groupES(files []*esFile) *esGrouped {
	groupWarn := countWarnings(files) > eslintWarnKeepMax
	gr := &esGrouped{byKey: map[string]*esMsgGroup{}}
	for _, f := range files {
		for _, m := range f.msgs {
			switch {
			case groupedWarning(groupWarn, m):
				gr.w.add(f, m)
				continue
			case len(m.extra) > 0:
				gr.verbatim = append(gr.verbatim, f.shown, m.raw)
				gr.verbatim = append(gr.verbatim, m.extra...)
				continue
			}
			k := m.key()
			g := gr.byKey[k]
			if g == nil {
				g = &esMsgGroup{head: joinNonEmpty("  ", m.sev, m.text, m.rule), pos: map[string][]string{}}
				gr.byKey[k] = g
				gr.order = append(gr.order, k)
			}
			if _, ok := g.pos[f.shown]; !ok {
				g.files = append(g.files, f.shown)
			}
			g.pos[f.shown] = append(g.pos[f.shown], m.pos)
			g.n++
			gr.total++
		}
	}

	slices.SortStableFunc(gr.order, func(a, b string) int {
		return strings.Compare(sevRank(gr.byKey[a].head), sevRank(gr.byKey[b].head))
	})
	return gr
}

func (gr *esGrouped) render(summary []string, maxPos int) []string {
	total, order, byKey, verbatim := gr.total, gr.order, gr.byKey, gr.verbatim
	head := fmt.Sprintf("[%d problems grouped by message (too many for per-file blocks); at most %d positions per file]", total, maxPos)
	if maxPos == 0 {
		head = fmt.Sprintf("[%d problems grouped by message (too many for per-file blocks); files with counts, positions hidden]", total)
	}
	r := []string{head}
	for _, k := range order {
		g := byKey[k]
		if g.n == 1 {
			f := g.files[0]
			r = append(r, g.head+"  "+f+":"+g.pos[f][0])
			continue
		}
		r = append(r, fmt.Sprintf("%s  ×%d in %s:", g.head, g.n, engine.Plural(len(g.files), "file", "files")))
		if maxPos == 0 {
			for s := 0; s < len(g.files); s += eslintFilesPerLine {
				var parts []string
				for _, f := range g.files[s:min(s+eslintFilesPerLine, len(g.files))] {
					parts = append(parts, fmt.Sprintf("%s ×%d", f, len(g.pos[f])))
				}
				r = append(r, "  "+strings.Join(parts, ", "))
			}
			continue
		}
		for _, f := range g.files {
			pos := g.pos[f]
			line := "  " + f
			if len(pos) > 1 {
				line += fmt.Sprintf(" ×%d", len(pos))
			}
			line += ": " + strings.Join(pos[:min(len(pos), maxPos)], " ")
			if len(pos) > maxPos {
				line += fmt.Sprintf(" … +%d more", len(pos)-maxPos)
			}
			r = append(r, line)
		}
	}
	r = append(r, verbatim...)
	if ws := gr.w.render(); ws != nil {
		r = append(r, "")
		r = append(r, ws...)
	}
	if len(summary) > 0 {
		r = append(r, "")
		r = append(r, summary...)
	}
	return r
}

type esMsgGroup struct {
	head  string
	files []string
	pos   map[string][]string
	n     int
}

func sevRank(head string) string {
	if strings.HasPrefix(head, "error") {
		return "0"
	}
	return "1"
}

func renderESFiles(files []*esFile, summary []string) []string {
	groupWarn := countWarnings(files) > eslintWarnKeepMax
	var (
		r []string
		w esWarnGroups
	)
	for _, f := range files {
		positions := map[string][]string{}
		for _, m := range f.msgs {
			if len(m.extra) == 0 {
				positions[m.key()] = append(positions[m.key()], m.pos)
			}
		}
		var body []string
		done := map[string]bool{}
		for _, m := range f.msgs {
			if groupedWarning(groupWarn, m) {
				w.add(f, m)
				continue
			}
			k := m.key()
			if len(m.extra) > 0 || len(positions[k]) < eslintFactorMin {
				body = append(body, m.raw)
				body = append(body, m.extra...)
				continue
			}
			if done[k] {
				continue
			}
			done[k] = true
			body = append(body, factorLine("  "+joinNonEmpty("  ", m.sev, m.text, m.rule), positions[k])...)
		}
		if len(body) > 0 {
			if len(r) > 0 {
				r = append(r, "")
			}
			r = append(r, f.shown)
			r = append(r, body...)
		}
	}
	if ws := w.render(); ws != nil {
		if len(r) > 0 {
			r = append(r, "")
		}
		r = append(r, ws...)
	}
	if len(summary) > 0 {
		r = append(r, "")
		r = append(r, summary...)
	}
	return r
}

func factorLine(head string, pos []string) []string {
	var r []string
	for s := 0; s < len(pos); s += eslintPosPerLine {
		chunk := strings.Join(pos[s:min(s+eslintPosPerLine, len(pos))], " ")
		if s == 0 {
			r = append(r, fmt.Sprintf("%s  ×%d: %s", head, len(pos), chunk))
		} else {
			r = append(r, "      "+chunk)
		}
	}
	return r
}

func countLocs(locs []string) string {
	shown := locs[:min(len(locs), eslintWarnLocs)]
	s := fmt.Sprintf("×%d: %s", len(locs), strings.Join(shown, " "))
	if len(locs) == 1 {
		s = shown[0]
	}
	if rest := len(locs) - len(shown); rest > 0 {
		s += fmt.Sprintf(" … +%d more", rest)
	}
	return s
}

func joinNonEmpty(sep string, parts ...string) string {
	var p []string
	for _, s := range parts {
		if s != "" {
			p = append(p, s)
		}
	}
	return strings.Join(p, sep)
}
