package golang

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// testText renders `go test` text output (plain, -v, -cover, -count,
// multi-package, build failures, panics, timeouts, data races):
//
//   - every "--- FAIL:" line is kept with all of its output (file_test.go:NN
//     messages, expected/got values, an Example's got:/want: blocks) and its
//     failing subtests; with -v the output is re-indented under its FAIL
//     line exactly as go test prints it without -v; with -count=N each run
//     keeps its own outcome;
//   - "=== RUN/PAUSE/CONT/NAME" and "--- PASS" lines are dropped and
//     counted; output of passing tests is hidden (counted) unless it is
//     short, except error-like lines, which are kept under a marked header;
//     skipped tests are listed with their reason (one per distinct reason
//     when there are many);
//   - in a failing package only output between its last "--- FAIL" and the
//     binary's final PASS/FAIL line can be hidden: what TestMain prints after
//     that line (race reports, leak checks, coverage) and the last output of
//     a binary that exited abnormally (log.Fatal, os.Exit, kill) is kept;
//   - panics and timeouts keep the panic message, the "running tests:" list
//     and the test's own frames; library frames and duplicate goroutines are
//     folded (engine.FoldStacks / engine.GroupGoroutines); a "panic: " line
//     that a test merely printed is recognized once its package goes on;
//   - compiler errors ([build failed], [setup failed]) are kept verbatim;
//   - "FAIL pkg" lines and the final FAIL are kept verbatim; more than three
//     passing packages whose "ok" lines differ only in name and time (also
//     with "[no tests to run]" or the same coverage figure) collapse into one
//     counted line, as do "[no test files]" lines;
//   - the footer counts what was hidden and never reads as a pass when a
//     package failed.
type testText struct{}

func (testText) Name() string { return "go-test" }

func (testText) Match(c *engine.Context) bool {
	if !isGo(c) {
		return false
	}
	sub, args := goArgs(c)
	return sub == "test" && !flagSet(args, "json") && testShapeFlagsOK(args)
}

// testShapeFlagsOK rejects go test modes whose output is not a test report:
// benchmarks and fuzzing (the numbers are the point), -list (the names are
// the point), -x/-n (command traces), -c (compile only: go-build handles it)
// and help.
func testShapeFlagsOK(args []string) bool {
	return !flagSet(args, "bench", "fuzz", "list", "x", "n", "c", "h", "help")
}

// GuardsErrors: the go test filters run the error guard themselves (see
// guardTest), over the output minus === RUN/PAUSE/CONT/NAME lines.
func (testText) GuardsErrors() bool { return true }

func (testText) Apply(c *engine.Context, out string) (string, bool) {
	lines := splitLines(out)
	if len(lines) == 0 {
		return "", false
	}
	fetched, _ := condenseFetch(lines)
	res, ok := parseText(fetched).render(c, runOpts(c))
	if !ok {
		return "", false
	}
	return guardTest(lines, res), true
}

// guardTest is engine.Guard over the original lines minus the === RUN /
// PAUSE / CONT / NAME markers. A marker only says a test started or
// resumed; it is error-class when the test's name holds a word like
// "conflict" or "error" (TestResolve/conflict), and its outcome is always
// in the view: the "--- FAIL" line is kept, a pass or skip is counted. Every
// other error-class line the renderer did not keep is re-added, exactly as
// the engine's runtime guard would.
//
// engine.Guard searches the whole output once per error line, which is
// quadratic on large failing runs (50k distinct error lines against a 3 MB
// view take ~20 s). Lines kept whole are found in a set first; only the
// rest (reformatted or really missing) go to engine.Guard.
func guardTest(lines []string, out string) string {
	kept := make(map[string]bool, strings.Count(out, "\n")+1)
	for _, ln := range strings.Split(out, "\n") {
		kept[squashSpace(ln)] = true
	}
	var b strings.Builder
	for _, ln := range lines {
		if matchMarker(ln) != nil || kept[squashSpace(ln)] || !engine.IsError(ln) {
			continue
		}
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return out
	}
	res, _ := engine.Guard(b.String(), out)
	return res
}

// squashSpace collapses whitespace runs as the error guard compares lines.
func squashSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// TestTextDetect renders go test text output found in the output of any
// command (make test, a CI script, docker compose run app go test). It
// returns ok=false unless the text carries go test's own verdict lines.
// Lines before the first go test line (a make recipe echo) are kept
// verbatim. Like the go-test filter it guards error lines itself, so a
// caller may treat the result as engine.Guarded.
func TestTextDetect(c *engine.Context, out string) (string, bool) {
	lines := splitLines(out)
	if len(lines) == 0 {
		return "", false
	}
	verdicts := 0
	for _, ln := range lines {
		if okRe.MatchString(ln) || failPkgRe.MatchString(ln) || noFilesRe.MatchString(ln) {
			verdicts++
		}
	}
	if verdicts == 0 {
		return "", false
	}
	fetched, _ := condenseFetch(lines)
	res, ok := parseText(fetched).render(c, options{})
	if !ok {
		return "", false
	}
	return guardTest(lines, res), true
}

// options are the command-line facts the renderer uses.
type options struct {
	run bool // -run given: the user wants to see the selected tests' output
}

func runOpts(c *engine.Context) options {
	_, args := goArgs(c)
	return options{run: flagSet(args, "run")}
}

