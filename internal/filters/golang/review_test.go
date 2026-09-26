package golang

// Regression tests for the problems found in the adversarial review of the
// first version of these filters. Each test names the failure it guards
// against; the real captures behind most of them are the lxcap2-* fixtures
// (testdata/captures/lxcap2).

import (
	"fmt"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
)

// mustApply runs f and fails unless it rendered; it also checks that no
// error-class line (=== markers aside) and, for a failing run, no file:line
// location was lost.
func mustApply(t *testing.T, f engine.Filter, c *engine.Context, in string) string {
	t.Helper()
	got, ok := f.Apply(c, in)
	if !ok {
		t.Fatalf("%s bailed on:\n%s", f.Name(), in)
	}
	ref := in
	if f.Name() == "go-test-json" {
		ref = decodeEvents(in)
	}
	if m := fixture.ErrorLinesMissing(stripMarkers(ref), got); len(m) > 0 {
		t.Errorf("error lines missing: %q\n%s", m, got)
	}
	return got
}

func wantAll(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in\n%s", w, got)
		}
	}
}

func wantNone(t *testing.T, got string, bad ...string) {
	t.Helper()
	for _, w := range bad {
		if strings.Contains(got, w) {
			t.Errorf("unexpected %q in\n%s", w, got)
		}
	}
}

func lines(ls ...string) string { return strings.Join(ls, "\n") }

// Bug: everything after the last "--- FAIL" of a failing package was taken
// for passing-test output and hidden, including what TestMain prints after
// the binary's final FAIL line: a data race report (its locations were
// lost), a goroutine leak report, the coverage figure. What was kept of it
// was labelled "printed by passing tests".
func TestReviewLinesAfterFinalFAILKept(t *testing.T) {
	race := []string{
		"==================",
		"WARNING: DATA RACE",
		"Write at 0x00c000012345 by goroutine 8:",
		"  example.com/app.worker()",
		"      /home/user/src/app/worker.go:12 +0x44",
		"",
		"Previous read at 0x00c000012345 by goroutine 7:",
		"  example.com/app.reader()",
		"      /home/user/src/app/worker.go:20 +0x30",
		"==================",
		"Found 1 data race(s)",
	}
	leak := []string{
		"leakcheck: found unexpected goroutines:",
		"goroutine 8 [chan receive]:",
		"example.com/app.worker(...)",
		"\t/home/user/src/app/worker.go:12",
		"created by example.com/app.TestStart in goroutine 7",
		"\t/home/user/src/app/worker_test.go:17 +0x6c",
	}
	chatter := []string{"later test chatter 1", "later test chatter 2"}
	for name, tc := range map[string]struct {
		epilogue []string
		want     []string
	}{
		"race":     {race, []string{"worker.go:12", "worker.go:20", "Previous read at 0x00c000012345 by goroutine 7:"}},
		"leak":     {leak, []string{"worker.go:12", "worker_test.go:17", "goroutine 8 [chan receive]:"}},
		"coverage": {[]string{"coverage: 41.2% of statements"}, []string{"coverage: 41.2% of statements"}},
	} {
		t.Run(name, func(t *testing.T) {
			// Without -v.
			in := lines(append(append(append([]string{"--- FAIL: TestA (0.00s)", "    a_test.go:3: boom"}, chatter...), "FAIL"),
				append(tc.epilogue, "FAIL\texample.com/app\t0.3s", "FAIL")...)...)
			got := mustApply(t, testText{}, ctx(1, "go", "test", "./..."), in)
			wantAll(t, got, tc.want...)
			wantNone(t, got, "later test chatter", "printed by passing tests")
			if strings.Index(got, "FAIL\n") > strings.Index(got, tc.want[0]) {
				t.Errorf("TestMain's output moved before the final FAIL line:\n%s", got)
			}
			// With -v: the same, chatter attributed to a passing test.
			in = lines(append([]string{"=== RUN   TestA", "    a_test.go:3: boom", "--- FAIL: TestA (0.00s)",
				"=== RUN   TestB", chatter[0], chatter[1], "--- PASS: TestB (0.00s)", "FAIL"},
				append(tc.epilogue, "FAIL\texample.com/app\t0.3s", "FAIL")...)...)
			got = mustApply(t, testText{}, ctx(1, "go", "test", "-v", "./..."), in)
			wantAll(t, got, tc.want...)
			wantNone(t, got, "later test chatter", "printed by passing tests")
		})
	}
}

