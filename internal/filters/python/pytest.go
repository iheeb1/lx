package python

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

type pytestFilter struct{}

func (pytestFilter) Name() string { return "pytest" }

func (pytestFilter) Match(c *engine.Context) bool {
	inv := parseInvocation(c)
	if inv.tool != "pytest" {
		return false
	}

	return !hasArg(inv.args, "--co", "--collect-only", "--collectonly", "--version", "-V", "--help", "-h",
		"--fixtures", "--funcargs", "--fixtures-per-test", "--markers", "--trace-config", "--setup-plan")
}

func (pytestFilter) GuardsErrors() bool { return true }

func (pytestFilter) Stream(c *engine.Context) bool {
	return hasArg(parseInvocation(c).args, "-f", "--looponfail")
}

func (pytestFilter) Apply(c *engine.Context, out string) (string, bool) {
	r, _, ok := reducePytest(c, out)
	return r, ok
}

var (
	ptSummaryRe    = lazyre.New(`^(?:=+ )?(?:(?:\d+ (?:subtests? )?(?:failed|passed|skipped|deselected|xfailed|xpassed|warnings?|errors?|rerun)(?:, )?)+|no tests ran) in \d+(?:\.\d+)?s(?:econds)?(?: \([\d:.]+\))?(?: =+)?$`)
	ptSectionRe    = lazyre.New(`^=+ (.+?) =+$`)
	ptBangRe       = lazyre.New(`^!{3,} .* !{3,}$`)
	ptBlockRe      = lazyre.New(`^_{3,} (.+?) _{3,}$`)
	ptDashRe       = lazyre.New(`^-{2,} (.+?) -{2,}$`)
	ptCaptureRe    = lazyre.New(`^-{3,} Captured (?:stdout|stderr|log|warnings?) (?:setup|call|teardown) -{3,}$`)
	ptHeaderRe     = lazyre.New(`^(?:platform \S+ -- Python |cachedir: |rootdir: |configfile: |inifile: |testpaths: |plugins: |hypothesis profile |asyncio: |benchmark: |django: settings|Django settings: |metadata: |timeout: |timeout method: |timeout func_only: |sensitiveurl: |base_url: |html: |cov: |anyio: |xdist: |scheduling tests via \w+$)`)
	ptCollectRe    = lazyre.New(`\bcollected \d+ items?\b|\bno tests collected\b|^\d+ workers? \[\d+ items?\]$`)
	ptCollectingRe = lazyre.New(`^collecting \.\.\. ?$|^bringing up nodes\.\.\.$|^created: \d+/\d+ workers?$`)

	ptProgressRe = lazyre.New(`^(?:(\S+\.py)(?:::\S+)? ?)?([.sFExXRu,]+) *(?:\[ *\d+(?:%|/\d+)\])?$|^\S+\.py \[ *\d+(?:%|/\d+)\]$|^\[ *\d+(?:%|/\d+)\]$`)

	ptVerboseRe = lazyre.New(`^(\S.*::.+?) (PASSED|FAILED|ERROR|SKIPPED|XFAIL|XPASS)(?: \(.*\))?(?: +\[ *\d+(?:%|/\d+)\])?$`)
	ptXdistRe   = lazyre.New(`^\[gw\d+\] \[ *\d+(?:%|/\d+)\] (PASSED|FAILED|ERROR|SKIPPED|XFAIL|XPASS) \S`)

	ptNodeIDRe = lazyre.New(`^\S+\.py::\S.*$`)

	ptLongLocRe  = lazyre.New(`^(\S(?:.*?\S)?):(\d+):(?: ([A-Za-z_][\w.]*))?$`)
	ptShortLocRe = lazyre.New(`^(\S(?:.*?\S)?):(\d+): in \S.*$`)
	ptFrameSepRe = lazyre.New(`^(?:_ ){3,}_?$`)
	ptELineRe    = lazyre.New(`^E(?:\s|$)`)
	ptObjArgRe   = lazyre.New(`^(?:[A-Za-z_]\w* = <[\w.]+ object at 0x[0-9a-f]+>(?:, |$))+$`)
	ptFuncargRe  = lazyre.New(`^[A-Za-z_]\w* = `)
	ptBracketsRe = lazyre.New(`^[\])},]+$`)

	ptWorkerRe = lazyre.New(`^\[gw\d+\] \S+ -- Python \S+ \S+$`)

	ptFailCountRe = lazyre.New(`(?:^|[ =])[1-9]\d* (?:subtests? )?(?:failed|errors?)\b`)
)