var (
	markerRe = lazyre.New(`^=== (RUN|PAUSE|CONT|NAME)(?:\s+(.*))?$`)
	resultRe = lazyre.New(`^(\s*)--- (FAIL|PASS|SKIP): (.+) \((\d+(?:\.\d+)?)s\)$`)
	// A result line printed right after test output that did not end in a
	// newline ("progress: 3/5--- FAIL: TestX (0.00s)"): go test prints it
	// glued to that output. The groups are those of resultRe, after the
	// glued text.
	gluedResultRe = lazyre.New(`^\S.*?( *)--- (FAIL|PASS|SKIP): (.+) \((\d+(?:\.\d+)?)s\)$`)
	// Lines cmd/go prints about a test binary it killed or that died from a
	// signal (exec.ExitError text), before the package's FAIL line.
	killedRe = lazyre.New(`^(?:\*\*\* Test (?:killed(?: with \w+)?: ran too long \(|I/O incomplete )` +
		`|signal: [a-z][a-z /-]*(?: \(core dumped\))?$)`)
	// Package verdict lines, exactly as cmd/go formats them.
	okRe      = lazyre.New(`^ok  \t(\S+)\t(.+)$`)
	failPkgRe = lazyre.New(`^FAIL\t(\S+)(?:[\t ].*)?$`)
	noFilesRe = lazyre.New(`^\?   \t(\S+)\t\[no test files\]$`)
	covOnlyRe = lazyre.New(`^\t(\S+)\t+coverage: `)
	// A crash ends normal test output: everything up to the package's
	// verdict is kept (folded).
	crashRe = lazyre.New(`^(?:panic: |fatal error: |SIG[A-Z]+: |\[signal |runtime: |unexpected fault address )`)
	// Compiler / vet diagnostics and their "# pkg" headers.
	diagRe     = lazyre.New(`^(?:vet: )?\S*?\.(?:go|s|c|h|cc|cpp|m|mod|sum|work):\d+(?::\d+)?: \S`)
	buildHdrRe = lazyre.New(`^# (?:\[[\w.~+\-/]+\]|[\w.~+\-/]+(?: \[[\w.~+\-/]+\])?)$`)
	exitRe     = lazyre.New(`^exit status \d+$`)
)

// Cheap prefix checks in front of the per-line regular expressions.
func isCrash(ln string) bool {
	if ln == "" || !strings.ContainsRune("pfSr[u", rune(ln[0])) {
		return false
	}
	return crashRe.MatchString(ln)
}

func matchMarker(ln string) []string {
	if !strings.HasPrefix(ln, "=== ") {
		return nil
	}
	return markerRe.FindStringSubmatch(ln)
}

func matchResult(ln string) []string {
	if !strings.Contains(ln, "--- ") {
		return nil
	}
	return resultRe.FindStringSubmatch(ln)
}

// matchGlued matches a result line glued to preceding test output. The
// returned groups are laid out like resultRe's; group 0 is the whole line,
// which is kept as the result line so the glued text is shown as printed.
func matchGlued(ln string) []string {
	if ln == "" || ln[0] == ' ' || ln[0] == '\t' || !strings.Contains(ln, "--- ") {
		return nil
	}
	return gluedResultRe.FindStringSubmatch(ln)
}

// isBare reports the test binary's final PASS / FAIL line.
func isBare(ln string) bool { return ln == "PASS" || ln == "FAIL" }

func isVerdict(ln string) bool {
	if ln == "" {
		return false
	}
	switch ln[0] {
	case 'o':
		return okRe.MatchString(ln)
	case 'F':
		return failPkgRe.MatchString(ln)
	case '?':
		return noFilesRe.MatchString(ln)
	case '\t':
		return covOnlyRe.MatchString(ln)
	}
	return false
}

// buildLine reports lines that come from the go command or the compiler
// rather than from a test: they are never hidden.
func buildLine(ln string) bool {
	return diagRe.MatchString(ln) || buildHdrRe.MatchString(ln) || exitRe.MatchString(ln) ||
		strings.HasPrefix(ln, "go: ") || strings.HasPrefix(ln, "[go: downloading ") ||
		strings.HasPrefix(ln, "[go -x: ") || ln == "too many errors" ||
		strings.HasPrefix(ln, "note: module requires Go") || killedRe.MatchString(ln)
}

// failEvidence reports a go command / compiler line that shows the run
// failed: a diagnostic, an exit status, a killed test binary, or any
// error-class go command line. "go: downloading", "go: warning: …" and
// "# pkg" headers alone are not evidence.
func failEvidence(ln string) bool {
	if !buildLine(ln) || strings.HasPrefix(ln, "[go") {
		return false
	}
	return diagRe.MatchString(ln) || exitRe.MatchString(ln) || killedRe.MatchString(ln) || engine.IsError(ln)
}

type outLine struct {
	text     string
	reindent bool // printed by -v / -json at 4 spaces whatever the depth
}

type gtest struct {
	name   string
	status byte // 'F', 'P', 'S'; 0 = no result line seen (still running)
	indent int  // indentation of the result line
	result string
	dur    float64
	out    []outLine
	ran    bool
	parent *gtest // the run of the parent test this subtest ran in (-json)
}

// depth is the subtest level: 0 for TestX, 1 for TestX/sub.
func (t *gtest) depth() int {
	if t.result != "" {
		return t.indent / 4
	}
	return strings.Count(t.name, "/")
}

type itemKind uint8

