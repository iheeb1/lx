package ci_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	_ "github.com/iheeb1/lx/internal/filters"
	"github.com/iheeb1/lx/internal/filters/ci"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

type corpusCase struct {
	name    string
	filter  string
	process string
	why     string

	drops    []string
	dropsWhy string
}

const guardNote = "[lx: error lines from the full output]"

var corpusCases = []corpusCase{
	{name: "gh-log-failed-astro", filter: "gh-run-log"},
	{name: "gh-log-failed-axum", filter: "gh-run-log"},
	{name: "gh-log-failed-bat", filter: "gh-run-log"},
	{name: "gh-log-failed-hugo", filter: "gh-run-log"},
	{name: "gh-log-failed-jest", filter: "gh-run-log"},
	{name: "gh-log-failed-prometheus", filter: "gh-run-log"},
	{name: "gh-log-failed-pytest-matrix", filter: "gh-run-log",
		drops:    []string{"encoding = None, errors = None", "def read_text(self, encoding=None, errors=None):"},
		dropsWhy: "the pytest filter folds the stdlib pathlib.read_text frame of the traceback; its location line is kept"},
	{name: "gh-log-failed-testify", filter: "gh-run-log"},
	{name: "gh-log-failed-turborepo", filter: "gh-run-log"},
	{name: "gh-log-failed-typescript-eslint", filter: "gh-run-log"},
	{name: "gh-log-failed-viper", filter: "gh-run-log"},
	{name: "gh-log-failed-vite", filter: "gh-run-log"},
	{name: "gh-log-job-vite", filter: "gh-run-log"},
	{name: "gh-log-job-hugo-pass", filter: "gh-run-log"},
	{name: "gh-api-job-logs-jest", filter: "gh-run-log"},
	{name: "gh-run-view-pytest", filter: "gh-run-view"},
	{name: "gh-run-view-vite", filter: "gh-run-view"},
	{name: "gh-run-watch-ruff", filter: "gh-run-watch"},
	{name: "gh-run-watch-astro", filter: "gh-run-watch"},
	{name: "gh-pr-checks-pytest", filter: "gh-pr-checks"},
	{name: "gh-pr-checks-vite", filter: "gh-pr-checks"},

	{name: "turbo-run-test", filter: "monorepo", process: "detected:monorepo"},
	{name: "turbo-run-build-test", filter: "monorepo", process: "detected:monorepo"},
	{name: "nx-run-many", filter: "monorepo", process: "detected:monorepo"},
	{name: "nx-run-many-stream", filter: "monorepo", process: "detected:monorepo"},
	{name: "nx-run-many-build-test", filter: "monorepo", process: "detected:monorepo"},
	{name: "lerna-run-test", filter: "monorepo", process: "detected:monorepo"},
	{name: "lerna-run-test-stream", filter: "monorepo", process: "detected:monorepo"},
	{name: "compose-up-tests", filter: "monorepo", process: "detected:monorepo"},
}

