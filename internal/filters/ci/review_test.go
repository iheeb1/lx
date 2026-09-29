package ci_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/filters/ci"
	"github.com/iheeb1/lx/internal/testenv"
)

type summaryOnly struct{}

func (summaryOnly) Name() string                                 { return "zz-summary" }
func (summaryOnly) Match(*engine.Context) bool                   { return false }
func (summaryOnly) Apply(*engine.Context, string) (string, bool) { return "[summary]", true }

func init() {
	engine.RegisterDetector(engine.Detector{
		Name:   "zz-summary",
		Detect: func(s string) bool { return strings.HasPrefix(s, "ZZSUMMARY\n") },
		Filter: summaryOnly{},
		Argv:   []string{"zz-summary"},
	})
}

func wholeLog(t *testing.T, in string) string {
	t.Helper()
	got := runLog(t, in)
	c := &engine.Context{Argv: []string{"gh", "run", "view", "1", "--log-failed"}, Cwd: "/home/user/src/x", Home: "/home/user"}
	if out, _, _, _, ok := ci.RenderLog(c, in); !ok || out != got {
		t.Errorf("the self-guard had to add lines:\n%s", got)
	}
	return got
}

func TestScriptGroupsAreOutput(t *testing.T) {
	head := []string{"##[group]Run ./ci.sh", "^[[36;1m./ci.sh^[[0m", "shell: /usr/bin/bash -e {0}", "##[endgroup]"}
	body := []string{
		"##[group]Run unit tests",
		"go test ./...",
		"--- FAIL: TestSecret (0.00s)",
		"    secret_test.go:9: token mismatch",
		"    secret_test.go:10: second",
		"    secret_test.go:11: third",
		"    secret_test.go:12: fourth",
		"    secret_test.go:13: fifth",
		"    secret_test.go:14: sixth",
		"    secret_test.go:15: seventh",
		"FAIL",
		"FAIL\texample.com/m\t0.01s",
		"##[endgroup]",
		"##[error]Process completed with exit code 1.",
	}
	for name, in := range map[string]string{
		"named step":   ghLines("build", "Test", append(append([]string{}, head...), body...)...),
		"UNKNOWN STEP": ghLines("build", "UNKNOWN STEP", append(append([]string{"Current runner version: '2.337.0'"}, head...), body...)...),
	} {
		got := wholeLog(t, in)
		for _, want := range []string{"./ci.sh", "Run unit tests", "secret_test.go:9: token mismatch", "secret_test.go:15: seventh", "FAIL\texample.com/m\t0.01s"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: missing %q in:\n%s", name, want, got)
			}
		}
		if strings.Contains(got, "script line") || strings.Contains(got, "✗ Run unit tests") || strings.Contains(got, "$ go test") {
			t.Errorf("%s: the script's own ::group::Run was read as a runner step:\n%s", name, got)
		}
	}
}

func TestMatrixDedupShowsWhatDiffers(t *testing.T) {
	step := func(job, test, msg string) string {
		return ghLines(job, "Test",
			"##[group]Run go test ./...",
			"^[[36;1mgo test ./...^[[0m",
			"shell: bash",
			"##[endgroup]",
			"--- FAIL: "+test+" (0.00s)",
			"    wait_test.go:12: "+msg,
			"FAIL",
			"FAIL\texample.com/m\t0.01s",
			"FAIL",
			"##[error]Process completed with exit code 1.")
	}
	got := wholeLog(t, step("test (a)", "TestWait", "waited 5s, want 3s")+step("test (b)", "TestWait", "waited 7s, want 3s"))
	b := got[strings.Index(got, "== test (b) =="):]
	if !strings.Contains(b, "[lx: same failure as test (a) above;") || !strings.Contains(b, "waited 7s, want 3s") {
		t.Errorf("a line that differs was hidden behind the same-failure note:\n%s", got)
	}

	got = wholeLog(t, step("test (a)", "TestParse1", `got "a", want "b"`)+step("test (b)", "TestParse2", "nil pointer in cache layer"))
	if strings.Contains(got, "same failure") || !strings.Contains(got, "nil pointer in cache layer") {
		t.Errorf("different failures reported as the same:\n%s", got)
	}
}

