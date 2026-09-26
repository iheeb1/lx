package jstest

// Regression tests for the issues found in the adversarial review: each
// test names the failure it guards against.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

var (
	markerSuitesRe = regexp.MustCompile(`^\[(\d+) passing (?:suite|file)s?\b`)
	markerTestsRe  = regexp.MustCompile(`^\[(?:\d+ passing (?:suite|file)s?, )?(\d+) passing tests? hidden`)
	sumJestSuites  = regexp.MustCompile(`^Test Suites: .*?(\d+) passed`)
	sumJestTests   = regexp.MustCompile(`^Tests: .*?(\d+) passed`)
	sumVitestFiles = regexp.MustCompile(`^ *Test Files .*?(\d+) passed`)
	sumVitestTests = regexp.MustCompile(`^ *Tests .*?(\d+) passed`)
	sumMocha       = regexp.MustCompile(`^ {2}(\d+) passing \(`)
)

func sumMatches(lines []string, res ...*regexp.Regexp) int {
	n := 0
	for _, ln := range lines {
		for _, re := range res {
			if m := re.FindStringSubmatch(ln); m != nil {
				k, _ := strconv.Atoi(m[1])
				n += k
			}
		}
	}
	return n
}

// TestMarkerCountsNeverExceedSummary: a "[N passing tests hidden]" marker
// must not claim more than the runner itself reports as passed (the tree
// reporter used to count the tests of passing files twice, and passing
// describe groups as tests: "8 passing tests hidden" for "6 passed").
func TestMarkerCountsNeverExceedSummary(t *testing.T) {
	for _, cc := range corpus {
		fc := load(t, cc)
		c := fc.Context()
		f := engine.Find(c)
		out, ok := f.Apply(c, fc.Clean())
		if !ok {
			continue
		}
		in := strings.Split(fc.Clean(), "\n")
		view := strings.Split(out, "\n")
		gotSuites := sumMatches(view, markerSuitesRe)
		gotTests := sumMatches(view, markerTestsRe)
		wantSuites := sumMatches(in, sumJestSuites, sumVitestFiles)
		wantTests := sumMatches(in, sumJestTests, sumVitestTests, sumMocha)
		if gotSuites > wantSuites || gotTests > wantTests {
			t.Errorf("%s: markers claim %d suites/files and %d tests hidden; the runner reports %d and %d passed",
				cc.name, gotSuites, gotTests, wantSuites, wantTests)
		}
	}
	fc, err := fixture.Read("testdata", "captures", "vitest-tree")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := vitestFilter{}.Apply(fc.Context(), fc.Clean())
	if !strings.Contains(out, "[1 passing file, 6 passing tests hidden;") {
		t.Errorf("vitest-tree marker:\n%s", out)
	}
}

// TestNoFailureIgnoresPassingConsole: a non-zero exit with no failing test
// must be flagged even when a passing test logged an error line (it used to
// suppress the "[lx: … exited N but reported no failing test …]" note, so
// jest-passlog-threshold read "1 passed" + an unrelated console.error).
func TestNoFailureIgnoresPassingConsole(t *testing.T) {
	fc, err := fixture.Read("testdata", "captures", "jest-passlog-threshold")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := jestFilter{}.Apply(fc.Context(), fc.Clean())
	if !strings.Contains(out, "[lx: jest exited 1 but reported no failing test") {
		t.Errorf("jest: exit 1 not flagged:\n%s", out)
	}

	vit := `
 RUN  v4.1.11 /home/user/src/proj

stderr | test/a.test.ts > works
Error: mock server refused the connection

 ✓ test/a.test.ts (1 test) 3ms

 Test Files  1 passed (1)
      Tests  1 passed (1)
   Start at  10:00:00
   Duration  100ms`
	mocha := `

  api
Error: connection refused
    at connect (lib/db.js:3:9)
    ✔ retries


  1 passing (4ms)
`
	for _, tc := range []struct {
		f  engine.Filter
		c  *engine.Context
		in string
	}{
		{vitestFilter{}, ctx(1, "vitest", "run"), vit},
		{mochaFilter{}, ctx(1, "mocha"), mocha},
	} {
		out, ok := tc.f.Apply(tc.c, tc.in)
		if !ok {
			t.Fatalf("%s bailed", tc.f.Name())
		}
		if !strings.Contains(out, fmt.Sprintf("[lx: %s exited 1 but reported no failing test", tc.f.Name())) {
			t.Errorf("%s: exit 1 not flagged:\n%s", tc.f.Name(), out)
		}
		if !strings.Contains(out, "refused") {
			t.Errorf("%s: the logged error was dropped:\n%s", tc.f.Name(), out)
		}
		tc.c.Exit = 0
		if out, _ := tc.f.Apply(tc.c, tc.in); strings.Contains(out, "[lx: ") {
			t.Errorf("%s: exit 0 flagged:\n%s", tc.f.Name(), out)
		}
	}
}

