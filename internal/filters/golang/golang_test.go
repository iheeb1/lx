package golang

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
)

func ctx(exit int, argv ...string) *engine.Context {
	return &engine.Context{Argv: argv, Exit: exit, Cwd: "/home/user/src/app", Home: "/home/user"}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		argv []string
		want string // filter name, "" for none of ours
	}{
		{[]string{"go", "test", "./..."}, "go-test"},
		{[]string{"/usr/local/go/bin/go", "test", "-v", "./..."}, "go-test"},
		{[]string{"go1.22.3", "test", "."}, "go-test"},
		{[]string{"go.exe", "test", "."}, "go-test"},
		{[]string{"gotip", "test", "."}, "go-test"},
		{[]string{"/home/user/sdk/go1.22.1/bin/go", "vet", "./..."}, "go-build"},
		{[]string{"go", "-C", "sub", "test", "./..."}, "go-test"},
		{[]string{"go", "test", "-run=TestX", "-count=1", "-race", "./..."}, "go-test"},
		{[]string{"go", "test", "-json=false", "./..."}, "go-test"},
		{[]string{"go", "test", "./...", "-args", "-bench", "-json"}, "go-test"},
		{[]string{"go", "test", "-o", "x.test", "./pkg"}, "go-test"},
		{[]string{"go", "test", "-json", "./..."}, "go-test-json"},
		{[]string{"go", "test", "./...", "-json"}, "go-test-json"},
		{[]string{"go", "test", "--json", "./..."}, "go-test-json"},
		{[]string{"go", "test", "-bench=.", "./..."}, ""},
		{[]string{"go", "test", "-test.bench", ".", "./..."}, ""},
		{[]string{"go", "test", "-fuzz=FuzzX", "."}, ""},
		{[]string{"go", "test", "-list", ".", "."}, ""},
		{[]string{"go", "test", "-x", "."}, ""},
		{[]string{"go", "test", "-json", "-bench", ".", "."}, "go-machine"},
		{[]string{"go", "test", "-c", "./pkg"}, "go-build"},
		{[]string{"go", "build", "./..."}, "go-build"},
		{[]string{"go", "vet", "./..."}, "go-build"},
		{[]string{"go", "install", "example.com/cmd@latest"}, "go-build"},
		{[]string{"go", "vet", "-json", "./..."}, "go-machine"},
		{[]string{"go", "build", "-x", "./..."}, ""},
		{[]string{"go", "build", "-json", "./..."}, "go-machine"},
		{[]string{"go", "env", "-json"}, "go-machine"},
		{[]string{"go", "mod", "tidy"}, "go-mod"},
		{[]string{"go", "mod", "download", "-x"}, "go-mod"},
		{[]string{"go", "mod", "vendor"}, "go-mod"},
		{[]string{"go", "get", "-u", "./..."}, "go-mod"},
		{[]string{"go", "mod", "download", "-json"}, "go-machine"},
		{[]string{"go", "mod", "graph"}, ""},
		{[]string{"go", "mod", "why", "x"}, ""},
		{[]string{"go", "mod"}, ""},
		{[]string{"go", "list", "-m", "all"}, "go-list"},
		{[]string{"go", "list", "./..."}, "go-list"},
		{[]string{"go", "list", "-json", "./..."}, "go-machine"},
		{[]string{"go", "list", "-m", "-json", "all"}, "go-machine"},
		{[]string{"go", "list", "-f", "{{.Dir}}", "./..."}, "go-machine"},
		{[]string{"go", "run", ".", "-json"}, ""},
		{[]string{"go", "tool", "cover", "-json"}, ""},
		{[]string{"go", "run", "."}, ""},
		{[]string{"go", "version"}, ""},
		{[]string{"go"}, ""},
		{[]string{"gofmt", "-l", "."}, ""},
		{[]string{"golangci-lint", "run"}, ""},
		{[]string{"make", "test"}, ""},
		{[]string{"go", "-x", "test"}, ""},
	}
	for _, tc := range cases {
		c := ctx(0, tc.argv...)
		got := ""
		for _, f := range []engine.Filter{testJSON{}, testText{}, build{}, mod{}, list{}, machine{}} {
			if f.Match(c) {
				got = f.Name()
				break
			}
		}
		if got != tc.want {
			t.Errorf("%q: matched %q, want %q", tc.argv, got, tc.want)
		}
	}
}

