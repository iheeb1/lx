package golang

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
	if len(out) < 5 {
		t.Fatalf("only %d detect captures", len(out))
	}
	return out
}

func TestGoTestDetectorCaptures(t *testing.T) {
	for _, dc := range loadDetectCases(t) {
		t.Run(dc.Name, func(t *testing.T) {
			clean := dc.Clean()
			fired := detectGoTest(clean)
			if dc.tool != "go-test" {
				if fired {
					t.Fatalf("go-test detector fired on %q (%s)", dc.Meta.Argv, dc.Meta.Description)
				}
				return
			}
			if !fired {
				t.Fatal("go-test detector did not fire")
			}
			c := dc.Context()
			out, shape := engine.GenericShape(c, clean)
			if shape != "detected:go-test" {
				t.Fatalf("shape %q", shape)
			}
			direct, ok := testText{}.Apply(&engine.Context{Argv: []string{"go", "test"}, Exit: c.Exit, Cwd: c.Cwd, Home: c.Home}, clean)
			if !ok || direct != out {
				t.Fatalf("detected view differs from the go-test filter's own view")
			}
			fixture.Golden(t, "detect", dc.Name, out)
			if miss := fixture.ErrorLinesMissing(clean, out); len(miss) > 0 {
				t.Errorf("error lines missing: %q", miss)
			}
			if miss := fixture.AppLocationsMissing(clean, out); len(miss) > 0 {
				t.Errorf("locations missing: %q", miss)
			}
			for _, ln := range strings.Split(clean, "\n") {
				if okRe.MatchString(ln) || failPkgRe.MatchString(ln) || strings.HasPrefix(ln, "--- FAIL") ||
					strings.HasPrefix(ln, "==> ") || strings.HasPrefix(ln, "Command failed") {
					if !strings.Contains(out, strings.TrimSpace(ln)) {
						t.Errorf("line not kept verbatim: %q", ln)
					}
				}
			}
		})
	}
}

func TestGoTestDetectorRejects(t *testing.T) {
	pass := "ok  \texample.com/a\t0.012s\nok  \texample.com/b\t0.020s\n"
	for name, s := range map[string]string{
		"benchmark":              "goos: linux\ngoarch: amd64\npkg: example.com/a\ncpu: X\nBenchmarkSum-8   \t 1000000\t      1053 ns/op\nPASS\n" + pass,
		"benchmark with metrics": "BenchmarkParse-16    \t   50000\t     23456 ns/op\t    4096 B/op\t      12 allocs/op\nPASS\n" + pass,
		"fuzz":                   "fuzz: elapsed: 0s, gathering baseline coverage: 0/1 completed\nfuzz: elapsed: 3s, execs: 1234 (411/sec), new interesting: 0 (total: 1)\nPASS\n" + pass,
		"list":                   "TestSum\nTestJoin\nExampleSum\n" + pass,
		"-x trace":               "WORK=/tmp/go-build123\nmkdir -p $WORK/b001/\n" + pass,
		"-json":                  `{"Action":"output","Package":"example.com/a","Output":"ok  \texample.com/a\t0.01s\n"}` + "\n",
		"no verdict":             "--- FAIL: TestX (0.00s)\n    x_test.go:3: boom\nFAIL\n",
		"verdict mid-line":       "prefix ok  \texample.com/a\t0.01s\n",
		"gotestsum":              "✓  example.com/a (12ms)\n✖  example.com/b (20ms)\n\nDONE 12 tests, 1 failure in 1.234s\n",
		"words only":             "the build is ok and FAIL is a word\nno test files here\n",
	} {
		if detectGoTest(name + "\n" + s) {
			t.Errorf("%s: detector fired on\n%s", name, s)
		}
	}
	for name, s := range map[string]string{
		"pass":       pass,
		"fail":       "--- FAIL: TestX (0.00s)\n    x_test.go:3: boom\nFAIL\nFAIL\texample.com/a\t0.01s\nFAIL\n",
		"build fail": "# example.com/a\n./a.go:3:1: undefined: x\nFAIL\texample.com/a [build failed]\nFAIL\n",
		"no files":   "?   \texample.com/cmd\t[no test files]\nok  \texample.com/a\t0.01s\n",
		"verbose":    "=== RUN   TestSum\n--- PASS: TestSum (0.00s)\nPASS\nok  \texample.com/a\t0.01s\n",
	} {
		if !detectGoTest(s) {
			t.Errorf("%s: detector missed\n%s", name, s)
		}
	}
}