const (
	itResult itemKind = iota
	itStray
	itBare // the test binary's final PASS / FAIL line
)

type item struct {
	kind     itemKind
	t        *gtest
	line     string
	prologue bool // stray line printed before any test activity
}

type nestEntry struct {
	t      *gtest
	indent int
}

// segment is the output of one package, up to and including its verdict.
type segment struct {
	pkg        string
	verdict    string // "ok  \tpkg\t…", "FAIL\tpkg\t…", "?   \tpkg\t…" ("" if none)
	hasBare    bool   // the binary printed its final PASS / FAIL line
	items      []item
	tests      map[string]*gtest
	order      []*gtest
	crash      []string
	crashTests []string // -json: the Test each crash line was attributed to
	crashing   bool
	noCrash    bool // replaying lines of a "crash" that was only test output
	activity   bool
	markers    int
	current    *gtest
	example    *gtest // failed Example whose got:/want: lines follow its result
	nest       []nestEntry
	blanks     int
	json       bool   // built from -json events: attribution is exact
	view       []item // items in render order (see viewItems)
	jsonFail   bool   // -json: package Action=fail without a FAIL line
	sawResult  bool
}

func newSegment() *segment { return &segment{tests: map[string]*gtest{}} }

// bare records the test binary's final PASS / FAIL line. (When the last
// test's output lacked a newline, go test prints it glued, "outputFAIL";
// such a line is left as ordinary output: a line merely ending in FAIL may
// be a test's own, and taking it for the final line would hide the output
// before it.) A real crash never reaches that line, so
// a "crash" still open here was a test printing "panic: …" or
// "runtime: …": its lines are replayed as ordinary output.
func (s *segment) bare(ln string) {
	if s.crashing {
		s.replayCrash()
	}
	s.items = append(s.items, item{kind: itBare, line: ln})
	s.hasBare = true
	s.crashing, s.current, s.nest, s.blanks, s.example = false, nil, nil, 0, nil
}

// setVerdict records the package's verdict line. A package that passed did
// not crash, so an open "crash" without a goroutine dump is replayed as
// ordinary output. (One with a dump stays a crash: that can only be another
// package's traceback whose own verdict line is missing.)
func (s *segment) setVerdict(ln string) {
	if s.crashing && strings.HasPrefix(ln, "ok") && !hasGoroutineHdr(s.crash) {
		s.replayCrash()
	}
	s.crashing = false
	s.verdict = ln
	if s.pkg == "" {
		s.pkg = verdictPkg(ln)
	}
}

func hasGoroutineHdr(lines []string) bool {
	for _, ln := range lines {
		if strings.HasPrefix(ln, "goroutine ") && gHdrRe.MatchString(ln) {
			return true
		}
	}
	return false
}

func (s *segment) replayCrash() {
	lines, tests := s.crash, s.crashTests
	s.crash, s.crashTests, s.crashing = nil, nil, false
	s.noCrash = true
	for i, ln := range lines {
		if s.json {
			s.feedJSON(ln, tests[i])
		} else {
			s.feedText(ln)
		}
	}
	s.noCrash = false
}

func (s *segment) addCrash(ln, test string) {
	s.crash = append(s.crash, ln)
	s.crashTests = append(s.crashTests, test)
}

func (s *segment) get(name string) *gtest {
	if t := s.tests[name]; t != nil {
		return t
	}
	return s.add(name)
}

// fresh returns the test instance a new run of name reports into: the
// current one unless it already has a result. With -count=N (or a test
// name reused by t.Run) a name runs several times, and each run keeps its
// own outcome and output: a flaky test failing once and passing later is
// shown failed.
func (s *segment) fresh(name string) *gtest {
	if t := s.tests[name]; t != nil && t.status == 0 {
		return t
	}
	return s.add(name)
}

func (s *segment) add(name string) *gtest {
	t := &gtest{name: name}
	for i := len(name) - 1; i > 0; i-- {
		if name[i] == '/' {
			if p := s.tests[name[:i]]; p != nil {
				t.parent = p
				break
			}
		}
	}
	s.tests[name] = t
	s.order = append(s.order, t)
	return t
}

func (s *segment) empty() bool {
	return s.verdict == "" && len(s.items) == 0 && len(s.crash) == 0 && s.markers == 0
}

// addBlanks attaches blank lines seen before ln to ln's destination.
func (s *segment) addBlanks(dst *[]outLine, stray bool) {
	for ; s.blanks > 0; s.blanks-- {
		if stray {
			s.items = append(s.items, item{kind: itStray, line: "", prologue: !s.activity})
		} else if dst != nil {
			*dst = append(*dst, outLine{})
		}
	}
}

func (s *segment) startCrash(ln, test string) {
	s.blanks = 0
	s.crashing = true
	s.addCrash(ln, test)
}

func (s *segment) marker(kind, name string) {
	s.markers++
	s.example = nil
	s.activity = true
	s.blanks = 0
	s.nest = nil
	if name == "" || kind == "PAUSE" {
		s.current = nil
		if kind == "PAUSE" && name != "" {
			s.get(name).ran = true
		}
		return
	}
	t := s.get(name)
	if kind == "RUN" {
		t = s.fresh(name)
	}
	t.ran = true
	s.current = t
}