func TestEmptyAndUnknownBail(t *testing.T) {
	filters := []engine.Filter{testJSON{}, testText{}, build{}, mod{}}
	inputs := map[string]string{
		"empty":      "",
		"blank":      "\n\n",
		"one word":   "hello",
		"localized":  "Échec des tests : 3 réussis, 1 échec\nTerminé en 0,4 s",
		"pytest":     "============ test session starts ============\ncollected 3 items\n\ntests/test_a.py ..F\n===== 1 failed, 2 passed in 0.12s =====",
		"jest":       "FAIL src/a.test.js\n  ● adds\n\nTests:       1 failed, 3 passed, 4 total",
		"json-array": `[{"Action":"pass"}]`,
	}
	for name, in := range inputs {
		for _, f := range filters {
			if out, ok := f.Apply(ctx(1, "go", "test"), in); ok {
				t.Errorf("%s/%s: want bail, got ok:\n%s", f.Name(), name, out)
			}
		}
	}
	if _, ok := TestTextDetect(ctx(1, "make", "test"), inputs["pytest"]); ok {
		t.Error("detector accepted pytest output")
	}
}

func TestSingleLines(t *testing.T) {
	cases := []struct {
		f    engine.Filter
		exit int
		in   string
		want string
	}{
		{testText{}, 0, "ok  \texample.com/a\t0.012s", "ok  \texample.com/a\t0.012s"},
		{testText{}, 1, "FAIL\texample.com/a [build failed]", "FAIL\texample.com/a [build failed]"},
		{testText{}, 0, "?   \texample.com/a\t[no test files]", "?   \texample.com/a\t[no test files]"},
		{build{}, 1, "./a.go:3:2: undefined: x", "./a.go:3:2: undefined: x"},
		{mod{}, 0, "go: downloading example.com/m v1.2.3", "[go: downloading 1 module: example.com/m]"},
		{list{}, 0, "example.com/m", "example.com/m"},
	}
	for _, tc := range cases {
		got, ok := tc.f.Apply(ctx(tc.exit, "go", "test"), tc.in)
		if !ok || got != tc.want {
			t.Errorf("%s(%q) = %q, %v; want %q", tc.f.Name(), tc.in, got, ok, tc.want)
		}
	}
}

// A failing exit status with nothing but passing verdicts must never be
// rendered as a pass: the filter bails and the generic view shows all.
func TestFailingExitWithPassLookingText(t *testing.T) {
	for _, in := range []string{
		"ok  \texample.com/a\t0.012s\nok  \texample.com/b\t0.020s",
		"=== RUN   TestA\n--- PASS: TestA (0.00s)\nPASS\nok  \texample.com/a\t0.012s",
		"?   \texample.com/a\t[no test files]",
	} {
		if out, ok := (testText{}).Apply(ctx(1, "go", "test", "./..."), in); ok {
			t.Errorf("exit 1 with only passes: want bail, got\n%s", out)
		}
		if out, ok := TestTextDetect(ctx(2, "make", "test"), in); ok {
			t.Errorf("detector: exit 2 with only passes: want bail, got\n%s", out)
		}
	}
	// With a failure present the view is rendered and the failure shown.
	in := "ok  \texample.com/a\t0.012s\n--- FAIL: TestB (0.00s)\n    b_test.go:9: boom\nFAIL\nFAIL\texample.com/b\t0.010s\nFAIL"
	out, ok := (testText{}).Apply(ctx(1, "go", "test", "./..."), in)
	if !ok || !strings.Contains(out, "--- FAIL: TestB") || !strings.HasSuffix(out, "FAIL") {
		t.Fatalf("got %v\n%s", ok, out)
	}
}