const (
	ptMaxStatusLines = 10
	ptSourceBefore   = 3
	ptSourceAfter    = 4
	ptCaptureHead    = 8
	ptCaptureTail    = 12
	ptMaxWarnGroups  = 20
	ptMaxDurations   = 25
	ptCollapseMax    = 2000
)

func ptSection(ln string) (string, bool) {
	if len(ln) < 5 || ln[0] != '=' {
		return "", false
	}
	if m := ptSectionRe.FindStringSubmatch(ln); m != nil {
		return m[1], true
	}
	return "", false
}

func ptIsSummary(ln string) bool {
	if len(ln) < 10 || !strings.Contains(ln, " in ") {
		return false
	}
	switch ln[len(ln)-1] {
	case 's', '=', ')':
		return ptSummaryRe.MatchString(ln)
	}
	return false
}

func ptIsBang(ln string) bool { return len(ln) > 8 && ln[0] == '!' && ptBangRe.MatchString(ln) }

func ptStatus(ln string) string {
	if strings.HasPrefix(ln, "[gw") {
		if m := ptXdistRe.FindStringSubmatch(ln); m != nil {
			return m[1]
		}
		return ""
	}
	if !strings.Contains(ln, "::") || !ptHasStatusWord(ln) {
		return ""
	}
	if m := ptVerboseRe.FindStringSubmatch(ln); m != nil {
		return m[2]
	}
	return ""
}

func ptHasStatusWord(ln string) bool {
	for _, w := range []string{" PASSED", " FAILED", " ERROR", " SKIPPED", " XFAIL", " XPASS"} {
		if strings.Contains(ln, w) {
			return true
		}
	}
	return false
}