// synthJest renders n failing tests in suites of ten; diff failures on even
// numbers, Expected/Received on odd ones.
func synthJest(n int) string {
	var b strings.Builder
	for k := 0; k < n; k++ {
		if k%10 == 0 {
			fmt.Fprintf(&b, "FAIL test/suite%d.test.js\n", k/10)
		}
		fmt.Fprintf(&b, "  ● suite%d › computes value %d\n\n", k/10, k)
		if k%2 == 0 {
			fmt.Fprintf(&b, "    expect(received).toEqual(expected) // deep equality\n\n    - Expected  - 1\n    + Received  + 1\n\n      Object {\n    -   \"value\": %d,\n    +   \"value\": %d,\n      }\n\n", k, k+1)
		} else {
			fmt.Fprintf(&b, "    expect(received).toBe(expected) // Object.is equality\n\n    Expected: %d\n    Received: %d\n\n", k, k+1)
		}
		fmt.Fprintf(&b, "      10 |   const v = compute(%d);\n    > 11 |   expect(v).toEqual(%d);\n         |             ^\n      12 | });\n\n      at Object.toEqual (test/suite%d.test.js:%d:13)\n\n", k, k, k/10, 11+k%10)
	}
	fmt.Fprintf(&b, "Test Suites: %d failed, %d total\nTests:       %d failed, %d total\nSnapshots:   0 total\nTime:        3.2 s\nRan all test suites.\n", (n+9)/10, (n+9)/10, n, n)
	return b.String()
}

func synthVitest(n int) string {
	var b strings.Builder
	b.WriteString(" RUN  v4.1.11 /home/user/src/proj\n\n")
	for k := 0; k < n; k += 10 {
		fmt.Fprintf(&b, " ❯ test/f%d.test.ts (10 tests | 10 failed) 5ms\n", k/10)
		for j := k; j < k+10; j++ {
			fmt.Fprintf(&b, "     × case %d 1ms\n", j)
		}
	}
	fmt.Fprintf(&b, "\n⎯⎯⎯⎯⎯⎯⎯ Failed Tests %d ⎯⎯⎯⎯⎯⎯⎯\n\n", n)
	for k := 0; k < n; k++ {
		fmt.Fprintf(&b, " FAIL  test/f%d.test.ts > suite > case %d\nAssertionError: expected %d to be %d // Object.is equality\n\n- Expected\n+ Received\n\n- %d\n+ %d\n\n ❯ test/f%d.test.ts:%d:13\n      %d|   expect(v).toBe(%d);\n       |             ^\n\n⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯[%d/%d]⎯\n\n",
			k/10, k, k, k+1, k+1, k, k/10, 3+k%10, 3+k%10, k+1, k+1, n)
	}
	fmt.Fprintf(&b, " Test Files  %d failed (%d)\n      Tests  %d failed (%d)\n   Start at  10:00:00\n   Duration  2s\n", n/10, n/10, n, n)
	return b.String()
}

