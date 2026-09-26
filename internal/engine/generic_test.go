package engine

import (
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/tokens"
)

func TestGenericShapes(t *testing.T) {
	c := &Context{Argv: []string{"x"}, Cwd: "/home/u/proj", Home: "/home/u"}
	cases := []struct {
		name, in, shape string
	}{
		{"json", issuesJSON(10), "json"},
		{"log", strings.Join(nginxLog(), "\n"), "log"},
		{"paths", strings.Repeat("./internal/engine/testdata/fixture.go\n", 1) + "./internal/engine/a.go\n./internal/engine/b.go\n./internal/engine/c.go\n./internal/engine/d.go\n./internal/engine/e.go\n./internal/tokens/t.go\n./internal/tokens/u.go", "paths"},
		{"lines", "building\nstep 1\nstep 2\ndone", "lines"},
	}
	for _, tc := range cases {
		out, shape := GenericShape(c, tc.in)
		if shape != tc.shape {
			t.Errorf("%s: shape %q, want %q\n%s", tc.name, shape, tc.shape, out)
		}
		if tokens.Count(out) > tokens.Count(tc.in) {
			t.Errorf("%s: output grew", tc.name)
		}
		if Generic(c, tc.in) != out {
			t.Errorf("%s: Generic and GenericShape differ", tc.name)
		}
	}
}

func TestGenericKeepsErrorLinesVerbatim(t *testing.T) {
	c := &Context{Cwd: "/home/u/proj", Home: "/home/u"}
	in := "compiling /home/u/proj/a.go\nerror: cannot open /home/u/proj/b.go\r\n" +
		strings.Repeat("progress 50% [#####     ]\n", 3) + "done"
	out := Generic(c, in)
	if !strings.Contains(out, "compiling a.go") {
		t.Errorf("not relativized:\n%s", out)
	}
	if m := MissingErrorLines(in, out); len(m) > 0 {
		t.Errorf("error line changed: %q\n%s", m, out)
	}
	if strings.Contains(out, "\r") {
		t.Error("CR survived")
	}
}

func TestGenericEdgeCases(t *testing.T) {
	for _, in := range []string{"", "one line", "\n\n", "ünïcödé ✓ 12ms"} {
		out := Generic(nil, in)
		if tokens.Count(out) > tokens.Count(in) {
			t.Errorf("Generic(%q) grew: %q", in, out)
		}
	}
}