func ptProgress(t string) (file, marks string, ok bool) {
	body := t
	if strings.HasSuffix(body, "]") {
		i := strings.LastIndexByte(body, '[')
		if i < 0 {
			return "", "", false
		}
		body = strings.TrimRight(body[:i], " ")
	}
	if body != "" && !strings.HasSuffix(body, ".py") && !strings.ContainsRune(".sFExXRu,", rune(body[len(body)-1])) {
		return "", "", false
	}
	m := ptProgressRe.FindStringSubmatch(t)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

func ptIsHeader(ln string) bool {
	return (strings.Contains(ln, ": ") || strings.HasPrefix(ln, "platform ") || strings.HasPrefix(ln, "hypothesis profile ") ||
		strings.HasPrefix(ln, "scheduling tests via ")) && ptHeaderRe.MatchString(ln)
}

func ptIsCollect(ln string) bool {
	return (strings.Contains(ln, "collected") || strings.Contains(ln, "worker")) && ptCollectRe.MatchString(ln)
}

func ptIsCollecting(t string) bool {
	return (strings.HasPrefix(t, "collecting") || strings.HasPrefix(t, "bringing") || strings.HasPrefix(t, "created:")) && ptCollectingRe.MatchString(t)
}

type ptReducer struct {
	c      *engine.Context
	lines  []string
	exempt []bool
	out    []string

	failIDs bool

	xdist      bool
	errMemo    []int8
	statusMemo []string
}

func (r *ptReducer) status(i int) string {
	if r.statusMemo[i] == "\x00" {
		r.statusMemo[i] = ptStatus(r.lines[i])
	}
	return r.statusMemo[i]
}

func (r *ptReducer) isErr(i int) bool {
	if r.errMemo[i] == 0 {
		r.errMemo[i] = 1
		if engine.IsError(r.lines[i]) {
			r.errMemo[i] = 2
		}
	}
	return r.errMemo[i] == 2
}

func (r *ptReducer) emit(s ...string) { r.out = append(r.out, s...) }

type ptRegion struct {
	title      string
	hdr        int
	start, end int
}

var ptReportSections = map[string]bool{"FAILURES": true, "ERRORS": true, "XFAILURES": true, "XPASSES": true, "PASSES": true}

func reducePytest(c *engine.Context, text string) (string, int, bool) {
	lines := strings.Split(text, "\n")
	summary := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if ptIsSummary(lines[i]) {
			summary = i
			break
		}
	}
	hasShort := false
	for _, ln := range lines {
		if t, ok := ptSection(ln); ok && t == "short test summary info" {
			hasShort = true
			break
		}
	}

	if summary < 0 && !hasShort && !pytestCrashed(lines) {
		return "", 0, false
	}
	r := &ptReducer{c: c, lines: lines, exempt: make([]bool, len(lines)), errMemo: make([]int8, len(lines)), statusMemo: make([]string, len(lines))}
	for i := range r.statusMemo {
		r.statusMemo[i] = "\x00"
	}
	regions := splitPytestRegions(lines, summary)

	for _, rg := range regions {
		for i := rg.start; i < rg.end && !r.failIDs; i++ {
			ln := lines[i]
			switch t := rg.title; {
			case t == "short test summary info":
				r.failIDs = strings.HasPrefix(ln, "FAILED ") || strings.HasPrefix(ln, "ERROR ") || strings.HasPrefix(ln, "SUBFAILED")
			case t == "FAILURES" || t == "ERRORS":
				r.failIDs = strings.TrimSpace(ln) != ""
			case rg.hdr < 0 || t == "test session starts":
				st := r.status(i)
				r.failIDs = st == "FAILED" || st == "ERROR"
			}
		}
		if rg.hdr < 0 || rg.title == "test session starts" {
			for i := rg.start; i < rg.end && !r.xdist; i++ {
				r.xdist = strings.HasPrefix(lines[i], "[gw") && ptXdistRe.MatchString(lines[i]) ||
					strings.Contains(lines[i], " workers [") && ptIsCollect(lines[i])
			}
		}
	}

	for _, rg := range regions {
		body := indexRange(rg.start, rg.end)
		switch t := rg.title; {
		case rg.hdr < 0 || t == "test session starts":
			r.progress(body)
		case t == "\x00summary", t == "\x00bang":
			r.emit(lines[rg.hdr])
			r.verbatim(body)
		case ptReportSections[t]:
			r.emit(lines[rg.hdr])
			r.failures(body)
		case strings.HasPrefix(t, "warnings summary"):
			r.emit(lines[rg.hdr])
			r.warnings(body)
		case t == "short test summary info":
			r.emit(lines[rg.hdr])
			r.shortSummary(body)
		case strings.HasPrefix(t, "slowest") && strings.Contains(t, "durations"):
			r.emit(lines[rg.hdr])
			r.capped(body, ptMaxDurations)
		default:
			r.emit(lines[rg.hdr])
			r.verbatim(body)
		}
	}

	if summary >= 0 {
		r.exitNote(lines[summary])
	} else if c.Exit != 0 {
		r.emit(fmt.Sprintf("[lx: pytest exited %d]", c.Exit))
	}
	out := r.out
	out, added := ensureErrorsFn(lines, r.exempt, out, r.isErr)
	out = relativize(c, out)
	return strings.TrimRight(strings.Join(out, "\n"), "\n"), added, true
}

func pytestCrashed(lines []string) bool {
	session := false
	for _, ln := range lines {
		if t, ok := ptSection(ln); ok && t == "test session starts" {
			session = true
			continue
		}
		if session && (strings.Contains(ln, "Fatal Python error: ") || faultRe.MatchString(ln)) {
			return true
		}
	}
	return false
}

func splitPytestRegions(lines []string, summary int) []ptRegion {
	var regions []ptRegion
	cur := ptRegion{hdr: -1, start: 0}
	seenSession := false
	limit := summary
	if limit < 0 {
		limit = len(lines)
	}

	nextResult := make([]int, len(lines)+1)
	nextResult[len(lines)] = -1
	for i := len(lines) - 1; i >= 0; i-- {
		nextResult[i] = nextResult[i+1]
		if i < limit && ptIsSummary(lines[i]) {
			nextResult[i] = i
		}
	}
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		isSummary := i == summary
		title, isSection := ptSection(ln)
		if !isSummary && !isSection && !ptIsBang(ln) {
			continue
		}
		if isSection && title == "test session starts" {
			if seenSession || ptReportSections[cur.title] {
				if nested := nextResult[i+1]; nested >= 0 {
					i = nested
					continue
				}
			}
			seenSession = true
		}
		cur.end = i
		regions = append(regions, cur)
		switch {
		case isSummary:
			cur = ptRegion{title: "\x00summary", hdr: i, start: i + 1}
		case isSection:
			cur = ptRegion{title: title, hdr: i, start: i + 1}
		default:
			cur = ptRegion{title: "\x00bang", hdr: i, start: i + 1}
		}
	}
	cur.end = len(lines)
	return append(regions, cur)
}

