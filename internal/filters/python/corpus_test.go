package python

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

// A fixture is a real captured output: the shared corpus
// (testdata/corpus/python) or this package's own captures
// (internal/filters/python/testdata/captured, made in a scratch venv with
// pytest 8.3.4, mypy 1.19.1, ruff 0.16.9 and pip 26.0.1, paths sanitized).
type fx struct {
	fixture.Case
	source string // "corpus" or "captured"
}

func loadFixtures(t *testing.T) []fx {
	t.Helper()
	var out []fx
	for _, c := range fixture.All(t) {
		if c.Category == "python" {
			out = append(out, fx{c, "corpus"})
		}
	}
	files, _ := filepath.Glob(filepath.Join("testdata", "captured", "*.txt"))
	sort.Strings(files)
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".txt")
		c, err := fixture.Read("testdata", "captured", name)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, fx{c, "captured"})
	}
	if len(out) < 50 {
		t.Fatalf("only %d fixtures found", len(out))
	}
	return out
}

// expectFind is the filter engine.Find must pick for each fixture.
func expectFind(name string) string {
	switch {
	case name == "ruff-check-statistics":
		return "" // --statistics prints a table, not diagnostics: not matched
	case strings.HasPrefix(name, "pytest"):
		return "pytest" // pytest-*, pytest313-* (pytest 9), pytest9-*
	case strings.HasPrefix(name, "python") || strings.HasPrefix(name, "unittest-"):
		return "python" // scripts, python -m unittest
	case strings.HasPrefix(name, "pip-install") || strings.HasPrefix(name, "pip21-install") || name == "pip-upgrade-pip":
		return "pip-install"
	case strings.HasPrefix(name, "pip-uninstall"):
		return "pip-uninstall"
	case strings.HasPrefix(name, "pip-list"):
		return "pip-list"
	case strings.HasPrefix(name, "pip-show"):
		return "pip-show"
	case strings.HasPrefix(name, "mypy"):
		return "mypy" // mypy-* (1.19), mypy2-* (2.3)
	case strings.HasPrefix(name, "ruff"):
		return "ruff" // ruff-* (0.16), ruff011-* (0.11, pre-0.12 format)
	}
	return "?"
}

// bails lists fixtures whose Apply must return ok=false, and why.
var bails = map[string]string{
	// No session header, no result line, no short summary: a conftest
	// ImportError is not a test report; the generic reducer shows it whole.
	"pytest-conftest-import-error": "not a test report",
}

// processAs lists fixtures where the full pipeline legitimately does not
// report the filter's name, with the reason. Outputs at or under
// engine.SmallOutput tokens are only normalized and need no entry.
var processAs = map[string]string{
	// The filter keeps these (nearly) as they are, saving less than
	// engine.DefaultMinSavings, so the normalized output is shown.
	"pip-install-no-version": "passthrough", // one 700-token ERROR line listing every version: kept verbatim
	"pip-list":               "passthrough", // data table, already compact
	"ruff-check-concise":     "passthrough", // one line per diagnostic already
	"ruff-check-all":         "passthrough",
	"ruff-check-grouped":     "passthrough",
	"ruff-check-statistics":  "passthrough", // not matched; generic finds nothing to fold
	"mypy-errors":            "passthrough", // every line is an error or a distinct note
	"mypy-pretty":            "passthrough",
	"mypy-strict":            "passthrough",
	"mypy2-errors":           "passthrough", // mypy 2.3: no repeated note left to fold
	"mypy2-ignore-notes":     "passthrough", // "Error code … not covered" notes are per location: all kept
	// pip 21's legacy "Command errored out" block is kept whole.
	"pip21-install-build-error": "passthrough",
}

// codeLineRe: lines a Guarded filter may drop although the classifier
// calls them errors, because they are source code (pytest traceback
// source, frame locals, echoed warning source; ruff snippets and fix diffs)
// or per-test status lines whose failures are reported elsewhere.
var codeLineRe = regexp.MustCompile(`^(?:INTERNALERROR>)?(?:\s|>)|^[A-Za-z_]\w* = |^\s*\d*\s*[|+-]\s`)

