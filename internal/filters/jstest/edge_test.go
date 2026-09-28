package jstest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

func ctx(exit int, argv ...string) *engine.Context {
	return &engine.Context{Argv: argv, Exit: exit, Cwd: "/home/user/src/proj", Home: "/home/user"}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		argv   string
		want   string
		stream bool
	}{
		{"jest", "jest", false},
		{"npx jest --ci", "jest", false},
		{"npx jest@29 test/a.test.js", "jest", false},
		{"/home/user/src/proj/node_modules/.bin/jest", "jest", false},
		{"./node_modules/.bin/jest --coverage", "jest", false},
		{"node node_modules/jest/bin/jest.js", "jest", false},
		{"node --experimental-vm-modules node_modules/.bin/jest", "jest", false},
		{"pnpm exec jest", "jest", false},
		{"pnpm jest", "jest", false},
		{"yarn jest --ci", "jest", false},
		{"bunx jest", "jest", false},
		{"npm exec -- jest", "jest", false},
		{"react-scripts test --watchAll=false", "jest", false},
		{"TZ=UTC npx jest", "jest", false},
		{"env -u CLAUDECODE -u AI_AGENT FORCE_COLOR=1 npx jest", "jest", false},
		{"jest --watch", "jest", true},
		{"jest --watchAll", "jest", true},
		{"jest --json", "", false},
		{"jest --listTests", "", false},
		{"jest --showConfig", "", false},
		{"vitest", "vitest", false},
		{"vitest run", "vitest", false},
		{"npx vitest run --reporter=verbose", "vitest", false},
		{"npx vitest run --reporter dot", "vitest", false},
		{"pnpm vitest run", "vitest", false},
		{"vitest watch", "vitest", true},
		{"vitest --watch", "vitest", true},
		{"vitest run --reporter=junit", "", false},
		{"vitest run --reporter json", "", false},
		{"vitest run --reporter=default --reporter=json --outputFile.json=/tmp/r.json", "vitest", false},
		{"vitest run --reporter=json --outputFile=/tmp/r.json", "", false},
		{"yarn workspace web test", "npm-test", false},
		{"pnpm --filter web test", "npm-test", false},
		{"npm -w web test", "npm-test", false},
		{"npx -y jest", "jest", false},
		{"npx --package=jest -- jest", "jest", false},
		{"vitest list", "", false},
		{"vitest bench", "", false},
		{"mocha", "mocha", false},
		{"npx mocha --reporter spec test/", "mocha", false},
		{"_mocha -R dot", "mocha", false},
		{"mocha --reporter tap", "", false},
		{"mocha -R json", "", false},
		{"mocha --watch", "mocha", true},
		{"npm test", "npm-test", false},
		{"npm t", "npm-test", false},
		{"npm run test", "npm-test", false},
		{"npm run test:unit", "npm-test", false},
		{"npm run-script test", "npm-test", false},
		{"npm --prefix app test", "npm-test", false},
		{"npm test -- --watch", "npm-test", true},
		{"yarn test", "npm-test", false},
		{"yarn run test", "npm-test", false},
		{"pnpm test", "npm-test", false},
		{"pnpm run test:e2e", "npm-test", false},
		{"bun test", "npm-test", false},
		{"bun run test", "npm-test", false},
		{"npm run build", "", false},
		{"npm install", "", false},
		{"yarn", "", false},
		{"node examples/search/index.js", "node-crash", false},
		{"/usr/local/bin/node server.js --port 3000", "node-crash", false},
		{"node --test", "", false},
		{"node --test test/", "", false},
		{"node -e 'throw new Error(1)'", "", false},
		{"node --version", "", false},
		{"node", "", false},
		{"go test ./...", "", false},
		{"git status", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.argv, func(t *testing.T) {
			c := ctx(0, strings.Fields(tc.argv)...)
			var got engine.Filter
			for _, f := range []engine.Filter{jestFilter{}, vitestFilter{}, mochaFilter{}, npmTestFilter{}, nodeFilter{}} {
				if f.Match(c) {
					got = f
					break
				}
			}
			name := ""
			if got != nil {
				name = got.Name()
			}
			if name != tc.want {
				t.Fatalf("matched %q, want %q", name, tc.want)
			}
			if f := engine.Find(c); tc.want != "" && (f == nil || f.Name() != tc.want) {
				t.Errorf("engine.Find = %v, want %s", f, tc.want)
			}
			if s, ok := got.(engine.Streamer); ok && s.Stream(c) != tc.stream {
				t.Errorf("Stream = %v, want %v", !tc.stream, tc.stream)
			} else if !ok && tc.stream {
				t.Error("want Stream, filter is no Streamer")
			}
		})
	}
}