func indexRange(a, b int) []int {
	s := make([]int, 0, max(0, b-a))
	for i := a; i < b; i++ {
		s = append(s, i)
	}
	return s
}

func (r *ptReducer) exitNote(summary string) {
	switch {
	case r.c.Exit == 5:
		r.emit("[lx: pytest exit 5: no tests were run]")
	case r.c.Exit != 0 && !ptFailCountRe.MatchString(summary):
		r.emit(fmt.Sprintf("[lx: pytest exited %d although the result line reports no failures; see the lines above]", r.c.Exit))
	}
}

func (r *ptReducer) verbatim(idx []int) {
	for _, i := range idx {
		r.emit(r.lines[i])
	}
	r.trimTrailingBlank()
}

func (r *ptReducer) trimTrailingBlank() {
	for len(r.out) > 0 && strings.TrimSpace(r.out[len(r.out)-1]) == "" {
		r.out = r.out[:len(r.out)-1]
	}
}

func (r *ptReducer) capped(idx []int, n int) {
	kept, hidden := 0, 0
	for _, i := range idx {
		ln := r.lines[i]
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if kept < n || r.isErr(i) {
			r.emit(ln)
			kept++
			continue
		}
		hidden++
	}
	if hidden > 0 {
		r.emit(fmt.Sprintf("[lx: +%d more lines]", hidden))
	}
}

func (r *ptReducer) progress(idx []int) {
	const (
		skip = iota
		keep
		progressLine
		passedLine
		statusLine
		startedLine
	)
	class := make([]int, len(idx))
	lastFile := -1
	for k, i := range idx {
		ln := r.lines[i]
		t := strings.TrimSpace(ln)
		switch {
		case t == "", ptIsHeader(ln):
			class[k] = skip
		case ptIsCollect(ln):
			class[k] = keep
		case ptIsCollecting(t):
			class[k] = progressLine
		default:
			if file, marks, ok := ptProgress(t); ok {
				class[k] = progressLine
				if file != "" || strings.HasSuffix(strings.Fields(t)[0], ".py") {
					lastFile = k
				}
				if !r.failIDs && strings.ContainsAny(marks, "FEu") {
					class[k] = keep
					if file == "" && lastFile >= 0 && lastFile != k {
						class[lastFile] = keep
					}
				}
				continue
			}
			switch status := r.status(i); status {
			case "":
				class[k] = keep
				if r.xdist && ptNodeIDRe.MatchString(ln) {
					class[k] = startedLine
				}
			case "PASSED":
				class[k] = passedLine
			case "SKIPPED", "XFAIL":
				class[k] = statusLine
			default:
				class[k] = keep
			}
		}
	}

	var progress, passed, started int
	hidden := map[string]int{}
	shown := map[string]int{}
	var keptIdx []int
	for k, i := range idx {
		switch class[k] {
		case keep:
			keptIdx = append(keptIdx, i)
		case progressLine:
			progress++
			r.exempt[i] = true
		case passedLine:
			passed++
			r.exempt[i] = true
		case startedLine:
			started++
			r.exempt[i] = true
		case statusLine:
			status := r.status(i)
			if shown[status] < ptMaxStatusLines {
				shown[status]++
				keptIdx = append(keptIdx, i)
			} else {
				hidden[status]++
				r.exempt[i] = true
			}
		}
	}

	text := make([]string, len(keptIdx))
	for n, i := range keptIdx {
		text[n] = r.lines[i]
	}
	folded, hiddenFrames := foldPyTracebacks(text)
	for _, n := range hiddenFrames {
		r.exempt[keptIdx[n]] = true
	}
	r.emit(folded...)

	for _, s := range []string{"SKIPPED", "XFAIL"} {
		if hidden[s] > 0 {
			r.emit(fmt.Sprintf("[lx: +%d more %s lines]", hidden[s], s))
		}
	}
	if passed > 0 {
		r.emit(fmt.Sprintf("[lx: %s hidden]", engine.Plural(passed, "PASSED line", "PASSED lines")))
	}
	if started > 0 {
		r.emit(fmt.Sprintf("[lx: %s hidden]", engine.Plural(started, "xdist test-start line", "xdist test-start lines")))
	}
	if progress > 0 {
		r.emit(fmt.Sprintf("[lx: %s hidden]", engine.Plural(progress, "progress line", "progress lines")))
	}
}

