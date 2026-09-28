package git

import (
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
)

func FuzzGitFilters(f *testing.F) {
	cases := fixture.All(f)
	if local, err := fixture.ReadAll("testdata"); err == nil {
		cases = append(cases, local...)
	}
	names := make([]string, len(allFilters))
	for i, argv := range allFilters {
		if fl := engine.Find(ctx(0, argv...)); fl != nil {
			names[i] = fl.Name()
		}
	}
	for _, fc := range cases {
		if fc.Category != "git" {
			continue
		}
		in := fc.Clean()
		if len(in) > 16<<10 {
			in = in[:16<<10]
		}
		fl := engine.Find(fc.Context())
		for i, n := range names {
			if fl != nil && n == fl.Name() {
				f.Add(uint8(i), uint8(fc.Meta.ExitCode), in)
			}
		}
		f.Add(uint8(len(fc.Name)), uint8(1), in)
	}
	f.Fuzz(func(t *testing.T, which, exit uint8, out string) {
		c := ctx(int(exit%2), allFilters[int(which)%len(allFilters)]...)
		fl := engine.Find(c)
		if fl == nil {
			t.Skip()
		}
		a, okA := fl.Apply(c, out)
		b, okB := fl.Apply(c, out)
		if a != b || okA != okB {
			t.Fatalf("%s not deterministic", fl.Name())
		}
		if !okA {
			return
		}

		if fl.Name() == "git-status" {
			for _, m := range fixture.ErrorLinesMissing(out, a) {
				if !statusConverted(m, a) {
					t.Fatalf("git-status dropped error line %q\ninput:\n%s\noutput:\n%s", m, out, a)
				}
			}
		}

		if fl.Name() == "git-diff" {
			if d, ok := parseDiffDoc(strings.Split(out, "\n")); ok {
				for _, p := range d.parts {
					if p.file == nil {
						continue
					}
					for _, cl := range p.file.conflicts() {
						if !strings.Contains(a, cl.text) {
							t.Fatalf("conflict marker %q hidden:\n%s", cl.text, a)
						}
					}
				}
			}
		}
	})
}
