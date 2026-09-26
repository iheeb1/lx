package jstest

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

// corpusCase is a real captured output: the shared corpus
// (testdata/corpus/<cat>/) or this package's own captures
// (internal/filters/jstest/testdata/captures/). The captures were recorded
// with the corpus repos' own node_modules (jest 29 from luxon, vitest 4.1
// from ufo, mocha 11 + nyc from express; node 24, npm 11) in small scratch
// projects exercising what the corpus lacks: suites that fail to load,
// thrown errors, snapshots, console output, unhandled rejections, coverage
// thresholds, pending tests, npm wrappers. Paths are sanitized to
// /home/user/src/<project>; <name>.meta.json holds argv, exit code and a
// description.
type corpusCase struct {
	cat, name string
	filter    string // expected filter
	// pipeline is what engine.Process reports when it is not the filter:
	// "passthrough" for outputs at or under engine.SmallOutput tokens, or
	// whose view saves less than engine.DefaultMinSavings (nothing worth
	// hiding: the agent sees the output as is).
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
	{"node", "vitest-pass", "vitest", "passthrough"}, // 82 tokens
	{"node", "vitest-pass-verbose", "vitest", ""},
	{"node", "mocha-fail", "npm-test", ""},
	{"node", "mocha-pass", "npm-test", ""},
	{"node", "node-cannot-find-module", "node-crash", ""},
	{"node", "node-eaddrinuse", "node-crash", ""},

	{"captures", "jest-mixed", "jest", ""},
	{"captures", "jest-mixed-verbose", "jest", ""},
	{"captures", "jest-syntax-only", "jest", ""},
	{"captures", "jest-coverage-threshold", "jest", "passthrough"}, // 243 tokens, view saves 6%
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
	{"captures", "mocha-dot", "mocha", "passthrough"}, // dots only to hide: view saves 5%
	{"captures", "npm-run-test-cov-mocha", "npm-test", ""},
	{"captures", "vitest-coverage", "vitest", ""},
	{"captures", "vitest-coverage-threshold", "vitest", ""},
	{"captures", "jest-console-errors", "jest", ""},
	{"captures", "jest-console-single", "jest", ""},
	{"captures", "vitest-dot", "vitest", ""},
	{"captures", "vitest-snapshot", "vitest", ""},

	// Adversarial captures (review): false-pass shapes, hook failures,
	// error-looking titles, bail/abort modes, other reporters, npm
	// workspaces, node ESM crashes.
	{"captures", "jest-passlog-threshold", "jest", ""},           // exit 1, only a passing test's console.error
	{"captures", "jest-hook-fail-verbose", "jest", ""},           // beforeAll throws in describe("error handling")
	{"captures", "jest-empty-suite", "jest", ""},                 // "Your test suite must contain at least one test."
	{"captures", "jest-ci-newsnap", "jest", ""},                  // --ci: new snapshot not written
	{"captures", "jest-assertions", "jest", ""},                  // expect.assertions, done(err)
	{"captures", "jest-bail", "jest", ""},                        // --bail: "1 of 7 total"
	{"captures", "mocha-indented-console", "mocha", ""},          // indented console output is not a describe title
	{"captures", "mocha-uncaught", "mocha", ""},                  // error thrown after the test; mocha crashes after its summary
	{"captures", "mocha-forbid-only", "mocha", ""},               // Exception during run
	{"captures", "mocha-list", "mocha", ""},                      // list reporter
	{"captures", "mocha-progress", "mocha", ""},                  // progress reporter
	{"captures", "mocha-parallel", "mocha", ""},                  // --parallel
	{"captures", "mocha-min", "mocha", "normalize"},              // min reporter: nothing to hide but the \r frames
	{"captures", "node-esm-enoent", "node-crash", "passthrough"}, // file:// frames; the view saves under 10%
	{"captures", "node-esm-import-missing", "node-crash", ""},
	{"captures", "node-syntax-error", "node-crash", ""},
	{"captures", "node-unhandled-rejection", "node-crash", ""},
	{"captures", "node-long-preamble", "node-crash", ""},
	{"captures", "node-reject-string", "node-crash", "passthrough"}, // nothing to fold
	{"captures", "npm-test-workspaces", "npm-test", ""},             // two jest runs, one per workspace
	{"captures", "npm-test-silent", "npm-test", ""},                 // no "> script" echo
	{"captures", "vitest-agent-hook", "vitest", ""},
	{"captures", "vitest-bail", "vitest", ""}, // "·" tests that did not run
	{"captures", "vitest-hook-fail", "vitest", ""},
	{"captures", "vitest-passlog-threshold", "vitest", ""},
	{"captures", "vitest-process-exit", "vitest", ""},
	{"captures", "vitest-retry-timeout", "vitest", ""},
	{"captures", "vitest-soft-each", "vitest", ""},
	{"captures", "vitest-tree", "vitest", ""},
	{"captures", "vitest-verbose-hook", "vitest", ""},
	{"captures", "vitest-minimal", "vitest", ""},
	{"captures", "vitest-tree-pass", "vitest", ""},    // tree reporter: passing files list their tests and groups
	{"captures", "vitest-verbose-pass", "vitest", ""}, // verbose reporter, all passing
}

// smallCaptures are real outputs at or under engine.SmallOutput tokens
// (the pipeline only normalizes them), or outputs a filter must not claim
// to understand: ok is whether Apply accepts them.
var smallCaptures = []struct {
	name    string
	filter  string
	ok      bool
	generic bool // not small: the filter bails and the generic reducer runs
}{
	{"jest-no-tests", "jest", false, false},
	{"vitest-no-tests", "vitest", false, false},
	{"mocha-pass-small", "mocha", true, false},
	{"jest-process-exit", "jest", false, false}, // process.exit in a test: no summary
	{"mocha-after-hook", "mocha", true, false},  // after() hook fails
	{"mocha-no-files", "mocha", false, false},   // Error: No test files found
	{"vitest-basic", "vitest", false, true},     // Startup Error (unknown reporter): no summary
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

// render runs the renderer behind a filter, exposing the benign lines and
// the safety net count.
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

// libLocation reports whether a file:line location is library code: jest,
// vitest and mocha views fold node_modules and node core frames into one
// counted "… N library frames (pkg, …)" line by design (the brief's
// "fold node_modules/internal frames"), so those locations are the one
// accepted exception to LocationsMissing on failing runs.
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
			// Fidelity: every error-class line is kept, except text the
			// view identified as test/suite titles, source context, or
			// folded library frames (listed in r.benignLines() and logged here
			// for review).
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
					// Accepted: folded library frames, and where a passing
					// test called console.log (its output is hidden and
					// counted).
					if !libLocation(clean, loc) && !inLines(r.quiet, loc) {
						t.Errorf("location dropped: %s", loc)
					}
				}
				if strings.Count(got, "\n") > 0 && !failLooking(got) {
					t.Errorf("exit %d but the view shows no failure", c.Exit)
				}
			}

			// Full pipeline.
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

// failLooking reports whether a view shows a failure an agent cannot miss.
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

// TestMatchSpecificity: across the whole shared corpus, these filters claim
// exactly the fixtures in scope.
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