func (r *ptReducer) shortSummary(idx []int) {
	passed := 0
	for _, i := range r.focusSummary(idx) {
		ln := r.lines[i]
		if strings.HasPrefix(ln, "PASSED ") {
			passed++
			r.exempt[i] = true
			continue
		}
		if strings.TrimSpace(ln) == "" {
			continue
		}
		r.emit(ln)
	}
	if passed > 0 {
		r.emit(fmt.Sprintf("[lx: %s hidden]", engine.Plural(passed, "PASSED line", "PASSED lines")))
	}
}

func (r *ptReducer) failures(idx []int) {
	k := 0
	for k < len(idx) && !ptBlockRe.MatchString(r.lines[idx[k]]) {
		if ln := r.lines[idx[k]]; strings.TrimSpace(ln) != "" {
			r.emit(ln)
		}
		k++
	}
	var blocks [][2]int
	for k < len(idx) {
		e := k + 1
		for e < len(idx) && !ptBlockRe.MatchString(r.lines[idx[e]]) {
			e++
		}
		blocks = append(blocks, [2]int{k, e})
		k = e
	}
	for _, b := range r.focusBlocks(idx, blocks) {
		r.emit(r.lines[idx[b[0]]])
		r.block(idx[b[0]+1 : b[1]])
	}
}

func trimBlankIdx(lines []string, idx []int) []int {
	for len(idx) > 0 && strings.TrimSpace(lines[idx[0]]) == "" {
		idx = idx[1:]
	}
	for len(idx) > 0 && strings.TrimSpace(lines[idx[len(idx)-1]]) == "" {
		idx = idx[:len(idx)-1]
	}
	return idx
}

func (r *ptReducer) block(body []int) {
	tbEnd := len(body)
	for k, i := range body {
		if ptDashRe.MatchString(r.lines[i]) {
			tbEnd = k
			break
		}
	}
	r.traceback(body[:tbEnd])
	rest := body[tbEnd:]
	for k := 0; k < len(rest); {
		ln := r.lines[rest[k]]
		if !ptCaptureRe.MatchString(ln) {
			if ptDashRe.MatchString(ln) {
				for _, i := range trimBlankIdx(r.lines, rest[k:]) {
					r.emit(r.lines[i])
				}
				return
			}
			if strings.TrimSpace(ln) != "" {
				r.emit(ln)
			}
			k++
			continue
		}
		e := k + 1
		for e < len(rest) && !ptDashRe.MatchString(r.lines[rest[e]]) {
			e++
		}
		r.emit(ln)
		sec := trimBlankIdx(r.lines, rest[k+1:e])
		text := make([]string, len(sec))
		for n, i := range sec {
			text[n] = r.lines[i]
		}

		var kept []string
		if len(text) > ptCollapseMax {
			kept, _ = capLines(text, ptCaptureHead, ptCaptureTail, func(n int) bool { return r.isErr(sec[n]) })
		} else {
			col := engine.CollapseSimilar(text)
			kept, _ = capLines(col, ptCaptureHead, ptCaptureTail, func(n int) bool { return engine.IsError(col[n]) })
		}
		r.emit(kept...)
		k = e
	}
}

type ptFrame struct {
	idx   []int
	loc   int
	short bool
	chain bool
	lib   bool
	root  string
	hasE  bool
}