func synthMocha(n int) string {
	var b strings.Builder
	b.WriteString("\n\n  big\n")
	for k := 0; k < n; k++ {
		fmt.Fprintf(&b, "    %d) case %d\n", k+1, k)
	}
	fmt.Fprintf(&b, "\n\n  0 passing (9s)\n  %d failing\n\n", n)
	for k := 0; k < n; k++ {
		fmt.Fprintf(&b, "  %d) big\n       nested describe\n         case %d:\n\n      AssertionError [ERR_ASSERTION]: %d == %d\n      + expected - actual\n\n      -%d\n      +%d\n\n      at Context.<anonymous> (test/big.js:%d:12)\n      at process.processImmediate (node:internal/timers:504:21)\n\n", k+1, k, k, k+1, k, k+1, k+3)
	}
	return b.String()
}

// TestBriefLevelKeepsValues: past fullFailures, a long run's failures are
// brief, but still show their expected/received values, including diffs
// (brief mode used to keep only the header and frame of a toEqual diff
// failure), and mocha failures keep their whole title path.
func TestBriefLevelKeepsValues(t *testing.T) {
	r, ok := renderJest(ctx(1, "jest"), synthJest(80)) // over briefAbove in full, under it at level 1
	if !ok || r.readded != 0 {
		t.Fatalf("ok=%v readded=%d", ok, r.readded)
	}
	if strings.Count(r.out, briefNote) != 1 || strings.Contains(r.out, namesNote) {
		t.Fatalf("expected level 1:\n%s", r.out)
	}
	for k := 0; k < 80; k++ {
		want := []string{fmt.Sprintf("● suite%d › computes value %d", k/10, k), fmt.Sprintf("test/suite%d.test.js:%d:13", k/10, 11+k%10)}
		if k%2 == 0 {
			want = append(want, fmt.Sprintf("-   \"value\": %d,", k), fmt.Sprintf("+   \"value\": %d,", k+1))
		} else {
			want = append(want, fmt.Sprintf("Expected: %d\n    Received: %d", k, k+1))
		}
		for _, w := range want {
			if !strings.Contains(r.out, w) {
				t.Errorf("failure %d: missing %q", k, w)
			}
		}
	}

	m, ok := renderMocha(ctx(90, "mocha"), synthMocha(90))
	if !ok {
		t.Fatal("mocha bailed")
	}
	if !strings.Contains(m.out, briefNote) {
		t.Fatalf("mocha not brief (%d tokens)", tokens.Count(m.out))
	}
	for k := 0; k < 90; k++ {
		title := fmt.Sprintf("  %d) big\n       nested describe\n         case %d:\n      AssertionError [ERR_ASSERTION]: %d == %d\n", k+1, k, k, k+1)
		if !strings.Contains(m.out, title) {
			t.Errorf("mocha failure %d: title path or message missing", k+1)
		}
		if !strings.Contains(m.out, fmt.Sprintf("      -%d\n      +%d\n", k, k+1)) {
			t.Errorf("mocha failure %d: diff values missing", k+1)
		}
	}
}