func TestTestShapes(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		exit int
		in   string
		want string
	}{
		{"non-v subtests keep nesting and all output", []string{"go", "test"}, 1,
			"--- FAIL: TestA (0.01s)\n    a_test.go:10: parent says hi\n    --- FAIL: TestA/sub (0.00s)\n        a_test.go:12: got 1\n            want 2\nFAIL\nFAIL\texample.com/a\t0.1s\nFAIL",
			"--- FAIL: TestA (0.01s)\n    a_test.go:10: parent says hi\n    --- FAIL: TestA/sub (0.00s)\n        a_test.go:12: got 1\n            want 2\nFAIL\nFAIL\texample.com/a\t0.1s\nFAIL"},
		{"-v output re-indented under its FAIL line, passes counted", []string{"go", "test", "-v"}, 1,
			"=== RUN   TestA\n=== RUN   TestA/sub\n    a_test.go:12: got 1\n        want 2\n=== RUN   TestA/ok\n    a_test.go:20: fine\n--- FAIL: TestA (0.00s)\n    --- FAIL: TestA/sub (0.00s)\n    --- PASS: TestA/ok (0.00s)\n=== RUN   TestB\n--- PASS: TestB (0.00s)\nFAIL\nFAIL\texample.com/a\t0.1s\nFAIL",
			"--- FAIL: TestA (0.00s)\n    --- FAIL: TestA/sub (0.00s)\n        a_test.go:12: got 1\n            want 2\nFAIL\nFAIL\texample.com/a\t0.1s\nFAIL\n" +
				"[2 passed, 2 failed (incl. subtests) · hidden: 6 === RUN/--- PASS lines, 1 line of passing-test output]"},
		{"ok lines collapse above three, coverage and [no tests to run] kept", []string{"go", "test", "./..."}, 0,
			"ok  \tex.com/m/a\t0.1s\nok  \tex.com/m/b\t(cached)\nok  \tex.com/m/c\t0.1s\nok  \tex.com/m/d\t0.1s\nok  \tex.com/m/e\t0.1s [no tests to run]\nok  \tex.com/m/f\t0.2s\tcoverage: 50.0% of statements\n?   \tex.com/m/g\t[no test files]",
			"ok  \t4 packages (1 cached) under ex.com/m: a, b, c, d\nok  \tex.com/m/e\t0.1s [no tests to run]\nok  \tex.com/m/f\t0.2s\tcoverage: 50.0% of statements\n?   \tex.com/m/g\t[no test files]"},
		{"short passing output shown when the run passed", []string{"go", "test", "-v"}, 0,
			"=== RUN   TestA\n    a_test.go:3: value=42\n--- PASS: TestA (0.00s)\nPASS\nok  \tex.com/a\t0.1s",
			"--- PASS: TestA (0.00s)\n    a_test.go:3: value=42\nok  \tex.com/a\t0.1s\n[1 passed · hidden: 1 === RUN/--- PASS line]"},
		{"stray output before the failure kept, after it hidden", []string{"go", "test"}, 1,
			"debug: x=1\n--- FAIL: TestA (0.00s)\n    a_test.go:3: boom\nnoise from a later test\nFAIL\nFAIL\tex.com/a\t0.1s\nFAIL",
			"debug: x=1\n--- FAIL: TestA (0.00s)\n    a_test.go:3: boom\nFAIL\nFAIL\tex.com/a\t0.1s\nFAIL\n[hidden: 1 line of passing-test output]"},
		{"failure without --- FAIL keeps every line", []string{"go", "test"}, 1,
			"=== RUN   TestLoad\n2026/09/26 10:00:00 config missing\nFAIL\tex.com/a\t0.1s\nFAIL",
			"=== RUN   TestLoad\n2026/09/26 10:00:00 config missing\nFAIL\tex.com/a\t0.1s\nFAIL"},
		{"skips listed with reasons when few", []string{"go", "test", "-v"}, 0,
			"=== RUN   TestA\n    a_test.go:5: needs docker\n--- SKIP: TestA (0.00s)\nPASS\nok  \tex.com/a\t0.1s",
			"--- SKIP: TestA (0.00s)\n    a_test.go:5: needs docker\nok  \tex.com/a\t0.1s\n[0 passed, 1 skipped · hidden: 1 === RUN/--- PASS line]"},
		{"error-like output of passing tests kept under a header", []string{"go", "test", "-v"}, 0,
			"=== RUN   TestA\nError: bad flag\nUsage: a [flags]\n" + strings.Repeat("  --flag string\n", 50) + "--- PASS: TestA (0.00s)\nPASS\nok  \tex.com/a\t0.1s",
			"[error-like lines printed by passing tests (the rest of their output is hidden):]\nError: bad flag\nok  \tex.com/a\t0.1s\n[1 passed · hidden: 2 === RUN/--- PASS lines, 52 lines of passing-test output]"},
		{"downloads condensed, compiler errors verbatim", []string{"go", "test", "./..."}, 1,
			"go: downloading github.com/pkg/errors v0.9.1\ngo: downloading example.com/x v1.0.0\n# ex.com/a\n./a.go:3:2: undefined: y\nFAIL\tex.com/a [build failed]\nFAIL",
			// pkg/errors is a module name, not an error: it folds into the count.
			"[go: downloading 2 modules: github.com/pkg/errors, example.com/x]\n# ex.com/a\n./a.go:3:2: undefined: y\nFAIL\tex.com/a [build failed]\nFAIL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ctx(tc.exit, tc.argv...)
			got, ok := testText{}.Apply(c, tc.in)
			if !ok || got != tc.want {
				t.Fatalf("ok=%v\n--- got ---\n%s\n--- want ---\n%s", ok, got, tc.want)
			}
			if m := fixture.ErrorLinesMissing(stripMarkers(tc.in), got); len(m) > 0 {
				t.Errorf("error lines missing: %q", m)
			}
		})
	}
}

