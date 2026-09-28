package engine

import (
	"fmt"
	"strings"
	"testing"
)

func withDetectors(t *testing.T, ds ...Detector) {
	t.Helper()
	detMu.Lock()
	saved := detectors
	detectors = ds
	detMu.Unlock()
	t.Cleanup(func() {
		detMu.Lock()
		detectors = saved
		detMu.Unlock()
	})
}

type detFake struct {
	name  string
	apply func(c *Context, s string) (string, bool)
}

func (f detFake) Name() string                              { return f.name }
func (detFake) Match(*Context) bool                         { return false }
func (f detFake) Apply(c *Context, s string) (string, bool) { return f.apply(c, s) }

type guardedDetFake struct{ detFake }

func (guardedDetFake) GuardsErrors() bool { return true }

func marker(s string) bool { return strings.Contains(s, "@@lx-detect-test@@") }

func TestDetectFallsThrough(t *testing.T) {
	var gotArgv []string
	var gotCtx Context
	withDetectors(t,
		Detector{Name: "quiet", Detect: func(string) bool { return false }, Argv: []string{"quiet"},
			Filter: detFake{"quiet", func(*Context, string) (string, bool) { t.Fatal("undetected filter applied"); return "", false }}},
		Detector{Name: "bails", Detect: marker, Argv: []string{"bails"},
			Filter: detFake{"bails", func(*Context, string) (string, bool) { return "", false }}},
		Detector{Name: "panics", Detect: marker, Argv: []string{"panics"},
			Filter: detFake{"panics", func(*Context, string) (string, bool) { panic("boom") }}},
		Detector{Name: "detect-panics", Detect: func(string) bool { panic("boom") }, Argv: []string{"x"},
			Filter: detFake{"x", func(*Context, string) (string, bool) { t.Fatal("applied after Detect panicked"); return "", false }}},
		Detector{Name: "renders", Detect: marker, Argv: []string{"tool", "test"},
			Filter: guardedDetFake{detFake{"renders", func(c *Context, s string) (string, bool) {
				gotArgv, gotCtx = c.Argv, *c
				return "view", true
			}}}},
		Detector{Name: "later", Detect: marker, Argv: []string{"later"},
			Filter: detFake{"later", func(*Context, string) (string, bool) { return "later view", true }}},
	)
	c := &Context{Argv: []string{"just", "test"}, Exit: 3, Cwd: "/w", Home: "/h", Budget: 1234}
	out, name, ok := Detect(c, "a\n@@lx-detect-test@@\nb")
	if !ok || out != "view" || name != "renders" {
		t.Fatalf("Detect = %q, %q, %v; want the first view that rendered", out, name, ok)
	}
	if strings.Join(gotArgv, " ") != "tool test" || gotCtx.Exit != 3 || gotCtx.Cwd != "/w" || gotCtx.Home != "/h" || gotCtx.Budget != 1234 {
		t.Errorf("filter context = %+v; want the detector's argv with the command's exit, cwd, home and budget", gotCtx)
	}
	if strings.Join(c.Argv, " ") != "just test" {
		t.Errorf("caller's context changed: %q", c.Argv)
	}
	if _, _, ok := Detect(c, "nothing to see"); ok {
		t.Error("Detect fired without a detector recognizing the output")
	}
	if _, _, ok := Detect(nil, "@@lx-detect-test@@"); ok {
		t.Error("Detect with a nil context fired")
	}
	if _, _, ok := Detect(&Context{}, "@@lx-detect-test@@"); ok {
		t.Error("Detect with an empty argv fired")
	}

	in := strings.Repeat("some line of output that is long enough\n", 20) + "@@lx-detect-test@@"
	v, shape := GenericShape(c, in)
	if v != "view" || shape != "detected:renders" {
		t.Fatalf("GenericShape = %q, %q", v, shape)
	}
	f := DetectedFilter(shape)
	if f == nil || f.Name() != "renders" {
		t.Fatalf("DetectedFilter(%q) = %v", shape, f)
	}
	if g, ok := f.(Guarded); !ok || !g.GuardsErrors() {
		t.Error("DetectedFilter lost the filter's Guarded interface")
	}
	for _, s := range []string{"", "json", "paths", "lines", "detected:", "detected:nope", "renders"} {
		if DetectedFilter(s) != nil {
			t.Errorf("DetectedFilter(%q) != nil", s)
		}
	}
}

func TestDetectNeverGrows(t *testing.T) {
	withDetectors(t, Detector{Name: "grows", Detect: marker, Argv: []string{"g"},
		Filter: detFake{"grows", func(_ *Context, s string) (string, bool) { return s + "\n" + s, true }}})
	in := "@@lx-detect-test@@\n" + strings.Repeat("x y z\n", 30)
	if _, shape := GenericShape(&Context{Argv: []string{"just", "test"}}, in); strings.HasPrefix(shape, DetectedPrefix) {
		t.Errorf("shape %q: a longer detected view was used", shape)
	}
}

