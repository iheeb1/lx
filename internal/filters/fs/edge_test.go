package fs

import (
	"fmt"
	"github.com/iheeb1/lx/internal/testenv"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

func ctx(argv ...string) *engine.Context {
	return &engine.Context{Argv: argv, Cwd: "/home/user/src/app", Home: "/home/user"}
}

func TestEffective(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"bash", "-c", "find . -name '*.js' -not -path './.git/*'"}, "find|.|-name|*.js|-not|-path|./.git/*"},
		{[]string{"/bin/sh", "-c", `grep -rn "a b" src 2>&1 | head -50`}, "grep|-rn|a b|src"},
		{[]string{"bash", "-lc", "du -sh node_modules/* | sort -h | tail -40"}, "du|-sh|node_modules/*"},
		{[]string{"zsh", "-c", "FOO=1 ls -la"}, "ls|-la"},
		{[]string{"bash", "-c", `rg "x\"y" .`}, `rg|x"y|.`},
		{[]string{"bash", "-c", "find . | xargs grep x"}, ""},
		{[]string{"bash", "-c", "cd src && ls"}, ""},
		{[]string{"bash", "-c", "ls > out.txt"}, ""},
		{[]string{"bash", "-c", "ls $HOME"}, ""},
		{[]string{"bash", "-c", "ls; rm x"}, ""},
		{[]string{"bash", "-c", "tail -f log | grep x"}, ""},
		{[]string{"bash", "-c", "ls | tail -f"}, ""},
		{[]string{"bash", "-c", "ls 'unterminated"}, ""},
		{[]string{"bash", "-o", "pipefail", "-c", "ls"}, ""},
		{[]string{"python3", "-c", "print(1)"}, ""},
		{[]string{"bash", "script.sh"}, ""},
	}
	for _, tc := range cases {
		c := ctx(tc.argv...)
		e := Effective(c)
		got := ""
		if e != c {
			got = strings.Join(e.Argv, "|")
		}
		if got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.argv, got, tc.want)
		}
	}
}