func allFilters(exit int) []struct {
	f engine.Filter
	c *engine.Context
} {
	return []struct {
		f engine.Filter
		c *engine.Context
	}{
		{jestFilter{}, ctx(exit, "npx", "jest")},
		{vitestFilter{}, ctx(exit, "npx", "vitest", "run")},
		{mochaFilter{}, ctx(exit, "npx", "mocha")},
		{npmTestFilter{}, ctx(exit, "npm", "test")},
		{nodeFilter{}, ctx(exit, "node", "app.js")},
	}
}

func TestBailsOnUnknown(t *testing.T) {
	inputs := map[string]string{
		"empty":       "",
		"single line": "Tests: 1 passed, 1 total",
		"blank lines": "\n\n\n",

		"localized":      "Testdateien  3 bestanden (3)\n     Tests  12 bestanden (12)",
		"jasmine":        "Started\n...F\n\nFailures:\n1) x\n  Message:\n    Expected 1 to be 2.\n\n4 specs, 1 failure",
		"bun":            "bun test v1.1.0\n\ntest/a.test.ts:\n✓ adds [0.12ms]\n(fail) subtracts [0.10ms]\n\n 1 pass\n 1 fail\nRan 2 tests across 1 files. [12.00ms]",
		"truncated jest": "PASS test/a.test.js\nFAIL test/b.test.js\n  ● b › works\n\n    expect(received).toBe(expected)\n",
		"no crash":       "Server listening on :3000\nGET / 200 3ms",
		"random":         "lorem ipsum\ndolor sit amet\nerror: something unrelated",
	}
	for name, in := range inputs {
		for _, fc := range allFilters(1) {
			if out, ok := fc.f.Apply(fc.c, in); ok {
				t.Errorf("%s on %q: expected bail, got\n%s", fc.f.Name(), name, out)
			}
		}
	}
}

func TestNonZeroExitNeverLooksPassing(t *testing.T) {
	cases := []struct {
		f  engine.Filter
		c  *engine.Context
		in string
	}{
		{jestFilter{}, ctx(1, "jest"), "PASS test/a.test.js\n\nTest Suites: 1 passed, 1 total\nTests:       2 passed, 2 total\nSnapshots:   0 total\nTime:        0.5 s\nRan all test suites.\nJest did not exit one second after the test run has completed."},
		{vitestFilter{}, ctx(1, "vitest", "run"), "\n RUN  v4.1.11 /home/user/src/proj\n\n\n Test Files  1 passed (1)\n      Tests  2 passed (2)\n   Start at  00:00:00\n   Duration  100ms\n"},
		{mochaFilter{}, ctx(3, "mocha"), "\n\n  a\n    ✔ works\n\n\n  1 passing (3ms)\n\n"},
	}
	for _, tc := range cases {
		out, ok := tc.f.Apply(tc.c, tc.in)
		if !ok {
			t.Fatalf("%s bailed", tc.f.Name())
		}
		if !strings.Contains(out, "[lx: "+strings.Split(tc.f.Name(), "-")[0]) || !strings.Contains(out, fmt.Sprintf("exited %d", tc.c.Exit)) {
			t.Errorf("%s: exit %d not made visible:\n%s", tc.f.Name(), tc.c.Exit, out)
		}

		tc.c.Exit = 0
		if out, _ := tc.f.Apply(tc.c, tc.in); strings.Contains(out, "[lx: ") {
			t.Errorf("%s: exit 0 got a failure note:\n%s", tc.f.Name(), out)
		}
	}
}

