package python

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// pytestFilter condenses pytest's terminal report.
//
// Kept verbatim: the "collected N items" line, FAILED/ERROR/XPASS lines of
// -v runs, every section header, every failure block header, every "E"
// line, every location line of project code, chained-exception separators,
// every message line of collection errors ("ImportError while importing
// test module …", hints, E lines), the "short test summary info" section,
// "!!! … !!!" interruption lines, unknown sections (coverage, plugins) and
// the final "N failed, M passed … in Xs" line (the counts are never
// rebuilt, so "1 error" can never be dropped).
//
// Folded, with counted markers: the session header, progress lines and
// PASSED lines, SKIPPED/XFAIL lines beyond ten per status, source lines
// more than three lines above the failing ">" line (or above the test's
// own def line), runs of library frames (site-packages, stdlib, <frozen>,
// _pytest, pluggy), captured output beyond 20 lines per section after
// similar lines are collapsed (error lines inside are always kept), the
// echoed source line of each warning, and warnings repeated at several
// locations.
//
// Source lines of tracebacks are code, not messages, but the line
// classifier cannot tell `assert not result.exception` from an error
// message, so the filter implements engine.Guarded and runs its own guard
// (ensureErrors) that exempts exactly the traceback source lines it folds.
type pytestFilter struct{}

func (pytestFilter) Name() string { return "pytest" }

func (pytestFilter) Match(c *engine.Context) bool {
	inv := parseInvocation(c)
	if inv.tool != "pytest" {
		return false
	}
	// Listings, help and version output are not test reports.
	return !hasArg(inv.args, "--co", "--collect-only", "--collectonly", "--version", "-V", "--help", "-h",
		"--fixtures", "--funcargs", "--fixtures-per-test", "--markers", "--trace-config", "--setup-plan")
}

func (pytestFilter) GuardsErrors() bool { return true }

// Stream: pytest-xdist's --looponfail re-runs forever.
func (pytestFilter) Stream(c *engine.Context) bool {
	return hasArg(parseInvocation(c).args, "-f", "--looponfail")
}

func (pytestFilter) Apply(c *engine.Context, out string) (string, bool) {
	r, _, ok := reducePytest(c, out)
	return r, ok
}

