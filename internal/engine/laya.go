package engine

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/lazyre"
	"github.com/iheeb1/lx/internal/tokens"
)

type Judge interface {
	Judge(family, task string, items []string, timeout time.Duration) ([]JudgeVerdict, error)
}

type JudgeVerdict struct {
	Keep       bool
	Confidence float64
}

const (
	FamilyLogs    = "log"
	FamilyLines   = "lines"
	FamilyListing = "listing"

	DefaultJudgeTimeout = 1200 * time.Millisecond

	judgeMinLines  = 80
	judgeMinTokens = 1500
	judgeMaxItems  = 24
	judgeItemBytes = 1500
	judgeMaxLines  = 3000

	chunkMin     = 12
	chunkMax     = 25
	chunkContext = 2
	chunkHead    = 3
	chunkTail    = 8

	routineRunes  = 200
	listingMargin = 0.15
)

var errJudgeTimeout = errors.New("judge timed out")

var (
	summaryRe = lazyre.New(`(?i)^\s*(?:summary|totals?|results?|finished|done|completed?|succeeded|success(?:ful)?|built|compiled|ran \d)\b` +
		`|\b\d+ (?:passed|failed|skipped|errors?|warnings?|tests?|specs?|suites?|problems?)\b` +
		`|\b(?:build|tests?) (?:success(?:ful)?|succeeded|failed|failure|passed|complete[d]?|finished)\b` +
		`|\bin \d+(?:\.\d+)?\s*(?:ms|s|sec|secs|seconds|m|min|minutes?)\b`)

	// levels logLevel misses: pino numbers, lvl=crit, CRIT, logrus ERRO[
	alarmRe = lazyre.New(`(?i:\b(?:log_?)?(?:level|lvl|severity)["']?\s*[=:]\s*["']?(?:warn\w*|wrn|err\w*|eror|crit\w*|fatal|fata|d?panic|alert|emerg\w*|severe|[456]0)\b)` +
		`|\b(?:CRIT|ALERT|EMERG(?:ENCY)?|ERRO?|EROR|WRN|FATA|PANI|DPANIC)\b`)

	tzRe = lazyre.New(`^[+-]\d{4}\]?$`)

	changeRe = lazyre.New(`^\s*(?:[-+~]|-/\+|\+/-)\s+\S|^@@ |^(?:---|\+\+\+) \S`)
)

type judgeRun struct {
	j       Judge
	task    string
	timeout time.Duration
	fm      *focusMatcher
	listing bool

	deadline time.Time
	off      bool
	seen     map[string]JudgeVerdict
	made     map[string]judgeFold
}

type judgeFold struct{ templates, lines, kept int }

func newJudgeRun(c *Context, o Options) *judgeRun {
	if o.Judge == nil {
		return nil
	}
	listing := false
	argv := c.Argv
	for range MaxPeel + 1 {
		ic := &Context{Argv: argv}
		if readsContent(ic) {
			return nil
		}
		listing = listing || listingCmd(ic)
		if argv, _ = Peel(argv); argv == nil {
			break
		}
	}
	t := o.JudgeTimeout
	if t <= 0 {
		t = DefaultJudgeTimeout
	}
	return &judgeRun{j: o.Judge, task: o.Task, timeout: t, fm: newFocusMatcher(o.Focus), listing: listing,
		seen: map[string]JudgeVerdict{}, made: map[string]judgeFold{}}
}

func readsContent(c *Context) bool {
	switch strings.TrimSuffix(c.Name(), ".exe") {
	case "cat", "tac", "head", "tail", "bat", "batcat", "less", "more", "sed", "gsed", "awk", "gawk", "mawk", "nawk",
		"nl", "cut", "column", "xxd", "od", "hexdump", "strings", "diff", "colordiff":
		return true
	case "git":
		switch c.Sub() {
		case "show", "cat-file", "blame", "annotate", "diff", "grep":
			return true
		}
	}
	return false
}

var alarmKeys = []string{"evel", "EVEL", "lvl", "LVL", "Lvl", "everity", "EVERITY", "CRIT", "ALERT", "EMERG", "ERR", "EROR", "WRN", "FATA", "PANI"}

func alarming(ln string) bool {
	if mayHaveLevel(ln) {
		switch logLevel(ln) {
		case "warn", "warning", "error", "fatal", "critical", "severe", "w", "e", "f":
			return true
		}
	}
	if Classify(ln) != Normal {
		return true
	}
	for _, k := range alarmKeys {
		if strings.Contains(ln, k) {
			return alarmRe.MatchString(ln)
		}
	}
	return false
}

// A template's example can be INFO while other lines in it are lvl=crit.
func (t *logTemplate) alarming() bool {
	if alarming(t.example) {
		return true
	}
	for k, st := range t.slots {
		switch strings.TrimPrefix(strings.TrimPrefix(strings.ToLower(t.labels[k]), "log"), "_") {
		case "level", "lvl", "severity", "levelname":
		default:
			continue
		}
		if st.overflow {
			return true
		}
		for _, v := range st.order {
			if alarmRe.MatchString("level=" + v) {
				return true
			}
		}
	}
	return false
}