func TestHugeOutputIsFastAndBounded(t *testing.T) {
	var jest, vit, mocha strings.Builder
	for i := 0; i < 45000; i++ {
		fmt.Fprintf(&jest, "PASS test/suite%d.test.js\n", i)
	}
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&jest, "FAIL test/f%d.test.js\n  ● f%d › fails with error\n\n    expect(received).toBe(expected) // Object.is equality\n\n    Expected: %d\n    Received: %d\n\n      1 | test(\"x\", () => {\n    > 2 |   expect(f()).toBe(%d);\n        |               ^\n      3 | });\n\n      at Object.toBe (test/f%d.test.js:2:15)\n\n", i, i, i, i+1, i, i)
	}
	jest.WriteString("Test Suites: 500 failed, 45000 passed, 45500 total\nTests:       500 failed, 45000 passed, 45500 total\nSnapshots:   0 total\nTime:        99 s\nRan all test suites.\n")

	vit.WriteString(" RUN  v4.1.11 /home/user/src/proj\n\n")
	vit.WriteString(" ❯ test/big.test.ts (50000 tests | 1 failed) 900ms\n")
	for i := 0; i < 50000; i++ {
		fmt.Fprintf(&vit, "     ✓ case %d handles errors 0ms\n", i)
	}
	vit.WriteString("     × case x 1ms\n\n⎯⎯⎯⎯⎯⎯⎯ Failed Tests 1 ⎯⎯⎯⎯⎯⎯⎯\n\n FAIL  test/big.test.ts > case x\nAssertionError: expected 1 to be 2\n ❯ test/big.test.ts:3:5\n\n⎯⎯⎯[1/1]⎯\n\n Test Files  1 failed (1)\n      Tests  1 failed | 50000 passed (50001)\n")

	mocha.WriteString("\n\n  big\n")
	for i := 0; i < 50000; i++ {
		fmt.Fprintf(&mocha, "    ✔ case %d\n", i)
	}
	mocha.WriteString("    1) fails\n\n\n  50000 passing (9s)\n  1 failing\n\n  1) big\n       fails:\n     Error: boom\n      at Context.<anonymous> (test/big.js:3:9)\n\n")

	for _, tc := range []struct {
		f  engine.Filter
		c  *engine.Context
		in string
	}{
		{jestFilter{}, ctx(1, "jest"), jest.String()},
		{vitestFilter{}, ctx(1, "vitest", "run"), vit.String()},
		{mochaFilter{}, ctx(1, "mocha"), mocha.String()},
	} {
		start := time.Now()
		r, ok := render(tc.f, tc.c, tc.in)
		el := time.Since(start)
		if !ok {
			t.Fatalf("%s bailed on huge output", tc.f.Name())
		}
		if el > 3*time.Second && !raceEnabled {
			t.Errorf("%s took %v on %d lines", tc.f.Name(), el, strings.Count(tc.in, "\n"))
		}
		if r.readded != 0 {
			t.Errorf("%s: safety net re-added %d lines", tc.f.Name(), r.readded)
		}
		res := engine.Process(tc.c, tc.in, engine.Options{})
		if res.Filter != tc.f.Name() || res.OutTokens > engine.DefaultBudget+200 {
			t.Errorf("%s: pipeline %s, %d tokens", tc.f.Name(), res.Filter, res.OutTokens)
		}
		t.Logf("%s: %d lines in %v → %d tokens (pipeline %d)", tc.f.Name(), strings.Count(tc.in, "\n"), el.Round(time.Millisecond), tokens.Count(r.out), res.OutTokens)
	}
}

func TestLongDiffKeepsEveryChange(t *testing.T) {
	var b strings.Builder
	b.WriteString("FAIL test/snap.test.js\n  ● renders\n\n    expect(received).toMatchSnapshot()\n\n    - Snapshot  - 3\n    + Received  + 3\n\n    @@ -1,200 +1,200 @@\n")
	for i := 0; i < 200; i++ {
		switch i {
		case 20, 120, 190:
			fmt.Fprintf(&b, "    -   \"row%d\": \"old\",\n    +   \"row%d\": \"new\",\n", i, i)
		default:
			fmt.Fprintf(&b, "        \"row%d\": \"same\",\n", i)
		}
	}
	b.WriteString("\n    > 2 |   expect(tree).toMatchSnapshot();\n        |                ^\n\n      at Object.toMatchSnapshot (test/snap.test.js:2:16)\n\n")
	b.WriteString("Test Suites: 1 failed, 1 total\nTests:       1 failed, 1 total\nSnapshots:   1 failed, 1 total\nTime:        1 s\n")
	out, ok := jestFilter{}.Apply(ctx(1, "jest"), b.String())
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{`-   "row20": "old",`, `+   "row120": "new",`, `+   "row190": "new",`, "unchanged lines", "test/snap.test.js:2:16", "Tests:       1 failed, 1 total"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if n := strings.Count(out, "\n"); n > 60 {
		t.Errorf("long diff not trimmed: %d lines", n)
	}
}

func TestHugeBlockIsCapped(t *testing.T) {
	var b strings.Builder
	b.WriteString("FAIL test/a.test.js\n  ● a › logs a lot\n\n    Error: first problem\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "    detail line %d\n", i)
	}
	b.WriteString("    TypeError: second problem\n\n      at Object.<anonymous> (test/a.test.js:9:3)\n\n")
	b.WriteString("Test Suites: 1 failed, 1 total\nTests:       1 failed, 1 total\nSnapshots:   0 total\nTime:        1 s\n")
	r, ok := renderJest(ctx(1, "jest"), b.String())
	if !ok {
		t.Fatal("bailed")
	}
	if r.readded != 0 {
		t.Errorf("safety net used:\n%s", r.out)
	}
	for _, want := range []string{"Error: first problem", "TypeError: second problem", "test/a.test.js:9:3", "lines omitted"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("missing %q", want)
		}
	}
	if n := strings.Count(r.out, "\n"); n > 80 {
		t.Errorf("block not capped: %d lines", n)
	}
}