func TestRunShowsSelectedTestsOutput(t *testing.T) {
	in := "=== RUN   TestA\n" + strings.Repeat("    a_test.go:3: step\n", 60) + "--- PASS: TestA (0.00s)\nPASS\nok  \tex.com/a\t0.1s"
	got, _ := testText{}.Apply(ctx(0, "go", "test", "-v"), in)
	if strings.Contains(got, "a_test.go:3") {
		t.Errorf("60 lines of passing output shown without -run:\n%s", got)
	}
	got, _ = testText{}.Apply(ctx(0, "go", "test", "-v", "-run", "TestA"), in)
	if strings.Count(got, "a_test.go:3: step") != 1 || !strings.Contains(got, "[×60]") {
		t.Errorf("-run: selected test's output not shown (collapsed):\n%s", got)
	}
}

func TestPanicAndTimeoutKeepEssentials(t *testing.T) {
	in := `=== RUN   TestSlow
panic: test timed out after 1s
	running tests:
		TestSlow (1s)

goroutine 7 [running]:
testing.(*M).startAlarm.func1()
	/usr/local/go/src/testing/testing.go:2802 +0x2cc
created by time.goFunc
	/usr/local/go/src/time/sleep.go:215 +0x38

goroutine 6 [chan receive]:
example.com/app.waitForever(0xc000010000)
	/home/user/src/app/app.go:12 +0x44
example.com/app.TestSlow(0xc0000a0000?)
	/home/user/src/app/app_test.go:8 +0x1c
testing.tRunner(0xc0000a0000, 0x1011)
	/usr/local/go/src/testing/testing.go:2036 +0xc4
created by testing.(*T).Run in goroutine 1
	/usr/local/go/src/testing/testing.go:2101 +0x3a8
FAIL	example.com/app	1.012s
FAIL`
	got, ok := testText{}.Apply(ctx(1, "go", "test", "-v", "./..."), in)
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{"panic: test timed out after 1s", "\trunning tests:", "\t\tTestSlow (1s)",
		"example.com/app.waitForever(...)", "\tapp.go:12", "\tapp_test.go:8", "FAIL\texample.com/app\t1.012s",
		"[paths: $GOROOT=/usr/local/go]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "+0x") || strings.Contains(got, "0xc000010000") {
		t.Errorf("offsets/argument words kept:\n%s", got)
	}
}

func TestElideRepeats(t *testing.T) {
	in := []string{"Error: a", "Usage:", "  x [flags]", "", "Flags:", "  -h", "", "Error: b", "Usage:", "  x [flags]", "", "Flags:", "  -h", "", "tail"}
	want := []string{"Error: a", "Usage:", "  x [flags]", "", "Flags:", "  -h", "", "Error: b", "… 5 lines repeated from above …", "", "tail"}
	if got := elideRepeats(in); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got  %q\nwant %q", got, want)
	}
	// Short repeats are left alone.
	in = []string{"a", "b", "c", "a", "b", "d"}
	if got := elideRepeats(in); len(got) != len(in) {
		t.Errorf("elided a 2-line repeat: %q", got)
	}
}

