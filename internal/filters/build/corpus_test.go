package build

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"

	_ "github.com/iheeb1/lx/internal/filters/golang"
)

type fx struct {
	fixture.Case
	source string
}

var corpusMisc = []string{"make-c-compile-errors", "make-cjson-build", "make-cjson-test", "make-go-test-gin", "make-go-vet-gin", "make-lua-build"}

func loadFixtures(t testing.TB) []fx {
	t.Helper()
	var out []fx
	for _, n := range corpusMisc {
		out = append(out, fx{fixture.Load(t, "misc", n), "corpus"})
	}
	for _, dir := range []string{"captured", "synthetic"} {
		files, _ := filepath.Glob(filepath.Join("testdata", dir, "*.txt"))
		sort.Strings(files)
		for _, f := range files {
			c, err := fixture.Read("testdata", dir, strings.TrimSuffix(filepath.Base(f), ".txt"))
			if err != nil {
				t.Fatal(err)
			}
			if dir == "synthetic" && !strings.HasPrefix(c.Meta.Description, "SYNTHETIC") {
				t.Fatalf("%s: synthetic fixture not labeled", c.Name)
			}
			out = append(out, fx{c, dir})
		}
	}
	if len(out) < 35 {
		t.Fatalf("only %d fixtures found", len(out))
	}
	return out
}

func expectFind(name string) string {
	for _, p := range []struct{ prefix, filter string }{
		{"make-", "make"}, {"cc-", "cc"}, {"clangxx-", "cc"}, {"cmake-build-", "cmake-build"},
		{"ninja-", "ninja"}, {"cargo-test-", "cargo-test"}, {"cargo-", "cargo"},
		{"gradle-", "gradle"}, {"mvn-", "maven"},
	} {
		if strings.HasPrefix(name, p.prefix) {
			return p.filter
		}
	}
	return "?"
}

var bails = map[string]string{

	"make-cjson-test": "unrecognized recipe output",
}

var processAs = map[string]string{
	"make-cjson-test":             "passthrough",
	"make-c-compile-errors":       "passthrough",
	"make-gnu-link-error":         "passthrough",
	"cargo-clippy-deny-warnings":  "passthrough",
	"make-silent-go-build-errors": "passthrough",
}

var allowedMissingLocs = map[string]map[string]string{

	"make-test-gin-fail": {
		"context_test.go:2739": "passing-test output", "context_test.go:2805": "passing-test output",
		"context_test.go:2869": "passing-test output", "context_test.go:2933": "passing-test output",
		"context_test.go:2998": "passing-test output", "context_test.go:3067": "passing-test output",
		"gin_test.go:98": "passing-test output",
	},
}

func TestCorpus(t *testing.T) {
	var rows []string
	for _, f := range loadFixtures(t) {
		t.Run(f.source+"/"+f.Name, func(t *testing.T) {
			c := f.Context()
			clean := f.Clean()
			want := expectFind(f.Name)
			flt := engine.Find(c)
			if flt == nil || flt.Name() != want {
				t.Fatalf("Find = %v, want %s", flt, want)
			}
			got, ok := flt.Apply(c, clean)
			if reason, bail := bails[f.Name]; bail {
				if ok {
					t.Fatalf("expected a bail (%s), got:\n%s", reason, got)
				}
				got = ""
			} else {
				if !ok {
					t.Fatal("filter bailed on real output")
				}
				fixture.Golden(t, "build", f.Name, got)
				checkFidelity(t, f, clean, got)
			}

			res := engine.Process(c, f.Raw, engine.Options{})
			if res.GuardAdded != 0 {
				t.Errorf("engine guard re-added %d lines:\n%s", res.GuardAdded, res.Output)
			}
			wantProc := want
			if p, ok := processAs[f.Name]; ok {
				wantProc = p
			}
			if tokens.Count(clean) <= engine.SmallOutput {
				wantProc = "passthrough"
			}
			if res.Filter != wantProc && !(wantProc == "passthrough" && res.Filter == "normalize") {
				t.Errorf("Process filter = %q, want %q (raw %d → out %d tokens)", res.Filter, wantProc, res.RawTokens, res.OutTokens)
			}
			if again := engine.Process(c, f.Raw, engine.Options{}); again.Output != res.Output {
				t.Error("Process is not deterministic")
			}
			if got2, ok2 := flt.Apply(c, clean); got2 != got && ok2 == ok {
				t.Error("Apply is not deterministic")
			}
			ft := tokens.Count(got)
			line := fmt.Sprintf("%-10s %-34s raw %6d → filter %5d → out %5d tokens (−%2.0f%%, %s)",
				f.source, f.Name, res.RawTokens, ft, res.OutTokens, 100*res.Saved(), res.Filter)
			t.Log(line)
			rows = append(rows, line)
		})
	}
	if p := os.Getenv("LX_SAVINGS"); p != "" {
		os.WriteFile(p, []byte(strings.Join(rows, "\n")+"\n"), 0o644)
	}
}

func checkFidelity(t *testing.T, f fx, clean, got string) {
	t.Helper()
	orig := map[string]string{}
	for _, ln := range strings.Split(clean, "\n") {
		orig[strings.TrimSpace(ln)] = ln
	}
	for _, m := range fixture.ErrorLinesMissing(clean, got) {
		if reason := justifiedDrop(orig[m]); reason != "" {
			continue
		}
		t.Errorf("error line missing: %q", orig[m])
	}
	if strings.Contains(got, "[lx: error lines from the full output]") {
		t.Errorf("the filter's own guard had to re-add lines:\n%s", got)
	}
	if f.Meta.ExitCode != 0 {
		checkLocations(t, f.Name, clean, got)
	}
}

var (
	libFrameRe = regexp.MustCompile(`/rustc/[0-9a-f]+/library/|^(?:\[ERROR\] )?\s+at (?:org\.junit\.|java\.base/|jdk\.internal\.|org\.apache\.maven\.|org\.codehaus\.plexus\.|java\.lang\.)`)
	inclLineRe = regexp.MustCompile(`^(?:In file included from |\s+from )`)
)

func justifiedDrop(line string) string {
	t := strings.TrimSpace(line)
	switch {
	case line == "":
		return ""
	case func() bool { _, ok := commandLabel(line); return ok }():
		return "hidden recipe command"
	case (line[0] == ' ' || line[0] == '\t') && strings.HasSuffix(line, "\\"):
		return "echoed shell recipe"
	case cargoStatusRe.MatchString(line) && knownVerb(cargoStatusRe.FindStringSubmatch(line)[1]):
		return "cargo status line"
	case strings.HasPrefix(t, "[ERROR]") && mvnHelpFooter.MatchString(strings.TrimSpace(strings.TrimPrefix(t, "[ERROR]"))):
		return "maven help footer"
	case strings.HasPrefix(t, "[ERROR]") && mvnFrameRe.MatchString(strings.TrimPrefix(t, "[ERROR]")):
		return "library stack frame under Maven's [ERROR] prefix, folded with a marker"
	}
	return ""
}

func checkLocations(t *testing.T, name, clean, got string) {
	t.Helper()
	lines := strings.Split(clean, "\n")
	for _, loc := range fixture.LocationsMissing(clean, got) {
		if allowedMissingLocs[name][filepath.Base(loc)] != "" {
			continue
		}
		folded := true
		for _, ln := range lines {
			if strings.Contains(ln, loc) && !inclLineRe.MatchString(ln) && !libFrameRe.MatchString(ln) {
				folded = false
				break
			}
		}
		if !folded {
			t.Errorf("location %s missing", loc)
		}
	}
}