var (
	// The final result line, with or without the === wrapper (-q).
	ptSummaryRe    = lazyre.New(`^(?:=+ )?(?:(?:\d+ (?:subtests? )?(?:failed|passed|skipped|deselected|xfailed|xpassed|warnings?|errors?|rerun)(?:, )?)+|no tests ran) in \d+(?:\.\d+)?s(?:econds)?(?: \([\d:.]+\))?(?: =+)?$`)
	ptSectionRe    = lazyre.New(`^=+ (.+?) =+$`)
	ptBangRe       = lazyre.New(`^!{3,} .* !{3,}$`)
	ptBlockRe      = lazyre.New(`^_{3,} (.+?) _{3,}$`)
	ptDashRe       = lazyre.New(`^-{2,} (.+?) -{2,}$`)
	ptCaptureRe    = lazyre.New(`^-{3,} Captured (?:stdout|stderr|log|warnings?) (?:setup|call|teardown) -{3,}$`)
	ptHeaderRe     = lazyre.New(`^(?:platform \S+ -- Python |cachedir: |rootdir: |configfile: |inifile: |testpaths: |plugins: |hypothesis profile |asyncio: |benchmark: |django: settings|Django settings: |metadata: |timeout: |timeout method: |timeout func_only: |sensitiveurl: |base_url: |html: |cov: |anyio: |xdist: |scheduling tests via \w+$)`)
	ptCollectRe    = lazyre.New(`\bcollected \d+ items?\b|\bno tests collected\b|^\d+ workers? \[\d+ items?\]$`)
	ptCollectingRe = lazyre.New(`^collecting \.\.\. ?$|^bringing up nodes\.\.\.$|^created: \d+/\d+ workers?$`)
	// Progress: "tests/test_x.py ..F.s [ 42%]", "....F  [100%]", ".... [ 14%]",
	// console_output_style=count "[ 5/23]", a lone "[100%]" (a plugin
	// printed in the middle of the line). Group 1 is the file, group 2 the
	// status characters (u and , are pytest 9 subtest results).
	ptProgressRe = lazyre.New(`^(?:(\S+\.py)(?:::\S+)? ?)?([.sFExXRu,]+) *(?:\[ *\d+(?:%|/\d+)\])?$|^\S+\.py \[ *\d+(?:%|/\d+)\]$|^\[ *\d+(?:%|/\d+)\]$`)
	// Verbose: "tests/test_x.py::test_y[p] PASSED   [ 42%]" (or "[ 5/23]"), xdist "[gw1] [ 42%] PASSED tests/…".
	ptVerboseRe = lazyre.New(`^(\S.*::.+?) (PASSED|FAILED|ERROR|SKIPPED|XFAIL|XPASS)(?: \(.*\))?(?: +\[ *\d+(?:%|/\d+)\])?$`)
	ptXdistRe   = lazyre.New(`^\[gw\d+\] \[ *\d+(?:%|/\d+)\] (PASSED|FAILED|ERROR|SKIPPED|XFAIL|XPASS) \S`)
	// xdist -v announces each test before a worker reports it: a bare node id.
	ptNodeIDRe = lazyre.New(`^\S+\.py::\S.*$`)
	// Traceback frames: long-style location ("path:12: AssertionError",
	// "path:12:"), short-style frame head ("path:12: in test_x").
	ptLongLocRe  = lazyre.New(`^(\S(?:.*?\S)?):(\d+):(?: ([A-Za-z_][\w.]*))?$`)
	ptShortLocRe = lazyre.New(`^(\S(?:.*?\S)?):(\d+): in \S.*$`)
	ptFrameSepRe = lazyre.New(`^(?:_ ){3,}_?$`)
	ptELineRe    = lazyre.New(`^E(?:\s|$)`)
	ptObjArgRe   = lazyre.New(`^(?:[A-Za-z_]\w* = <[\w.]+ object at 0x[0-9a-f]+>(?:, |$))+$`)
	ptFuncargRe  = lazyre.New(`^[A-Za-z_]\w* = `)
	ptBracketsRe = lazyre.New(`^[\])},]+$`)
	// pytest-xdist prints the worker's platform at the top of each failure.
	ptWorkerRe = lazyre.New(`^\[gw\d+\] \S+ -- Python \S+ \S+$`)
	// A non-zero failure count in the result line ("1 xfailed" is not one).
	ptFailCountRe = lazyre.New(`(?:^|[ =])[1-9]\d* (?:subtests? )?(?:failed|errors?)\b`)
)

const (
	ptMaxStatusLines = 10 // SKIPPED / XFAIL lines kept per status in -v runs
	ptSourceBefore   = 3  // source lines kept above the failing ">" line
	ptSourceAfter    = 4  // continuation lines kept below it
	ptCaptureHead    = 8  // captured output kept per section: head …
	ptCaptureTail    = 12 // … and tail
	ptMaxWarnGroups  = 20
	ptMaxDurations   = 25
	ptCollapseMax    = 2000 // captured sections longer than this are only capped
)

// Per-line matchers with cheap necessary conditions in front of the
// regular expressions: a 50k-line report is matched line by line several
// times, and most lines fail on the first byte.

// ptSection returns the title of a "=== title ===" line.
func ptSection(ln string) (string, bool) {
	if len(ln) < 5 || ln[0] != '=' {
		return "", false
	}
	if m := ptSectionRe.FindStringSubmatch(ln); m != nil {
		return m[1], true
	}
	return "", false
}

// ptIsSummary matches the result line ("… in 0.12s", with or without "=").
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

// ptStatus returns the outcome of a -v status line (plain or xdist), or "".
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

