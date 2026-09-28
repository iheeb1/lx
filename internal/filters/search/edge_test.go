package search

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/filters/fs"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

func ctx(argv ...string) *engine.Context {
	return &engine.Context{Argv: argv, Cwd: "/home/user/src/app", Home: "/home/user"}
}

func TestMatchSpecific(t *testing.T) {
	want := map[string]string{
		"grep -rn x .":                   "search",
		"/usr/bin/grep -n x a.go":        "search",
		"egrep -r a|b src":               "search",
		"fgrep -rn a.b .":                "search",
		"git grep -n x":                  "search",
		"git -C sub grep x":              "search",
		"git --no-pager grep -n x":       "search",
		"rg x":                           "search",
		"rg -n -C2 x src":                "search",
		"rg --vimgrep x":                 "search",
		"rg --files":                     "rg-files",
		"rg --files src":                 "rg-files",
		"rg -l x":                        "",
		"rg --count x":                   "",
		"rg --json x":                    "",
		"rg --heading x":                 "",
		"rg -p x":                        "",
		"rg --stats x":                   "",
		"rg --type-list":                 "",
		"rg -h":                          "",
		"rg --files --null":              "",
		"grep -c x f":                    "",
		"grep -l x f":                    "",
		"grep -rlZ x .":                  "",
		"grep -q x f":                    "",
		"grep -b x f":                    "",
		"grep --help":                    "",
		"grep -V":                        "",
		"git grep --heading x":           "",
		"git grep -p x":                  "",
		"git grep -c x":                  "",
		"git log --grep x":               "",
		"git status":                     "",
		"ripgrep x":                      "",
		"grepcidr 10.0.0.0/8":            "",
		"bash -c grep":                   "search",
		"grep -rn --include=*.go x .":    "search",
		"grep -A 3 -B2 -e x -e y f":      "search",
		"rg -A3 -e x --glob *.ts":        "search",
		"grep --context 3 x f":           "search",
		"grep --null-data x f":           "",
		"git grep --open-files-in-pager": "",
	}
	for cmd, name := range want {
		c := ctx(strings.Fields(cmd)...)
		got := ""
		for _, f := range []engine.Filter{rgFiles{}, matches{}} {
			if f.Match(c) {
				got = f.Name()
				break
			}
		}
		if got != name {
			t.Errorf("%q matched %q, want %q", cmd, got, name)
		}
	}
}

func TestOptsShape(t *testing.T) {
	cases := []struct {
		cmd                       string
		withFile                  int
		numbered, context, column bool
		pattern                   string
	}{
		{"grep -rn Context .", 1, true, false, false, "Context"},
		{"grep -n x a.go", -1, true, false, false, "x"},
		{"grep -n x a.go b.go", 1, true, false, false, "x"},
		{"grep -hn x a.go b.go", -1, true, false, false, "x"},
		{"grep -Hn x a.go", 1, true, false, false, "x"},
		{"grep -rn -C2 x .", 1, true, true, false, "x"},
		{"grep -rn -2 x .", 1, true, true, false, "x"},
		{"grep -rne x -e y .", 1, true, false, false, "x"},
		{"grep -n x *.go", 0, true, false, false, "x"},
		{"rg x", 1, false, false, false, "x"},
		{"rg -n x src", 1, true, false, false, "x"},
		{"rg -n x a.go", -1, true, false, false, "x"},
		{"rg -nN x", 1, false, false, false, "x"},
		{"rg --vimgrep x", 1, true, false, true, "x"},
		{"rg --column x .", 1, true, false, true, "x"},
		{"rg -C 0 x", 1, false, false, false, "x"},
		{"rg -I -n x src", -1, true, false, false, "x"},
		{"git grep -n x", 1, true, false, false, "x"},
		{"git grep -hn x", -1, true, false, false, "x"},
		{"git grep -W x", 1, false, true, false, "x"},
	}
	for _, tc := range cases {
		e := ctx(strings.Fields(tc.cmd)...)
		tl, args := tool(e)
		o := parseOpts(tl, e.Name(), args)
		if o.withFile != tc.withFile || o.numbered != tc.numbered || o.context != tc.context || o.column != tc.column ||
			len(o.patterns) == 0 || o.patterns[0] != tc.pattern {
			t.Errorf("%q: withFile=%d numbered=%v context=%v column=%v patterns=%q", tc.cmd, o.withFile, o.numbered, o.context, o.column, o.patterns)
		}
	}
}