func TestJestPassingSuiteConsoleErrorsKept(t *testing.T) {
	in := `PASS test/a.test.js
  ● Console

    console.log
      just chatter

      at Object.log (test/a.test.js:2:11)

    console.error
      Error: could not connect to db

      at Object.error (test/a.test.js:5:11)

Test Suites: 1 passed, 1 total
Tests:       1 passed, 1 total
Snapshots:   0 total
Time:        0.3 s
Ran all test suites.`
	r, ok := renderJest(ctx(0, "jest"), in)
	if !ok {
		t.Fatal("bailed")
	}
	if !strings.Contains(r.out, "PASS test/a.test.js\n    console.error\n      Error: could not connect to db\n      at Object.error (test/a.test.js:5:11)") {
		t.Errorf("error console entry of a passing suite not kept:\n%s", r.out)
	}
	if strings.Contains(r.out, "just chatter") || !strings.Contains(r.out, "1 console message of passing suites hidden") {
		t.Errorf("chatter not hidden/counted:\n%s", r.out)
	}
}

func TestWrapperLines(t *testing.T) {
	in := `yarn run v1.22.19
$ jest --ci
FAIL test/a.test.js
  ● a › works

    expect(received).toBe(expected) // Object.is equality

    Expected: 2
    Received: 1

      at Object.toBe (test/a.test.js:3:15)

Test Suites: 1 failed, 1 total
Tests:       1 failed, 1 total
Snapshots:   0 total
Time:        0.4 s
Ran all test suites.
error Command failed with exit code 1.
info Visit https://yarnpkg.com/en/docs/cli/run for documentation about this command.`
	out, ok := npmTestFilter{}.Apply(ctx(1, "yarn", "test"), in)
	if !ok {
		t.Fatal("bailed")
	}
	if strings.Contains(out, "yarn run v") || strings.Contains(out, "info Visit") {
		t.Errorf("yarn noise kept:\n%s", out)
	}
	for _, want := range []string{"$ jest --ci", "error Command failed with exit code 1.", "Expected: 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}

	npm := `
> app@1.0.0 test
> jest

FAIL test/a.test.js
  ● a › works

    Expected: 2
    Received: 1

Test Suites: 1 failed, 1 total
Tests:       1 failed, 1 total
Snapshots:   0 total
Time:        0.4 s
npm error Lifecycle script ` + "`test`" + ` failed with error:
npm error code 1
npm error path /home/user/src/proj
npm error command failed
npm error command sh -c jest`
	out, ok = npmTestFilter{}.Apply(ctx(1, "npm", "test"), npm)
	if !ok {
		t.Fatal("bailed on npm")
	}
	if !strings.HasPrefix(out, "> app@1.0.0 test\n> jest\n") || strings.Count(out, "> jest") != 1 {
		t.Errorf("npm echo not kept once:\n%s", out)
	}
	if len(fixture.ErrorLinesMissing(npm, out)) != 0 {
		t.Errorf("npm error lines dropped: %q", fixture.ErrorLinesMissing(npm, out))
	}
}

func TestJestOpenHandlesAfterSummary(t *testing.T) {
	in := `PASS test/server.test.js

Test Suites: 1 passed, 1 total
Tests:       1 passed, 1 total
Snapshots:   0 total
Time:        0.8 s
Ran all test suites.

Jest has detected the following 1 open handle potentially keeping Jest from exiting:

  ●  TCPSERVERWRAP

      3 | const app = require("../app");
      4 |
    > 5 | app.listen(3000);
        |     ^
      6 |

      at Function.listen (node_modules/express/lib/application.js:635:24)
      at Object.<anonymous> (test/server.test.js:5:5)
`
	r, ok := renderJest(ctx(1, "jest", "--detectOpenHandles"), in)
	if !ok || r.readded != 0 {
		t.Fatalf("ok=%v readded=%d", ok, r.readded)
	}
	for _, want := range []string{"open handle", "●  TCPSERVERWRAP", "> 5 | app.listen(3000);", "test/server.test.js:5:5"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("missing %q:\n%s", want, r.out)
		}
	}
}