// ptProgress matches a progress line (trimmed): its file and status marks.
func ptProgress(t string) (file, marks string, ok bool) {
	// Necessary condition: without its "[ 42%]" tail the line is empty,
	// ends with ".py" or ends with a status mark ("… PASSED [ 42%]" does not).
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

// ptReducer carries the state of one reduction.
type ptReducer struct {
	c      *engine.Context
	lines  []string
	exempt []bool // input lines that are traceback source code
	out    []string
	// failIDs: the report names its failing tests somewhere else than in
	// the progress lines (short test summary, -v status lines, FAILURES /
	// ERRORS blocks). Without it (--tb=no -rN, -qq -rs) the F/E progress
	// lines are the only trace of which files failed, and are kept.
	failIDs bool
	// xdist: pytest-xdist output (-n), whose -v mode announces every test
	// with a bare node id line before a worker reports its status.
	xdist      bool
	errMemo    []int8   // engine.IsError per input line: 0 unknown, 1 no, 2 yes
	statusMemo []string // ptStatus per input line ("\x00" = not computed)
}

// status is ptStatus for input line i, computed once.
func (r *ptReducer) status(i int) string {
	if r.statusMemo[i] == "\x00" {
		r.statusMemo[i] = ptStatus(r.lines[i])
	}
	return r.statusMemo[i]
}

// isErr is engine.IsError for input line i, computed once: the classifier
// costs tens of microseconds on lines holding an error-like word, and
// captured output is checked by the cap and by the guard.
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

// ptRegion is a part of the report between two section headers.
type ptRegion struct {
	title      string
	hdr        int // header line index, -1 for the preamble
	start, end int // body [start, end)
}

var ptReportSections = map[string]bool{"FAILURES": true, "ERRORS": true, "XFAILURES": true, "XPASSES": true, "PASSES": true}

// reducePytest returns the condensed report, the number of lines its own
// guard had to re-add (0 for a correct reduction), and ok=false when the
// output is not a pytest report it recognizes.
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
	// A report ends with the result line; -qq omits it but still prints the
	// short test summary; a run killed by a signal (segfault in a C
	// extension, faulthandler_timeout) ends with faulthandler's dump after
	// the session header. Anything else (a run killed without a dump, usage
	// errors, a conftest ImportError) goes to the generic reducer untouched.
	if summary < 0 && !hasShort && !pytestCrashed(lines) {
		return "", 0, false
	}
	r := &ptReducer{c: c, lines: lines, exempt: make([]bool, len(lines)), errMemo: make([]int8, len(lines)), statusMemo: make([]string, len(lines))}
	for i := range r.statusMemo {
		r.statusMemo[i] = "\x00"
	}
	regions := splitPytestRegions(lines, summary)

	// What identifies the failing tests besides the progress lines?
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
			// Unknown sections (coverage, rerun summary, plugin reports)
			// are what the user asked for: verbatim.
			r.emit(lines[rg.hdr])
			r.verbatim(body)
		}
	}

	if summary >= 0 {
		r.exitNote(lines[summary])
	} else if c.Exit != 0 {
		// -qq, a crash, an unknown result line: nothing else carries the
		// verdict.
		r.emit(fmt.Sprintf("[lx: pytest exited %d]", c.Exit))
	}
	out := r.out
	out, added := ensureErrorsFn(lines, r.exempt, out, r.isErr)
	out = relativize(c, out)
	return strings.TrimRight(strings.Join(out, "\n"), "\n"), added, true
}

// pytestCrashed recognizes a session that died in a faulthandler dump:
// the session header, then "Fatal Python error: …" (or a timeout dump).
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

