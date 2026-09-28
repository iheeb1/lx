package python

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
	if len(out) < 6 {
		t.Fatalf("only %d detect captures", len(out))
	}
	return out
}

func TestPytestDetectorCaptures(t *testing.T) {
	for _, dc := range loadDetectCases(t) {
		t.Run(dc.Name, func(t *testing.T) {
			clean := dc.Clean()
			fired := detectPytest(clean)
			if dc.tool != "pytest" {
				if fired {
					t.Fatalf("pytest detector fired on %q (%s)", dc.Meta.Argv, dc.Meta.Description)
				}
				return
			}
			if !fired {
				t.Fatal("pytest detector did not fire")
			}
			c := dc.Context()
			out, shape := engine.GenericShape(c, clean)
			if shape != "detected:pytest" {
				t.Fatalf("shape %q", shape)
			}
			direct, ok := pytestFilter{}.Apply(&engine.Context{Argv: []string{"pytest"}, Exit: c.Exit, Cwd: c.Cwd, Home: c.Home}, clean)
			if !ok || direct != out {
				t.Fatal("detected view differs from the pytest filter's own view")
			}
			fixture.Golden(t, "detect", dc.Name, out)

			for _, m := range fixture.ErrorMessagesMissing(clean, out) {
				if m != "assert not result.exception" {
					t.Errorf("error message missing: %q", m)
				}
			}
			if miss := fixture.AppLocationsMissing(clean, out); len(miss) > 0 {
				t.Errorf("locations missing: %q", miss)
			}
			for _, ln := range strings.Split(clean, "\n") {
				if ptIsSummary(ln) || strings.HasPrefix(ln, "FAILED ") || strings.HasPrefix(ln, "Script ") ||
					strings.HasPrefix(ln, "Command failed") || ln == "rake aborted!" {
					if !strings.Contains(out, ln) {
						t.Errorf("line not kept verbatim: %q", ln)
					}
				}
			}
		})
	}
}

func TestPytestDetectorRejects(t *testing.T) {
	hdr := "============================= test session starts ==============================\n" +
		"platform linux -- Python 3.12.1, pytest-8.3.4, pluggy-1.5.0\nrootdir: /w\ncollected 3 items\n\n"
	run := "tests/test_a.py ..F                                                      [100%]\n"
	for name, s := range map[string]string{
		"collect-only":  hdr + "<Module tests/test_a.py>\n  <Function test_x>\n\n========================= 3 tests collected in 0.01s ==========================\n",
		"no tests ran":  hdr + "\n============================ no tests ran in 0.02s =============================\n",
		"quiet":         run + "1 failed, 2 passed in 0.10s\n",
		"no header":     run + "========================= 1 failed, 2 passed in 0.10s ==========================\n",
		"no summary":    hdr + run,
		"summary first": "========================= 1 failed, 2 passed in 0.10s ==========================\n" + hdr + run,
		"two sessions (tox)": "py39: commands[0]> pytest\n" + hdr + run + "=================== 1 failed, 2 passed in 0.10s ===================\n" +
			"py312: commands[0]> pytest\n" + hdr + run + "=================== 1 failed, 2 passed in 0.12s ===================\n",
		"prose": "the test session starts at nine\n== 3 passed in review ==\n",
	} {
		if detectPytest(s) {
			t.Errorf("%s: detector fired on\n%s", name, s)
		}
	}
	for name, s := range map[string]string{
		"fail":      hdr + run + "=================== 1 failed, 2 passed in 0.10s ===================\n",
		"warnings":  hdr + run + "=========== 1 failed, 2 passed, 3 warnings in 1.23s (0:00:01) ===========\n",
		"errors":    hdr + "=================== 2 errors in 0.31s ===================\n",
		"xdist":     "============================= test session starts (xdist) =============================\n" + "=== 3 passed in 1.0s ===\n",
		"deselect":  hdr + "=================== 23 deselected, 1 warning in 0.15s ===================\n",
		"subtests":  hdr + "=================== 1 failed, 4 subtests passed in 0.15s ===================\n",
		"with echo": "pytest tests\n" + hdr + run + "=================== 3 passed in 0.10s ===================\nrake aborted!\n",
	} {
		if !detectPytest(s) {
			t.Errorf("%s: detector missed\n%s", name, s)
		}
	}
}