func TestMochaErrorLookingTitlesAreNotErrors(t *testing.T) {
	in := `

  error handling
    when the db is down
      ✔ returns 503
      - retries after failure
  uploads
    ✔ rejects files that are too large

  2 passing (10ms)
  1 pending

`
	r, ok := renderMocha(ctx(0, "mocha"), in)
	if !ok {
		t.Fatal("bailed")
	}
	if r.readded != 0 || strings.Contains(r.out, "error handling") {
		t.Errorf("suite titles leaked:\n%s", r.out)
	}
	if !strings.Contains(r.out, "[2 passing tests hidden]\n  2 passing (10ms)\n  1 pending") {
		t.Errorf("summary:\n%s", r.out)
	}
}

func TestVitestConsoleOfFailingTestKept(t *testing.T) {
	in := `
 RUN  v4.1.11 /home/user/src/proj

stdout | test/a.test.ts > math > adds
computing 1 + 1

stderr | test/a.test.ts > math > subtracts
warning: slow path

 ❯ test/a.test.ts (2 tests | 1 failed) 5ms
     × adds 3ms
     ✓ subtracts 1ms

⎯⎯⎯⎯⎯⎯⎯ Failed Tests 1 ⎯⎯⎯⎯⎯⎯⎯

 FAIL  test/a.test.ts > math > adds
AssertionError: expected 3 to be 2 // Object.is equality
 ❯ test/a.test.ts:4:19
      2| describe("math", () => {
      3|   it("adds", () => {
      4|     expect(1 + 2).toBe(2);
       |                   ^
      5|   });

⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯[1/1]⎯

 Test Files  1 failed (1)
      Tests  1 failed | 1 passed (2)
   Start at  10:00:00
   Duration  200ms
`
	r, ok := renderVitest(ctx(1, "vitest", "run"), in)
	if !ok {
		t.Fatal("bailed")
	}
	if !strings.Contains(r.out, "stdout | test/a.test.ts > math > adds\ncomputing 1 + 1") {
		t.Errorf("failing test's console dropped:\n%s", r.out)
	}
	if strings.Contains(r.out, "slow path") {
		t.Errorf("passing test's console kept:\n%s", r.out)
	}
	if strings.Contains(r.out, "Start at") || !strings.Contains(r.out, "      4|     expect(1 + 2).toBe(2);\n       |                   ^") {
		t.Errorf("view:\n%s", r.out)
	}
}

func TestCoverageTableLarge(t *testing.T) {
	var b strings.Builder
	b.WriteString("PASS test/a.test.js\n")
	sep := "----------|---------|----------|---------|---------|-------------------"
	b.WriteString(sep + "\nFile      | % Stmts | % Branch | % Funcs | % Lines | Uncovered Line #s\n" + sep + "\n")
	b.WriteString("All files |   85.00 |    80.00 |   90.00 |   85.00 |\n src      |   85.00 |    80.00 |   90.00 |   85.00 |\n")
	for i := 0; i < 100; i++ {
		p := 100.0
		if i%2 == 0 {
			p = float64(40 + i/2)
		}
		fmt.Fprintf(&b, "  f%03d.js |  %6.2f |   100.00 |  100.00 |  %6.2f | 1-3\n", i, p, p)
	}
	b.WriteString(sep + "\n")
	b.WriteString("Jest: \"global\" coverage threshold for lines (80%) not met: 70%\n")
	b.WriteString("Test Suites: 1 passed, 1 total\nTests:       1 passed, 1 total\nSnapshots:   0 total\nTime:        1 s\n")
	r, ok := renderJest(ctx(1, "jest", "--coverage"), b.String())
	if !ok {
		t.Fatal("bailed")
	}

	if !strings.Contains(r.out, "f000.js") || !strings.Contains(r.out, "f058.js") || strings.Contains(r.out, "f060.js") || strings.Contains(r.out, "f001.js") {
		t.Errorf("wrong rows:\n%s", r.out)
	}
	if !strings.Contains(r.out, "[coverage: 60 files at ≥80% hidden; 10 more below 80% not shown]") {
		t.Errorf("marker:\n%s", r.out)
	}
	if !strings.Contains(r.out, "All files |") || !strings.Contains(r.out, "File      | % Stmts") || !strings.Contains(r.out, " src      |") {
		t.Errorf("header rows:\n%s", r.out)
	}
}