// splitPytestRegions splits the report at section headers ("=== title
// ==="), "!!! … !!!" lines and the result line. A second session header
// inside a report section is the output of a nested pytest run printed by
// a test (pytester, subprocess): up to that run's own result line it stays
// part of the captured output it belongs to, provided such a result line
// exists before the report's own.
func splitPytestRegions(lines []string, summary int) []ptRegion {
	var regions []ptRegion
	cur := ptRegion{hdr: -1, start: 0}
	seenSession := false
	limit := summary
	if limit < 0 {
		limit = len(lines)
	}
	// nextResult[i]: the first result line at or after i and before the
	// report's own, or -1 (one backward pass: no rescans per header).
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
					i = nested // lines i..nested stay in the current region
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

// exitNote adds a marker when the exit status contradicts a pass-looking
// result line, and explains exit 5.
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

// capped keeps the first n non-blank lines of a section.
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

// progress handles the session header and the per-test progress region.
// Header lines, progress lines, PASSED lines and SKIPPED/XFAIL lines
// beyond ten per status are counted; everything else (collected N items,
// FAILED/ERROR/XPASS lines, captured -s output, plugin and INTERNALERROR
// lines) is kept, with library frames of any traceback among them folded.
// When nothing else in the report names the failing tests, progress lines
// with F/E marks are kept too, with the file line a continuation line
// belongs to.
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
	lastFile := -1 // position of the last progress line that names a file
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
				class[k] = keep // unknown: captured -s output, plugin lines
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
			// Status lines: directory names like tests/errors/ must not
			// make them look like failures to the guard.
			progress++
			r.exempt[i] = true
		case passedLine:
			passed++
			r.exempt[i] = true
		case startedLine:
			started++
			r.exempt[i] = true // a test name, its result is reported below
		case statusLine:
			status := r.status(i)
			if shown[status] < ptMaxStatusLines {
				shown[status]++
				keptIdx = append(keptIdx, i)
			} else {
				hidden[status]++
				r.exempt[i] = true // skip reasons are not failures
			}
		}
	}
	// Tracebacks printed outside the report sections (INTERNALERROR>,
	// pytest-timeout's stack dumps): library frames folded.
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