func (r *ptReducer) traceback(idx []int) {
	idx = trimBlankIdx(r.lines, idx)
	if len(idx) == 0 {
		return
	}

	for _, i := range idx {
		if _, _, _, frame := parseFrame(r.lines[i]); frame || isTracebackStart(r.lines[i]) {
			text := make([]string, len(idx))
			for n, i := range idx {
				text[n] = r.lines[i]
			}
			folded, hidden := foldPyTracebacks(text)
			for _, n := range hidden {
				r.exempt[idx[n]] = true
			}
			r.emit(folded...)
			return
		}
	}

	var frames []ptFrame
	cur := ptFrame{loc: -1}
	flush := func() {
		if len(cur.idx) > 0 {
			frames = append(frames, cur)
		}
		cur = ptFrame{loc: -1}
	}
	for _, i := range idx {
		ln := r.lines[i]
		switch {
		case ptWorkerRe.MatchString(ln):
		case ptFrameSepRe.MatchString(ln):
			flush()
			r.exempt[i] = true
		case chainRe.MatchString(ln):
			flush()
			frames = append(frames, ptFrame{idx: []int{i}, loc: -1, chain: true})
		case isLocCandidate(ln) && ptShortLocRe.MatchString(ln):
			flush()
			cur.idx, cur.loc, cur.short = []int{i}, i, true
		case isLocCandidate(ln) && !cur.short && ptLongLocRe.MatchString(ln) && !ptFuncargRe.MatchString(ln):
			cur.idx = append(cur.idx, i)
			cur.loc = i
			flush()
		default:
			cur.idx = append(cur.idx, i)
		}
	}
	flush()

	for k := range frames {
		f := &frames[k]
		if f.loc >= 0 {
			var file string
			if m := ptShortLocRe.FindStringSubmatch(r.lines[f.loc]); m != nil {
				file = m[1]
			} else if m := ptLongLocRe.FindStringSubmatch(r.lines[f.loc]); m != nil {
				file = m[1]
			}
			f.lib = isLibPath(file)
			if f.lib {
				f.root = libRoot(file)
			}
		}
		for _, i := range f.idx {
			if ptELineRe.MatchString(r.lines[i]) {
				f.hasE = true
			}
		}
	}

	for k := 0; k < len(frames); {
		f := frames[k]
		if !(f.lib && !f.hasE) {
			r.frame(f)
			k++
			continue
		}
		e := k
		var roots []string
		for e < len(frames) && frames[e].lib && !frames[e].hasE && !frames[e].chain {
			roots = append(roots, frames[e].root)
			for _, i := range frames[e].idx {
				r.exempt[i] = true
			}
			e++
		}
		if e-k == 1 {
			r.emit(r.lines[f.loc])
		} else {
			r.emit(foldMarker("", e-k, roots))
		}
		k = e
	}
}

func isLocCandidate(ln string) bool {
	return ln != "" && ln[0] != ' ' && ln[0] != '\t' && ln[0] != '>' && !ptELineRe.MatchString(ln)
}

func (r *ptReducer) frame(f ptFrame) {
	if f.chain {
		r.emit("", r.lines[f.idx[0]])
		return
	}
	if f.short {
		for _, i := range f.idx {
			if strings.TrimSpace(r.lines[i]) != "" {
				r.emit(r.lines[i])
			}
		}
		return
	}

	lines := r.lines
	arrow := -1
	for n, i := range f.idx {
		if strings.HasPrefix(lines[i], ">") {
			arrow = n
			break
		}
	}
	keep := make([]bool, len(f.idx))
	isSource := func(n int) bool {
		ln := lines[f.idx[n]]
		return ln != "" && (ln[0] == ' ' || ln[0] == '\t')
	}
	for n, i := range f.idx {
		ln := lines[i]
		switch {
		case strings.TrimSpace(ln) == "":
		case isSource(n):
		case ptObjArgRe.MatchString(ln):
		case f.lib && ptFuncargRe.MatchString(ln):
		case f.lib && arrow >= 0 && !strings.HasPrefix(ln, ">") && !ptELineRe.MatchString(ln) && i != f.loc:
		default:
			keep[n] = true
		}
	}
	if arrow < 0 {
		for n := range f.idx {
			if isSource(n) {
				keep[n] = true
			}
		}
	} else if !f.lib {
		got := 0
		for n := arrow - 1; n >= 0 && got < ptSourceBefore; n-- {
			t := strings.TrimSpace(lines[f.idx[n]])
			if !isSource(n) {
				if t == "" {
					continue
				}
				break
			}
			if ptBracketsRe.MatchString(t) {
				continue
			}
			keep[n] = true
			got++
			if strings.HasPrefix(t, "def ") || strings.HasPrefix(t, "async def ") {
				break
			}
		}
		got = 0
		for n := arrow + 1; n < len(f.idx) && got < ptSourceAfter && isSource(n); n++ {
			keep[n] = true
			got++
		}
	}
	for n, i := range f.idx {
		if keep[n] {
			r.emit(lines[i])
		} else if isSource(n) || ptFuncargRe.MatchString(lines[i]) || ptObjArgRe.MatchString(lines[i]) || f.lib {
			r.exempt[i] = true
		}
	}
}

