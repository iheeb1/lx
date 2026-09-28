package jstest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

type corpusCase struct {
	cat, name string
	filter    string

	pipeline string
}

var corpus = []corpusCase{
	{"node", "jest-fail", "jest", ""},
	{"node", "jest-fail-color", "jest", ""},
	{"node", "jest-fail-timezone", "jest", ""},
	{"node", "jest-pass", "jest", ""},
	{"node", "jest-pass-verbose", "jest", ""},
	{"node", "npm-test-jest-coverage-fail", "npm-test", ""},
	{"node", "vitest-fail", "vitest", ""},
	{"node", "vitest-fail-color", "vitest", ""},
	{"node", "vitest-fail-verbose", "vitest", ""},
	{"node", "vitest-pass", "vitest", "passthrough"},
	{"node", "vitest-pass-verbose", "vitest", ""},
	{"node", "mocha-fail", "npm-test", ""},
	{"node", "mocha-pass", "npm-test", ""},
	{"node", "node-cannot-find-module", "node-crash", ""},
	{"node", "node-eaddrinuse", "node-crash", ""},

	{"captures", "jest-mixed", "jest", ""},
	{"captures", "jest-mixed-verbose", "jest", ""},
	{"captures", "jest-syntax-only", "jest", ""},
	{"captures", "jest-coverage-threshold", "jest", "passthrough"},
	{"captures", "npm-test-jest-mixed", "npm-test", ""},
	{"captures", "npm-run-test-jest-mixed", "npm-test", ""},
	{"captures", "vitest-mixed", "vitest", ""},
	{"captures", "vitest-mixed-agent", "vitest", ""},
	{"captures", "vitest-mixed-verbose", "vitest", ""},
	{"captures", "vitest-load-fail-only", "vitest", ""},
	{"captures", "vitest-import-fail-only", "vitest", ""},
	{"captures", "vitest-unhandled-only", "vitest", ""},
	{"captures", "npm-test-vitest-mixed", "npm-test", ""},
	{"captures", "mocha-mixed", "mocha", ""},
	{"captures", "mocha-load-error", "mocha", ""},
	{"captures", "npm-test-mocha-mixed", "npm-test", ""},
	{"captures", "mocha-dot", "mocha", "passthrough"},
	{"captures", "npm-run-test-cov-mocha", "npm-test", ""},
	{"captures", "vitest-coverage", "vitest", ""},
	{"captures", "vitest-coverage-threshold", "vitest", ""},
	{"captures", "jest-console-errors", "jest", ""},
	{"captures", "jest-console-single", "jest", ""},
	{"captures", "vitest-dot", "vitest", ""},
	{"captures", "vitest-snapshot", "vitest", ""},

	{"captures", "jest-passlog-threshold", "jest", ""},
	{"captures", "jest-hook-fail-verbose", "jest", ""},
	{"captures", "jest-empty-suite", "jest", ""},
	{"captures", "jest-ci-newsnap", "jest", ""},
	{"captures", "jest-assertions", "jest", ""},
	{"captures", "jest-bail", "jest", ""},
	{"captures", "mocha-indented-console", "mocha", ""},
	{"captures", "mocha-uncaught", "mocha", ""},
	{"captures", "mocha-forbid-only", "mocha", ""},
	{"captures", "mocha-list", "mocha", ""},
	{"captures", "mocha-progress", "mocha", ""},
	{"captures", "mocha-parallel", "mocha", ""},
	{"captures", "mocha-min", "mocha", "normalize"},
	{"captures", "node-esm-enoent", "node-crash", "passthrough"},
	{"captures", "node-esm-import-missing", "node-crash", ""},
	{"captures", "node-syntax-error", "node-crash", ""},
	{"captures", "node-unhandled-rejection", "node-crash", ""},
	{"captures", "node-long-preamble", "node-crash", ""},
	{"captures", "node-reject-string", "node-crash", "passthrough"},
	{"captures", "npm-test-workspaces", "npm-test", ""},
	{"captures", "npm-test-silent", "npm-test", ""},
	{"captures", "vitest-agent-hook", "vitest", ""},
	{"captures", "vitest-bail", "vitest", ""},
	{"captures", "vitest-hook-fail", "vitest", ""},
	{"captures", "vitest-passlog-threshold", "vitest", ""},
	{"captures", "vitest-process-exit", "vitest", ""},
	{"captures", "vitest-retry-timeout", "vitest", ""},
	{"captures", "vitest-soft-each", "vitest", ""},
	{"captures", "vitest-tree", "vitest", ""},
	{"captures", "vitest-verbose-hook", "vitest", ""},
	{"captures", "vitest-minimal", "vitest", ""},
	{"captures", "vitest-tree-pass", "vitest", ""},
	{"captures", "vitest-verbose-pass", "vitest", ""},
}