// Bug: when the test binary exited without its final PASS / FAIL line
// (log.Fatal or os.Exit in a later test, a test killed for running too
// long), the reason was hidden as "passing-test output" if an earlier test
// had failed: the agent saw TestA's failure but not that the binary died.
func TestReviewAbnormalExitKeepsLastLines(t *testing.T) {
	for name, tail := range map[string][]string{
		"log.Fatal": {"loading config from testdata/app.yaml", "2026/09/26 10:00:00 config file testdata/app.yaml missing"},
		"os.Exit":   {"some debug from TestB before exiting"},
		"killed":    {"progress output of TestHang", "*** Test killed: ran too long (11m0s)."},
		"signal":    {"progress output of TestHang", "signal: killed"},
	} {
		t.Run(name, func(t *testing.T) {
			in := lines(append(append([]string{"--- FAIL: TestA (0.00s)", "    a_test.go:3: boom"}, tail...),
				"FAIL\texample.com/app\t0.3s", "FAIL")...)
			got := mustApply(t, testText{}, ctx(1, "go", "test", "./..."), in)
			wantAll(t, got, tail...)
			wantNone(t, got, "hidden:", "printed by passing tests")
		})
	}
	// The real capture: log.Fatal in TestLoad after TestParse failed.
	fc := loadCapture(t, "go", "lxcap2-test")
	got := mustApply(t, testText{}, fc.Context(), fc.Clean())
	wantAll(t, got, "loading config from testdata/app.yaml", "open testdata/app.yaml: permission issue")
}

// Bug: go test prints a result line or the final FAIL glued to test output
// that did not end in a newline. The glued "--- FAIL" was not counted, the
// glued "--- PASS" made its test look like it was still running when the
// package ended ("=== RUN TestOK" shown), and the -json decoder glued the
// partial output to the framing line that test2json had kept separate.
func TestReviewGluedOutput(t *testing.T) {
	// Real captures (Go 1.26): plain, -v and -json.
	fc := loadCapture(t, "go", "lxcap2-test")
	got := mustApply(t, testText{}, fc.Context(), fc.Clean())
	wantAll(t, got, "progress: 3/5--- FAIL: TestNoNewline (0.00s)", "ok-no-newlineFAIL\nFAIL\texample.com/lxcap2/glued")

	fc = loadCapture(t, "go", "lxcap2-test-v")
	got = mustApply(t, testText{}, fc.Context(), fc.Clean())
	wantNone(t, got, "=== RUN   TestOK", "ok-no-newline--- PASS")
	wantAll(t, got, "--- FAIL: TestNoNewline (0.00s)", "[16 passed, 7 failed (incl. subtests)")

	fc = loadCapture(t, "go", "lxcap2-test-json")
	got = mustApply(t, testJSON{}, fc.Context(), fc.Clean())
	wantNone(t, got, "=== RUN   TestOK", "ok-no-newline--- PASS")
	wantAll(t, got, "[16 passed, 7 failed (incl. subtests)")

	// A glued FAIL result in -json must still count as a failure.
	ev := func(action, test, output string) string {
		s := fmt.Sprintf(`{"Action":%q,"Package":"example.com/g"`, action)
		if test != "" {
			s += fmt.Sprintf(`,"Test":%q`, test)
		}
		if output != "" {
			s += fmt.Sprintf(`,"Output":%q`, output)
		}
		return s + "}"
	}
	in := lines(
		ev("run", "TestGlue", ""),
		ev("output", "TestGlue", "=== RUN   TestGlue\n"),
		ev("output", "TestGlue", "half a line"),
		ev("output", "TestGlue", "--- FAIL: TestGlue (0.00s)\n"),
		ev("fail", "TestGlue", ""),
		ev("output", "", "FAIL\n"),
		ev("output", "", "FAIL\texample.com/g\t0.1s\n"),
		ev("fail", "", ""),
	)
	got = mustApply(t, testJSON{}, ctx(1, "go", "test", "-json"), in)
	wantAll(t, got, "--- FAIL: TestGlue (0.00s)\nhalf a line", "0 passed, 1 failed")
}

