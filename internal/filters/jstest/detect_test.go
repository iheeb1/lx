package jstest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
)

type detectCase struct {
	fixture.Case
	tool string
}

func loadDetectCases(t *testing.T) []detectCase {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join("testdata", "detect", "*.txt"))
	var out []detectCase
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".txt")
		c, err := fixture.Read("testdata", "detect", name)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join("testdata", "detect", name+".meta.json"))
		if err != nil {
			t.Fatal(err)
		}
		var m struct{ Tool string }
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		tool, _, _ := strings.Cut(m.Tool, ",")
		out = append(out, detectCase{c, tool})
	}
	if len(out) < 10 {
		t.Fatalf("only %d detect captures", len(out))
	}
	return out
}

var jsDetectors = map[string]struct {
	detect func(string) bool
	filter engine.Filter
	argv   []string
}{
	"jest":   {detectJest, jestFilter{}, []string{"jest"}},
	"vitest": {detectVitest, vitestFilter{}, []string{"vitest", "run"}},
	"mocha":  {detectMocha, mochaFilter{}, []string{"mocha"}},
}

var benignMissing = map[string]map[string]bool{
	"npm-run-jest-fail": {"Summary of all failing tests": true},
	"rake-test-mocha-fail": {"when error occurs in response handler": true, "when an error occurs": true,
		"error handling": true, "on failure": true, "error-pages": true,
		"at error (node_modules/supertest/lib/test.js:335:15)": true},
}

func TestRunnerDetectorCaptures(t *testing.T) {
	for _, dc := range loadDetectCases(t) {
		t.Run(dc.Name, func(t *testing.T) {
			clean := dc.Clean()
			d, mine := jsDetectors[dc.tool]
			for name, o := range jsDetectors {
				if name != dc.tool && o.detect(clean) {
					t.Errorf("%s detector fired on %q (%s)", name, dc.Meta.Argv, dc.Meta.Description)
				}
			}
			if !mine {
				return
			}
			if !d.detect(clean) {
				t.Fatalf("%s detector did not fire", dc.tool)
			}
			c := dc.Context()
			direct, ok := d.filter.Apply(&engine.Context{Argv: d.argv, Exit: c.Exit, Cwd: c.Cwd, Home: c.Home}, clean)
			if !ok {
				t.Fatalf("%s filter bailed", dc.tool)
			}
			if engine.Detectable(c) {
				out, shape := engine.GenericShape(c, clean)
				if shape != "detected:"+dc.tool || out != direct {
					t.Fatalf("shape %q; detected view equals the %s filter's own view: %v", shape, dc.tool, out == direct)
				}
			}
			fixture.Golden(t, "detect", dc.Name, direct)
			for _, m := range fixture.ErrorMessagesMissing(clean, direct) {
				if !benignMissing[dc.Name][m] {
					t.Errorf("error message missing: %q", m)
				}
			}
			if dc.Meta.ExitCode != 0 {
				if miss := fixture.AppLocationsMissing(clean, direct); len(miss) > 0 {
					t.Errorf("locations missing: %q", miss)
				}
			}

			for _, ln := range strings.Split(clean, "\n") {
				if jestTestsRe.MatchString(ln) || jestSummaryRe.MatchString(ln) || vitestFilesRe.MatchString(ln) ||
					mochaPassingRe.MatchString(ln) || strings.HasPrefix(ln, "--- ") || strings.HasPrefix(ln, "Script ") ||
					strings.HasPrefix(ln, "Command failed") || strings.HasPrefix(ln, "> ") || strings.Contains(ln, "): error TS") {
					if !strings.Contains(direct, strings.TrimSpace(ln)) {
						t.Errorf("line not kept verbatim: %q", ln)
					}
				}
			}
		})
	}
}

func TestRunnerDetectorsReject(t *testing.T) {
	jestSummary := "Test Suites: 1 failed, 2 passed, 3 total\nTests:       1 failed, 9 passed, 10 total\nSnapshots:   0 total\nTime:        1.2 s\n"
	vitestSummary := " Test Files  1 failed | 2 passed (3)\n      Tests  1 failed | 9 passed (10)\n   Start at  10:00:00\n   Duration  1.20s (transform 10ms)\n"
	for name, s := range map[string]string{
		"jest Tests line only":   "Tests:       1 failed, 9 passed, 10 total\n",
		"jest Suites line only":  "Test Suites: 1 failed, 2 passed, 3 total\n",
		"jest JSON reporter":     jestSummary + `{"numFailedTestSuites":1,"numTotalTests":10,"success":false}` + "\n",
		"vitest Files line only": " Test Files  1 failed | 2 passed (3)\n",
		"vitest JSON reporter":   vitestSummary + `{"numTotalTestSuites":3,"numTotalTests":10}` + "\n",
		"vitest bench":           " BENCH  Summary\n\n  sort - bench/a.bench.ts > sort\n    1.10x faster than reverse\n" + vitestSummary,
		"mocha JSON reporter":    `{"stats":{"suites":1,"tests":2,"passes":2}}` + "\n  2 passing (3ms)\n" + `"numTotalTests"`,
		"turbo prefixes":         "web:test: " + strings.ReplaceAll(strings.TrimSuffix(jestSummary, "\n"), "\n", "\nweb:test: ") + "\nweb:test:   2 passing (3ms)\n",
		"bun test":               "bun test v1.1.0 (5e2a9c1)\n\na.test.ts:\n✓ adds [0.12ms]\n✗ subtracts [0.30ms]\n\n 1 pass\n 1 fail\n 2 expect() calls\nRan 2 tests across 1 files. [12.00ms]\n",
		"deno test":              "running 2 tests from ./a_test.ts\nadds ... ok (1ms)\nsubtracts ... FAILED (2ms)\n\nFAILED | 1 passed | 1 failed (25ms)\n\nerror: Test failed\n",
		"node --test":            "✔ adds (0.5ms)\n✖ subtracts (1.2ms)\nℹ tests 2\nℹ suites 0\nℹ pass 1\nℹ fail 1\n",
		"prose":                  "The Tests: section lists 3 total cases; 4 passing (in review).\n",
	} {
		for dn, d := range jsDetectors {
			if d.detect(s) {
				t.Errorf("%s: %s detector fired on\n%s", name, dn, s)
			}
		}
	}
	for name, want := range map[string]string{
		jestSummary:   "jest",
		vitestSummary: "vitest",
		"\n  res\n    ✓ sends (12ms)\n\n  3 passing (45ms)\n  1 failing\n": "mocha",
	} {
		for dn, d := range jsDetectors {
			if got := d.detect(name); got != (dn == want) {
				t.Errorf("%s detector on %s output: %v", dn, want, got)
			}
		}
	}
}