func TestRegisterDetectorRejectsBadDetectors(t *testing.T) {
	withDetectors(t)
	ok := Detector{Name: "ok", Detect: marker, Argv: []string{"ok"}, Filter: detFake{"ok", nil}}
	RegisterDetector(ok)
	for name, d := range map[string]Detector{
		"duplicate":  ok,
		"no name":    {Detect: marker, Argv: []string{"x"}, Filter: detFake{"x", nil}},
		"space":      {Name: "a b", Detect: marker, Argv: []string{"x"}, Filter: detFake{"x", nil}},
		"no detect":  {Name: "x", Argv: []string{"x"}, Filter: detFake{"x", nil}},
		"no filter":  {Name: "x", Detect: marker, Argv: []string{"x"}},
		"empty argv": {Name: "x", Detect: marker, Filter: detFake{"x", nil}},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: RegisterDetector did not panic", name)
				}
			}()
			RegisterDetector(d)
		}()
	}
	if n := len(Detectors()); n != 1 {
		t.Errorf("%d detectors registered, want 1", n)
	}

	argv := []string{"tool"}
	RegisterDetector(Detector{Name: "copy", Detect: marker, Argv: argv, Filter: detFake{"copy", nil}})
	argv[0] = "changed"
	if ds := Detectors(); ds[1].Argv[0] != "tool" {
		t.Errorf("registered Argv aliases the caller's slice: %q", ds[1].Argv)
	}
}

func TestScanLines(t *testing.T) {
	long := strings.Repeat("x", MaxSignatureLine) + " Tests: 1 total"
	s := "Tests: first\nmiddle Tests: 2\n" + long + "\nlast Tests: 3"
	var got []string
	ScanLines(s, "Tests: ", func(ln string) bool { got = append(got, ln); return true })
	if strings.Join(got, "|") != "Tests: first|middle Tests: 2|last Tests: 3" {
		t.Errorf("ScanLines visited %q (the long line must be skipped)", got)
	}
	got = nil
	ScanLines(s, "Tests: ", func(ln string) bool { got = append(got, ln); return false })
	if len(got) != 1 {
		t.Errorf("ScanLines did not stop: %q", got)
	}
	got = nil
	ScanLines("a\n\nb", "", func(ln string) bool { got = append(got, ln); return true })
	if strings.Join(got, "|") != "a||b" {
		t.Errorf("ScanLines with no sub visited %q", got)
	}
	ScanLines("", "x", func(string) bool { t.Error("visited a line of empty input"); return true })

	if !HasLine(s, "Tests: ", func(ln string) bool { return strings.HasPrefix(ln, "last") }) {
		t.Error("HasLine missed the last line")
	}
	if HasLine(s, "Tests: 1 total", func(string) bool { return true }) {
		t.Error("HasLine matched a line over MaxSignatureLine")
	}

	p := "ok  \ta\t1s\nxok  \tb\nok  \tc\t2s\n" + "ok  \t" + strings.Repeat("y", MaxSignatureLine) + "\nok  \td"
	if n := CountLinePrefix(p, "ok  \t", nil, 0); n != 3 {
		t.Errorf("CountLinePrefix = %d, want 3 (first line, a middle line and the last line; not mid-line, not long)", n)
	}
	if n := CountLinePrefix(p, "ok  \t", nil, 2); n != 2 {
		t.Errorf("CountLinePrefix with limit 2 = %d", n)
	}
	if n := CountLinePrefix(p, "ok  \t", func(ln string) bool { return strings.HasSuffix(ln, "s") }, 0); n != 2 {
		t.Errorf("CountLinePrefix with match = %d, want 2", n)
	}
	if !HasLinePrefix("x\nFAIL\tpkg", "FAIL\t", nil) || HasLinePrefix("x FAIL\tpkg", "FAIL\t", nil) || HasLinePrefix(p, "", nil) {
		t.Error("HasLinePrefix")
	}
}

func TestPositionalWords(t *testing.T) {
	got := strings.Join(positionalWords([]string{"go", "test", "-list", ".", "--run=x", "-"}), " ")
	if got != "go test . -" {
		t.Errorf("positionalWords = %q", got)
	}
	if got := positionalWords([]string{"-weird"}); len(got) != 1 {
		t.Errorf("argv[0] must stay: %q", got)
	}
}