var statusLineRe = regexp.MustCompile(`^\S+\.py[ :]| (?:PASSED|SKIPPED|XFAIL)\b`)

func TestCorpus(t *testing.T) {
	var rows []string
	for _, f := range loadFixtures(t) {
		f := f
		t.Run(f.source+"/"+f.Name, func(t *testing.T) {
			c := f.Context()
			clean := f.Clean()
			want := expectFind(f.Name)
			flt := engine.Find(c)
			switch {
			case want == "" && flt != nil && strings.HasPrefix(flt.Name(), "p"):
				t.Fatalf("Find = %s, want no python filter", flt.Name())
			case want != "" && (flt == nil || flt.Name() != want):
				t.Fatalf("Find = %v, want %s", flt, want)
			}
			var got string
			if want != "" {
				var ok bool
				got, ok = flt.Apply(c, clean)
				if _, bail := bails[f.Name]; bail {
					if ok {
						t.Fatalf("expected a bail (%s), got:\n%s", bails[f.Name], got)
					}
				} else {
					if !ok {
						t.Fatal("filter bailed on real output")
					}
					fixture.Golden(t, "python", f.Name, got)
					checkFidelity(t, f, flt, clean, got)
				}
			}

			res := engine.Process(c, f.Raw, engine.Options{})
			if res.GuardAdded != 0 {
				t.Errorf("engine guard re-added %d lines:\n%s", res.GuardAdded, res.Output)
			}
			wantProc := want
			if p, ok := processAs[f.Name]; ok {
				wantProc = p
			}
			if wantProc == "" {
				wantProc = "generic"
			}
			if tokens.Count(clean) <= engine.SmallOutput {
				wantProc = "passthrough" // small outputs are only normalized
			}
			if res.Filter != wantProc && !(wantProc == "passthrough" && res.Filter == "normalize") {
				t.Errorf("Process filter = %q, want %q (raw %d → out %d tokens)", res.Filter, wantProc, res.RawTokens, res.OutTokens)
			}
			// Determinism of the whole pipeline.
			if again := engine.Process(c, f.Raw, engine.Options{}); again.Output != res.Output {
				t.Error("Process is not deterministic")
			}
			ft := 0
			if got != "" {
				ft = tokens.Count(got)
			}
			line := f.source + "/" + f.Name + ": raw " + itoa(res.RawTokens) + " → filter " + itoa(ft) + " → out " + itoa(res.OutTokens) +
				" (" + pct(res.RawTokens, res.OutTokens) + ", " + res.Filter + ")"
			t.Log(line)
			rows = append(rows, line)
		})
	}
	if p := os.Getenv("LX_SAVINGS"); p != "" {
		os.WriteFile(p, []byte(strings.Join(rows, "\n")+"\n"), 0o644)
	}
}

func checkFidelity(t *testing.T, f fx, flt engine.Filter, clean, got string) {
	t.Helper()
	orig := map[string]string{} // trimmed → untrimmed line
	for _, ln := range strings.Split(clean, "\n") {
		orig[strings.TrimSpace(ln)] = ln
	}
	_, guarded := flt.(engine.Guarded)
	_, content := flt.(engine.Content)
	for _, m := range fixture.ErrorLinesMissing(clean, got) {
		o := orig[m]
		if (guarded || content) && (codeLineRe.MatchString(o) || statusLineRe.MatchString(o)) {
			continue // justified: see codeLineRe / statusLineRe
		}
		t.Errorf("error line missing: %q", o)
	}
	if strings.Contains(got, "[lx: error lines from the full output]") {
		t.Errorf("filter's own guard had to re-add lines:\n%s", got)
	}
	if f.Meta.ExitCode == 0 {
		return
	}
	for _, loc := range fixture.LocationsMissing(clean, got) {
		// Justified: library frames (site-packages, stdlib) are folded into
		// "… N library frames (…)" markers by design.
		if isLibPath(loc) {
			continue
		}
		t.Errorf("location missing: %s", loc)
	}
}

func itoa(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
		if n == 0 {
			break
		}
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func pct(raw, out int) string {
	if raw == 0 {
		return "0%"
	}
	return itoa(int(100*(1-float64(out)/float64(raw))+0.5)) + "%"
}