func loadCase(t testing.TB, name string) fixture.Case {
	t.Helper()
	c, err := fixture.Read("testdata", "ci", name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCorpusCoversEveryCapture(t *testing.T) {
	have := map[string]bool{}
	for _, cc := range corpusCases {
		have[cc.name] = true
	}
	local, err := fixture.ReadAll("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range local {
		if !have[c.Name] {
			t.Errorf("capture %s/%s has no corpus case", c.Category, c.Name)
		}
	}
}

func view(t *testing.T, cc corpusCase, fc fixture.Case) string {
	t.Helper()
	c := fc.Context()
	clean := fc.Clean()
	if cc.filter == "monorepo" {
		if f := engine.Find(c); f != nil {
			t.Fatalf("filter %s claims %q", f.Name(), c.Argv)
		}
		out, name, ok := engine.Detect(c, clean)
		if !ok || name != "monorepo" {
			t.Fatalf("detected %q (ok=%v), want monorepo", name, ok)
		}
		return out
	}
	f := engine.Find(c)
	if f == nil || f.Name() != cc.filter {
		t.Fatalf("filter = %v, want %s", f, cc.filter)
	}
	got, ok := f.Apply(c, clean)
	if !ok {
		t.Fatal("filter bailed")
	}
	if again, _ := f.Apply(c, clean); again != got {
		t.Fatal("Apply is not deterministic")
	}
	return got
}

func TestCorpus(t *testing.T) {
	var table strings.Builder
	for _, cc := range corpusCases {
		t.Run(cc.name, func(t *testing.T) {
			fc := loadCase(t, cc.name)
			got := view(t, cc, fc)
			fixture.Golden(t, "ci", cc.name, got)
			if strings.Contains(got, guardNote) {
				t.Errorf("the view lost error lines; the guard re-added them:\n%s", got[strings.Index(got, guardNote):])
			}
			switch cc.filter {
			case "gh-run-log":
				logFidelity(t, cc, fc, got)
			case "monorepo":
				tasksFidelity(t, fc, got)
			default:
				if m := fixture.ErrorLinesMissing(fc.Clean(), got); len(m) > 0 {
					t.Errorf("%d error lines missing, e.g. %q", len(m), m[0])
				}
				for _, ln := range strings.Split(fc.Clean(), "\n") {
					if strings.HasPrefix(ln, "X ") || strings.HasPrefix(ln, "  X ") || strings.Contains(ln, "\tfail\t") {
						if !strings.Contains(got, ln) {
							t.Errorf("failure row dropped: %q", ln)
						}
					}
				}
			}
			res := engine.Process(fc.Context(), fc.Raw, engine.Options{})
			want := cc.process
			if want == "" {
				want = cc.filter
			}
			if res.Filter != want {
				t.Errorf("pipeline filter = %s, want %s (%s)", res.Filter, want, cc.why)
			}
			raw, out := tokens.Count(fc.Raw), res.OutTokens
			line := fmt.Sprintf("%-32s %8d → %6d tokens %6.1f%%  %s", cc.name, raw, out, 100*(1-float64(out)/float64(max(raw, 1))), res.Filter)
			t.Log(line)
			table.WriteString(line + "\n")
		})
	}
	t.Log("\n" + table.String())
}

func logFidelity(t *testing.T, cc corpusCase, fc fixture.Case, got string) {
	t.Helper()
	out, texts, _, failed, ok := ci.RenderLog(fc.Context(), fc.Clean())
	if !ok {
		t.Fatal("RenderLog failed")
	}
	if out != got {
		t.Error("Apply differs from the rendered view: the self-guard added lines")
	}
	flat := squashAll(got)
	for _, tx := range texts {
		if strings.HasPrefix(tx, "##[error]") && !strings.Contains(flat, squash(tx)) {
			t.Errorf("annotation dropped: %q", tx)
		}
	}
	sections := jobSections(got)
	for _, f := range failed {
		sec := sections[f.Job]
		if f.Job == "" {
			sec = got
		}
		if !strings.Contains(sec, "✗ "+f.Label) {
			t.Errorf("job %q: failed step %q has no header", f.Job, f.Label)
		}
		for _, a := range f.Trailer {
			if !strings.Contains(sec, a) {
				t.Errorf("job %q: %q not shown in the job's section", f.Job, a)
			}
		}
		if f.Deduped {
			continue
		}
		if m := engine.AppLocationsMissing(errorLines(f.Body), got); len(m) > 0 {
			t.Errorf("job %q step %q lost locations of error lines: %v", f.Job, f.Label, m)
		}
		for _, m := range engine.ErrorMessagesMissing(f.Body, got) {
			if !slices.Contains(cc.drops, m) {
				t.Errorf("job %q step %q lost error message %q", f.Job, f.Label, m)
			}
		}
	}
}

func tasksFidelity(t *testing.T, fc fixture.Case, got string) {
	t.Helper()
	out, texts, _, ok := ci.RenderTasks(fc.Context(), fc.Clean())
	if !ok {
		t.Fatal("RenderTasks failed")
	}
	if !strings.HasSuffix(got, out) && got != out {
		t.Error("detector output differs from the rendered view: the self-guard added lines")
	}
	all := strings.Join(texts, "\n")
	if m := engine.AppLocationsMissing(all, got); len(m) > 0 {
		t.Errorf("lost locations: %v", m)
	}
	for _, m := range engine.ErrorMessagesMissing(all, got) {
		if !jestTitle(m, got) {
			t.Errorf("error message missing: %q", m)
		}
	}
}

// jest drops "✕ name (N ms)" when the "● name" block is shown
func jestTitle(line, got string) bool {
	name, ok := strings.CutPrefix(line, "✕ ")
	if !ok {
		return false
	}
	if i := strings.LastIndex(name, " ("); i > 0 {
		name = name[:i]
	}
	return strings.Contains(got, "● "+name)
}

func errorLines(s string) string {
	var b strings.Builder
	for _, ln := range strings.Split(s, "\n") {
		if engine.IsError(ln) {
			b.WriteString(ln)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func jobSections(got string) map[string]string {
	out := map[string]string{}
	name := ""
	var b strings.Builder
	for _, ln := range strings.Split(got, "\n") {
		if strings.HasPrefix(ln, "== ") && strings.HasSuffix(ln, " ==") {
			if name != "" {
				out[name] += b.String()
			}
			name = strings.TrimSuffix(strings.TrimPrefix(ln, "== "), " ==")
			b.Reset()
			continue
		}
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	if name != "" {
		out[name] += b.String()
	}
	return out
}

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

func squashAll(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = squash(ln)
	}
	return strings.Join(lines, "\n")
}