func TestElideArgs(t *testing.T) {
	cases := map[string]string{
		"testing.(*T).Run(0x1, {0x2, 0x3}, 0x4)":     "testing.(*T).Run(...)",
		"main.main()":                                "main.main()",
		"pkg.f(...)":                                 "pkg.f(...)",
		"created by testing.(*T).Run in goroutine 1": "created by testing.(*T).Run in goroutine 1",
		"pkg.(*T).m(0xc0, {0x1, 0x2, 0x3})":          "pkg.(*T).m(...)",
	}
	for in, want := range cases {
		if got := elideArgs(in); got != want {
			t.Errorf("elideArgs(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildDedupeAndHeaders(t *testing.T) {
	in := "# ex.com/a\n./a.go:3:2: undefined: y\n# ex.com/a [ex.com/a.test]\n./a.go:3:2: undefined: y\n./a_test.go:9:1: missing return"
	got, ok := build{}.Apply(ctx(1, "go", "vet", "./..."), in)
	want := "# ex.com/a\n./a.go:3:2: undefined: y [×2]\n# ex.com/a [ex.com/a.test]\n./a_test.go:9:1: missing return"
	if !ok || got != want {
		t.Fatalf("got %v\n%s\nwant\n%s", ok, got, want)
	}
	// Unrecognized output (a program's own text) is not claimed.
	if _, ok := (build{}).Apply(ctx(1, "go", "build"), "some other tool output\nwith no diagnostics"); ok {
		t.Error("claimed unrecognized output")
	}
}

func TestModTrace(t *testing.T) {
	in := `# get https://proxy.golang.org/github.com/!burnt!sushi/toml/@v/v1.3.2.zip
# get https://proxy.golang.org/example.com/gone/@v/list
# get https://proxy.golang.org/github.com/!burnt!sushi/toml/@v/v1.3.2.zip: 200 OK (0.1s)
# get https://proxy.golang.org/example.com/gone/@v/list: 410 Gone (0.1s)
go: example.com/gone@latest: reading https://proxy.golang.org/example.com/gone/@v/list: 410 Gone`
	got, ok := mod{}.Apply(ctx(1, "go", "mod", "download", "-x"), in)
	want := "[go -x: 3 \"# get\" trace lines hidden (2 requests, 1 OK response); module zips fetched: github.com/BurntSushi/toml@v1.3.2]\n" +
		"# get https://proxy.golang.org/example.com/gone/@v/list: 410 Gone (0.1s)\n" +
		"go: example.com/gone@latest: reading https://proxy.golang.org/example.com/gone/@v/list: 410 Gone"
	if !ok || got != want {
		t.Fatalf("got %v\n%s\nwant\n%s", ok, got, want)
	}
}

func TestDownloadSummaryLong(t *testing.T) {
	var b strings.Builder
	for i := range 40 {
		fmt.Fprintf(&b, "go: downloading example.com/m%d v1.0.%d\n", i, i)
	}
	b.WriteString("go: added example.com/m3 v1.0.3")
	got, _ := mod{}.Apply(ctx(0, "go", "get", "example.com/m3"), b.String())
	want := "[go: downloading 40 modules (1 named below)]\ngo: added example.com/m3 v1.0.3"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestJSONShapes(t *testing.T) {
	ev := func(action, pkg, test, output string) string {
		s := fmt.Sprintf(`{"Time":"2026-09-26T10:00:00Z","Action":%q,"Package":%q`, action, pkg)
		if test != "" {
			s += fmt.Sprintf(`,"Test":%q`, test)
		}
		if output != "" {
			s += fmt.Sprintf(`,"Output":%q`, output)
		}
		return s + "}"
	}
	lines := []string{
		`{"ImportPath":"ex.com/b [ex.com/b.test]","Action":"build-output","Output":"# ex.com/b [ex.com/b.test]\n"}`,
		`{"ImportPath":"ex.com/b [ex.com/b.test]","Action":"build-output","Output":"./b_test.go:3:1: undefined: z\n"}`,
		`{"ImportPath":"ex.com/b [ex.com/b.test]","Action":"build-fail"}`,
		"go: warning: stderr line that is not JSON",
		ev("start", "ex.com/a", "", ""),
		ev("run", "ex.com/a", "TestP", ""),
		ev("output", "ex.com/a", "TestP", "=== RUN   TestP\n"),
		ev("run", "ex.com/a", "TestP/x", ""),
		ev("output", "ex.com/a", "TestP/x", "=== RUN   TestP/x\n"),
		ev("run", "ex.com/a", "TestP/y", ""),
		ev("output", "ex.com/a", "TestP/y", "=== RUN   TestP/y\n"),
		// Interleaved parallel output: attribution comes from the Test field.
		ev("output", "ex.com/a", "TestP/y", "    a_test.go:9: y says "),
		ev("output", "ex.com/a", "TestP/x", "    a_test.go:9: x fine\n"),
		ev("output", "ex.com/a", "TestP/y", "boom\n"),
		ev("output", "ex.com/a", "TestP/y", "--- FAIL: TestP/y (0.00s)\n"),
		ev("fail", "ex.com/a", "TestP/y", ""),
		ev("output", "ex.com/a", "TestP/x", "--- PASS: TestP/x (0.00s)\n"),
		ev("pass", "ex.com/a", "TestP/x", ""),
		ev("output", "ex.com/a", "TestP", "--- FAIL: TestP (0.00s)\n"),
		ev("fail", "ex.com/a", "TestP", ""),
		ev("output", "ex.com/a", "", "FAIL\n"),
		ev("output", "ex.com/a", "", "FAIL\tex.com/a\t0.1s\n"),
		ev("fail", "ex.com/a", "", ""),
		ev("output", "ex.com/b", "", "FAIL\tex.com/b [build failed]\n"),
		ev("fail", "ex.com/b", "", ""),
		ev("start", "ex.com/c", "", ""),
		ev("fail", "ex.com/c", "", ""),
	}
	in := strings.Join(lines, "\n")
	got, ok := testJSON{}.Apply(ctx(1, "go", "test", "-json", "./..."), in)
	want := strings.Join([]string{
		"# ex.com/b [ex.com/b.test]",
		"./b_test.go:3:1: undefined: z",
		"go: warning: stderr line that is not JSON",
		"--- FAIL: TestP (0.00s)",
		"    --- FAIL: TestP/y (0.00s)",
		"        a_test.go:9: y says boom",
		"FAIL",
		"FAIL\tex.com/a\t0.1s",
		"FAIL\tex.com/b [build failed]",
		"FAIL\tex.com/c [lx: -json reported the package failed without a FAIL line]",
		"[1 passed, 2 failed (incl. subtests) · hidden: 4 === RUN/--- PASS lines, 1 line of passing-test output]",
	}, "\n")
	if !ok || got != want {
		t.Fatalf("ok=%v\n--- got ---\n%s\n--- want ---\n%s", ok, got, want)
	}
	dec, _ := DecodeTestJSON(in)
	if m := fixture.ErrorLinesMissing(stripMarkers(dec), got); len(m) > 0 {
		t.Errorf("error lines missing: %q", m)
	}
}

func distinctErrors(n int) string {
	var b strings.Builder
	b.WriteString("=== RUN   TestBig\n")
	for i := range n {
		fmt.Fprintf(&b, "    big_test.go:%d: error: case %d failed with value %d\n", i, i, i*7)
	}
	b.WriteString("--- FAIL: TestBig (0.00s)\nFAIL\nFAIL\tex.com/big\t1.0s\nFAIL")
	return b.String()
}

func deepNesting(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "%s--- FAIL: T%d (0.00s)\n", strings.Repeat(" ", i%200), i)
	}
	for range n {
		b.WriteString("  x\n")
	}
	b.WriteString("FAIL\tp\t0.1s")
	return b.String()
}

// synth builds n lines of plausible go test -v output for one package.
func synth(n int, fail bool) string {
	var b strings.Builder
	for i := range n / 3 {
		fmt.Fprintf(&b, "=== RUN   TestCase%d\n    x_test.go:%d: log line %d\n--- PASS: TestCase%d (0.00s)\n", i, i, i, i)
	}
	if fail {
		b.WriteString("=== RUN   TestBad\n    x_test.go:1: boom\n--- FAIL: TestBad (0.00s)\nFAIL\nFAIL\tex.com/big\t9.9s\nFAIL\n")
	} else {
		b.WriteString("PASS\nok  \tex.com/big\t9.9s\n")
	}
	return b.String()
}

func okLines(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "ok  \tex.com/m/p%d\t0.1s\n", i)
	}
	return b.String()
}