// shortSummary keeps "short test summary info" except PASSED lines (-rA).
func (r *ptReducer) shortSummary(idx []int) {
	passed := 0
	for _, i := range idx {
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

// failures handles FAILURES / ERRORS (and PASSES / XFAILURES) sections:
// one block per "____ test_name ____" header.
func (r *ptReducer) failures(idx []int) {
	// Lines before the first block header: --tb=line output, one location
	// line per failure. Kept.
	k := 0
	for k < len(idx) && !ptBlockRe.MatchString(r.lines[idx[k]]) {
		if ln := r.lines[idx[k]]; strings.TrimSpace(ln) != "" {
			r.emit(ln)
		}
		k++
	}
	for k < len(idx) {
		h := idx[k]
		e := k + 1
		for e < len(idx) && !ptBlockRe.MatchString(r.lines[idx[e]]) {
			e++
		}
		body := idx[k+1 : e]
		r.emit(r.lines[h])
		// Collection errors ("ERROR collecting …") go through the same
		// path: their messages, hints and E lines are all kept; only runs
		// of _pytest/importlib frames are folded.
		r.block(body)
		k = e
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

// block condenses one failure: its traceback, then its captured output
// sections. A dashed line that is not a "Captured …" header (pytest-cov's
// "---- coverage: … ----", "- generated xml file … -") ends the block and
// everything from there on is kept verbatim.
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
				// Not a capture: keep the remainder as is.
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
		// Runs of lines differing only in numbers/ids collapse first, then
		// the section is capped; both keep every error line. Very long
		// sections are only capped: collapsing classifies every line again.
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

// ptFrame is one traceback entry of a long/short/auto style report.
type ptFrame struct {
	idx   []int // all lines of the entry, location line included
	loc   int   // index (into r.lines) of the location line, -1 if none
	short bool  // "path:N: in func" entry (location first)
	chain bool  // a chained-exception separator line
	lib   bool
	root  string
	hasE  bool
}

// traceback condenses the traceback part of a failure block.
func (r *ptReducer) traceback(idx []int) {
	idx = trimBlankIdx(r.lines, idx)
	if len(idx) == 0 {
		return
	}
	// --tb=native: Python tracebacks, folded like a script's.
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
			r.emit(r.lines[f.loc]) // a lone library frame: its location only
		} else {
			r.emit(foldMarker("", e-k, roots))
		}
		k = e
	}
}

// isLocCandidate: location lines start at column 0 and are neither the
// failing-line marker nor an E line.
func isLocCandidate(ln string) bool {
	return ln != "" && ln[0] != ' ' && ln[0] != '\t' && ln[0] != '>' && !ptELineRe.MatchString(ln)
}

// frame emits one kept traceback entry.
func (r *ptReducer) frame(f ptFrame) {
	if f.chain {
		r.emit("", r.lines[f.idx[0]])
		return
	}
	if f.short {
		// "path:N: in func", its source line(s), E lines: all short.
		for _, i := range f.idx {
			if strings.TrimSpace(r.lines[i]) != "" {
				r.emit(r.lines[i])
			}
		}
		return
	}
	// Long entry: [funcargs] [source … > failing line …] [E lines] location.
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
			// Decided below.
		case ptObjArgRe.MatchString(ln):
			// "runner = <click.testing.CliRunner object at 0x…>": no information.
		case f.lib && ptFuncargRe.MatchString(ln):
			// Arguments of a library frame.
		case f.lib && arrow >= 0 && !strings.HasPrefix(ln, ">") && !ptELineRe.MatchString(ln) && i != f.loc:
		default:
			keep[n] = true
		}
	}
	if arrow < 0 {
		// No failing-line marker: an unusual shape, keep its source.
		for n := range f.idx {
			if isSource(n) {
				keep[n] = true
			}
		}
	} else if !f.lib {
		// Up to 3 non-blank source lines above ">", up to 4 below it.
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
				continue // "],", ")": the tail of a decorator argument list
			}
			keep[n] = true
			got++
			if strings.HasPrefix(t, "def ") || strings.HasPrefix(t, "async def ") {
				break // the function's own signature: context above it is another scope
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

// warnings groups the "warnings summary" section: pytest already groups
// one warning (message + location) with the tests that raised it; groups
// with the same category and message at different locations are merged,
// test ids beyond the first are counted, and the echoed source line is
// dropped.
func (r *ptReducer) warnings(idx []int) {
	// The section ends with pytest's "-- Docs: …" line. Whatever follows is
	// not a warning: plugins print their terminal summary right after it
	// (pytest-cov < 5's "---- coverage: … ----" table and its "FAIL
	// Required test coverage …" verdict, junitxml/html report paths). It is
	// kept verbatim; so is everything from a dashed separator line on when
	// the Docs line is missing.
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
		msg   []string // message lines (first carries "path:line: Category: text")
		key   string
		locs  []string
		first int
	}
	var groups []*group
	byKey := map[string]*group{}
	var ids, msg []int
	flush := func() {
		// Blank lines inside a message (pytest indents every line of
		// multi-line warnings, including empty ones) are decoration.
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
		// The last line indented 4+ is the echoed source line.
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
				r.exempt[i] = true // merged into an identical message
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
			// Link to pytest's documentation: dropped.
		case strings.TrimSpace(ln) == "":
			if len(msg) > 0 {
				msg = append(msg, i)
			}
		case ln[0] != ' ' && ln[0] != '\t':
			// A test id (or location) line: after message lines it
			// starts the next group.
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
			// Beyond the cap; a warning carrying an error line (an
			// unraisable exception's traceback) is still shown.
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
	// Test ids of grouped warnings are test names, not messages.
	for _, i := range idx {
		ln := r.lines[i]
		if ln != "" && ln[0] != ' ' && !strings.HasPrefix(ln, "-- Docs: ") {
			r.exempt[i] = true
		}
	}
}

var warnMsgRe = lazyre.New(`^\s*(\S(?:.*?\S)?):(\d+): ([A-Za-z_][\w.]*): (.*)$`)

// warnKey returns the grouping key (category + message without location)
// and the location of a warning's message lines.
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