// TestLevelsDegradeWithExactCounts: hundreds of failures used to be left to
// the engine budget, which kept the FAIL lines and replaced everything else
// with "… N lines omitted …". Now every failure is either listed (full,
// brief or by name) or counted in one "[+N more failing tests not shown …]"
// marker, the first briefFailures keep their messages, and the view fits
// the budget.
func TestLevelsDegradeWithExactCounts(t *testing.T) {
	const n = 400
	omittedRe := regexp.MustCompile(`\[\+(\d+) more failing tests? not shown`)
	for _, tc := range []struct {
		f       engine.Filter
		c       *engine.Context
		in      string
		titleRe *regexp.Regexp
	}{
		{jestFilter{}, ctx(1, "jest"), synthJest(n), regexp.MustCompile(`(?m)^  ● suite\d+ › computes value (\d+)$`)},
		{vitestFilter{}, ctx(1, "vitest", "run"), synthVitest(n), regexp.MustCompile(`(?m)^ FAIL  test/f\d+\.test\.ts > suite > case (\d+)$`)},
		{mochaFilter{}, ctx(n, "mocha"), synthMocha(n), regexp.MustCompile(`(?m)^  \d+\) big\n       nested describe\n         case (\d+):$`)},
	} {
		start := time.Now()
		r, ok := render(tc.f, tc.c, tc.in)
		el := time.Since(start)
		if !ok {
			t.Fatalf("%s bailed", tc.f.Name())
		}
		listed := map[int]bool{}
		for _, m := range tc.titleRe.FindAllStringSubmatch(r.out, -1) {
			k, _ := strconv.Atoi(m[1])
			listed[k] = true
		}
		omitted := 0
		for _, m := range omittedRe.FindAllStringSubmatch(r.out, -1) {
			k, _ := strconv.Atoi(m[1])
			omitted += k
		}
		if len(listed)+omitted != n {
			t.Errorf("%s: %d failures listed + %d counted as not shown ≠ %d", tc.f.Name(), len(listed), omitted, n)
		}
		for k := 0; k < briefFailures; k++ {
			if !listed[k] || !strings.Contains(r.out, fmt.Sprintf("%d", k+1)) {
				t.Errorf("%s: failure %d not listed", tc.f.Name(), k)
			}
		}
		for _, note := range []string{briefNote, namesNote} {
			if strings.Count(r.out, note) != 1 {
				t.Errorf("%s: %q appears %d times", tc.f.Name(), note, strings.Count(r.out, note))
			}
		}
		res := engine.Process(tc.c, tc.in, engine.Options{})
		if res.Filter != tc.f.Name() || res.OutTokens > engine.DefaultBudget {
			t.Errorf("%s: pipeline %s, %d tokens", tc.f.Name(), res.Filter, res.OutTokens)
		}
		// Omitted failures' distinct error messages are not lost: the
		// safety net lists them (up to its cap, with a count).
		if tc.f.Name() != "jest" && (r.readded == 0 || !strings.Contains(r.out, "more error lines")) {
			t.Errorf("%s: messages of omitted failures not surfaced", tc.f.Name())
		}
		t.Logf("%s: %d failures → %d listed, %d counted; view %d tokens, pipeline %d; %v",
			tc.f.Name(), n, len(listed), omitted, tokens.Count(r.out), res.OutTokens, el.Round(time.Millisecond))
	}
}

// TestJestRunsAreIndependent: npm workspaces run jest once per package. A
// failure identical to one in an earlier run (same file name, same block)
// used to be dropped as a repeat of the "Summary of all failing tests"
// section, and later runs' passing suites were not counted.
func TestJestRunsAreIndependent(t *testing.T) {
	run := func(pkg string) string {
		return `
> @mono/` + pkg + `@1.0.0 test
> jest

PASS test/ok.test.js
FAIL test/index.test.js
  ● template › renders

    expect(received).toBe(expected) // Object.is equality

    Expected: "a"
    Received: "b"

      at Object.toBe (test/index.test.js:3:15)

Test Suites: 1 failed, 1 passed, 2 total
Tests:       1 failed, 1 passed, 2 total
Snapshots:   0 total
Time:        0.2 s
Ran all test suites.
npm error Lifecycle script ` + "`test`" + ` failed with error:
npm error workspace @mono/` + pkg + `@1.0.0`
	}
	in := run("a") + "\n" + run("b")
	r, ok := renderScript(ctx(1, "npm", "test", "--workspaces"), in)
	if !ok || r.readded != 0 {
		t.Fatalf("ok=%v readded=%d\n%s", ok, r.readded, r.out)
	}
	if n := strings.Count(r.out, "● template › renders"); n != 2 {
		t.Errorf("failure shown %d times, want once per workspace:\n%s", n, r.out)
	}
	if n := strings.Count(r.out, "[1 passing suite hidden]"); n != 2 {
		t.Errorf("passing-suite marker shown %d times, want once per run:\n%s", n, r.out)
	}
	if !strings.Contains(r.out, "npm error workspace @mono/a@1.0.0\n\n> @mono/b@1.0.0 test") {
		t.Errorf("runs not separated:\n%s", r.out)
	}

	fc, err := fixture.Read("testdata", "captures", "npm-test-workspaces")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := npmTestFilter{}.Apply(fc.Context(), fc.Clean())
	if !strings.Contains(out, "> @mono/b@1.0.0 test\n> jest\n\n[1 passing suite, 1 passing test hidden]") {
		t.Errorf("second workspace:\n%s", out)
	}
}