// Bug: -json test-level pass/fail/skip events were ignored; a test whose
// "--- FAIL" line never came (older test2json, output cut short) was shown
// as "=== RUN" (still running) and not counted as failed.
func TestReviewJSONTestActions(t *testing.T) {
	in := lines(
		`{"Action":"run","Package":"example.com/h","Test":"TestHidden"}`,
		`{"Action":"output","Package":"example.com/h","Test":"TestHidden","Output":"=== RUN   TestHidden\n"}`,
		`{"Action":"output","Package":"example.com/h","Test":"TestHidden","Output":"    h_test.go:5: the real reason\n"}`,
		`{"Action":"fail","Package":"example.com/h","Test":"TestHidden","Elapsed":0.01}`,
		`{"Action":"output","Package":"example.com/h","Output":"FAIL\n"}`,
		`{"Action":"output","Package":"example.com/h","Output":"FAIL\texample.com/h\t0.1s\n"}`,
		`{"Action":"fail","Package":"example.com/h","Elapsed":0.1}`,
	)
	got := mustApply(t, testJSON{}, ctx(1, "go", "test", "-json"), in)
	wantAll(t, got, "--- FAIL: TestHidden (0.01s)\n    h_test.go:5: the real reason", "0 passed, 1 failed")
	wantNone(t, got, "=== RUN   TestHidden")
}

// Bug: a test that printed a line starting with "panic: " or "runtime: "
// started a "crash" that swallowed the rest of the package: result lines
// were not counted, passing-test output was not hidden, and a passing
// package was treated as failed.
func TestReviewFalseCrash(t *testing.T) {
	var pass []string
	for i := range 30 {
		pass = append(pass, fmt.Sprintf("=== RUN   TestN%d", i), fmt.Sprintf("--- PASS: TestN%d (0.00s)", i))
	}
	in := lines(append(append([]string{"=== RUN   TestRecover", "panic: recovered from bad input", "--- PASS: TestRecover (0.00s)"}, pass...),
		"PASS", "ok  \texample.com/app\t0.3s")...)
	got := mustApply(t, testText{}, ctx(0, "go", "test", "-v"), in)
	wantAll(t, got, "panic: recovered from bad input", "ok  \texample.com/app\t0.3s", "[31 passed")
	wantNone(t, got, "=== RUN   TestN", "--- PASS: TestN")

	var chatter []string
	for i := range 30 {
		chatter = append(chatter, fmt.Sprintf("chatter %d", i))
	}
	in = lines(append(append([]string{"runtime: 120ms", "--- FAIL: TestA (0.00s)", "    a_test.go:3: boom"}, chatter...),
		"FAIL", "FAIL\texample.com/app\t0.3s", "FAIL")...)
	got = mustApply(t, testText{}, ctx(1, "go", "test"), in)
	wantAll(t, got, "runtime: 120ms", "--- FAIL: TestA (0.00s)", "[hidden: 30 lines of passing-test output]")
	wantNone(t, got, "chatter")

	// A real crash is still folded and kept.
	in = lines("--- FAIL: TestA (0.00s)", "panic: boom [recovered]", "", "goroutine 7 [running]:",
		"example.com/app.TestA(0xc000)", "\t/home/user/src/app/a_test.go:9 +0x1c", "FAIL\texample.com/app\t0.1s", "FAIL")
	got = mustApply(t, testText{}, ctx(1, "go", "test"), in)
	wantAll(t, got, "panic: boom [recovered]", "a_test.go:9", "example.com/app.TestA(...)")

	// A test printing a lone "FAIL" in a package that passed is not a
	// failure (go test's verdict says ok); the line is still shown.
	in = lines("=== RUN   TestA", "FAIL", "--- PASS: TestA (0.00s)", "PASS", "ok  \texample.com/app\t0.1s")
	got = mustApply(t, testText{}, ctx(0, "go", "test", "-v"), in)
	wantAll(t, got, "ok  \texample.com/app\t0.1s", "[1 passed")
}

// Bug: when every test passed but the package failed (TestMain, a leak or
// race check after m.Run), the footer's last word was "[30 passed · …]".
func TestReviewFooterNeverPassLike(t *testing.T) {
	var pass []string
	for i := range 30 {
		pass = append(pass, fmt.Sprintf("=== RUN   TestN%d", i), fmt.Sprintf("--- PASS: TestN%d (0.00s)", i))
	}
	in := lines(append(pass, "PASS", "leakcheck: 2 goroutines leaked", "FAIL\texample.com/app\t0.3s", "FAIL")...)
	got := mustApply(t, testText{}, ctx(1, "go", "test", "-v"), in)
	wantAll(t, got, "PASS\nleakcheck: 2 goroutines leaked\nFAIL\texample.com/app\t0.3s", "[30 passed, but 1 package FAILED")
}