var smallCaptures = []struct {
	name    string
	filter  string
	ok      bool
	generic bool
}{
	{"jest-no-tests", "jest", false, false},
	{"vitest-no-tests", "vitest", false, false},
	{"mocha-pass-small", "mocha", true, false},
	{"jest-process-exit", "jest", false, false},
	{"mocha-after-hook", "mocha", true, false},
	{"mocha-no-files", "mocha", false, false},
	{"vitest-basic", "vitest", false, true},
}

func load(t testing.TB, cc corpusCase) fixture.Case {
	t.Helper()
	if cc.cat == "captures" {
		fc, err := fixture.Read("testdata", "captures", cc.name)
		if err != nil {
			t.Fatal(err)
		}
		return fc
	}
	return fixture.Load(t, cc.cat, cc.name)
}

func render(f engine.Filter, c *engine.Context, clean string) (result, bool) {
	switch f.(type) {
	case jestFilter:
		return renderJest(c, clean)
	case vitestFilter:
		return renderVitest(c, clean)
	case mochaFilter:
		return renderMocha(c, clean)
	case npmTestFilter:
		return renderScript(c, clean)
	}
	out, ok := f.Apply(c, clean)
	return result{out: out}, ok
}

func libLocation(clean, loc string) bool {
	for _, ln := range strings.Split(clean, "\n") {
		if strings.Contains(ln, loc) && isFrame(ln) {
			if _, lib := libRoot(ln); lib {
				return true
			}
		}
	}
	return false
}

func TestCorpus(t *testing.T) {
	for _, cc := range corpus {
		t.Run(cc.name, func(t *testing.T) {
			fc := load(t, cc)
			c := fc.Context()
			clean := fc.Clean()
			f := engine.Find(c)
			if f == nil || f.Name() != cc.filter {
				t.Fatalf("filter = %v, want %s", f, cc.filter)
			}
			got, ok := f.Apply(c, clean)
			if !ok {
				t.Fatal("filter bailed on real output")
			}
			fixture.Golden(t, "jstest", cc.name, got)

			r, _ := render(f, c, clean)
			if r.out != got {
				t.Fatal("render and Apply disagree")
			}
			if r.readded > 0 {
				t.Errorf("safety net re-added %d error lines:\n%s", r.readded, got[strings.Index(got, "[lx: error lines"):])
			}

			benign := map[string]bool{}
			for _, b := range r.benignLines() {
				benign[b] = true
			}
			for _, m := range fixture.ErrorLinesMissing(clean, got) {
				if !benign[m] {
					t.Errorf("error line dropped: %q", m)
				}
			}
			if len(r.benignLines()) > 0 {
				t.Logf("benign error-looking lines dropped: %q", r.benignLines())
			}
			if c.Exit != 0 {
				for _, loc := range fixture.LocationsMissing(clean, got) {
					if !libLocation(clean, loc) && !inLines(r.quiet, loc) {
						t.Errorf("location dropped: %s", loc)
					}
				}
				if strings.Count(got, "\n") > 0 && !failLooking(got) {
					t.Errorf("exit %d but the view shows no failure", c.Exit)
				}
			}

			res := engine.Process(c, fc.Raw, engine.Options{})
			if res.GuardAdded != 0 {
				t.Errorf("engine guard re-added %d lines", res.GuardAdded)
			}
			want := cc.filter
			if cc.pipeline != "" {
				want = cc.pipeline
			}
			if res.Filter != want {
				t.Errorf("pipeline used %s, want %s", res.Filter, want)
			}
			raw, outTok := tokens.Count(fc.Raw), tokens.Count(got)
			t.Logf("savings %-32s %6d → %5d tokens (%5.1f%%) pipeline=%s %d→%d", cc.name, raw, outTok,
				100*(1-float64(outTok)/float64(max(raw, 1))), res.Filter, res.RawTokens, res.OutTokens)
		})
	}
}

func inLines(lines []string, s string) bool {
	for _, ln := range lines {
		if strings.Contains(ln, s) {
			return true
		}
	}
	return false
}

func failLooking(s string) bool {
	for _, ln := range strings.Split(s, "\n") {
		if engine.IsError(ln) {
			return true
		}
	}
	return false
}

func TestCapturesListed(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "captures", "*.txt"))
	listed := map[string]bool{}
	for _, cc := range corpus {
		listed[cc.name] = true
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".txt")
		small := false
		for _, sc := range smallCaptures {
			small = small || sc.name == name
		}
		if !listed[name] && !small {
			t.Errorf("capture %s is not in the corpus table", name)
		}
	}
}

func TestMatchSpecificity(t *testing.T) {
	mine := map[string]bool{"jest": true, "vitest": true, "mocha": true, "npm-test": true, "node-crash": true}
	listed := map[string]bool{}
	for _, cc := range corpus {
		if cc.cat != "captures" {
			listed[cc.cat+"/"+cc.name] = true
		}
	}
	for _, fc := range fixture.All(t) {
		name := fc.Category + "/" + fc.Name
		f := engine.Find(fc.Context())
		claimed := f != nil && mine[f.Name()]
		if claimed != listed[name] {
			t.Errorf("%s: claimed=%v (filter %v), in scope=%v", name, claimed, f, listed[name])
		}
	}
}