// TestMochaTitlesNeedATestBelow: an indented line followed by a deeper one
// used to be dropped as a describe() title, which hid indented console
// output (including "  Error: …" lines followed by their stack).
func TestMochaTitlesNeedATestBelow(t *testing.T) {
	in := `

  reporting
  Report for job 42
    Error: connection refused while fetching /api
      at fetch (lib/api.js:9:3)
    ✔ prints a report
  error handling
    when the db fails
      ✔ returns 503


  2 passing (4ms)
`
	r, ok := renderMocha(ctx(0, "mocha"), in)
	if !ok || r.readded != 0 {
		t.Fatalf("ok=%v readded=%d", ok, r.readded)
	}
	for _, want := range []string{"Report for job 42", "Error: connection refused while fetching /api", "at fetch (lib/api.js:9:3)"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("console output %q dropped:\n%s", want, r.out)
		}
	}
	for _, title := range []string{"error handling", "when the db fails"} {
		if strings.Contains(r.out, title) {
			t.Errorf("describe title %q kept:\n%s", title, r.out)
		}
	}
	fc, err := fixture.Read("testdata", "captures", "mocha-indented-console")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := mochaFilter{}.Apply(fc.Context(), fc.Clean())
	if !strings.Contains(out, "  Report for job 42\n    Error: connection refused while fetching /api\n    retrying") {
		t.Errorf("capture:\n%s", out)
	}
}

// TestRelativizeFileURLs: engine.Relativize turned "file:///cwd/a.mjs:3:21"
// into the broken "file://a.mjs:3:21". Frames now read "a.mjs:3:21"; other
// lines holding a file:// URL (an error's url property) are left as they are.
func TestRelativizeFileURLs(t *testing.T) {
	c := ctx(1, "node", "esm.mjs")
	in := `node:internal/modules/esm/resolve:271
    throw new ERR_MODULE_NOT_FOUND(
          ^

Error [ERR_MODULE_NOT_FOUND]: Cannot find module '/home/user/src/proj/nope.mjs' imported from /home/user/src/proj/esm.mjs
    at load (file:///home/user/src/proj/esm.mjs:3:21)
    at file:///home/user/src/proj/esm.mjs:6:1 {
  code: 'ERR_MODULE_NOT_FOUND',
  url: 'file:///home/user/src/proj/nope.mjs'
}

Node.js v24.18.0`
	out, ok := nodeFilter{}.Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{"at load (esm.mjs:3:21)", "at esm.mjs:6:1 {", "url: 'file:///home/user/src/proj/nope.mjs'",
		"Cannot find module '/home/user/src/proj/nope.mjs' imported from /home/user/src/proj/esm.mjs"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "file://esm") || strings.Contains(out, "file://nope") {
		t.Errorf("broken file URL:\n%s", out)
	}
	// The same helper serves the runner views (mocha with ESM specs).
	if got := relativize(ctx(1, "mocha"), "      at Context.<anonymous> (file:///home/user/src/proj/test/a.mjs:4:9)"); got != "      at Context.<anonymous> (test/a.mjs:4:9)" {
		t.Errorf("relativize: %q", got)
	}
}

// TestNpmWorkspaceFlagIsNotWatch: npm's own -w is --workspace; only a -w
// passed to the script (after "--") means watch. `npm test -w web` used to
// run unfiltered as a watcher.
func TestNpmWorkspaceFlagIsNotWatch(t *testing.T) {
	for _, tc := range []struct {
		argv   string
		stream bool
	}{
		{"npm test -w web", false},
		{"npm test --workspace=web", false},
		{"npm -w web test", false},
		{"npm test -- -w", true},
		{"npm test -- --watch", true},
		{"npm test --watch", true},
		{"yarn test -w", true},
		{"pnpm test --watch", true},
	} {
		c := ctx(0, strings.Fields(tc.argv)...)
		if !(npmTestFilter{}).Match(c) {
			t.Errorf("%s: not matched", tc.argv)
			continue
		}
		if got := (npmTestFilter{}).Stream(c); got != tc.stream {
			t.Errorf("%s: Stream = %v, want %v", tc.argv, got, tc.stream)
		}
	}
}

