package ci_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/filters/ci"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/textutil"
)

func TestMatch(t *testing.T) {
	for cmd, want := range map[string]string{
		"gh run view 123 --log-failed":              "gh-run-log",
		"gh run view --log --job 456":               "gh-run-log",
		"gh -R cli/cli run view 1 --log":            "gh-run-log",
		"gh run view 1 --repo cli/cli --log-failed": "gh-run-log",
		"gh.exe run view 1 --log":                   "gh-run-log",
		"gh api repos/o/r/actions/jobs/123/logs":    "gh-run-log",
		"gh api /repos/o/r/actions/jobs/123/logs":   "gh-run-log",
		"gh run view 1":                             "gh-run-view",
		"gh run view 1 -v":                          "gh-run-view",
		"gh run view --job 456":                     "gh-run-view",
		"gh run watch 1":                            "gh-run-watch",
		"gh run watch 1 --exit-status --interval 5": "gh-run-watch",
		"gh pr checks":                              "gh-pr-checks",
		"gh pr checks 12 --repo o/r":                "gh-pr-checks",
		"gh run view 1 --json jobs":                 "",
		"gh run view 1 --log --jq .":                "",
		"gh run view 1 --web":                       "",
		"gh run view 1 -w":                          "",
		"gh run view 1 -t '{{.name}}'":              "",
		"gh pr checks --json name,state":            "",
		"gh pr checks -w":                           "",
		"gh pr checks --watch":                      "",
		"gh api repos/o/r/actions/runs/1":           "",
		"gh api repos/o/r/actions/runs/1/logs":      "",
		"gh run list":                               "",
		"gh pr view 1":                              "",
		"gh run rerun 1 --failed":                   "",
		"git run view 1 --log":                      "",
		"gh workflow run ci.yml":                    "",
	} {
		f := engine.Find(&engine.Context{Argv: strings.Fields(cmd)})
		got := ""
		if f != nil {
			got = f.Name()
		}
		if got != want && (want != "" || ours[got]) {
			t.Errorf("%s: filter %q, want %q", cmd, got, want)
		}
	}
}