func (s *segment) result(ln string, m []string) *gtest {
	s.activity = true
	s.sawResult = true
	s.blanks = 0
	t := s.fresh(m[3])
	t.status = m[2][0]
	t.indent = indentWidth(m[1])
	t.result = ln
	t.dur, _ = strconv.ParseFloat(m[4], 64)
	s.items = append(s.items, item{kind: itResult, t: t})
	for len(s.nest) > 0 && s.nest[len(s.nest)-1].indent >= t.indent {
		s.nest = s.nest[:len(s.nest)-1]
	}
	s.nest = append(s.nest, nestEntry{t: t, indent: t.indent})
	s.current = nil
	s.example = nil
	if t.status == 'F' && strings.HasPrefix(t.name, "Example") {
		// A failed example prints its "got:" / "want:" blocks unindented
		// after its result line.
		s.example = t
	}
	return t
}

func (s *segment) stray(ln string) {
	s.addBlanks(nil, true)
	s.items = append(s.items, item{kind: itStray, line: ln, prologue: !s.activity})
}

// feedText adds one line of text output, inferring which test printed it:
// with -v, the test named by the last === RUN/CONT/NAME marker; without -v,
// the test whose "--- FAIL" line the (deeper indented) line follows.
func (s *segment) feedText(ln string) {
	if s.crashing {
		s.addCrash(ln, "")
		return
	}
	if strings.TrimSpace(ln) == "" {
		s.blanks++
		return
	}
	if !s.noCrash && isCrash(ln) {
		s.startCrash(ln, "")
		return
	}
	if m := matchMarker(ln); m != nil {
		s.marker(m[1], m[2])
		return
	}
	if m := matchResult(ln); m != nil {
		s.result(ln, m)
		return
	}
	if m := matchGlued(ln); m != nil && (s.markers == 0 || s.tests[m[3]] != nil) {
		// The glued output belongs to the test being reported (-v) or to
		// a test that ran before it; the line is kept whole as printed.
		// With -v, only a test that was started can report.
		s.result(ln, m)
		return
	}
	if s.example != nil && !buildLine(ln) {
		s.addBlanks(&s.example.out, false)
		s.example.out = append(s.example.out, outLine{text: ln})
		return
	}
	w := indentWidth(ln)
	for i := len(s.nest) - 1; i >= 0; i-- {
		if w > s.nest[i].indent {
			// Back at this test's level: deeper subtests are done.
			s.nest = s.nest[:i+1]
			t := s.nest[i].t
			s.addBlanks(&t.out, false)
			t.out = append(t.out, outLine{text: ln})
			return
		}
	}
	s.nest = nil
	if s.current != nil && !buildLine(ln) {
		s.addBlanks(&s.current.out, false)
		s.current.out = append(s.current.out, outLine{text: ln, reindent: true})
		return
	}
	s.stray(ln)
}

// run is a whole go test invocation.
type run struct {
	pre  []string // lines before the first package (-json: stderr, build output)
	segs []*segment
	json bool
}

// parseText splits text output into package segments at verdict lines.
func parseText(lines []string) *run {
	r := &run{}
	s := newSegment()
	for _, ln := range lines {
		if isVerdict(ln) {
			s.setVerdict(ln)
			r.segs = append(r.segs, s)
			s = newSegment()
			continue
		}
		if isBare(ln) {
			s.bare(ln)
			continue
		}
		s.feedText(ln)
	}
	if !s.empty() {
		r.segs = append(r.segs, s)
	}
	return r
}

func verdictPkg(ln string) string {
	for _, re := range []*lazyre.Regexp{okRe, failPkgRe, noFilesRe, covOnlyRe} {
		if m := re.FindStringSubmatch(ln); m != nil {
			return m[1]
		}
	}
	return ""
}

// failed reports whether the segment shows a failure.
func (s *segment) failed() bool {
	if strings.HasPrefix(s.verdict, "FAIL") || len(s.crash) > 0 || s.jsonFail {
		return true
	}
	if s.verdict != "" {
		// "ok" / "?" / coverage-only: go test's own verdict says it passed
		// (a test may print a lone "FAIL" line of its own).
		return false
	}
	for _, it := range s.items {
		if it.kind == itBare && it.line == "FAIL" {
			return true
		}
	}
	for _, t := range s.order {
		if t.status == 'F' {
			return true
		}
	}
	return false
}

// Caps for the passing-test output that is shown rather than hidden.
const (
	showPassingMax    = 40  // lines, any run
	showPassingMaxRun = 200 // lines, when -run selected the tests
	showSkipsMax      = 5   // skipped tests all listed with their reason
	skipGroupsMax     = 5   // otherwise, one skipped test per distinct reason
	leadingStrayMax   = 120 // unattributed lines kept in a failing package
)

type renderer struct {
	c           *engine.Context
	opt         options
	showPassing bool
	showSkips   bool
	verbose     bool

	passed, failedN, skipped int
	failedPkgs               int
	hiddenMarkers            int // === lines and unshown --- PASS lines
	hiddenSkips              int // unshown --- SKIP lines
	hiddenOut                int // output lines of passing tests
	hiddenSkipOut            int // output lines of unshown skipped tests
	evidence                 bool
	subtests                 bool
}

// span locates, in a segment's items, the last FAIL result and the last
// final PASS / FAIL line (-1 when there is none).
type span struct{ lastFail, lastBare int }

func spanOf(items []item) span {
	sp := span{-1, -1}
	for i, it := range items {
		switch {
		case it.kind == itResult && it.t.status == 'F':
			sp.lastFail = i
		case it.kind == itBare:
			sp.lastBare = i
		}
	}
	return sp
}