func TestSafetyNetReaddsUnhandledErrors(t *testing.T) {
	d := newDoc(ctx(1, "jest"), "ok line\nError: something broke\n  describe error handling\nplain", nil)
	d.keep(0)
	d.drop(2)
	r := d.finish()
	if r.readded != 1 || !strings.Contains(r.out, "[lx: error lines from the full output]\nError: something broke") {
		t.Errorf("safety net: %+v", r)
	}
	if len(r.benignLines()) != 1 || r.benignLines()[0] != "describe error handling" {
		t.Errorf("benign: %q", r.benignLines())
	}
}

func TestDeterministicOnCorpus(t *testing.T) {
	for _, cc := range corpus {
		fc := load(t, cc)
		c := fc.Context()
		f := engine.Find(c)
		a, _ := f.Apply(c, fc.Clean())
		b, _ := f.Apply(c, fc.Clean())
		if a != b {
			t.Errorf("%s: not deterministic", cc.name)
		}
	}
}

func FuzzFilters(f *testing.F) {
	for _, cc := range corpus {
		fc := load(f, cc)
		f.Add(fc.Clean(), 1)
	}
	for _, sc := range smallCaptures {
		fc, err := fixture.Read("testdata", "captures", sc.name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(fc.Clean(), 1)
	}
	f.Add("", 0)
	f.Add("FAIL x\n  ● a\n    > 1 |\n      at x (node_modules/a/b.js:1:1)\nTest Suites: 1 failed, 1 total\nTests: 1 failed, 1 total", 1)

	f.Add(synthJest(12), 1)
	f.Add(synthVitest(12), 1)
	f.Add(synthMocha(12), 12)
	f.Add("> a@1 test\n> jest\n\nPASS a.js\nTest Suites: 1 passed, 1 total\nTests: 1 passed, 1 total\n\n> b@1 test\n> jest\n\nPASS a.js\nTest Suites: 1 passed, 1 total\nTests: 1 passed, 1 total", 1)
	f.Add("\n\n  a\n  Error: x\n    at y (file:///home/user/src/proj/a.mjs:1:2)\n    ✔ b\n\n\n  1 passing (1ms)\n", 1)
	f.Fuzz(func(t *testing.T, in string, exit int) {
		for _, fc := range allFilters(exit % 256) {
			a, okA := fc.f.Apply(fc.c, in)
			b, okB := fc.f.Apply(fc.c, in)
			if a != b || okA != okB {
				t.Fatalf("%s not deterministic", fc.f.Name())
			}

			if okA && fc.c.Exit != 0 && fc.f.Name() != "node-crash" && !failLooking(a) {
				t.Fatalf("%s: exit %d but no failure in the view:\n%s", fc.f.Name(), fc.c.Exit, a)
			}

			r, ok := render(fc.f, fc.c, in)
			if !ok || r.readded >= maxSafetyLines {
				continue
			}
			if _, guarded := fc.f.(engine.Guarded); !guarded {
				continue
			}
			benign := map[string]bool{}
			for _, s := range r.benignLines() {
				benign[s] = true
			}
			for _, m := range engine.MissingErrorLines(in, r.out) {
				if !benign[m] {
					t.Fatalf("%s dropped error line %q", fc.f.Name(), m)
				}
			}
		}
	})
}

func TestVitestV1Layout(t *testing.T) {
	in := `
 RUN  v1.6.0 /home/user/src/proj

 ✓ test/ok.test.ts (3)
 ❯ test/fail.test.ts (2)
   ❯ math (2)
     × adds
     ✓ subtracts

⎯⎯⎯⎯⎯⎯⎯ Failed Tests 1 ⎯⎯⎯⎯⎯⎯⎯

 FAIL  test/fail.test.ts > math > adds
AssertionError: expected 3 to be 2 // Object.is equality
 ❯ test/fail.test.ts:4:19

⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯[1/1]⎯

 Test Files  1 failed | 1 passed (2)
      Tests  1 failed | 4 passed (5)
   Start at  10:00:00
   Duration  200ms
`
	r, ok := renderVitest(ctx(1, "vitest", "run"), in)
	if !ok || r.readded != 0 {
		t.Fatalf("ok=%v readded=%d", ok, r.readded)
	}
	for _, want := range []string{" ❯ test/fail.test.ts (2)", "     × adds", "[1 passing file, 4 passing tests hidden]", "AssertionError: expected 3 to be 2"} {
		if !strings.Contains(r.out, want) {
			t.Errorf("missing %q:\n%s", want, r.out)
		}
	}
	if strings.Contains(r.out, "❯ math (2)") {
		t.Errorf("describe group kept:\n%s", r.out)
	}
}

func TestNodeCrashLongPreamble(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "2026-09-26T10:00:%02d.000Z info request %d served in %dms\n", i%60, i, i%7)
	}
	b.WriteString(`/home/user/src/proj/server.js:42
    throw new Error("db pool exhausted");
    ^

Error: db pool exhausted
    at handle (/home/user/src/proj/server.js:42:11)
    at Layer.handle [as handle_request] (/home/user/src/proj/node_modules/express/lib/router/layer.js:95:5)
    at next (/home/user/src/proj/node_modules/express/lib/router/route.js:149:13)
    at Route.dispatch (/home/user/src/proj/node_modules/express/lib/router/route.js:119:3)
    at Layer.handle [as handle_request] (/home/user/src/proj/node_modules/express/lib/router/layer.js:95:5)
    at /home/user/src/proj/node_modules/express/lib/router/index.js:284:15
    at Function.process_params (/home/user/src/proj/node_modules/express/lib/router/index.js:346:12)
    at next (/home/user/src/proj/node_modules/express/lib/router/index.js:280:10)
    at process.processTicksAndRejections (node:internal/process/task_queues:95:5)

Node.js v24.18.0
`)
	c := ctx(1, "node", "server.js")
	out, ok := nodeFilter{}.Apply(c, strings.TrimRight(b.String(), "\n"))
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{"server.js:42", `throw new Error("db pool exhausted");`, "Error: db pool exhausted", "at handle (server.js:42:11)", "library frames (express", "Node.js v24.18.0"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "\n"); n > 40 {
		t.Errorf("preamble not reduced: %d lines\n%s", n, out)
	}
	if m := fixture.ErrorLinesMissing(b.String(), out); len(m) > 0 {
		t.Errorf("error lines dropped: %q", m)
	}
}