func (r *ptReducer) warnings(idx []int) {
	var tail []int
	for k, i := range idx {
		ln := r.lines[i]
		if strings.HasPrefix(ln, "-- Docs: ") {
			tail = idx[k+1:]
			idx = idx[:k+1]
			break
		}
		if ptDashRe.MatchString(ln) || strings.HasPrefix(ln, "----------") {
			tail = idx[k:]
			idx = idx[:k]
			break
		}
	}
	defer func() {
		for _, i := range trimBlankIdx(r.lines, tail) {
			r.emit(r.lines[i])
		}
	}()
	type group struct {
		ids   []string
		msg   []string
		key   string
		locs  []string
		first int
	}
	var groups []*group
	byKey := map[string]*group{}
	var ids, msg []int
	flush := func() {
		var m []int
		for _, i := range msg {
			if strings.TrimSpace(r.lines[i]) != "" {
				m = append(m, i)
			}
		}
		msg = m
		if len(ids) == 0 && len(msg) == 0 {
			return
		}

		if n := len(msg); n > 0 && strings.HasPrefix(r.lines[msg[n-1]], "    ") {
			r.exempt[msg[n-1]] = true
			msg = msg[:n-1]
		}
		var mtext []string
		for _, i := range msg {
			mtext = append(mtext, r.lines[i])
		}
		key, loc := warnKey(mtext)
		g := byKey[key]
		if g == nil || key == "" {
			g = &group{msg: mtext, key: key, first: len(groups)}
			groups = append(groups, g)
			if key != "" {
				byKey[key] = g
			}
		} else {
			for _, i := range msg {
				r.exempt[i] = true
			}
		}
		if loc != "" {
			g.locs = append(g.locs, loc)
		}
		for _, i := range ids {
			g.ids = append(g.ids, r.lines[i])
		}
		ids, msg = nil, nil
	}
	for _, i := range idx {
		ln := r.lines[i]
		switch {
		case strings.HasPrefix(ln, "-- Docs: "):
		case strings.TrimSpace(ln) == "":
			if len(msg) > 0 {
				msg = append(msg, i)
			}
		case ln[0] != ' ' && ln[0] != '\t':
			if len(msg) > 0 {
				flush()
			}
			ids = append(ids, i)
		default:
			msg = append(msg, i)
		}
	}
	flush()
	shown, restGroups, restEntries := 0, 0, 0
	for _, g := range groups {
		if shown >= ptMaxWarnGroups && !anyError(g.msg) {
			restGroups++
			restEntries += max(1, len(g.ids))
			continue
		}
		shown++
		if len(g.ids) > 0 {
			id := g.ids[0]
			if len(g.ids) > 1 {
				id += fmt.Sprintf(" (+%d more)", len(g.ids)-1)
			}
			r.emit(id)
		}
		r.emit(relativize(r.c, append([]string(nil), g.msg...))...)
		if n := len(uniq(g.locs)); n > 1 {
			locs := uniq(g.locs)[1:]
			more := ""
			if len(locs) > 5 {
				more = fmt.Sprintf(", … +%d", len(locs)-5)
				locs = locs[:5]
			}
			r.emit(fmt.Sprintf("  [lx: same warning also at %s%s]", strings.Join(relativize(r.c, locs), ", "), more))
		}
	}
	if restGroups > 0 {
		r.emit(fmt.Sprintf("[lx: +%d more warning groups (%d test/location entries)]", restGroups, restEntries))
	}

	for _, i := range idx {
		ln := r.lines[i]
		if ln != "" && ln[0] != ' ' && !strings.HasPrefix(ln, "-- Docs: ") {
			r.exempt[i] = true
		}
	}
}

var warnMsgRe = lazyre.New(`^\s*(\S(?:.*?\S)?):(\d+): ([A-Za-z_][\w.]*): (.*)$`)

func warnKey(msg []string) (key, loc string) {
	if len(msg) == 0 {
		return "", ""
	}
	m := warnMsgRe.FindStringSubmatch(msg[0])
	if m == nil {
		return strings.Join(msg, "\n"), ""
	}
	rest := append([]string{m[3] + ": " + m[4]}, msg[1:]...)
	return strings.Join(rest, "\n"), m[1] + ":" + m[2]
}

func uniq(s []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