func TestStripCaret(t *testing.T) {
	for in, want := range map[string]string{
		"^[[36;1mpnpm format^[[0m":        "pnpm format",
		"^[[1m^[[31merror^[[0m: boom":     "error: boom",
		"done^[[K":                        "done",
		"grep '^[[:space:]]*#' file":      "grep '^[[:space:]]*#' file",
		"^[[A-Z]":                         "^[[A-Z]",
		"^[[0-9]+":                        "^[[0-9]+",
		"plain":                           "plain",
		"^[[38;5;12m175^[[0m ^[[1m|^[[0m": "175 |",
	} {
		if got := ci.StripCaret(in); got != want {
			t.Errorf("StripCaret(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCutStamp(t *testing.T) {
	for _, tc := range []struct {
		in, ts, rest string
		ok           bool
	}{
		{"2026-09-28T12:20:55.0849543Z ##[group]Run x", "2026-09-28T12:20:55.0849543Z", "##[group]Run x", true},
		{"\ufeff2026-09-28T12:20:55.0849543Z Current runner version: '2.337.0'", "2026-09-28T12:20:55.0849543Z", "Current runner version: '2.337.0'", true},
		{"2026-09-28T12:20:55Z text", "2026-09-28T12:20:55Z", "text", true},
		{"2026-09-28T12:20:55.1Z", "2026-09-28T12:20:55.1Z", "", true},
		{"2026-09-28T12:20:55.1Ztext", "", "", false},
		{"2026-09-28 12:20:55.1Z text", "", "", false},
		{"2026-09-28T12:20:55+00:00 text", "", "", false},
		{"  \"build\": {", "", "", false},
		{"", "", "", false},
	} {
		ts, rest, ok := ci.CutStamp(tc.in)
		if ts != tc.ts || rest != tc.rest || ok != tc.ok {
			t.Errorf("CutStamp(%q) = %q, %q, %v", tc.in, ts, rest, ok)
		}
	}
}

func TestShellWords(t *testing.T) {
	for in, want := range map[string]string{
		"go test -v -tags 'viper_finder' -shuffle=on ./...":  "go|test|-v|-tags|viper_finder|-shuffle=on|./...",
		`pytest -k "not slow" tests/`:                        "pytest|-k|not slow|tests/",
		"cargo +1.80 test --locked":                          "cargo|+1.80|test|--locked",
		"npx turbo run test --filter=@acme/web":              "npx|turbo|run|test|--filter=@acme/web",
		"api#test":                                           "api#test",
		"pnpm format && git diff --exit-code":                "",
		"echo $HOME":                                         "",
		`echo "$HOME"`:                                       "",
		"ls *.go":                                            "",
		"tox run -e py311 --installpkg `find dist/*.tar.gz`": "",
		"# comment":                                          "",
		"a 'unterminated":                                    "",
		"":                                                   "",
	} {
		w, ok := ci.ShellWords(in)
		got := ""
		if ok {
			got = strings.Join(w, "|")
		}
		if got != want {
			t.Errorf("ShellWords(%q) = %q, want %q", in, got, want)
		}
	}
}

func ghLines(job, step string, lines ...string) string {
	var b strings.Builder
	for i, l := range lines {
		b.WriteString(job + "\t" + step + "\t")
		if i == 0 {
			b.WriteString("\ufeff")
		}
		b.WriteString("2026-09-28T10:00:0" + string(rune('0'+i%10)) + ".1000000Z " + l + "\n")
	}
	return b.String()
}

func runLog(t *testing.T, in string) string {
	t.Helper()
	c := &engine.Context{Argv: []string{"gh", "run", "view", "1", "--log-failed"}, Cwd: "/home/user/src/x", Home: "/home/user"}
	out, ok := engine.Find(c).Apply(c, in)
	if !ok {
		t.Fatalf("bailed on:\n%s", in)
	}
	return out
}

func TestAnnotationsKeptInPlace(t *testing.T) {
	in := ghLines("build", "Run go vet ./...",
		"##[group]Run go vet ./...",
		"^[[36;1mgo vet ./...^[[0m",
		"shell: /usr/bin/bash -e {0}",
		"##[endgroup]",
		"# example.com/m",
		"##[error]m.go:3:2: undefined: x",
		"##[error]Process completed with exit code 1.",
		"Node 20 is being deprecated.")
	got := runLog(t, in)
	if strings.Count(got, "##[error]m.go:3:2: undefined: x") != 1 {
		t.Errorf("problem-matcher annotation not kept exactly once:\n%s", got)
	}
	if !strings.Contains(got, "##[error]Process completed with exit code 1.\nNode 20 is being deprecated.") {
		t.Errorf("exit line not moved after the step's output:\n%s", got)
	}
	if strings.Contains(got, "shell: ") || strings.Contains(got, "\n##[group]") || strings.Contains(got, "2026-09-28T") {
		t.Errorf("header, markers or timestamps leaked:\n%s", got)
	}
}

func TestUnknownStepSegmentation(t *testing.T) {
	in := ghLines("test", "UNKNOWN STEP",
		"Current runner version: '2.337.0'",
		"Complete job name: test",
		"##[group]Run actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1",
		"with:",
		"  token: ***",
		"##[endgroup]",
		"Syncing repository: o/r",
		"##[start-action display=Install deps;id=__self.__run]",
		"##[group]Run npm ci",
		"^[[36;1mnpm ci^[[0m",
		"shell: /usr/bin/bash -e {0}",
		"##[endgroup]",
		"added 12 packages",
		"##[end-action id=__self.__run;outcome=success;conclusion=success;duration_ms=100]",
		"##[start-action display=Skipped one;id=__self.__run2]",
		"##[end-action id=__self.__run2;outcome=skipped;conclusion=skipped;duration_ms=0]",
		"##[group]Run make test",
		"^[[36;1mmake test^[[0m",
		"shell: /usr/bin/bash -e {0}",
		"env:",
		"  CI_FAIL_ON_ERROR: true",
		"##[endgroup]",
		"go test ./...",
		"--- FAIL: TestX (0.00s)",
		"    x_test.go:9: boom",
		"FAIL",
		"make: *** [Makefile:3: test] Error 1",
		"##[error]Process completed with exit code 2.",
		"Post job cleanup.",
		"[command]/usr/bin/git version",
		"Cleaning up orphan processes")
	got := runLog(t, in)
	for _, want := range []string{
		"✗ Run make test",
		"[lx: 4 steps folded",
		"Set up job · actions/checkout · Install deps · Skipped one",
		"x_test.go:9: boom",
		"make: *** [Makefile:3: test] Error 1",
		"##[error]Process completed with exit code 2.",
		"Post job cleanup · Complete job",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "CI_FAIL_ON_ERROR") || strings.Contains(got, "token: ***") {
		t.Errorf("step env/with block leaked:\n%s", got)
	}
}

func TestContinuationLinesStayInStep(t *testing.T) {
	in := ghLines("check", "UNKNOWN STEP",
		"##[group]Run exit 1",
		"^[[36;1mexit 1^[[0m",
		"shell: /usr/bin/bash -e {0}",
		"env:",
		"  INPUT_JOBS: {") +
		"check\tUNKNOWN STEP\t  \"build\": \"failure\"\n" +
		"check\tUNKNOWN STEP\t}\n" +
		ghLines("check", "UNKNOWN STEP", "##[endgroup]", "##[error]Process completed with exit code 1.")
	got := runLog(t, in)
	if strings.Contains(got, "UNKNOWN STEP") || strings.Contains(got, `"build": "failure"`) {
		t.Errorf("continuation lines leaked as stray lines:\n%s", got)
	}
}

func TestRawJobLog(t *testing.T) {
	var b strings.Builder
	for _, l := range []string{
		"\ufeff2026-09-28T10:00:00.0000000Z Current runner version: '2.337.0'",
		"2026-09-28T10:00:01.0000000Z ##[group]Run cargo test",
		"2026-09-28T10:00:01.0000000Z ^[[36;1mcargo test^[[0m",
		"2026-09-28T10:00:01.0000000Z shell: /usr/bin/bash -e {0}",
		"2026-09-28T10:00:01.0000000Z ##[endgroup]",
		"2026-09-28T10:00:02.0000000Z error[E0425]: cannot find value `x` in this scope",
		"2026-09-28T10:00:02.0000000Z  --> src/lib.rs:3:5",
		"2026-09-28T10:00:03.0000000Z error: could not compile `m` (lib) due to 1 previous error",
		"2026-09-28T10:00:03.5000000Z ##[error]Process completed with exit code 101.",
	} {
		b.WriteString(l + "\n")
	}
	c := &engine.Context{Argv: []string{"gh", "api", "repos/o/r/actions/jobs/1/logs"}}
	got, ok := engine.Find(c).Apply(c, b.String())
	if !ok {
		t.Fatal("bailed on a raw job log")
	}
	for _, want := range []string{"✗ Run cargo test · 2.5s", "src/lib.rs:3:5", "##[error]Process completed with exit code 101."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "== ") {
		t.Errorf("a single raw log has no job name to show:\n%s", got)
	}
}

func TestMatrixDedupKeepsDifferingErrorLines(t *testing.T) {
	step := func(job, root string) string {
		return ghLines(job, "Test",
			"##[group]Run go test ./...",
			"^[[36;1mgo test ./...^[[0m",
			"shell: bash",
			"##[endgroup]",
			"--- FAIL: TestPath (0.00s)",
			"    path_test.go:12: open "+root+"testdata/x: no such file or directory",
			"FAIL",
			"FAIL\texample.com/m\t0.01s",
			"FAIL",
			"##[error]Process completed with exit code 1.")
	}
	got := runLog(t, step("test (ubuntu)", "/home/runner/work/m/m/")+step("test (windows)", `D:\a\m\m\`))
	if !strings.Contains(got, "[lx: same failure as test (ubuntu) above;") {
		t.Fatalf("second job not deduplicated:\n%s", got)
	}
	if !strings.Contains(got, `D:\a\m\m\testdata/x`) {
		t.Errorf("windows error line dropped from the deduplicated job:\n%s", got)
	}
	if strings.Count(got, "##[error]Process completed with exit code 1.") != 2 {
		t.Errorf("each job keeps its exit line:\n%s", got)
	}
}

func TestNotALog(t *testing.T) {
	c := &engine.Context{Argv: []string{"gh", "run", "view", "1", "--log"}}
	for _, in := range []string{
		"failed to get run log: log not found",
		"run or job ID required when not running interactively\n\nUsage:  gh run view [<run-id>] [flags]",
		"2026-09-28T10:00:00Z one\nplain\nplain\nplain\nplain",
	} {
		if _, ok := engine.Find(c).Apply(c, in); ok {
			t.Errorf("claimed %q", in)
		}
	}
}

func TestTasksNotDetected(t *testing.T) {
	prefix := func(p string, lines ...string) string {
		for i := range lines {
			lines[i] = p + lines[i]
		}
		return strings.Join(lines, "\n")
	}
	for name, in := range map[string]string{
		"one turbo prefix, no framing":  prefix("web:test: ", "PASS a.test.js", "Tests: 1 passed", "Ran all test suites."),
		"grep -n":                       "src/a.go:12: foo\nsrc/a.go:13: bar\nsrc/b.go:1: baz",
		"compose logs without framing":  prefix("db-1  | ", "ready", "ready") + "\n" + prefix("web-1 | ", "GET /", "GET /x"),
		"pnpm frame, no prefixed lines": "Scope: 2 of 3 workspace projects\nDone",
		"nx frame, no tasks":            " NX   Running target test for 0 projects\n",
		"python dict repr":              "config: {'a': 1}\nmodule:attr: value\n",
	} {
		if ci.StyleOf(in) != "" {
			if _, _, _, ok := ci.RenderTasks(&engine.Context{Argv: []string{"monorepo"}}, in); ok {
				t.Errorf("%s: detected as monorepo output", name)
			}
		}
		for _, d := range engine.Detectors() {
			if d.Name == "monorepo" && d.Detect(in) {
				t.Errorf("%s: monorepo detector fired", name)
			}
		}
	}
}

func TestSummaryMergesAnnotations(t *testing.T) {
	in := `
X main CI o/r#1 · 42
Triggered via push about 1 minute ago

JOBS
X a in 1m (ID 1)
  ✓ Set up job
  X Run tests
  - Upload
✓ b in 1m (ID 2)
  ✓ Set up job

ANNOTATIONS
X Process completed with exit code 1.
a: .github#10

! Node.js 20 is deprecated.
a: .github#1

! Node.js 20 is deprecated.
b: .github#1

To see what failed, try: gh run view 42 --log-failed`
	c := &engine.Context{Argv: []string{"gh", "run", "view", "42"}}
	got, ok := engine.Find(c).Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{"X a in 1m (ID 1)\n  X Run tests\n", "! Node.js 20 is deprecated. [×2]\n  a: .github#1 · b: .github#1", "X Process completed with exit code 1.\na: .github#10", "[lx: 3 step lines not shown (passed or skipped)]", "To see what failed, try: gh run view 42 --log-failed"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestWatchShowsLatestRefresh(t *testing.T) {
	snap := func(state, step string) string {
		return "Refreshing run status every 3 seconds. Press Ctrl+C to quit.\n\n" + state + " main CI · 42\nTriggered via push about 1 minute ago\n\nJOBS\n" + state + " a (ID 1)\n  " + step + " Run tests\n"
	}
	in := snap("*", "*") + snap("*", "*") + snap("X", "X") + "\nX Run CI (42) completed with 'failure'"
	c := &engine.Context{Argv: []string{"gh", "run", "watch", "42", "--exit-status"}, Exit: 1}
	got, ok := engine.Find(c).Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	if strings.Count(got, "main CI · 42") != 1 || !strings.Contains(got, "X a (ID 1)\n  X Run tests") || !strings.Contains(got, "completed with 'failure'") {
		t.Errorf("want only the latest refresh and the verdict:\n%s", got)
	}
	if !strings.HasPrefix(got, "[lx: 3 status refreshes; the latest is shown]") {
		t.Errorf("missing refresh note:\n%s", got)
	}
}

func TestPRChecksKeepsFailuresAndErrorLikeNames(t *testing.T) {
	in := "lint\tpass\t10s\thttps://x/1\t\ntest (ubuntu)\tfail\t1m\thttps://x/2\t\nError budget\tpass\t1s\thttps://x/3\t\ndeploy\tpending\t0\thttps://x/4\t\ndocs\tskipping\t0\thttps://x/5\t"
	c := &engine.Context{Argv: []string{"gh", "pr", "checks"}, Exit: 1}
	got, ok := engine.Find(c).Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{"test (ubuntu)\tfail\t1m\thttps://x/2", "deploy\tpending", "[lx: 1 passing check: lint]", "[lx: 1 skipped check: docs]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if m := engine.MissingErrorLines(in, got); len(m) > 0 {
		t.Errorf("lost %q", m)
	}
	if _, ok := engine.Find(c).Apply(c, "no checks reported on the 'main' branch"); ok {
		t.Error("claimed a message with no rows")
	}
}

func TestPnpmRecursiveRegrouped(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "pnpm", "pnpm-r-no-bail-test.log"))
	if err != nil {
		t.Fatal(err)
	}
	in := textutil.Clean(string(b))
	c := &engine.Context{Argv: []string{"monorepo"}, Exit: 1, Cwd: "/home/user/src/acme", Home: "/home/user"}
	got, texts, _, ok := ci.RenderTasks(c, in)
	if !ok {
		t.Fatal("not recognized")
	}
	fixture.Golden(t, "ci", "pnpm-r-no-bail-test", got)
	for _, want := range []string{
		"[lx: 3 tasks (pnpm), 2 failed: packages/api test, packages/web test",
		"✗ packages/api test · go test ./...",
		"price_test.go:18: Slug(\"Red Shoes\") = \"Red_Shoes\", want \"red-shoes\"",
		"✗ packages/web test · jest",
		"at Object.toBe (cart.test.js:8:23)",
		"web@0.1.0 test: `jest`",
		"Summary: 2 fails, 1 passes",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	all := strings.Join(texts, "\n")
	for _, m := range engine.ErrorMessagesMissing(all, got) {
		if !jestTitle(m, got) {
			t.Errorf("error message missing: %q", m)
		}
	}
}