func TestMatchSpecific(t *testing.T) {
	want := map[string]string{
		"ls -la":                       "ls",
		"/bin/ls -l":                   "ls",
		"find . -type f":               "find",
		"/usr/bin/find src":            "find",
		"fd -e go":                     "find",
		"du -sh *":                     "du",
		"tree -L 2":                    "tree",
		"find . -print0":               "",
		"find . -exec grep x {} ;":     "",
		"find . -printf %p":            "",
		"find . -ls":                   "",
		"fd -x rm":                     "",
		"du --time -sh":                "",
		"du -0 .":                      "",
		"tree -J":                      "",
		"tree -h":                      "",
		"tree -i":                      "",
		"ls --help":                    "",
		"lsof -i":                      "",
		"git ls-files":                 "",
		"treefmt":                      "",
		"dust":                         "",
		"findstr x":                    "",
		"python -m http.server":        "",
		"bash -c ls -la":               "ls",
		"du -sh --null":                "",
		"tree --du":                    "",
		"ls --zero":                    "",
		"tree -X":                      "",
		"tree -o out.txt":              "",
		"tree -C":                      "tree",
		"fd --format {}":               "",
		"find / -name x -fprint out":   "",
		"find . -name x -delete":       "",
		"find . -type f -execdir x ;":  "",
		"fdfind -t d":                  "find",
		"gfind . -type d":              "find",
		"gls -la":                      "ls",
		"gdu -sh":                      "du",
		"tree -a -I node_modules -L 3": "tree",
	}
	for cmd, name := range want {
		c := ctx(strings.Fields(cmd)...)
		var got string
		for _, f := range []engine.Filter{ls{}, find{}, du{}, treeCmd{}} {
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

func TestEmptyAndBlank(t *testing.T) {
	for _, tc := range []struct {
		f    engine.Filter
		argv []string
	}{
		{ls{}, []string{"ls", "-la"}}, {ls{}, []string{"ls"}}, {ls{}, []string{"ls", "-R"}},
		{find{}, []string{"find", "."}}, {du{}, []string{"du", "-sh", "x"}}, {treeCmd{}, []string{"tree"}},
	} {
		for _, in := range []string{"", "\n", "   "} {
			if out, ok := tc.f.Apply(ctx(tc.argv...), in); ok {
				t.Errorf("%v on %q: ok with %q", tc.argv, in, out)
			}
		}
	}
}

func TestAmbiguousNamesQuoted(t *testing.T) {
	lines, _ := NewPathTree([]string{"d/a  b.txt", "d/ lead.txt", "d/plain.txt"}, nil).Render(0)
	got := strings.Join(lines, "\n")
	if got != "d/\n  \" lead.txt\"  \"a  b.txt\"  plain.txt" {
		t.Fatalf("got %q", got)
	}
}

func TestFindArgs(t *testing.T) {
	cases := []struct {
		args     string
		roots    string
		dirsOnly bool
	}{
		{". -type f", ".", false},
		{"src test -type d", "src|test", true},
		{"-L . -type d -name x", ".", true},
		{". ! -type d", ".", false},
		{". -not -type d", ".", false},
		{". -type d -o -type f", ".", false},
		{". ( -type d )", ".", true},
		{"", "", false},
	}
	for _, tc := range cases {
		roots, dirs := findArgs(strings.Fields(tc.args))
		if strings.Join(roots, "|") != tc.roots || dirs != tc.dirsOnly {
			t.Errorf("findArgs(%q) = %q, %v", tc.args, roots, dirs)
		}
	}
}

func TestSingleLine(t *testing.T) {
	out, ok := find{}.Apply(ctx("find", ".", "-name", "main.go"), "./cmd/app/main.go")
	if !ok || !strings.Contains(out, "cmd/app/") || !strings.Contains(out, "main.go") {
		t.Fatalf("got %q ok=%v", out, ok)
	}
	out, ok = du{}.Apply(ctx("du", "-sh", "."), "4.0K\t.")
	if !ok || out != "4.0K\t." {
		t.Fatalf("got %q ok=%v", out, ok)
	}
	out, ok = ls{}.Apply(ctx("ls", "-l", "go.mod"), "-rw-r--r--  1 user  staff  1458 Sep 26 00:47 go.mod")
	if !ok || !strings.Contains(out, "1458 Sep 26 00:47 go.mod") {
		t.Fatalf("got %q ok=%v", out, ok)
	}
}

func TestUnknownBails(t *testing.T) {
	cases := []struct {
		f    engine.Filter
		argv []string
		in   string
	}{

		{ls{}, []string{"ls", "-la"}, "total 8\ndrwxr-xr-x  3 user  staff  96 26 sept. 00:48 .\n-rw-r--r--  1 user  staff  10 26 sept. 00:48 a.go"},

		{ls{}, []string{"ls", "-l", "--time-style=+%s"}, "-rw-r--r-- 1 user user 10 1758850080 a.go\n-rw-r--r-- 1 user user 10 1758850080 b.go"},

		{ls{}, []string{"ls", "-li"}, "123 -rw-r--r-- 1 user user 10 Sep 26 00:48 a.go"},

		{ls{}, []string{"ls", "-l"}, "-rw-r--r-- 1 user staff 10 Sep 26 00:48 a.go\n-rw-r--r-- 1 DOMAIN user staff 10 Sep 26 00:48 b.go"},

		{du{}, []string{"du", "-sh", "x"}, "usage: du [-H | -L | -P] [-a | -s | -d depth]"},
		{du{}, []string{"du", "-sh", "x"}, "4.0K\tx\nsomething else"},

		{treeCmd{}, []string{"tree"}, ".\n├── a.go\n└── b.go\n\n0 répertoire, 2 fichiers"},

		{treeCmd{}, []string{"tree"}, ".\na.go\nb.go"},

		{find{}, []string{"find", "nosuch"}, "find: nosuch: No such file or directory"},
		{ls{}, []string{"ls", "-R"}, "a\nb"},
	}
	for _, tc := range cases {
		if out, ok := tc.f.Apply(ctx(tc.argv...), tc.in); ok {
			t.Errorf("%v must bail on %q, got\n%s", tc.argv, tc.in, out)
		}
	}
}

func TestFailureKeepsDiagnostics(t *testing.T) {
	var b strings.Builder
	for i := range 200 {
		fmt.Fprintf(&b, "./src/pkg%d/file.go\n", i)
		if i == 100 {
			b.WriteString("find: ./src/locked: Permission denied\n")
		}
	}
	c := ctx("find", ".")
	c.Exit = 1
	out, ok := find{}.Apply(c, b.String())
	if !ok || !strings.HasPrefix(out, "find: ./src/locked: Permission denied\n") {
		t.Fatalf("diagnostic not first:\n%s", out)
	}
	res := engine.Process(c, b.String(), engine.Options{})
	if !strings.Contains(res.Output, "find: ./src/locked: Permission denied") {
		t.Fatal("diagnostic lost in the pipeline")
	}
	for _, bad := range []string{"No issues", "no errors", "success", "✓"} {
		if strings.Contains(res.Output, bad) {
			t.Errorf("pass-like text %q in failing output", bad)
		}
	}

	c = ctx("ls", "-la", "nope", "src")
	c.Exit = 2
	in := "ls: cannot access 'nope': No such file or directory\nsrc:\ntotal 8\n-rw-r--r-- 1 user user 10 Sep 26 00:48 a.go\n-rw-r--r-- 1 user user 12 Sep 26 00:48 b.go"
	out, ok = ls{}.Apply(c, in)
	if !ok || !strings.HasPrefix(out, "ls: cannot access 'nope': No such file or directory\n") {
		t.Fatalf("got\n%s", out)
	}
}

func TestNoteFlood(t *testing.T) {
	var b strings.Builder
	for i := range 5000 {
		fmt.Fprintf(&b, "find: /proc/%d/fd: Permission denied\n", i)
		if i%1000 == 0 {
			fmt.Fprintf(&b, "/home/user/f%d.txt\n", i)
		}
	}
	b.WriteString("find: /mnt/x: No such file or directory")
	c := ctx("find", "/", "-name", "f*.txt")
	c.Exit = 1
	out, ok := find{}.Apply(c, b.String())
	if !ok {
		t.Fatal("bailed")
	}
	if !strings.Contains(out, "… +4,971 more: Permission denied ×4,970, No such file or directory ×1") {
		t.Fatalf("flood not counted:\n%s", out)
	}
	if n := strings.Count(out, "\n"); n > 60 {
		t.Errorf("%d lines", n)
	}
}

func TestLsGNU(t *testing.T) {
	in := `total 24
drwxr-xr-x 3 user user 4096 2026-09-26 00:48 .
drwxr-xr-x 9 user user 4096 2026-09-26 00:40 ..
-rwxr-xr-x 1 user user 2031 2026-09-26 00:48 build.sh
-rw-r--r-- 1 user user  120 2026-09-26 00:48 my file.txt
lrwxrwxrwx 1 user user    6 2026-09-26 00:48 latest -> v1.2.3
crw-rw-rw- 1 root root 1, 3 2026-09-01 10:00 null
-rw-r--r-- 1 user user  880 2026-09-26 00:48 z.go`
	out, ok := ls{}.Apply(ctx("ls", "-la", "--time-style=long-iso"), in)
	if !ok {
		t.Fatal("bailed on GNU long-iso listing")
	}
	for _, want := range []string{
		"-rwxr-xr-x 2031 2026-09-26 00:48 build.sh",
		"owner user, group user unless shown",
		"120 2026-09-26 00:48 my file.txt",
		"6 2026-09-26 00:48 latest -> v1.2.3",
		"crw-rw-rw- root root 1, 3 2026-09-01 10:00 null",
		"4096 2026-09-26 00:48 ./",
		"mode -rw-r--r-- files, drwxr-xr-x dirs unless shown",
		"total 24",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "user user 4096") {
		t.Errorf("majority owner/group repeated on rows:\n%s", out)
	}
}

func TestLsHumanAndOld(t *testing.T) {
	in := "total 12K\n-rw-r--r--  1 user  staff   4.0K Jan  3  2024 old.txt\n-rw-r--r--  1 user  staff    12K Sep 26 00:48 new.txt"
	out, ok := ls{}.Apply(ctx("ls", "-lh"), in)
	if !ok || !strings.Contains(out, "4.0K Jan  3  2024 old.txt") || !strings.Contains(out, "12K Sep 26 00:48 new.txt") {
		t.Fatalf("got ok=%v\n%s", ok, out)
	}
}

func TestLsManyEntriesNamesOnly(t *testing.T) {
	var b strings.Builder
	b.WriteString("total 400\n")
	for i := range 600 {
		kind := "-"
		if i%3 == 0 {
			kind = "d"
		}
		fmt.Fprintf(&b, "%srw-r--r--  1 user  staff  %d Sep 26 00:48 entry%03d\n", kind, 100+i, i)
	}
	out, ok := ls{}.Apply(ctx("ls", "-l"), strings.TrimSpace(b.String()))
	if !ok {
		t.Fatal("bailed")
	}
	if !strings.Contains(out, "600 entries: 200 dirs (marked /), 400 files") || !strings.Contains(out, "entry000/") || !strings.Contains(out, "entry599") ||
		!strings.Contains(out, "names only: modes, link counts, sizes and dates dropped (~") {
		t.Fatalf("got\n%s", out)
	}
}

func TestLsModerateListingKeepsLongForm(t *testing.T) {
	var b strings.Builder
	b.WriteString("total 400\n")
	for i := range 120 {
		fmt.Fprintf(&b, "-rw-r--r--  1 user  staff  %d Sep %d 00:48 file%03d.log\n", 1000*i, 1+i%28, i)
	}
	out, ok := ls{}.Apply(ctx("ls", "-lS"), strings.TrimSpace(b.String()))
	if !ok || strings.Contains(out, "names only") || !strings.Contains(out, "119000 Sep 8 00:48 file119.log") {
		t.Fatalf("got ok=%v\n%s", ok, out)
	}
}

func TestDuCaps(t *testing.T) {
	var b strings.Builder
	for i := range 100 {
		fmt.Fprintf(&b, "%dK\tdir%02d\n", (i*37)%100+1, i)
	}
	b.WriteString("du: dir99/locked: Permission denied\n")
	b.WriteString("5000K\ttotal")
	c := ctx("du", "-shc", "*")
	out, ok := du{}.Apply(c, b.String())
	if !ok {
		t.Fatal("bailed")
	}
	lines := strings.Split(out, "\n")
	if lines[0] != "du: dir99/locked: Permission denied" {
		t.Errorf("diagnostic not first: %q", lines[0])
	}
	if lines[len(lines)-2] != "5000K\ttotal" {
		t.Errorf("-c total not kept last: %q", lines[len(lines)-2])
	}
	if !strings.HasPrefix(lines[len(lines)-1], "… 70 smaller entries omitted (each ≤ ") || !strings.Contains(lines[len(lines)-1], "together") {
		t.Errorf("marker: %q", lines[len(lines)-1])
	}
	if len(lines) != 1+30+1+1 {
		t.Errorf("%d lines", len(lines))
	}

	prev := -1
	for _, ln := range lines[1:31] {
		var n int
		fmt.Sscanf(ln[strings.Index(ln, "dir")+3:], "%d", &n)
		if n < prev {
			t.Errorf("order changed at %q", ln)
		}
		prev = n
	}
}

func TestTreeCommand(t *testing.T) {

	in := ".\n" +
		"├── LICENSE\n" +
		"├── README.md\n" +
		"├── empty\n" +
		"├── node_modules\n" +
		"│   ├── a\n" +
		"│   │   └── index.js\n" +
		"│   └── b\n" +
		"│       └── index.js\n" +
		"├── secret [error opening dir]\n" +
		"└── src\n" +
		"    ├── main.go\n" +
		"    └── util\n" +
		"        └── strings.go\n" +
		"\n" +
		"7 directories, 6 files"
	out, ok := treeCmd{}.Apply(ctx("tree"), in)
	if !ok {
		t.Fatal("bailed")
	}

	want := "├── secret [error opening dir]\n" +
		"[tree: 1 of the names without / is a directory (tree -F marks them)]\n" +
		"LICENSE  README.md  empty\n" +
		"node_modules/ [2 entries]\n" +
		"secret/\n" +
		"src/\n" +
		"  main.go\n" +
		"  util/\n" +
		"    strings.go\n" +
		"7 directories, 6 files"
	if out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}

	ascii := "src\n|-- a.go\n`-- sub\n    `-- b.go\n\n1 directory, 2 files"
	out, ok = treeCmd{}.Apply(ctx("tree", "--charset=ascii", "src"), ascii)
	if ok {
		t.Fatalf("--charset is excluded by Match, got %q", out)
	}
	out, ok = treeCmd{}.Apply(ctx("tree", "src"), ascii)
	if !ok || out != "src/\n  a.go\n  sub/\n    b.go\n1 directory, 2 files" {
		t.Fatalf("ascii: ok=%v\n%s", ok, out)
	}

	dirs := ".\n├── a\n│   └── b\n└── c\n\n3 directories"
	out, ok = treeCmd{}.Apply(ctx("tree", "-d"), dirs)
	if !ok || out != "a/b/\nc/\n3 directories" {
		t.Fatalf("-d: ok=%v\n%s", ok, out)
	}
}

func TestHugeListings(t *testing.T) {
	var ls1, du1, tr strings.Builder
	ls1.WriteString("total 99999\n")
	tr.WriteString(".\n")
	for i := range 50000 {
		fmt.Fprintf(&ls1, "-rw-r--r--  1 user  staff  %d Sep 26 00:48 file%05d.txt\n", i, i)
		fmt.Fprintf(&du1, "%d\t./d%d/e%d\n", i%977, i%50, i)
		fmt.Fprintf(&tr, "├── f%05d.go\n", i)
	}
	tr.WriteString("└── last.go\n\n0 directories, 50001 files")
	for _, tc := range []struct {
		f    engine.Filter
		argv []string
		in   string
	}{
		{ls{}, []string{"ls", "-l"}, ls1.String()},
		{du{}, []string{"du", "."}, du1.String()},
		{treeCmd{}, []string{"tree"}, tr.String()},
	} {
		start := time.Now()
		out, ok := tc.f.Apply(ctx(tc.argv...), strings.TrimSpace(tc.in))
		if !ok {
			t.Errorf("%v bailed", tc.argv)
			continue
		}
		if el := time.Since(start); el > testenv.Scale(3*time.Second) {
			t.Errorf("%v took %v", tc.argv, el)
		}
		t.Logf("%v: 50k lines → %d tokens", tc.argv, tokens.Count(out))
	}
}

func FuzzFilters(f *testing.F) {
	for _, dir := range []string{filepath.Join(fixture.Root(), "testdata", "corpus"), "testdata/corpus"} {
		cases, _ := fixture.ReadAll(dir)
		for _, c := range cases {
			if c.Category != "fs" {
				continue
			}
			s := c.Clean()
			if len(s) > 1<<14 {
				s = s[:1<<14]
			}
			f.Add(s, uint8(len(c.Name)))
		}
	}
	f.Add("├── a\n│   └── b\n└── c", uint8(9))
	f.Add("/usr/bin/find: ‘x’: No such file or directory\n./a\n./b/c", uint8(12))
	f.Add(".:\na\n\n./a:\nb\n\n./a/b:\n/bin/ls: cannot open directory './a/b': Permission denied", uint8(13))
	f.Add(".\n├── cmd/\n│   └── lx/\n└── go.mod\n\n2 directories, 1 file", uint8(16))
	contexts := [][]string{
		{"ls", "-la"}, {"ls", "-R", "src"}, {"ls", "-lR"}, {"ls"}, {"find", "."}, {"find", ".", "-type", "d"},
		{"fd"}, {"du", "-sh", "*"}, {"du", "-h", "."}, {"tree"}, {"tree", "-d", "src"}, {"ls", "-l", "a", "b"},
		{"/usr/bin/find", ".", "-maxdepth", "2"}, {"/bin/ls", "-R"}, {"bash", "-c", "find . -type f | sort | head -20"},
		{"bash", "-c", "du -sh * | sort -rh | head"}, {"tree", "-F", "-L", "2"}, {"fd", "-t", "f", "--max-depth", "3"},
		{"ls", "-lt"}, {"find", "src", "-name", "*.go", "-o", "-type", "d"},
	}
	filters := []engine.Filter{ls{}, find{}, du{}, treeCmd{}}
	f.Fuzz(func(t *testing.T, in string, which uint8) {
		c := ctx(contexts[int(which)%len(contexts)]...)

		c.Exit = []int{0, 0, 1, 137}[int(which>>6)]
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
			if !okA || flt.Name() == "tree" {
				continue
			}

			e := Effective(c)
			var diags []string
			for _, ln := range strings.Split(in, "\n") {
				if NoteHasPrefix(e, ln, flt.Name()) {
					diags = append(diags, ln)
				}
			}
			if len(diags) <= maxNotes {
				for _, d := range diags {
					if !strings.Contains(a, d) {
						t.Fatalf("%s dropped diagnostic %q:\n%s", flt.Name(), d, a)
					}
				}
			}
		}
	})
}