func TestEmptyAndUnknown(t *testing.T) {
	cases := []struct {
		argv []string
		in   string
	}{
		{[]string{"grep", "-rn", "x", "."}, ""},
		{[]string{"grep", "-rn", "x", "."}, "\n"},
		{[]string{"grep", "-rn", "x", "."}, "Usage: grep [OPTION]... PATTERNS [FILE]...\nTry 'grep --help' for more information."},
		{[]string{"rg", "-n", "x"}, "error: unexpected argument '--foo' found\n\nUsage: rg [OPTIONS] PATTERN [PATH ...]"},

		{[]string{"grep", "-rn", "x", "."}, "x marks the spot\nand again x"},

		{[]string{"grep", "-rn", "x", "src"}, "other/a.go:1:x\nother/b.go:2:x"},
		{[]string{"rg", "--files"}, ""},
	}
	for _, tc := range cases {
		c := ctx(tc.argv...)
		for _, f := range []engine.Filter{rgFiles{}, matches{}} {
			if !f.Match(c) {
				continue
			}
			if out, ok := f.Apply(c, tc.in); ok {
				t.Errorf("%v on %q: must bail, got\n%s", tc.argv, tc.in, out)
			}
		}
	}
}

func TestSingleLine(t *testing.T) {
	out, ok := matches{}.Apply(ctx("grep", "-rn", "x", "."), "./a.go:12:\tx := 1")
	if !ok || out != "./a.go:12:x := 1" {
		t.Fatalf("got %q ok=%v", out, ok)
	}
}

func TestFailureKeepsDiagnostics(t *testing.T) {
	var b strings.Builder
	b.WriteString("grep: nosuch: No such file or directory\n")
	for i := range 300 {
		fmt.Fprintf(&b, "src/f%d.go:%d:// ok  all tests passed %d\n", i%40, i+1, i)
	}
	b.WriteString("grep: src/locked: Permission denied")
	c := ctx("grep", "-rn", "passed", "nosuch", "src")
	c.Exit = 2
	out, ok := matches{}.Apply(c, b.String())
	if !ok {
		t.Fatal("bailed")
	}
	lines := strings.Split(out, "\n")
	if lines[1] != "grep: nosuch: No such file or directory" || lines[2] != "grep: src/locked: Permission denied" {
		t.Errorf("diagnostics not first:\n%s", strings.Join(lines[:4], "\n"))
	}
	res := engine.Process(c, b.String(), engine.Options{})
	for _, want := range []string{"grep: nosuch: No such file or directory", "grep: src/locked: Permission denied"} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("pipeline lost %q", want)
		}
	}
	if strings.Contains(lines[0], "no match") {
		t.Errorf("header claims no matches: %q", lines[0])
	}
}

func TestContextMisparse(t *testing.T) {
	in := "a.go-11-// started\na.go-12-log at 10:30:00 ok\na.go:13:target()\na.go-14-}\n--\nb.go:3:target()"
	c := ctx("rg", "-n", "-C1", "target")
	out, ok := matches{}.Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	want := "a.go\n11-// started\n12-log at 10:30:00 ok\n13:target()\n14-}\n\nb.go:3:target()"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}

func TestContextDedent(t *testing.T) {
	in := "x.go-9-\tfunc f() {\nx.go:10:\t\tif err != nil {\nx.go-11-\t\t\treturn err\nx.go-12-\t\t}\n--\nx.go:40:\t\tother()"
	out, ok := matches{}.Apply(ctx("grep", "-n", "-C1", "err", "x.go", "y.go"), in)
	if !ok {
		t.Fatal("bailed")
	}
	want := "x.go\n9-func f() {\n10:\tif err != nil {\n11-\t\treturn err\n12-\t}\n--\n40:other()"
	if out != want {
		t.Fatalf("got\n%q\nwant\n%q", out, want)
	}
}