func (c *Context) judging() *judgeRun {
	if c == nil || c.jr == nil || c.jr.off {
		return nil
	}
	return c.jr
}

func judgeThreshold(m Mode) float64 {
	switch m {
	case ModeError:
		return 0.80
	case ModeDebug:
		return 0.75
	case ModeVerify:
		return 0.60
	case ModeMinimal:
		return 0.55
	}
	return 0.65
}

func (r *judgeRun) ask(family string, items []string) []JudgeVerdict {
	out := make([]JudgeVerdict, len(items))
	var todo []string
	var at []int
	for i, it := range items {
		if v, ok := r.seen[family+"\x00"+it]; ok {
			out[i] = v
			continue
		}
		todo, at = append(todo, it), append(at, i)
	}
	if len(todo) == 0 {
		return out
	}
	if r.deadline.IsZero() {
		r.deadline = time.Now().Add(r.timeout)
	}
	left := time.Until(r.deadline)
	if left <= 0 {
		r.off = true
		return nil
	}
	vs, err := r.call(family, todo, left)
	if err != nil || len(vs) != len(todo) {
		r.off = true
		return nil
	}
	for k, v := range vs {
		r.seen[family+"\x00"+todo[k]] = v
		out[at[k]] = v
	}
	return out
}

// A judge that overruns its timeout is abandoned, not waited for.
func (r *judgeRun) call(family string, items []string, left time.Duration) ([]JudgeVerdict, error) {
	type reply struct {
		v   []JudgeVerdict
		err error
	}
	ch := make(chan reply, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				ch <- reply{err: fmt.Errorf("judge: %v", p)}
			}
		}()
		v, err := r.j.Judge(family, r.task, items, left)
		ch <- reply{v, err}
	}()
	t := time.NewTimer(left)
	defer t.Stop()
	select {
	case rp := <-ch:
		return rp.v, rp.err
	case <-t.C:
		return nil, errJudgeTimeout
	}
}

func routine(v JudgeVerdict, thr float64) bool {
	return !v.Keep && v.Confidence >= thr && v.Confidence <= 1 && !math.IsNaN(v.Confidence)
}

func judgeWorth(lines []string) bool {
	if len(lines) > judgeMinLines {
		return true
	}
	return tokens.Count(strings.Join(lines, "\n")) > judgeMinTokens
}

func (c *Context) judgeTemplates(tmpls, owner []*logTemplate, lines, body []string) (keep, noise map[*logTemplate]bool) {
	r := c.judging()
	if r == nil || !judgeWorth(body) {
		return nil, nil
	}
	forced := map[*logTemplate]bool{}
	if r.fm != nil && owner != nil {
		for _, fl := range r.fm.rankLines(strings.Join(lines, "\n")) {
			if fl.i < len(owner) && owner[fl.i] != nil {
				forced[owner[fl.i]] = true
			}
		}
	}
	var cand []*logTemplate
	for _, t := range tmpls {
		if !t.isErr && t.count >= 2 && !forced[t] && !t.alarming() {
			cand = append(cand, t)
		}
	}
	if len(cand) == 0 {
		return nil, nil
	}
	sort.SliceStable(cand, func(a, b int) bool { return cand[a].count > cand[b].count })
	cand = cand[:min(len(cand), judgeMaxItems)]
	texts := make([]string, len(cand))
	for i, t := range cand {
		texts[i] = fmt.Sprintf("%s\n[×%d similar lines]", ShortenLine(t.example, 400), t.count)
	}
	vs := r.ask(FamilyLogs, texts)
	if vs == nil {
		return nil, nil
	}
	thr := judgeThreshold(modeOf(c))
	keep, noise = map[*logTemplate]bool{}, map[*logTemplate]bool{}
	for i, t := range cand {
		switch v := vs[i]; {
		case routine(v, thr):
			noise[t] = true
			r.made[t.routineLine()] = judgeFold{templates: 1}
		case v.Keep && v.Confidence >= thr && v.Confidence <= 1:
			keep[t] = true
			r.made[t.render(styleExample)[0]] = judgeFold{kept: 1}
		}
	}
	return keep, noise
}

func (t *logTemplate) routineLine() string { return t.countLine() + " (routine)" }

func (t *logTemplate) countLine() string {
	raw, _ := logTokens(t.example)
	start := t.tsTokens()
	var parts []string
	dropped := false
	for k, tok := range t.toks[:t.head] {
		if dropped && k < len(raw) && tzRe.MatchString(raw[k]) {
			dropped = false
			continue
		}
		dropped = false
		if k < len(raw) {
			switch {
			case len(t.slots[k].counts) == 1 && !t.slots[k].overflow:
				tok = raw[k]
			case tok != "<*>":
			case strings.HasPrefix(raw[k], `"`) && strings.Contains(raw[k], `":`):
				tok = raw[k][:strings.Index(raw[k], `":`)+2] + tok
			default:
				if key, _, ok := strings.Cut(raw[k], "="); ok && isWordish(key) {
					tok = key + "=" + tok
				}
			}
		}
		switch {
		case k < start:
		case len(parts) == 0 && placeholder(tok):
		case timeTok(t.toks[k]):
			dropped = true
		default:
			parts = append(parts, tok)
		}
	}
	if len(parts) == 0 {
		parts = t.toks[:t.head]
	}
	s := cutRunes(cellEscaper.Replace(strings.Join(parts, " ")), routineRunes)
	return fmt.Sprintf("[×%s] %s", commaInt(t.count), s)
}