func TestSmallCaptures(t *testing.T) {
	for _, tc := range smallCaptures {
		fc, err := fixture.Read("testdata", "captures", tc.name)
		if err != nil {
			t.Fatal(err)
		}
		c := fc.Context()
		f := engine.Find(c)
		if f == nil || f.Name() != tc.filter {
			t.Fatalf("%s: filter %v", tc.name, f)
		}
		if _, ok := f.Apply(c, fc.Clean()); ok != tc.ok {
			t.Errorf("%s: ok=%v want %v", tc.name, ok, tc.ok)
		}
		res := engine.Process(c, fc.Raw, engine.Options{})
		if tc.generic {
			if res.Filter != "generic" || len(fixture.ErrorLinesMissing(fc.Clean(), res.Output)) != 0 {
				t.Errorf("%s: pipeline %s dropped error lines:\n%s", tc.name, res.Filter, res.Output)
			}
			continue
		}
		if res.Output != fc.Clean() || res.Lossy {
			t.Errorf("%s: small output altered (%s)", tc.name, res.Filter)
		}
	}
}

func TestSafetyNetSameTextBenignAndNot(t *testing.T) {
	d := newDoc(ctx(1, "mocha"), "  Error: timeout\nfine\nError: timeout", nil)
	d.drop(0)
	d.keep(1)
	r := d.finish()
	if r.readded != 1 || len(r.benignLines()) != 0 {
		t.Errorf("readded=%d benign=%q\n%s", r.readded, r.benignLines(), r.out)
	}
}