func TestHeavyOnlyIsShown(t *testing.T) {
	var b strings.Builder
	for i := range 5 {
		fmt.Fprintf(&b, "./node_modules/pkg%d/index.js:%d:setHeader()\n", i, i+1)
	}
	out, ok := matches{}.Apply(ctx("grep", "-rn", "setHeader", "."), b.String())
	if !ok || strings.Contains(out, "summarized") || !strings.Contains(out, "./node_modules/pkg4/index.js:5:setHeader()") {
		t.Fatalf("dependency-only hits must be shown:\n%s", out)
	}
}

func TestBinaryNotesCapped(t *testing.T) {
	var b strings.Builder
	for i := range 50 {
		fmt.Fprintf(&b, "Binary file img/%d.png matches\n", i)
	}
	b.WriteString("src/a.go:1:PNG header\nsrc/a.go:2:PNG again")
	out, ok := matches{}.Apply(ctx("grep", "-rn", "PNG", "img", "src"), b.String())
	if !ok || !strings.Contains(out, "… +30 more binary files matched") || !strings.Contains(out, "Binary file img/19.png matches") ||
		strings.Contains(out, "Binary file img/20.png matches") {
		t.Fatalf("got\n%s", out)
	}
}

func TestWindow(t *testing.T) {
	long := strings.Repeat("a", 5000) + "process.env.NODE_ENV" + strings.Repeat("b", 5000)
	mt := newMatcher(opts{patterns: []string{"process.env.NODE_ENV"}, flavor: 'b'})
	got := window(long, mt)
	if !strings.Contains(got, "process.env.NODE_ENV") || !strings.HasPrefix(got, "…[+") || !strings.HasSuffix(got, "chars]…") {
		t.Fatalf("window missed the match: %q", got)
	}
	if n := len([]rune(got)); n > windowSize+40 {
		t.Errorf("window too long: %d", n)
	}

	got = window(long, nil)
	if !strings.HasPrefix(got, "aaaa") || !strings.HasSuffix(got, " …[+9860 chars]…") {
		t.Fatalf("got %q", got)
	}

	u := strings.Repeat("é", 300) + "x" + strings.Repeat("ü", 300)
	got = window(u, newMatcher(opts{patterns: []string{"x"}, flavor: 'e'}))
	if !strings.Contains(got, "x") || strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("got %q", got)
	}

	if s := "short line"; window(s, mt) != s {
		t.Error("short line changed")
	}
}

func TestBRE(t *testing.T) {
	cases := map[string]string{
		`foo\|bar`:       `foo|bar`,
		`a\(b\)c`:        `a(b)c`,
		`x{2}`:           `x\{2\}`,
		`a+b?`:           `a\+b\?`,
		`\<word\>`:       `\bword\b`,
		`[(|)]`:          `[(|)]`,
		`*star`:          `\*star`,
		`func (c \*Ctx)`: `func \(c \*Ctx\)`,
	}
	for in, want := range cases {
		if got := breToRE2(in); got != want {
			t.Errorf("breToRE2(%q) = %q, want %q", in, got, want)
		}
	}
	m := newMatcher(opts{patterns: []string{`func (c \*Context)`}, flavor: 'b'})
	if _, _, ok := m.find("x func (c *Context) Bind()"); !ok {
		t.Error("BRE pattern with literal parens did not match")
	}
	if newMatcher(opts{patterns: []string{`(a)\1`}, flavor: 'e'}) != nil {
		t.Error("backreference should not compile under RE2")
	}
}

func TestHugeSearch(t *testing.T) {
	var b strings.Builder
	for i := range 50000 {
		fmt.Fprintf(&b, "./pkg%d/file%d.go:%d:\tvalue := compute(%d) // Context\n", i%100, i%5000, i, i)
	}
	c := ctx("grep", "-rn", "Context", ".")
	start := time.Now()
	out, ok := matches{}.Apply(c, strings.TrimSpace(b.String()))
	if !ok {
		t.Fatal("bailed")
	}
	if el := time.Since(start); el > 3*time.Second {
		t.Errorf("50k matches took %v", el)
	}
	if n := tokens.Count(out); n > engine.DefaultBudget {
		t.Errorf("output %d tokens, over the budget", n)
	}
	if !strings.Contains(out, "[50,000 matches in 5,000 files") || !strings.Contains(out, "more files with") {
		t.Errorf("header/tail missing:\n%s", out[:min(len(out), 600)])
	}
}