func TestHugeOutputIsFast(t *testing.T) {
	cases := map[string]struct {
		f   engine.Filter
		c   *engine.Context
		in  string
		max int // max output lines
	}{
		"v-pass":  {testText{}, ctx(0, "go", "test", "-v"), synth(50000, false), 5},
		"v-fail":  {testText{}, ctx(1, "go", "test", "-v"), synth(50000, true), 10},
		"stray":   {testText{}, ctx(1, "go", "test"), strings.Repeat("some log line from the program\n", 50000) + "FAIL\tex.com/big\t9.9s\nFAIL", 100},
		"repeats": {testText{}, ctx(1, "go", "test"), strings.Repeat("Error: flag x\nUsage:\n  cmd [flags]\n\n", 12500) + "FAIL\tex.com/big\t9.9s\nFAIL", 20},
		"build":   {build{}, ctx(1, "go", "build"), strings.Repeat("./a.go:1:1: undefined: x\n", 50000), 3},
		"mod":     {mod{}, ctx(0, "go", "mod", "tidy"), strings.Repeat("go: downloading example.com/m v1.0.0\n", 50000), 3},
		// 50k distinct error lines in one failing test, all kept: the error
		// guard must not be quadratic (engine.Guard alone takes ~20 s here).
		"errors": {testText{}, ctx(1, "go", "test", "-v"), distinctErrors(50000), 50010},
		// Deeply nested result lines followed by shallow output lines.
		"nesting": {testText{}, ctx(1, "go", "test"), deepNesting(20000), 60000},
		// Abnormal exit after a failure: nothing is hidden, the run is capped.
		"abnormal exit": {testText{}, ctx(1, "go", "test"),
			"--- FAIL: TestA (0.00s)\n    a_test.go:1: boom\n" + strings.Repeat("output before the exit\n", 50000) + "FAIL\tex.com/big\t1.0s\nFAIL", 100},
		// A "panic: " line printed by a passing test, then 100k lines.
		"false crash": {testText{}, ctx(0, "go", "test", "-v"),
			"panic: not really\n" + synth(100000, false), 5},
		"ok lines": {testText{}, ctx(0, "go", "test"), okLines(50000), 2},
		"one huge line": {testJSON{}, ctx(1, "go", "test", "-json"),
			strings.Repeat(`{"Action":"output","Package":"p","Output":"x"} `, 20000) + "\n" + `{"Action":"fail","Package":"p"}`, 5},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			got, ok := tc.f.Apply(tc.c, tc.in)
			if d := time.Since(start); d > slowdown*3*time.Second {
				t.Errorf("took %v", d)
			}
			if !ok {
				t.Fatal("bailed")
			}
			if n := strings.Count(got, "\n") + 1; n > tc.max {
				t.Errorf("%d output lines, want <= %d:\n%s", n, tc.max, got[:min(len(got), 2000)])
			}
		})
	}
	// The same through the full pipeline, -json included.
	var b strings.Builder
	for i := range 20000 {
		fmt.Fprintf(&b, `{"Action":"output","Package":"ex.com/big","Test":"TestCase%d","Output":"=== RUN   TestCase%d\n"}`+"\n", i, i)
		fmt.Fprintf(&b, `{"Action":"output","Package":"ex.com/big","Test":"TestCase%d","Output":"--- PASS: TestCase%d (0.00s)\n"}`+"\n", i, i)
		fmt.Fprintf(&b, `{"Action":"pass","Package":"ex.com/big","Test":"TestCase%d"}`+"\n", i)
	}
	b.WriteString(`{"Action":"output","Package":"ex.com/big","Output":"ok  \tex.com/big\t1.0s\n"}` + "\n" + `{"Action":"pass","Package":"ex.com/big"}`)
	start := time.Now()
	res := engine.Process(ctx(0, "go", "test", "-json", "./..."), b.String(), engine.Options{})
	if d := time.Since(start); d > slowdown*5*time.Second {
		t.Errorf("-json 60k lines took %v", d)
	}
	if res.Filter != "go-test-json" || !strings.Contains(res.Output, "20,000 passed") {
		t.Errorf("filter %s:\n%s", res.Filter, res.Output)
	}
}