// hideable reports whether stray item i may be hidden as output of tests
// that passed: it follows the first test activity, is not compiler / go
// command output, and either the package passed, or it lies between the
// package's last "--- FAIL" and the test binary's final PASS / FAIL line.
// In a failing package nothing else is hidden:
//
//   - before the last "--- FAIL" is the output of the failing tests
//     (without -v a test's output precedes its result line);
//   - after the final PASS / FAIL line is TestMain's own output: race
//     reports, leak checks, coverage;
//   - without a final PASS / FAIL line the binary exited abnormally
//     (log.Fatal, os.Exit, a crash, a timeout) and its last lines say why.
func hideable(items []item, i int, failed bool, sp span) bool {
	it := items[i]
	if it.prologue || buildLine(it.line) {
		return false
	}
	if !failed {
		return true
	}
	return sp.lastFail >= 0 && i > sp.lastFail && i < sp.lastBare
}

// viewItems returns the items in render order: as printed for text
// output, parent-first for -json.
func (s *segment) viewItems() []item {
	if s.view == nil {
		s.view = s.items
		if s.json {
			s.view = s.treeOrder()
		}
	}
	return s.view
}

// treeOrder returns the items with each failing subtest's result moved
// right after its parent's (go test -json reports a subtest before its
// parent), and records the subtest depth for indentation.
func (s *segment) treeOrder() []item {
	parent := func(t *gtest) *gtest {
		if t.parent != nil {
			if t.parent.status != 0 {
				return t.parent
			}
			return nil
		}
		for i := len(t.name) - 1; i > 0; i-- {
			if t.name[i] == '/' {
				if p := s.tests[t.name[:i]]; p != nil && p.status != 0 {
					return p
				}
			}
		}
		return nil
	}
	children := map[*gtest][]item{}
	var top []item
	for _, it := range s.items {
		if it.kind == itResult {
			if p := parent(it.t); p != nil {
				children[p] = append(children[p], it)
				continue
			}
		}
		top = append(top, it)
	}
	out := make([]item, 0, len(s.items))
	var walk func(it item, depth int)
	walk = func(it item, depth int) {
		if it.kind == itResult && depth > 0 {
			t := it.t
			t.indent = 4 * depth
			t.result = strings.Repeat("    ", depth) + strings.TrimLeft(t.result, " \t")
		}
		out = append(out, it)
		if it.kind == itResult {
			for _, ch := range children[it.t] {
				walk(ch, depth+1)
			}
		}
	}
	for _, it := range top {
		walk(it, 0)
	}
	return out
}

func (r *run) render(c *engine.Context, opt options) (string, bool) {
	rd := &renderer{c: c, opt: opt, verbose: r.json}
	recognized := false
	skips := 0
	hideTotal := 0
	for _, s := range r.segs {
		if s.verdict != "" || s.sawResult || s.markers > 0 || s.hasBare || s.jsonFail {
			recognized = true
		}
		if s.markers > 0 {
			rd.verbose = true
		}
		items := s.viewItems()
		failed, sp := s.failed(), spanOf(items)
		if failed {
			rd.evidence = true
			// Not the run's own final FAIL line after the last package.
			if s.verdict != "" || len(s.crash) > 0 || s.jsonFail {
				rd.failedPkgs++
			}
		}
		for i, it := range items {
			if it.kind != itStray {
				continue
			}
			if hideable(items, i, failed, sp) && it.line != "" {
				hideTotal++
			}
			if failEvidence(it.line) {
				rd.evidence = true
			}
		}
		for _, t := range s.order {
			switch t.status {
			case 'P':
				hideTotal += nonBlank(t.out)
			case 'S':
				skips++
			}
		}
	}
	for _, ln := range r.pre {
		if failEvidence(ln) {
			rd.evidence = true
		}
	}
	if !recognized {
		return "", false
	}
	if c.Failed() && !rd.evidence {
		// Exit status says failure but the report shows none (killed after
		// the last package, a wrapper's own failure, …): show everything.
		return "", false
	}
	// Short output of passing tests is shown when the run passed (the agent
	// is likely reading its t.Log lines) or when -run picked the tests;
	// next to failures it is noise.
	switch {
	case opt.run:
		rd.showPassing = hideTotal <= showPassingMaxRun
	case !rd.evidence:
		rd.showPassing = hideTotal <= showPassingMax
	}
	rd.showSkips = skips <= showSkipsMax

	var chunks [][]string
	pre := r.pre
	if len(pre) > 0 {
		chunks = append(chunks, pre)
	}
	// Passing packages whose verdict lines differ only in name and time
	// collapse into one counted line per group (see okKey).
	type collapsed struct {
		chunk  int
		line   string
		pkg    string
		cached bool
	}
	groups := map[string][]collapsed{}
	var keys []string
	for _, s := range r.segs {
		body, key, ok := rd.segment(s)
		if ok && len(body) > 0 {
			// Output shown for a package stays above its own verdict line.
			body, ok = append(body, s.verdict), false
		}
		chunks = append(chunks, body)
		if !ok {
			continue
		}
		if _, seen := groups[key]; !seen {
			keys = append(keys, key)
		}
		groups[key] = append(groups[key], collapsed{len(chunks), s.verdict, s.pkg, strings.Contains(s.verdict, "\t(cached)")})
		chunks = append(chunks, nil)
	}
	for _, key := range keys {
		list := groups[key]
		if len(list) <= 3 {
			for _, e := range list {
				chunks[e.chunk] = []string{e.line}
			}
			continue
		}
		pkgs := make([]string, len(list))
		cached := 0
		for i, e := range list {
			pkgs[i] = e.pkg
			if e.cached {
				cached++
			}
		}
		chunks[list[len(list)-1].chunk] = []string{groupSummary(key, pkgs, cached)}
	}

	var b []string
	for _, ch := range chunks {
		b = append(b, ch...)
	}
	if f := rd.footer(); f != "" {
		b = append(b, f)
	}
	res := strings.Join(trimBlankRuns(b), "\n")
	return engine.RelativizeNonErrors(c, res), true
}