func TestRgFilesHuge(t *testing.T) {
	var b strings.Builder
	for i := range 50000 {
		fmt.Fprintf(&b, "src/m%d/s%d/f%d.ts\n", i%60, i%9, i)
	}
	out, ok := rgFiles{}.Apply(ctx("rg", "--files"), b.String())
	if !ok || tokens.Count(out) > 2*4000 || !strings.HasPrefix(out, "[50,000 files · large tree") {
		t.Fatalf("ok=%v tokens=%d\n%s", ok, tokens.Count(out), out[:min(len(out), 300)])
	}
}

func FuzzSearch(f *testing.F) {
	for _, dir := range []string{filepath.Join(fixture.Root(), "testdata", "corpus"), "testdata/corpus"} {
		cases, _ := fixture.ReadAll(dir)
		for _, c := range cases {
			if c.Category != "search" {
				continue
			}
			s := c.Clean()
			if len(s) > 1<<14 {
				s = s[:1<<14]
			}
			f.Add(s, uint8(len(c.Name)))
		}
	}
	f.Add("a.go-12-at 10:30:00\na.go:13:x\n--\nb.go-1-y", uint8(3))
	f.Add("/usr/bin/grep: warning: recursive search of stdin\na.go:x\nb.go:y", uint8(12))
	f.Add("logs/12:30:00.log:1:ERROR a\nlogs/12:30:00.log:3:ERROR b", uint8(0))
	f.Add("a.go:1:5:x x\na.go:1:7:x x\nimg.png: binary file matches (found \"\\0\" byte around offset 7)", uint8(6))
	contexts := [][]string{
		{"grep", "-rn", "x", "."}, {"grep", "-rn", "-C2", "x", "."}, {"grep", "-n", "x", "a.go"},
		{"rg", "-n", "x"}, {"rg", "x"}, {"rg", "-C1", "x"}, {"rg", "--vimgrep", "x"}, {"git", "grep", "-n", "x"},
		{"rg", "--files"}, {"grep", "-r", "x", "*.go"}, {"grep", "x"}, {"rg", "-n", "-C2", "(x", "src"},
		{"/usr/bin/grep", "-r", "x"}, {"grep", "-rv", "^#", "conf"}, {"grep", "-rno", "x.", "."},
		{"bash", "-c", "grep -rn x . | sort | head -50"}, {"git", "grep", "-n", "-W", "x", "HEAD"},
		{"rg", "-n", "--column", "-A1", "x", "src", "lib"}, {"egrep", "-rn", "a|b", "src/"},
	}
	filters := []engine.Filter{rgFiles{}, matches{}}
	f.Fuzz(func(t *testing.T, in string, which uint8) {
		c := ctx(contexts[int(which)%len(contexts)]...)
		c.Exit = []int{0, 0, 2, 137}[int(which>>6)]
		for _, flt := range filters {
			if !flt.Match(c) {
				continue
			}
			a, okA := flt.Apply(c, in)
			b, okB := flt.Apply(c, in)
			if a != b || okA != okB {
				t.Fatalf("%s not deterministic", flt.Name())
			}
			if !okA && a != "" {
				t.Fatalf("%s: ok=false with output", flt.Name())
			}
			if !okA || flt.Name() != "search" {
				continue
			}

			e := fs.Effective(c)
			tl, _ := tool(e)
			isNote := noteFunc(e, tl)
			var diags []string
			for _, ln := range strings.Split(in, "\n") {
				if isNote(ln) && !isBinaryNote(ln) {
					diags = append(diags, ln)
				}
			}
			if len(diags) <= 40 {
				for _, d := range diags {
					if !strings.Contains(a, d) {
						t.Fatalf("dropped diagnostic %q:\n%s", d, a)
					}
				}
			}
		}
	})
}
