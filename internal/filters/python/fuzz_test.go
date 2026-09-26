package python

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/textutil"
)

// fuzzContexts gives every filter a command line it would match.
var fuzzContexts = map[string][]string{
	"pytest":        {"pytest"},
	"pip-install":   {"pip", "install", "x"},
	"pip-uninstall": {"pip", "uninstall", "-y", "x"},
	"pip-list":      {"pip", "list"},
	"pip-show":      {"pip", "show", "x"},
	"mypy":          {"mypy", "."},
	"ruff":          {"ruff", "check", "."},
	"python":        {"python", "x.py"},
}

// FuzzFilters checks that no filter panics, that each is deterministic,
// and that pytest never loses its result line or an E line.
//
//	go test ./internal/filters/python -run '^$' -fuzz FuzzFilters -fuzztime 20s
func FuzzFilters(f *testing.F) {
	for _, c := range fixture.All(f) {
		if c.Category == "python" {
			f.Add(c.Clean(), 1)
		}
	}
	files, _ := filepath.Glob(filepath.Join("testdata", "captured", "*.txt"))
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err == nil {
			f.Add(textutil.Clean(string(b)), 0)
		}
	}
	f.Add(pytestShapes, 1)
	f.Add(ruffOldFull, 1)
	f.Add("collected 3 items\n\ntests/test_a.py .F.  [100%]\n\n==== 1 failed, 2 passed in 0.1s ====", 1)
	f.Add("..F.  [100%]\n==== short test summary info ====\nSKIPPED [1] t.py:3: x", 1)
	f.Add("==== test session starts ====\ncollected 2 items\n\nt.py .Fatal Python error: Segmentation fault\n\nCurrent thread 0x01 (most recent call first):\n  File \"/usr/lib/python3.12/a.py\", line 1 in f\n  File \"t.py\", line 2 in g", 139)
	f.Add("INTERNALERROR> Traceback (most recent call last):\nINTERNALERROR>   File \"/x/site-packages/p/a.py\", line 1, in f\nINTERNALERROR>     raise exception\nINTERNALERROR> E: boom\n==== 2 passed in 0.1s ====", 3)
	f.Add("  + Exception Group Traceback (most recent call last):\n  |   File \"/usr/lib/python3.13/a.py\", line 1, in f\n  |     x()\n  | ExceptionGroup: g (1 sub-exception)", 1)
	f.Add("==== warnings summary ====\nt.py::a\n  t.py:1: UserWarning: w\n    w()\n\n-- Docs: https://x\n---- coverage: x ----\nFAIL Required test coverage of 90% not reached.\n==== 1 passed in 0.1s ====", 1)
	f.Add("All checks passed!", 1)
	f.Add("Success: no issues found in 1 source file", 2)
	f.Add("", 0)
	f.Add("=== 1 passed in 0.01s ===", 5)
	f.Fuzz(func(t *testing.T, in string, exit int) {
		in = textutil.Clean(in)
		for name, argv := range fuzzContexts {
			c := &engine.Context{Argv: argv, Exit: exit, Cwd: "/home/user/src/demo", Home: "/home/user"}
			flt := engine.Find(c)
			if flt == nil || flt.Name() != name {
				t.Fatalf("%v: found %v, want %s", argv, flt, name)
			}
			start := time.Now()
			out1, ok1 := flt.Apply(c, in)
			if el := time.Since(start); el > fuzzSlow {
				t.Fatalf("%s took %v on %d bytes", name, el, len(in))
			}
			out2, ok2 := flt.Apply(c, in)
			if out1 != out2 || ok1 != ok2 {
				t.Fatalf("%s is not deterministic", name)
			}
			// Never pass-looking on failure: a failing status leaves an
			// error line or an "[lx: … exit…]" note in the output.
			if ok1 && exit != 0 && !anyError(strings.Split(out1, "\n")) && !exitNoteRe.MatchString(out1) {
				t.Fatalf("%s: exit %d but nothing in the output says so:\n%s", name, exit, out1)
			}
			if name != "pytest" || !ok1 {
				continue
			}
			lines := strings.Split(in, "\n")
			for i := len(lines) - 1; i >= 0; i-- {
				if ptSummaryRe.MatchString(lines[i]) {
					if !strings.Contains(out1, lines[i]) {
						t.Fatalf("result line %q lost", lines[i])
					}
					break
				}
			}
			// Every E line of a FAILURES / ERRORS section survives.
			squashed := squashAll(out1)
			section := ""
			for _, ln := range lines {
				if m := ptSectionRe.FindStringSubmatch(ln); m != nil {
					section = m[1]
					continue
				}
				if (section == "FAILURES" || section == "ERRORS") && ptELineRe.MatchString(ln) &&
					engine.IsError(ln) && !strings.Contains(squashed, squashLine(ln)) {
					t.Fatalf("E line %q lost:\n%s", ln, out1)
				}
			}
		}
	})
}

// fuzzSlow: no input the fuzzer builds (at most a few hundred KB) may take
// this long; that would be super-linear behavior.
var fuzzSlow = 2 * time.Second * raceSlowdown

var exitNoteRe = regexp.MustCompile(`\[lx: .*\bexit(?:ed)? -?\d+`)

func squashAll(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = squashLine(ln)
	}
	return strings.Join(lines, "\n")
}