func nonBlank(out []outLine) int {
	n := 0
	for _, o := range out {
		if strings.TrimSpace(o.text) != "" {
			n++
		}
	}
	return n
}

// skipLocRe is the file:line prefix of a t.Skip message.
var skipLocRe = lazyre.New(`^\S+\.go:\d+: `)

// skipReason is a skipped test's first output line without its location:
// tests skipped for the same reason are shown once.
func skipReason(t *gtest) string {
	for _, o := range t.out {
		if s := strings.TrimSpace(o.text); s != "" {
			return skipLocRe.ReplaceAllString(s, "")
		}
	}
	return ""
}

// segment renders one package. When the package passed with a verdict
// line that may be collapsed with others (see okKey), the verdict is not
// part of body and key/collapse report its group.
func (rd *renderer) segment(s *segment) (body []string, key string, collapse bool) {
	failed := s.failed()
	var hiddenErr []string
	errFromSkip := false
	hide := func(lines []outLine, skip bool) {
		for _, o := range lines {
			if strings.TrimSpace(o.text) == "" {
				continue
			}
			if skip {
				rd.hiddenSkipOut++
			} else {
				rd.hiddenOut++
			}
			if engine.IsError(o.text) {
				hiddenErr = append(hiddenErr, strings.TrimSpace(o.text))
				errFromSkip = errFromSkip || skip
			}
		}
	}
	flushHiddenErr := func() {
		if len(hiddenErr) == 0 {
			return
		}
		who := "passing tests"
		if errFromSkip {
			who = "passing or skipped tests"
		}
		body = append(body, "[error-like lines printed by "+who+" (the rest of their output is hidden):]")
		body = append(body, dedupeCount(hiddenErr)...)
		hiddenErr, errFromSkip = nil, false
	}
	rd.hiddenMarkers += s.markers
	// Unattributed lines that are kept come in runs; each run has repeated
	// blocks (the same usage text printed by many tests) elided and is
	// capped, error-class and build lines always kept.
	var strayRun []string
	flush := func() {
		if len(strayRun) == 0 {
			return
		}
		run := elideOutsideRace(tidyRace(strayRun))
		if len(run) > leadingStrayMax {
			run = capStrays(run)
		}
		body = append(body, run...)
		strayRun = nil
	}
	emitTest := func(t *gtest) {
		flush()
		body = append(body, t.result)
		body = append(body, rd.testOutput(t)...)
	}
	// Skipped tests beyond showSkipsMax: the first test of each distinct
	// reason is shown, the others are counted per reason.
	type skipGroup struct {
		first *gtest
		more  int
	}
	var groups []*skipGroup
	byReason := map[string]*skipGroup{}
	otherSkips := 0

	items := s.viewItems()
	sp := spanOf(items)
	for i, it := range items {
		switch it.kind {
		case itResult:
			t := it.t
			if strings.Contains(t.name, "/") {
				rd.subtests = true
			}
			switch t.status {
			case 'F':
				rd.failedN++
				emitTest(t)
			case 'P':
				rd.passed++
				if rd.showPassing && nonBlank(t.out) > 0 {
					emitTest(t)
				} else {
					rd.hiddenMarkers++
					hide(t.out, false)
				}
			case 'S':
				rd.skipped++
				if rd.showSkips {
					emitTest(t)
					break
				}
				key := skipReason(t)
				g := byReason[key]
				if g == nil && len(groups) < skipGroupsMax {
					g = &skipGroup{first: t}
					byReason[key] = g
					groups = append(groups, g)
					emitTest(t)
					break
				}
				if g != nil {
					g.more++
				} else {
					otherSkips++
				}
				rd.hiddenSkips++
				hide(t.out, true)
			}
		case itStray:
			if !rd.showPassing && hideable(items, i, failed, sp) {
				hide([]outLine{{text: it.line}}, false)
				continue
			}
			strayRun = append(strayRun, it.line)
		case itBare:
			flush()
			switch {
			case failed || s.verdict == "":
				if i == sp.lastBare {
					flushHiddenErr()
				}
				body = append(body, it.line)
			case it.line != "PASS":
				// A lone "FAIL" printed by a test of a package that passed.
				hide([]outLine{{text: it.line}}, false)
			}
		}
	}
	flush()
	for _, g := range groups {
		if g.more > 0 {
			body = append(body, fmt.Sprintf("[+%d more skipped for the same reason as %s]", g.more, g.first.name))
		}
	}
	if otherSkips > 0 {
		body = append(body, fmt.Sprintf("[+%s]", plural(otherSkips, "more skipped test", "more skipped tests")))
	}
	// Tests still running when the package ended (panic, timeout, kill).
	for _, t := range s.order {
		if t.status != 0 || (!t.ran && len(t.out) == 0) {
			continue
		}
		if failed {
			if len(t.out) > 0 {
				body = append(body, "=== RUN   "+t.name)
				if rd.hiddenMarkers > 0 {
					rd.hiddenMarkers-- // shown after all
				}
				body = append(body, rd.testOutput(t)...)
			}
		} else {
			hide(t.out, false)
		}
	}
	if len(s.crash) > 0 {
		body = append(body, foldCrash(rd.c, s.crash)...)
	}
	flushHiddenErr()
	if failed || s.verdict == "" {
		if s.jsonFail && !strings.HasPrefix(s.verdict, "FAIL") {
			body = append(body, "FAIL\t"+s.pkg+" [lx: -json reported the package failed without a FAIL line]")
		}
		if s.verdict != "" {
			body = append(body, s.verdict)
		}
		return body, "", false
	}
	if k, ok := okKey(s.verdict); ok {
		return body, k, true
	}
	return append(body, s.verdict), "", false
}