func TestDebugLinesCountedErrorsKept(t *testing.T) {
	in := ghLines("deploy", "Build",
		"##[group]Run make",
		"^[[36;1mmake^[[0m",
		"shell: /usr/bin/bash -e {0}",
		"##[endgroup]",
		"##[debug]Error: cache service unavailable",
		"built") +
		ghLines("deploy", "Upload",
			"##[group]Run actions/upload-artifact@v4",
			"with:",
			"  name: dist",
			"##[endgroup]",
			"##[debug]Resolved path is /home/runner/work/x/x/dist",
			"##[debug]Error: EACCES: permission denied, open 'dist/a.js'",
			"##[error]No files were found with the provided path: dist.")
	got := wholeLog(t, in)
	for _, want := range []string{"3 ##[debug] lines hidden unless error-like", "##[debug]Error: cache service unavailable", "##[debug]Error: EACCES: permission denied", "##[error]No files were found"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Resolved path") {
		t.Errorf("plain ##[debug] line shown:\n%s", got)
	}
}

func TestUnguardedDetectorIsGuarded(t *testing.T) {
	in := ghLines("deploy", "Deploy",
		"##[group]Run ./deploy.sh",
		"^[[36;1m./deploy.sh^[[0m",
		"shell: /usr/bin/bash -e {0}",
		"##[endgroup]",
		"ZZSUMMARY",
		"uploading",
		"error: disk quota exceeded",
		"##[error]Process completed with exit code 1.")
	if got := wholeLog(t, in); !strings.Contains(got, "error: disk quota exceeded") {
		t.Errorf("an error line was dropped by a detector filter that does not guard errors:\n%s", got)
	}
}

func TestRestoreAnnotationsScales(t *testing.T) {
	var view, anns, orig []string
	for i := range 40000 {
		view = append(view, fmt.Sprintf("line %d", i))
		a := fmt.Sprintf("file%d.ts:1:1: regrouped by the tool's filter", i)
		anns, orig = append(anns, a), append(orig, "##[error]"+a)
	}
	view = append(view, "line 7")
	anns, orig = append(anns, "line 7", "line 7"), append(orig, "##[warning]line 7", "##[warning]line 7")
	start := time.Now()
	got := ci.RestoreAnnotations(view, anns, orig)
	if d := time.Since(start); d > testenv.Scale(time.Second) {
		t.Errorf("took %v", d)
	}
	if len(got) != 80001 || got[7] != "##[warning]line 7" || got[40000] != "##[warning]line 7" || got[40001] != "##[error]"+anns[0] {
		t.Errorf("annotations not restored in place: len %d, %q, %q, %q", len(got), got[7], got[40000], got[40001])
	}
}

func TestTaskFailuresFromRunnerSummaries(t *testing.T) {
	nx := ` NX   Running target test for 2 projects

   ✔  nx run utils:test (1s)

> nx run web:test

> jest

 FAIL  ./cart.test.js
  ● currency has two decimals

      at Object.toBe (cart.test.js:8:23)

Tests:       1 failed, 2 passed, 3 total

 ———————————————————————————————————————————————

 >  NX   Ran target test for 2 projects (3s)

   ✔  1/2 succeeded [0 read from cache]

   ✖  1/2 targets failed, including the following:

      - nx run web:test
`
	turbo := `• Packages in scope: web
• Running test in 1 packages
web:test: cache miss, executing 5c2a0e7f1b3d4a96
web:test: FAIL src/a.test.js
web:test: ERROR: command finished with error: command (/w/apps/web) /usr/local/bin/npm run test exited (2)

 Tasks:    0 successful, 1 total
`
	for name, in := range map[string]string{"nx": nx, "turbo 1.x": turbo} {
		got, _, _, ok := ci.RenderTasks(&engine.Context{Argv: []string{"monorepo"}, Exit: 1}, in)
		if !ok {
			t.Fatalf("%s: not recognized", name)
		}
		if !strings.Contains(got, "1 failed: web:test") || !strings.Contains(got, "✗ web:test") {
			t.Errorf("%s: failed task not marked:\n%s", name, got)
		}
	}
}

func TestLinesWithOneColumnAreKept(t *testing.T) {
	lines := []string{"##[group]Run make", "^[[36;1mmake^[[0m", "shell: bash", "##[endgroup]"}
	for i := range 20 {
		lines = append(lines, fmt.Sprintf("cc -c unit%d.c", i))
	}
	in := ghLines("build", "Test", lines...) + "build\tTest\n" + "build\terror: linker command failed\n" +
		ghLines("build", "Test", "##[error]Process completed with exit code 2.")
	if got := wholeLog(t, in); !strings.Contains(got, "error: linker command failed") {
		t.Errorf("line dropped:\n%s", got)
	}
}