// TestMatchCrossEnv: `cross-env VAR=x jest`, `npx cross-env …` and
// `npx react-scripts test` are the runner behind a wrapper, like `env`.
func TestMatchCrossEnv(t *testing.T) {
	for argv, want := range map[string]string{
		"cross-env NODE_ENV=test jest --ci":                    "jest",
		"npx cross-env CI=true vitest run":                     "vitest",
		"./node_modules/.bin/cross-env TZ=UTC mocha test/":     "mocha",
		"npx cross-env NODE_OPTIONS=--experimental-vm-modules": "",
		"cross-env FOO=1 node server.js":                       "node-crash",
		"npx react-scripts test --watchAll=false":              "jest",
		"npx craco test":                      "jest",
		"yarn react-scripts test":             "jest",
		"pnpm exec cross-env CI=1 vitest run": "vitest",
		"npx npm test":                        "",
		"yarn build":                          "",
		"pnpm dlx mocha@10 test/":             "mocha",
	} {
		f := engine.Find(ctx(0, strings.Fields(argv)...))
		got := ""
		if f != nil {
			got = f.Name()
		}
		if got != want {
			t.Errorf("%s: matched %q, want %q", argv, got, want)
		}
	}
}

// TestWindowsLibraryFrames: node_modules frames with backslashes are
// library frames (folded), like their slash forms.
func TestWindowsLibraryFrames(t *testing.T) {
	for ln, root := range map[string]string{
		`      at Object.<anonymous> (C:\proj\node_modules\jest-circus\build\utils.js:298:28)`: "jest-circus",
		`      at run (C:\proj\node_modules\@jest\core\build\cli.js:1:2)`:                      "@jest/core",
		`      at x (C:\proj\node_modules\@jest\core\build\cli.js:1:2)`:                        "@jest/core",
	} {
		got, lib := libRoot(ln)
		if !lib || strings.ReplaceAll(got, `\`, "/") != root {
			t.Errorf("%s: root %q lib=%v", ln, got, lib)
		}
	}
	in := "FAIL test\\a.test.js\n  ● a › b\n\n    TypeError: x is not a function\n\n      at Object.<anonymous> (test\\a.test.js:3:9)\n" +
		"      at Promise.then.completed (C:\\proj\\node_modules\\jest-circus\\build\\utils.js:298:28)\n" +
		"      at new Promise (<anonymous>)\n      at callAsyncCircusFn (C:\\proj\\node_modules\\jest-circus\\build\\utils.js:231:10)\n\n" +
		"Test Suites: 1 failed, 1 total\nTests:       1 failed, 1 total\nSnapshots:   0 total\nTime:        1 s\n"
	out, ok := jestFilter{}.Apply(ctx(1, "jest"), in)
	if !ok || !strings.Contains(out, "… 3 library frames (jest-circus)") || !strings.Contains(out, `test\a.test.js:3:9`) {
		t.Errorf("ok=%v\n%s", ok, out)
	}
}

// TestVitestFramePathWithSpaces: the code frame is cut at the line the
// "❯ file:line:col" names even when the path has spaces (the frame used not
// to be recognized, and a stale line number from an earlier frame could be
// used instead).
func TestVitestFramePathWithSpaces(t *testing.T) {
	in := `
 RUN  v4.1.11 /home/user/src/proj

⎯⎯⎯⎯⎯⎯⎯ Failed Tests 1 ⎯⎯⎯⎯⎯⎯⎯

 FAIL  test/my tests/a b.test.ts > adds
AssertionError: expected 3 to be 2 // Object.is equality
 ❯ helper test/my tests/helper.ts:2:9
 ❯ test/my tests/a b.test.ts:4:19
      2| describe("math", () => {
      3|   it("adds", () => {
      4|     expect(1 + 2).toBe(2);
       |                   ^
      5|   });

⎯⎯⎯[1/1]⎯

 Test Files  1 failed (1)
      Tests  1 failed (1)
`
	out, ok := vitestFilter{}.Apply(ctx(1, "vitest", "run"), in)
	if !ok {
		t.Fatal("bailed")
	}
	if !strings.Contains(out, "      4|     expect(1 + 2).toBe(2);\n       |                   ^") || strings.Contains(out, `describe("math"`) {
		t.Errorf("code frame not cut at line 4:\n%s", out)
	}
}

// TestVitestNotRunTests: --bail lists tests that did not run with "·";
// they are titles, not results, and not passing tests.
func TestVitestNotRunTests(t *testing.T) {
	fc, err := fixture.Read("testdata", "captures", "vitest-bail")
	if err != nil {
		t.Fatal(err)
	}
	r, _ := renderVitest(fc.Context(), fc.Clean())
	if strings.Contains(r.out, "· handles error case") || r.readded != 0 {
		t.Errorf("not-run tests kept:\n%s", r.out)
	}
	if !strings.Contains(r.out, "[1 passing file, 2 passing tests hidden;") {
		t.Errorf("marker:\n%s", r.out)
	}
}

// TestMochaCrashAfterReport: mocha itself crashing after its report (done()
// called twice) keeps the crash, with its library frames folded.
func TestMochaCrashAfterReport(t *testing.T) {
	fc, err := fixture.Read("testdata", "captures", "mocha-uncaught")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := mochaFilter{}.Apply(fc.Context(), fc.Clean())
	for _, want := range []string{"Uncaught Error: uncaught boom after test", "\n\nnode_modules/mocha/lib/runner.js:1120\n", "Error: done() called multiple times", "library frames (mocha, node:internal)", "Node.js v24.18.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

// TestHugeRunsAreFast: 50k-line outputs render well under the 200ms
// target (asserted loosely here; BenchmarkHuge* measure it).
func TestHugeRunsAreFast(t *testing.T) {
	for _, tc := range []struct {
		f  engine.Filter
		c  *engine.Context
		in string
	}{
		{jestFilter{}, ctx(1, "jest"), synthJest(3500)}, // ~50k lines, 3500 failures
		{vitestFilter{}, ctx(1, "vitest", "run"), synthVitest(3000)},
		{mochaFilter{}, ctx(3000, "mocha"), synthMocha(3500)},
	} {
		lines := strings.Count(tc.in, "\n")
		start := time.Now()
		out, ok := tc.f.Apply(tc.c, tc.in)
		el := time.Since(start)
		if !ok {
			t.Fatalf("%s bailed", tc.f.Name())
		}
		if el > 2*time.Second && !raceEnabled {
			t.Errorf("%s: %d lines took %v", tc.f.Name(), lines, el)
		}
		t.Logf("%s: %d lines in %v → %d tokens", tc.f.Name(), lines, el.Round(time.Millisecond), tokens.Count(out))
	}
}

// TestNoFailureIgnoresUnattributedConsole: a --verbose/single-file console
// entry without a frame cannot be tied to a suite; it still is console
// output, never how jest reports a failure.
func TestNoFailureIgnoresUnattributedConsole(t *testing.T) {
	in := `  console.error
    Error: mock server refused the connection

PASS test/a.test.js
  ✓ works (2 ms)

Jest: "global" coverage threshold for lines (95%) not met: 40%
Test Suites: 1 passed, 1 total
Tests:       1 passed, 1 total
Snapshots:   0 total
Time:        0.3 s`
	out, ok := jestFilter{}.Apply(ctx(1, "jest", "--coverage"), in)
	if !ok || !strings.Contains(out, "[lx: jest exited 1 but reported no failing test") || !strings.Contains(out, "Error: mock server refused the connection") {
		t.Errorf("ok=%v\n%s", ok, out)
	}
}

// TestCRLFOutput: Windows line endings give the same view (the pipeline
// normalizes them before the filter runs).
func TestCRLFOutput(t *testing.T) {
	for _, name := range []string{"jest-mixed", "vitest-mixed", "mocha-mixed"} {
		fc, err := fixture.Read("testdata", "captures", name)
		if err != nil {
			t.Fatal(err)
		}
		c := fc.Context()
		lf := engine.Process(c, fc.Raw, engine.Options{})
		crlf := engine.Process(c, strings.ReplaceAll(fc.Raw, "\n", "\r\n"), engine.Options{})
		if crlf.Filter != lf.Filter || crlf.Output != lf.Output {
			t.Errorf("%s: CRLF view differs (filter %s vs %s)", name, crlf.Filter, lf.Filter)
		}
	}
}

// TestCrashAfterPassingSummary: output after the runner's summary (a
// crash of the process, a teardown error) is kept, and the verdict does not
// read as a pass.
func TestCrashAfterPassingSummary(t *testing.T) {
	jest := `PASS test/a.test.js
Test Suites: 1 passed, 1 total
Tests:       1 passed, 1 total
Snapshots:   0 total
Time:        0.3 s
Ran all test suites.

<--- Last few GCs --->
FATAL ERROR: Reached heap limit Allocation failed - JavaScript heap out of memory
 1: 0x100a8c0 node::OOMErrorHandler(char const*, v8::OOMDetails const&) [/usr/local/bin/node]`
	out, ok := jestFilter{}.Apply(ctx(134, "jest"), jest)
	if !ok || !strings.Contains(out, "FATAL ERROR: Reached heap limit Allocation failed - JavaScript heap out of memory") {
		t.Errorf("jest: ok=%v\n%s", ok, out)
	}
	vit := `
 RUN  v4.1.11 /home/user/src/proj

 ✓ test/a.test.ts (1 test) 3ms

 Test Files  1 passed (1)
      Tests  1 passed (1)
   Start at  10:00:00
   Duration  100ms

Error: globalTeardown failed: could not stop the database container
    at teardown (test/global-setup.ts:12:9)`
	out, ok = vitestFilter{}.Apply(ctx(1, "vitest", "run"), vit)
	if !ok || !strings.Contains(out, "Error: globalTeardown failed: could not stop the database container") || !strings.Contains(out, "test/global-setup.ts:12:9") {
		t.Errorf("vitest: ok=%v\n%s", ok, out)
	}
}

// TestMochaDashLinesArePendingOnlyWhenCounted: "- title" lines were always
// dropped as pending tests, so a logged "  - Error: …" list item vanished
// (marked benign, never re-added).
func TestMochaDashLinesArePendingOnlyWhenCounted(t *testing.T) {
	in := `

  cleanup
    ✔ removes temp files
  - Error: disk full while writing /tmp/x
  - retried 3 times


  1 passing (4ms)
`
	r, ok := renderMocha(ctx(0, "mocha"), in)
	if !ok || !strings.Contains(r.out, "- Error: disk full while writing /tmp/x") || !strings.Contains(r.out, "- retried 3 times") {
		t.Errorf("ok=%v\n%s", ok, r.out)
	}
	pending := `

  store
    ✔ works
    - supports prefixes
    - handles unicode


  1 passing (4ms)
  2 pending
`
	r, _ = renderMocha(ctx(0, "mocha"), pending)
	if strings.Contains(r.out, "supports prefixes") || !strings.Contains(r.out, "  2 pending") {
		t.Errorf("pending titles kept:\n%s", r.out)
	}
}

// TestErrorCountsAreKept: rtk read "10 errors" as clean (it contains "0
// error"). vitest's "Errors  10 errors" summary line with every test
// passing is kept verbatim and is itself the failure an agent sees.
func TestErrorCountsAreKept(t *testing.T) {
	in := `
 RUN  v4.1.11 /home/user/src/proj

 ✓ test/a.test.ts (3 tests) 3ms

 Test Files  1 passed (1)
      Tests  3 passed (3)
     Errors  10 errors
   Start at  10:00:00
   Duration  100ms`
	out, ok := vitestFilter{}.Apply(ctx(1, "vitest", "run"), in)
	if !ok || !strings.Contains(out, "     Errors  10 errors") || !failLooking(out) {
		t.Errorf("ok=%v\n%s", ok, out)
	}
	res := engine.Process(ctx(1, "vitest", "run"), in, engine.Options{})
	if !strings.Contains(res.Output, "Errors  10 errors") {
		t.Errorf("pipeline dropped the error count:\n%s", res.Output)
	}
}