// okTimeRe splits the rest of an "ok" line into its time and the suffix.
var okTimeRe = lazyre.New(`^(?:\d+(?:\.\d+)?s|\(cached\))(.*)$`)

// okKey returns the collapse group of a passing verdict line: "" for a
// plain "ok pkg time", the suffix for "[no tests to run]" and for a
// coverage figure (only packages with the very same figure collapse), and
// "?" for "[no test files]". Other lines are not collapsed.
func okKey(verdict string) (string, bool) {
	if noFilesRe.MatchString(verdict) {
		return "?", true
	}
	m := okRe.FindStringSubmatch(verdict)
	if m == nil {
		return "", false
	}
	t := okTimeRe.FindStringSubmatch(m[2])
	if t == nil {
		return "", false
	}
	switch suffix := t[1]; {
	case suffix == "", suffix == " [no tests to run]", strings.HasPrefix(suffix, "\tcoverage: ") && !strings.Contains(suffix[1:], "\t"):
		return suffix, true
	}
	return "", false
}

// testOutput returns a test's output as go test prints it without -v:
// indented one level deeper than the test's "--- FAIL" line.
func (rd *renderer) testOutput(t *gtest) []string {
	pad := strings.Repeat("    ", t.depth())
	lines := make([]string, 0, len(t.out))
	for _, o := range t.out {
		switch {
		case o.text == "":
			lines = append(lines, "")
		case o.reindent:
			lines = append(lines, pad+o.text)
		default:
			lines = append(lines, o.text)
		}
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return foldCrashInTest(rd.c, engine.CollapseRuns(tidyRace(lines)))
}

// footer summarizes what was hidden, with exact counts. It never reads as
// a pass when a package failed: with no failing test to count, it says how
// many packages failed.
func (rd *renderer) footer() string {
	var parts []string
	if rd.verbose && rd.passed+rd.failedN+rd.skipped > 0 {
		counts := num(rd.passed) + " passed"
		if rd.failedN > 0 {
			counts += ", " + num(rd.failedN) + " failed"
		}
		if rd.skipped > 0 {
			counts += ", " + num(rd.skipped) + " skipped"
		}
		if rd.subtests {
			counts += " (incl. subtests)"
		}
		if rd.failedN == 0 && rd.failedPkgs > 0 {
			counts += ", but " + plural(rd.failedPkgs, "package", "packages") + " FAILED"
		}
		parts = append(parts, counts)
	}
	var hidden []string
	if n := rd.hiddenMarkers + rd.hiddenSkips; n > 0 {
		label := "=== RUN/--- PASS line"
		if rd.hiddenSkips > 0 {
			label = "=== RUN/--- PASS/--- SKIP line"
		}
		hidden = append(hidden, plural(n, label, label+"s"))
	}
	if rd.hiddenOut > 0 {
		hidden = append(hidden, plural(rd.hiddenOut, "line", "lines")+" of passing-test output")
	}
	if rd.hiddenSkipOut > 0 {
		hidden = append(hidden, plural(rd.hiddenSkipOut, "line", "lines")+" of skipped-test output")
	}
	if len(hidden) > 0 {
		parts = append(parts, "hidden: "+strings.Join(hidden, ", "))
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, " · ") + "]"
}

// dedupeCount keeps the first occurrence of each line, suffixed with [×N]
// when it occurred N>1 times.
func dedupeCount(lines []string) []string {
	count := map[string]int{}
	var order []string
	for _, ln := range lines {
		if count[ln] == 0 {
			order = append(order, ln)
		}
		count[ln]++
	}
	out := make([]string, len(order))
	for i, ln := range order {
		out[i] = ln
		if n := count[ln]; n > 1 {
			out[i] = fmt.Sprintf("%s [×%d]", ln, n)
		}
	}
	return out
}

// elideRepeats replaces every run of three or more lines that repeats an
// earlier run of the same lines verbatim with one counted marker. The first
// occurrence is always kept, so no line's text disappears. Matches may
// overlap their source (as in LZ77), so a block printed N times in a row
// becomes the block plus a single marker.
func elideRepeats(lines []string) []string {
	last := make(map[string]int, len(lines))
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		ln := lines[i]
		if j, seen := last[ln]; seen && strings.TrimSpace(ln) != "" {
			k := 0
			for i+k < len(lines) && lines[j+k] == lines[i+k] {
				k++
			}
			// Do not end the elided run on blank lines: they separate blocks.
			for k > 0 && strings.TrimSpace(lines[i+k-1]) == "" {
				k--
			}
			if k >= 3 {
				for m := 0; m < k; m++ {
					last[lines[i+m]] = i + m
				}
				out = append(out, "… "+plural(k, "line", "lines")+" repeated from above …")
				i += k
				continue
			}
		}
		last[ln] = i
		out = append(out, ln)
		i++
	}
	return out
}

