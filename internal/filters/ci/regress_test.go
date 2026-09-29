package ci_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
)

var ours = map[string]bool{"gh-run-log": true, "gh-run-view": true, "gh-run-watch": true, "gh-pr-checks": true, "monorepo": true}

func otherCaptures(t *testing.T) []fixture.Case {
	t.Helper()
	cases := fixture.All(t)
	root := fixture.Root()
	var files []string
	err := filepath.WalkDir(filepath.Join(root, "internal", "filters"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "fuzz" || p == filepath.Join(root, "internal", "filters", "ci")) {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(p, ".txt") && strings.Contains(p, string(filepath.Separator)+"testdata"+string(filepath.Separator)) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	for _, f := range files {
		dir := filepath.Dir(f)
		c, err := fixture.Read(filepath.Dir(dir), filepath.Base(dir), strings.TrimSuffix(filepath.Base(f), ".txt"))
		if err != nil {
			t.Fatal(err)
		}
		c.Category = strings.TrimPrefix(dir, root+string(filepath.Separator))
		cases = append(cases, c)
	}
	if len(cases) < 500 {
		t.Fatalf("only %d captures found", len(cases))
	}
	return cases
}

func metaTool(c fixture.Case) string {
	p := filepath.Join(fixture.Root(), "testdata", "corpus", c.Category, c.Name+".meta.json")
	if strings.HasPrefix(c.Category, "internal"+string(filepath.Separator)) {
		p = filepath.Join(fixture.Root(), c.Category, c.Name+".meta.json")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var m struct {
		Tool string `json:"tool"`
	}
	_ = json.Unmarshal(b, &m)
	return m.Tool
}

func TestOtherCapturesUntouched(t *testing.T) {
	var det engine.Detector
	for _, d := range engine.Detectors() {
		if d.Name == "monorepo" {
			det = d
		}
	}
	if det.Detect == nil {
		t.Fatal("monorepo detector not registered")
	}
	allowed := map[string]bool{
		"internal/filters/jstools/testdata/node/pnpm-r-build-tsc-fail":       true,
		"internal/filters/jstools/testdata/node/pnpm-build-script-recursive": true,
	}
	n := 0
	for _, c := range otherCaptures(t) {
		id := c.Category + "/" + c.Name
		ctx := c.Context()
		if f := engine.Find(ctx); f != nil && ours[f.Name()] {
			t.Errorf("%s: claimed by %s", id, f.Name())
		}
		clean := c.Clean()
		if det.Detect(clean) {
			n++
			f, _ := engine.Resolve(ctx)
			switch {
			case !allowed[id]:
				t.Errorf("%s: monorepo detector fired (tool %q)", id, metaTool(c))
			case f == nil || engine.Detectable(ctx):
				t.Errorf("%s: monorepo output with no owning filter", id)
			}
		}
	}
	if n != len(allowed) {
		t.Errorf("monorepo detector fired on %d captures, want %d", n, len(allowed))
	}
}