// Bug: any go command line counted as evidence of a failure, so exit 1
// with only "ok" lines and a "go: warning" rendered as a clean run.
func TestReviewFailureEvidence(t *testing.T) {
	in := lines(`go: warning: "./..." matched only vendored packages`, "ok  \texample.com/a\t0.1s", "ok  \texample.com/b\t0.1s")
	if out, ok := (testText{}).Apply(ctx(1, "go", "test", "./..."), in); ok {
		t.Errorf("exit 1 without a failure rendered:\n%s", out)
	}
	in = lines("ok  \texample.com/a\t0.1s", "signal: killed", "FAIL\texample.com/b\t0.3s", "FAIL")
	got := mustApply(t, testText{}, ctx(1, "go", "test", "./..."), in)
	wantAll(t, got, "signal: killed")
}

// Bug: go build / go mod with a failing exit and nothing but download
// lines rendered only "[go: downloading N modules …]".
func TestReviewBuildModBailWithoutFailureLine(t *testing.T) {
	var b strings.Builder
	for i := range 30 {
		fmt.Fprintf(&b, "go: downloading example.com/mod%d v1.0.%d\n", i, i)
	}
	for _, f := range []engine.Filter{build{}, mod{}} {
		if out, ok := f.Apply(ctx(1, "go", "build"), b.String()); ok {
			t.Errorf("%s: exit 1 with only downloads rendered:\n%s", f.Name(), out)
		}
		if _, ok := f.Apply(ctx(0, "go", "build"), b.String()); !ok {
			t.Errorf("%s: exit 0 downloads not condensed", f.Name())
		}
	}
	// A go: error line that the classifier does not flag still counts.
	got := mustApply(t, mod{}, ctx(1, "go", "get", "example.com/x@v1.2.3"),
		b.String()+"go: example.com/x@v1.2.3: invalid version: unknown revision v1.2.3")
	wantAll(t, got, "go: example.com/x@v1.2.3: invalid version: unknown revision v1.2.3")
}

// Improvement: with more than 5 skipped tests the reasons were all hidden
// ("needs docker" matters: those tests did not run); now each distinct
// reason is shown once with a count.
func TestReviewSkipGroups(t *testing.T) {
	var in []string
	for i := range 30 {
		reason := "needs docker"
		if i%10 == 9 {
			reason = "set LX_NET=1 to run"
		}
		in = append(in, fmt.Sprintf("=== RUN   TestN%d", i), fmt.Sprintf("    x_test.go:%d: %s", i+1, reason), fmt.Sprintf("--- SKIP: TestN%d (0.00s)", i))
	}
	in = append(in, "PASS", "ok  \texample.com/app\t0.3s")
	got := mustApply(t, testText{}, ctx(0, "go", "test", "-v"), lines(in...))
	wantAll(t, got,
		"--- SKIP: TestN0 (0.00s)\n    x_test.go:1: needs docker",
		"--- SKIP: TestN9 (0.00s)\n    x_test.go:10: set LX_NET=1 to run",
		"[+26 more skipped for the same reason as TestN0]",
		"[+2 more skipped for the same reason as TestN9]",
		"0 passed, 30 skipped",
		"28 lines of skipped-test output")
}

// Improvement: passing packages whose verdict lines differ only in name
// and time collapse, also with "[no tests to run]" (a -run pattern that
// matched nothing: kept visible) or an identical coverage figure.
func TestReviewOKCollapse(t *testing.T) {
	var in []string
	for i := range 5 {
		in = append(in, fmt.Sprintf("ok  \tex.com/m/n%d\t0.%ds [no tests to run]", i, i+1))
	}
	for i := range 4 {
		in = append(in, fmt.Sprintf("ok  \tex.com/m/c%d\t0.%ds\tcoverage: [no statements]", i, i+1))
	}
	for i := range 4 {
		in = append(in, fmt.Sprintf("ok  \tex.com/m/d%d\t0.%ds\tcoverage: %d0.0%% of statements", i, i+1, i+1))
	}
	got := mustApply(t, testText{}, ctx(0, "go", "test", "-cover", "-run", "X", "./..."), lines(in...))
	wantAll(t, got,
		"ok  \t5 packages [no tests to run] under ex.com/m: n0, n1, n2, n3, n4",
		"ok  \t4 packages (coverage: [no statements]) under ex.com/m: c0, c1, c2, c3",
		"ok  \tex.com/m/d0\t0.1s\tcoverage: 10.0% of statements",
		"ok  \tex.com/m/d3\t0.4s\tcoverage: 40.0% of statements")
}