// elideOutsideRace is elideRepeats applied to everything but data race
// reports, whose two stacks often share frames: they are kept whole.
func elideOutsideRace(lines []string) []string {
	var out []string
	start := 0
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "WARNING: DATA RACE" {
			continue
		}
		b := i
		if b > start && strings.TrimSpace(lines[b-1]) == "==================" {
			b--
		}
		e := i + 1
		for e < len(lines) && strings.TrimSpace(lines[e]) != "==================" {
			e++
		}
		e = min(e+1, len(lines))
		out = append(out, elideRepeats(lines[start:b])...)
		out = append(out, lines[b:e]...)
		start, i = e, e-1
	}
	if start == 0 {
		return elideRepeats(lines)
	}
	return append(out, elideRepeats(lines[start:])...)
}

// raceFrameRe is a frame location line of a data race report.
var raceFrameRe = lazyre.New(`^\s+\S.*\.go:\d+ \+0x[0-9a-f]+$`)

// tidyRace drops the +0x offsets of the frame locations inside data race
// reports ("WARNING: DATA RACE" up to the closing "=================="),
// as tidyFrames does for tracebacks. Error-class lines are never changed.
func tidyRace(lines []string) []string {
	var out []string
	in := false
	for i, ln := range lines {
		switch t := strings.TrimSpace(ln); {
		case t == "WARNING: DATA RACE":
			in = true
		case in && t == "==================":
			in = false
		case in && raceFrameRe.MatchString(ln) && !engine.IsError(ln):
			if out == nil {
				out = append([]string(nil), lines...)
			}
			out[i] = offsetRe.ReplaceAllString(ln, "")
		}
	}
	if out == nil {
		return lines
	}
	return out
}

// capStrays keeps the first and last lines of a long unattributed run plus
// every error-class or build line between them, with a counted marker.
func capStrays(lines []string) []string {
	const head, tail = 60, 30
	if len(lines) <= head+tail {
		return lines
	}
	out := append([]string(nil), lines[:head]...)
	gap := 0
	for _, ln := range lines[head : len(lines)-tail] {
		if engine.IsError(ln) || buildLine(ln) {
			if gap > 0 {
				out = append(out, fmt.Sprintf("… %d lines omitted …", gap))
				gap = 0
			}
			out = append(out, ln)
			continue
		}
		gap++
	}
	if gap > 0 {
		out = append(out, fmt.Sprintf("… %d lines omitted …", gap))
	}
	return append(out, lines[len(lines)-tail:]...)
}

// trimBlankRuns collapses runs of blank lines and trims leading/trailing
// blank lines.
func trimBlankRuns(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" && (len(out) == 0 || strings.TrimSpace(out[len(out)-1]) == "") {
			continue
		}
		out = append(out, ln)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

// commonPkgPrefix returns the longest common import-path prefix (whole
// segments, at least two) of pkgs, or "".
func commonPkgPrefix(pkgs []string) string {
	if len(pkgs) < 2 {
		return ""
	}
	prefix := strings.Split(pkgs[0], "/")
	for _, p := range pkgs[1:] {
		segs := strings.Split(p, "/")
		n := 0
		for n < len(prefix) && n < len(segs) && prefix[n] == segs[n] {
			n++
		}
		prefix = prefix[:n]
	}
	if len(prefix) < 2 {
		return ""
	}
	return strings.Join(prefix, "/")
}

// maxPkgList: longer package lists are reduced to their count.
const maxPkgList = 10

func pkgList(pkgs []string) string {
	if len(pkgs) > maxPkgList {
		if p := commonPkgPrefix(pkgs); p != "" {
			return " under " + p
		}
		return ""
	}
	p := commonPkgPrefix(pkgs)
	if p == "" {
		return ": " + strings.Join(pkgs, ", ")
	}
	rel := make([]string, len(pkgs))
	for i, k := range pkgs {
		rel[i] = strings.TrimPrefix(strings.TrimPrefix(k, p), "/")
		if rel[i] == "" {
			rel[i] = "."
		}
	}
	return " under " + p + ": " + strings.Join(rel, ", ")
}

// groupSummary is the one line standing for a group of passing packages.
func groupSummary(key string, pkgs []string, cached int) string {
	if key == "?" {
		return "?   \t" + num(len(pkgs)) + " packages [no test files]" + pkgList(pkgs)
	}
	s := "ok  \t" + num(len(pkgs)) + " packages"
	if key == " [no tests to run]" {
		s += key
	}
	var notes []string
	switch {
	case cached == len(pkgs):
		notes = append(notes, "all cached")
	case cached > 0:
		notes = append(notes, num(cached)+" cached")
	}
	if strings.HasPrefix(key, "\t") {
		notes = append(notes, key[1:])
	}
	if len(notes) > 0 {
		s += " (" + strings.Join(notes, "; ") + ")"
	}
	return s + pkgList(pkgs)
}