func TestRelabelExitNotes(t *testing.T) {
	tool := []string{"vitest", "run"}
	for _, c := range []struct{ view, clean, cmd, want string }{
		{"a\n[lx: vitest exited 1 but reported no failing test; see the lines above or the full output]", "a", "just",
			"a\n[lx: just exited 1 but reported no failing test; see the lines above or the full output]"},
		{"[lx: vitest run exited 2]", "", "rake", "[lx: rake exited 2]"},
		{"[lx: vitest exit 5: no tests were run]", "", "task", "[lx: task exit 5: no tests were run]"},

		{"[lx: vitest exited 1]", "x\n[lx: vitest exited 1]\n", "just", "[lx: vitest exited 1]"},
		{"[lx: 3 passing tests hidden]", "", "just", "[lx: 3 passing tests hidden]"},
		{"[lx: vitestx exited 1]", "", "just", "[lx: vitestx exited 1]"},
		{"[lx: vitest exits]", "", "just", "[lx: vitest exits]"},
		{"[lx: vitest exited 1]", "", "vitest", "[lx: vitest exited 1]"},
		{"[lx: vitest exited 1]", "", "", "[lx: vitest exited 1]"},
		{"  [lx: vitest exited 1]", "", "just", "  [lx: vitest exited 1]"},
	} {
		if got := relabelExitNotes(c.view, c.clean, tool, c.cmd); got != c.want {
			t.Errorf("relabelExitNotes(%q, cmd %q) = %q, want %q", c.view, c.cmd, got, c.want)
		}
	}
	if got := relabelExitNotes("[lx: x exited 1]", "", nil, "just"); got != "[lx: x exited 1]" {
		t.Errorf("no tool argv: %q", got)
	}
}

func TestCommandLabel(t *testing.T) {
	for argv, want := range map[string]string{
		"just test": "just", "timeout 600 just test": "just", "env -u X /usr/local/bin/task lint": "task",
		"sh scripts/ci.sh": "sh", "./scripts/ci.sh": "ci.sh", "": "",
	} {
		if got := commandLabel(&Context{Argv: strings.Fields(argv)}); got != want {
			t.Errorf("commandLabel(%q) = %q, want %q", argv, got, want)
		}
	}
}

func TestDetectKeepsTheViewThatLosesLeast(t *testing.T) {
	in := "@@lx-detect-test@@\nfirst failure at src/a.ts:12:5\nsecond failure at src/b.ts:7:1\nlibrary at node_modules/x/y.js:3:1"
	keep := func(drop ...string) func(*Context, string) (string, bool) {
		return func(_ *Context, s string) (string, bool) {
			var out []string
			for _, ln := range strings.Split(s, "\n") {
				dropped := false
				for _, d := range drop {
					dropped = dropped || strings.Contains(ln, d)
				}
				if !dropped {
					out = append(out, ln)
				}
			}
			return strings.Join(out, "\n"), true
		}
	}
	c := &Context{Argv: []string{"just", "test"}}
	for _, tc := range []struct {
		name string
		ds   []Detector
		want string
	}{
		{"a later view keeps more", []Detector{
			{Name: "caps", Detect: marker, Argv: []string{"caps"}, Filter: detFake{"caps", keep("src/a.ts", "src/b.ts")}},
			{Name: "folds-one", Detect: marker, Argv: []string{"folds"}, Filter: detFake{"folds", keep("src/b.ts")}},
			{Name: "keeps", Detect: marker, Argv: []string{"keeps"}, Filter: detFake{"keeps", keep("node_modules")}},
		}, "keeps"},
		{"a tie goes to registration order", []Detector{
			{Name: "first", Detect: marker, Argv: []string{"first"}, Filter: detFake{"first", keep("library")}},
			{Name: "second", Detect: marker, Argv: []string{"second"}, Filter: detFake{"second", keep()}},
		}, "first"},
		{"a later view that loses more never wins", []Detector{
			{Name: "keeps", Detect: marker, Argv: []string{"keeps"}, Filter: detFake{"keeps", keep()}},
			{Name: "caps", Detect: marker, Argv: []string{"caps"}, Filter: detFake{"caps", keep("src/")}},
		}, "keeps"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withDetectors(t, tc.ds...)
			if _, name, ok := Detect(c, in); !ok || name != tc.want {
				t.Errorf("Detect chose %q (ok=%v), want %q", name, ok, tc.want)
			}
		})
	}

	var pad strings.Builder
	for i := range 400 {
		fmt.Fprintf(&pad, "verbose line %d of a passing run with nothing in it\n", i)
	}
	long := "@@lx-detect-test@@\n" + pad.String() + "handler at src/a.ts:12:5\nrouter at src/b.ts:7:1\n" + pad.String() + "done"
	withDetectors(t,
		Detector{Name: "condenses", Detect: marker, Argv: []string{"condenses"}, Filter: detFake{"condenses",
			func(*Context, string) (string, bool) {
				return "handler at src/a.ts:12:5\n[800 verbose lines hidden]\ndone", true
			}}},
		Detector{Name: "verbatim", Detect: marker, Argv: []string{"verbatim"}, Filter: detFake{"verbatim",
			func(_ *Context, s string) (string, bool) { return s, true }}},
	)
	if _, name, _ := Detect(&Context{Argv: []string{"just", "test"}, Budget: 300}, long); name != "condenses" {
		t.Errorf("Detect chose %q: a view the budget cuts won", name)
	}
	if _, name, _ := Detect(&Context{Argv: []string{"just", "test"}, Budget: 1 << 20}, long); name != "verbatim" {
		t.Errorf("Detect chose %q under a budget that cuts nothing", name)
	}
}