// Race report frames keep their locations but lose the +0x offsets.
func TestReviewRaceOffsets(t *testing.T) {
	fc := loadCapture(t, "go", "lxcap2-test-race-post")
	got := mustApply(t, testText{}, fc.Context(), fc.Clean())
	wantAll(t, got, "      racepost/r_test.go:19\n", "WARNING: DATA RACE", "Previous write at")
	wantNone(t, got, "+0x")
}

// Bug (shared-code gap, worked around here): engine.MachineReadable lets
// the go command's -json through to the filters, and with no filter the
// generic reducer turned go list -json into a table. It is now kept
// verbatim (passthrough) up to the token budget.
func TestReviewMachineOutputVerbatim(t *testing.T) {
	fc := loadCapture(t, "go", "lxcap2-list-json")
	res := engine.Process(fc.Context(), fc.Raw, engine.Options{})
	if res.Output != strings.TrimRight(fc.Raw, "\n") {
		t.Errorf("go list -json changed (%s):\n%s", res.Filter, res.Output)
	}
	big := strings.Repeat(`{"ImportPath": "example.com/some/package/with/a/long/name", "Stale": true}`+"\n", 3000)
	if _, ok := (machine{}).Apply(ctx(0, "go", "list", "-json", "./..."), big); ok {
		t.Error("machine output over the budget should go to the generic reducer")
	}
}

// Windows line endings: Process normalizes them before the filter runs, so
// the view is the same as for LF output.
func TestReviewCRLF(t *testing.T) {
	fc := fixture.Load(t, "go", "go-test-fail-v")
	crlf := strings.ReplaceAll(fc.Raw, "\n", "\r\n")
	a := engine.Process(fc.Context(), fc.Raw, engine.Options{})
	b := engine.Process(fc.Context(), crlf, engine.Options{})
	if a.Output != b.Output || b.Filter != "go-test" || b.GuardAdded != 0 {
		t.Errorf("CRLF view differs (%s, guard %d):\n%s", b.Filter, b.GuardAdded, b.Output)
	}
}

// Bug: a failing Example prints its got:/want: blocks unindented after its
// "--- FAIL" line; they were hidden as passing-test output (with and
// without -v), leaving the failure without its assertion.
func TestReviewExampleOutputKept(t *testing.T) {
	for _, name := range []string{"lxcap2-example", "lxcap2-example-v", "lxcap2-example-json"} {
		fc := loadCapture(t, "go", name)
		f := engine.Find(fc.Context())
		got := mustApply(t, f, fc.Context(), fc.Clean())
		wantAll(t, got, "--- FAIL: Example_greeting (0.00s)\ngot:\nhello\nworld\nwant:\nhello\nthere", "--- FAIL: TestFirst")
		if name != "lxcap2-example" {
			// Without -v, TestAfter's chatter precedes a --- FAIL line, so
			// it may belong to a failing test and is kept.
			wantNone(t, got, "after chatter")
		}
	}
}

// Bug: with -count=N a test name reports N times; a test that failed on
// one run and passed on a later one was shown as passed ("4 passed"), its
// failure message hidden, and only the guard re-added its --- FAIL line.
func TestReviewCountKeepsEachRun(t *testing.T) {
	for _, name := range []string{"lxcap2-flaky-count-v", "lxcap2-flaky-count-json"} {
		fc := loadCapture(t, "go", name)
		f := engine.Find(fc.Context())
		got := mustApply(t, f, fc.Context(), fc.Clean())
		wantAll(t, got, "--- FAIL: TestFlaky (0.00s)\n    f_test.go:14: first run: got 1, want 2", "[3 passed, 1 failed")
		res := engine.Process(fc.Context(), fc.Raw, engine.Options{})
		if res.GuardAdded != 0 {
			t.Errorf("%s: guard re-added lines:\n%s", name, res.Output)
		}
	}
	// Without -v each run prints its own result block.
	in := lines("--- FAIL: TestFlaky (0.00s)", "    f_test.go:14: run 1", "--- FAIL: TestFlaky (0.00s)", "    f_test.go:14: run 2", "FAIL", "FAIL\tex.com/f\t0.1s", "FAIL")
	got := mustApply(t, testText{}, ctx(1, "go", "test", "-count=2"), in)
	wantAll(t, got, "f_test.go:14: run 1", "f_test.go:14: run 2")
}