func placeholder(tok string) bool {
	t := strings.Trim(tok, "[](){}:,|-")
	return len(t) > 2 && t[0] == '<' && t[len(t)-1] == '>' && !strings.ContainsAny(t[1:len(t)-1], "<> ")
}

func JudgeChunks(c *Context, lines []string) []string {
	r := c.judging()
	if r == nil || len(lines) > judgeMaxLines || !judgeWorth(lines) {
		return lines
	}
	n := len(lines)
	ctx := max(chunkContext, modeOf(c).knobs().errs.context)
	keep := make([]bool, n)
	hold := func(i, w int) {
		for k := max(i-w, 0); k <= min(i+w, n-1); k++ {
			keep[k] = true
		}
	}
	for i := 0; i < min(chunkHead, n); i++ {
		keep[i] = true
	}
	for i := max(n-chunkTail, 0); i < n; i++ {
		keep[i] = true
	}
	for i := 0; i < n; i++ {
		ln := lines[i]
		switch {
		case alarming(ln), summaryRe.MatchString(ln), changeRe.MatchString(ln):
			hold(i, ctx)
		case locationRe.MatchString(ln):
			keep[i] = true
		default:
			if f, ok := parseFrame(lines, i); ok && !f.filler {
				for k := i; k < f.end; k++ {
					keep[k] = true
				}
			}
		}
	}
	if r.fm != nil {
		for _, fl := range r.fm.rankLines(strings.Join(lines, "\n")) {
			if fl.i < n {
				keep[fl.i] = true
			}
		}
	}

	type span struct{ a, b int }
	var chunks []span
	for i := 0; i < n; {
		if keep[i] {
			i++
			continue
		}
		j := i
		for j < n && !keep[j] {
			j++
		}
		if size := j - i; size >= chunkMin {
			k := (size + chunkMax - 1) / chunkMax
			for p := 0; p < k; p++ {
				chunks = append(chunks, span{i + size*p/k, i + size*(p+1)/k})
			}
		}
		i = j
	}
	if len(chunks) == 0 || len(chunks) > judgeMaxItems {
		return lines
	}
	texts := make([]string, len(chunks))
	for i, s := range chunks {
		texts[i] = clipMiddle(strings.Join(lines[s.a:s.b], "\n"), judgeItemBytes)
	}
	family, thr := FamilyLines, judgeThreshold(modeOf(c))
	if r.listing || listingCmd(c) {
		family, thr = FamilyListing, thr+listingMargin
	}
	vs := r.ask(family, texts)
	if vs == nil {
		return lines
	}
	folded := make([]bool, n)
	hit := false
	for i, s := range chunks {
		if routine(vs[i], thr) {
			for k := s.a; k < s.b; k++ {
				folded[k] = true
			}
			hit = true
		}
	}
	if !hit {
		return lines
	}
	out := make([]string, 0, n)
	for i := 0; i < n; {
		if !folded[i] {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		for j < n && folded[j] {
			j++
		}
		m := fmt.Sprintf("[… %d lines judged routine (laya) …]", j-i)
		r.made[m] = judgeFold{lines: j - i}
		out = append(out, m)
		i = j
	}
	return out
}

func listingCmd(c *Context) bool {
	switch c.Name() {
	case "ls", "find", "fd", "tree", "du", "locate", "eza", "exa", "lsd":
		return true
	}
	switch c.Sub() {
	case "list", "ls", "ls-files", "ls-tree", "ls-remote":
		return true
	}
	return c.HasFlag("--list", "--collect-only", "--co", "--listTests", "--list-tests")
}

func clipMiddle(s string, n int) string {
	if len(s) <= n {
		return s
	}
	head, tail := n*2/3, len(s)-n/3
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	for tail < len(s) && !utf8.RuneStart(s[tail]) {
		tail++
	}
	return s[:head] + "\n[…]\n" + s[tail:]
}

func (r *judgeRun) note(out string) string {
	if r == nil || len(r.made) == 0 {
		return ""
	}
	var f judgeFold
	for _, ln := range strings.Split(out, "\n") {
		if m, ok := r.made[strings.TrimSpace(ln)]; ok {
			f.templates += m.templates
			f.lines += m.lines
			f.kept += m.kept
		}
	}
	var parts []string
	if f.kept > 0 {
		parts = append(parts, Plural(f.kept, "template", "templates")+" kept in full")
	}
	if f.templates > 0 {
		parts = append(parts, Plural(f.templates, "template", "templates")+" judged routine")
	}
	if f.lines > 0 {
		parts = append(parts, fmt.Sprintf("%d routine lines folded", f.lines))
	}
	if len(parts) == 0 {
		return ""
	}
	return "laya: " + strings.Join(parts, ", ")
}
