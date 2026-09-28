// Package fixture loads the test corpus.
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

type Meta struct {
	Argv        []string `json:"argv"`
	Shell       string   `json:"shell"`
	Repo        string   `json:"repo"`
	ExitCode    int      `json:"exit_code"`
	Description string   `json:"description"`
	Cwd         string   `json:"cwd"`
}

type Case struct {
	Category string
	Name     string
	Raw      string
	Meta     Meta
}

func (c Case) Clean() string { return textutil.Clean(c.Raw) }

func (c Case) Context() *engine.Context {
	cwd := c.Meta.Cwd
	switch {
	case strings.HasPrefix(cwd, "/"):
	case cwd != "":

		cwd = "/home/user/src/" + strings.TrimPrefix(cwd, "repos/")
	case c.Meta.Repo != "":
		cwd = "/home/user/src/" + filepath.Base(c.Meta.Repo)
	}
	return &engine.Context{Argv: c.Meta.Argv, Exit: c.Meta.ExitCode, Cwd: cwd, Home: "/home/user"}
}

func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func Load(t testing.TB, category, name string) Case {
	t.Helper()
	c, err := Read(filepath.Join(Root(), "testdata", "corpus"), category, name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

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

func All(t testing.TB) []Case {
	t.Helper()
	out, err := ReadAll(filepath.Join(Root(), "testdata", "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

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

func ErrorLinesMissing(in, out string) []string {
	return engine.MissingErrorLines(in, out)
}

var LocRe = engine.LocRe

func LocationsMissing(in, out string) []string { return engine.LocationsMissing(in, out) }

func ErrorMessagesMissing(in, out string) []string { return engine.ErrorMessagesMissing(in, out) }

func AppLocations(s string) []string { return engine.AppLocations(s) }

func AppLocationsMissing(in, out string) []string { return engine.AppLocationsMissing(in, out) }