// Output shown for a passing package keeps its own "ok" line instead of
// being folded into a collapsed line that does not say whose it is.
func TestReviewCollapseKeepsAttribution(t *testing.T) {
	in := lines(
		"=== RUN   TestA", "    a_test.go:3: value=42", "--- PASS: TestA (0.00s)", "PASS", "ok  \tex.com/m/a\t0.1s",
		"ok  \tex.com/m/b\t0.1s", "ok  \tex.com/m/c\t0.1s", "ok  \tex.com/m/d\t0.1s", "ok  \tex.com/m/e\t0.1s")
	got := mustApply(t, testText{}, ctx(0, "go", "test", "-v", "./..."), in)
	wantAll(t, got, "    a_test.go:3: value=42\nok  \tex.com/m/a\t0.1s", "ok  \t4 packages under ex.com/m: b, c, d, e")
}

// Bug: -json reports a subtest before its parent; with -count=2 both runs'
// subtests were nested under the second run of the parent.
func TestReviewCountSubtestNesting(t *testing.T) {
	fc := loadCapture(t, "go", "lxcap2-count-sub-json")
	got := mustApply(t, testJSON{}, fc.Context(), fc.Clean())
	block := "--- FAIL: TestÜnicode (0.00s)\n    --- FAIL: TestÜnicode/naïve_case (0.00s)\n        ü_test.go:7: got \"café\", want \"cafe\"\n"
	wantAll(t, got, block+block)
}

// Data race reports are kept whole: their stacks are not elided as
// "lines repeated from above" (only the +0x offsets go).
func TestReviewRaceReportNotElided(t *testing.T) {
	fc := loadCapture(t, "go", "lxcap-test-race")
	got := mustApply(t, testText{}, fc.Context(), fc.Clean())
	wantNone(t, got, "repeated from above", "+0x")
	if n := strings.Count(got, "race/race_test.go:10\n"); n != 2 {
		t.Errorf("both accesses' frames should be shown, got %d:\n%s", n, got)
	}
	// Repeats outside the report are still elided.
	block := []string{"Error: bad flag", "Usage:", "  cmd [flags]", ""}
	var in []string
	for range 3 {
		in = append(in, block...)
	}
	in = append(in, "==================", "WARNING: DATA RACE", "  a.f()", "      /a.go:1 +0x1", "", "  a.f()", "      /a.go:1 +0x2", "==================")
	out := elideOutsideRace(tidyRace(in))
	if s := strings.Join(out, "\n"); !strings.Contains(s, "repeated from above") || strings.Count(s, "/a.go:1") != 2 {
		t.Errorf("got\n%s", s)
	}
}

// A line that merely ends in "FAIL" right before the verdict (a glued final
// FAIL looks the same) is not taken for the binary's final line: that would
// hide the output of a test that then called os.Exit.
func TestReviewLineEndingInFAILIsNotFinal(t *testing.T) {
	in := lines("--- FAIL: TestA (0.00s)", "    a_test.go:3: boom", "loading config for TestB", "Status: FAIL", "FAIL\tex.com/a\t0.1s", "FAIL")
	got := mustApply(t, testText{}, ctx(1, "go", "test"), in)
	wantAll(t, got, "loading config for TestB\nStatus: FAIL\nFAIL\tex.com/a\t0.1s")
}

// A real traceback followed by another package's "ok" line (its own FAIL
// line missing, e.g. interleaved output) stays a crash: its frames are not
// re-read as passing-test output and hidden.
func TestReviewRealCrashBeforeOKStays(t *testing.T) {
	in := lines("panic: boom", "", "goroutine 7 [running]:", "example.com/app.TestA(0xc000)",
		"\t/home/user/src/app/a_test.go:9 +0x1c", "ok  \texample.com/other\t0.1s")
	got := mustApply(t, testText{}, ctx(1, "go", "test", "./..."), in)
	wantAll(t, got, "panic: boom", "a_test.go:9", "goroutine 7 [running]:")
	wantNone(t, got, "hidden:")
}
