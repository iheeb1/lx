// Package fixture loads the real-output corpus (testdata/corpus) and provides
// golden-file and fidelity assertions for filter tests.
//
// Golden files live in testdata/golden/<dir>/<name>.out. Regenerate with
//
//	LX_UPDATE_GOLDEN=1 go test ./...
//
// and review the diff: a golden file is the exact text an agent will read.
package fixture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/textutil"
)

// Meta mirrors <name>.meta.json.
type Meta struct {
	Argv        []string `json:"argv"`
	Shell       string   `json:"shell"`
	Repo        string   `json:"repo"`
	ExitCode    int      `json:"exit_code"`
	Description string   `json:"description"`
	Cwd         string   `json:"cwd"`
}

// Case is one captured command output.
type Case struct {
	Category string // corpus sub-directory: git, node, go, ...
	Name     string // file name without extension
	Raw      string // exactly what the command printed
	Meta     Meta
}

// Clean is the normalized output filters receive.
func (c Case) Clean() string { return textutil.Clean(c.Raw) }

// Context builds the engine context the command ran in.
func (c Case) Context() *engine.Context {
	cwd := c.Meta.Cwd
	switch {
	case strings.HasPrefix(cwd, "/"):
	case cwd != "":
		// Captured relative to the corpus root: repos/<name>[/sub] → /home/user/src/<name>[/sub].
		cwd = "/home/user/src/" + strings.TrimPrefix(cwd, "repos/")
	case c.Meta.Repo != "":
		cwd = "/home/user/src/" + filepath.Base(c.Meta.Repo)
	}
	return &engine.Context{Argv: c.Meta.Argv, Exit: c.Meta.ExitCode, Cwd: cwd, Home: "/home/user"}
}

// Root returns the repository root.
func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// Load reads testdata/corpus/<category>/<name>.txt and its meta.
func Load(t testing.TB, category, name string) Case {
	t.Helper()
	c, err := Read(filepath.Join(Root(), "testdata", "corpus"), category, name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// Read loads one case from a corpus directory laid out as
// <dir>/<category>/<name>.txt + <name>.meta.json.
func Read(dir, category, name string) (Case, error) {
	base := filepath.Join(dir, category, name)
	raw, err := os.ReadFile(base + ".txt")
	if err != nil {
		return Case{}, fmt.Errorf("fixture %s/%s: %w", category, name, err)
	}
	c := Case{Category: category, Name: name, Raw: string(raw)}
	if mb, err := os.ReadFile(base + ".meta.json"); err == nil {
		if err := json.Unmarshal(mb, &c.Meta); err != nil {
			return Case{}, fmt.Errorf("fixture %s/%s meta: %w", category, name, err)
		}
	}
	return c, nil
}

// All returns every corpus case, sorted by category then name.
func All(t testing.TB) []Case {
	t.Helper()
	out, err := ReadAll(filepath.Join(Root(), "testdata", "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ReadAll loads every case under a corpus directory.
func ReadAll(dir string) ([]Case, error) {
	var out []Case
	cats, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, cat := range cats {
		if !cat.IsDir() {
			continue
		}
		files, _ := filepath.Glob(filepath.Join(dir, cat.Name(), "*.txt"))
		sort.Strings(files)
		for _, f := range files {
			c, err := Read(dir, cat.Name(), strings.TrimSuffix(filepath.Base(f), ".txt"))
			if err != nil {
				return nil, err
			}
			out = append(out, c)
		}
	}
	return out, nil
}

// Golden compares got with testdata/golden/<dir>/<name>.out, rewriting the
// file instead when LX_UPDATE_GOLDEN=1.
func Golden(t testing.TB, dir, name, got string) {
	t.Helper()
	p := filepath.Join(Root(), "testdata", "golden", dir, name+".out")
	if os.Getenv("LX_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(got+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("missing golden %s (run with LX_UPDATE_GOLDEN=1): %v", p, err)
	}
	if strings.TrimRight(string(want), "\n") != strings.TrimRight(got, "\n") {
		t.Errorf("output differs from %s (LX_UPDATE_GOLDEN=1 to accept):\n--- got ---\n%s", p, got)
	}
}

// ErrorLinesMissing returns the error-class lines of in whose text does not
// appear anywhere in out. Whitespace runs are collapsed on both sides, so
// re-aligned columns still count as kept; any other change counts as loss.
func ErrorLinesMissing(in, out string) []string {
	return engine.MissingErrorLines(in, out)
}

// The location and message metrics live in internal/engine (locs.go), so
// `lx discover --fidelity` scores real sessions with the same functions as
// the benchmark and the filter tests. These names delegate to them.

// LocRe matches file:line[:col] locations in compiler, linter, test and
// stack-trace output.
var LocRe = engine.LocRe

// LocationsMissing returns file:line locations present in in but absent
// from out. Locations are compared by base name + line so relativized paths
// still match.
func LocationsMissing(in, out string) []string { return engine.LocationsMissing(in, out) }

// ErrorMessagesMissing is ErrorLinesMissing for filters that regroup
// diagnostics: an error line counts as kept when its message — the line's
// words with every token that contains a digit removed (positions, counts,
// durations) — still appears in out. Locations are checked separately by
// LocationsMissing. The benchmark scores every strategy (lx, rtk, head/tail)
// with this same function.
func ErrorMessagesMissing(in, out string) []string { return engine.ErrorMessagesMissing(in, out) }

// AppLocations returns the distinct file:line locations in s that point at
// application code (not dependencies or runtimes).
func AppLocations(s string) []string { return engine.AppLocations(s) }

// AppLocationsMissing is LocationsMissing restricted to application code.
func AppLocationsMissing(in, out string) []string { return engine.AppLocationsMissing(in, out) }
