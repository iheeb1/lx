package fs

import (
	"fmt"
	"github.com/iheeb1/lx/internal/testenv"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
)

// Regression: GNU tools prefix diagnostics with argv[0] as invoked, so
// `/usr/bin/find . nosuch` prints "/usr/bin/find: ‘nosuch’: No such file
// or directory". The filters only knew "find: ", so find drew the message
// into the tree as "/usr/bin/" + a file named "find: ‘nosuch’: …", and
// ls -R split it across the "secret" directory. These are Content filters
// (no guard), so the error was effectively hidden.
func TestAbsoluteArgv0Diagnostics(t *testing.T) {
	cases := []struct {
		f    engine.Filter
		argv []string
		in   string
		diag string
	}{
		{find{}, []string{"/usr/bin/find", ".", "nosuch"},
			"./a.go\n./b/c.go\n/usr/bin/find: ‘nosuch’: No such file or directory",
			"/usr/bin/find: ‘nosuch’: No such file or directory"},
		{ls{}, []string{"/bin/ls", "-R"},
			".:\na\nsecret\n\n./a:\nx.go\n\n./secret:\n/bin/ls: cannot open directory './secret': Permission denied",
			"/bin/ls: cannot open directory './secret': Permission denied"},
		{ls{}, []string{"/bin/ls", "-la", "nope", "go.mod"},
			"/bin/ls: cannot access 'nope': No such file or directory\n-rw-r--r-- 1 user user 1458 Sep 26 00:47 go.mod",
			"/bin/ls: cannot access 'nope': No such file or directory"},
		{du{}, []string{"/usr/bin/du", "-sh", "a", "b", "c"},
			"4.0K\ta\n/usr/bin/du: cannot read directory 'b': Permission denied\n8.0K\tc",
			"/usr/bin/du: cannot read directory 'b': Permission denied"},
		// The same through bash -c, as the corpus records commands.
		{find{}, []string{"bash", "-c", "/usr/bin/find . nosuch -name '*.go'"},
			"./a.go\n/usr/bin/find: ‘nosuch’: No such file or directory",
			"/usr/bin/find: ‘nosuch’: No such file or directory"},
	}
	for _, tc := range cases {
		c := ctx(tc.argv...)
		c.Exit = 1
		out, ok := tc.f.Apply(c, tc.in)
		if !ok {
			t.Errorf("%q bailed", tc.argv)
			continue
		}
		if !strings.HasPrefix(out, tc.diag+"\n") {
			t.Errorf("%q: diagnostic not kept verbatim and first:\n%s", tc.argv, out)
		}
		if strings.Count(out, "Permission denied")+strings.Count(out, "No such file") != 1 {
			t.Errorf("%q: diagnostic also drawn into the listing:\n%s", tc.argv, out)
		}
	}
}

// Regression: bash -c pipelines were unwrapped for any head/tail/cat/sort
// stage, including ones that rewrite lines or read another file.
func TestEffectiveConsumers(t *testing.T) {
	unwrapped := []string{
		"find . | head -n 5", "find . | head -5", "find . | head -n5", "find . | head --lines=5",
		"find . | tail -n +2", "find . | tail -3", "find . | cat", "find . | cat -",
		"find . | sort", "find . | sort -rh", "find . | sort -t / -k2", "find . | sort -k 2,2 -u",
		"find . 2>/dev/null", "find . 2>/dev/null | sort", "find . 2>&1 | head -3",
	}
	kept := []string{
		"find . | cat -n",            // numbers every line
		"find . | cat -A",            // shows $ and ^I
		"find . | cat - notes.txt",   // appends a file
		"find . | head -2 other.txt", // reads another file
		"find . | head -c 100",       // cuts mid-line
		"find . | tail -f",           // follows
		"find . | sort -o out.txt",   // writes elsewhere
		"find . | sort -z",           // NUL-separated
		"find . | sort a.txt",        // sorts a file
		"find . > out.txt",           // redirection
		"find . 2>/dev/nullx",        // not /dev/null
		"find . 2>errors.log",        // stderr to a file is fine, but not modeled
		"find . | wc -l",             // counts
		"find . | xargs ls",          // another command
	}
	for _, s := range unwrapped {
		c := ctx("bash", "-c", s)
		if e := Effective(c); e == c || e.Name() != "find" {
			t.Errorf("%q should unwrap to find", s)
		}
	}
	for _, s := range kept {
		c := ctx("bash", "-c", s)
		if e := Effective(c); e != c {
			t.Errorf("%q must not unwrap (got %q)", s, e.Argv)
		}
	}
	// -x / -v make the shell print into the captured output.
	for _, fl := range []string{"-xc", "-vc", "-cx"} {
		c := ctx("bash", fl, "find .")
		if e := Effective(c); e != c {
			t.Errorf("bash %s must not unwrap", fl)
		}
	}
	// A `| cat -n` listing is not claimed by find (it would draw
	// "     1\t./" as a directory).
	if (find{}).Match(ctx("bash", "-c", "find . -type f | cat -n")) {
		t.Error("find claimed cat -n output")
	}
}

// Regression: find without -type f lists directories too, and those that
// find did not descend into (-maxdepth, -name matches) are leaves printed
// like files. Counts said "[3 files]" for node_modules/ holding three
// package directories.
func TestFindEntriesNoun(t *testing.T) {
	var b strings.Builder
	b.WriteString(".\n./src\n./src/a.go\n./node_modules\n")
	for i := range 30 {
		fmt.Fprintf(&b, "./node_modules/pkg%d\n", i)
	}
	out, ok := find{}.Apply(ctx("find", ".", "-maxdepth", "2"), strings.TrimSpace(b.String()))
	if !ok || !strings.Contains(out, "node_modules/ [30 entries]") || !strings.Contains(out, "names without / may be directories") {
		t.Fatalf("got\n%s", out)
	}
	// -type f: every leaf is a file.
	out, ok = find{}.Apply(ctx("find", ".", "-type", "f"), "./src/a.go\n./node_modules/x/index.js\n./node_modules/y/index.js")
	if !ok || !strings.Contains(out, "node_modules/ [2 files]") || strings.Contains(out, "may be directories") {
		t.Fatalf("-type f: got\n%s", out)
	}
	// fd -t f likewise; fd without -t is ambiguous.
	out, _ = find{}.Apply(ctx("fd", "-t", "f", "x"), "src/a.go\nnode_modules/x/index.js")
	if !strings.Contains(out, "node_modules/ [1 file]") {
		t.Fatalf("fd -t f: got\n%s", out)
	}
	out, _ = find{}.Apply(ctx("fd", "--max-depth", "2"), "src/a.go\nnode_modules/x")
	if !strings.Contains(out, "node_modules/ [1 entry]") || !strings.Contains(out, "may be directories") {
		t.Fatalf("fd --max-depth: got\n%s", out)
	}
}

// Regression: tree marks no directory, so leaf directories (empty, past
// -L, unfollowed links) were drawn as files with "[N files]" counts.
func TestTreeUnmarkedLeafDirs(t *testing.T) {
	in := ".\n├── cmd\n│   └── lx\n├── internal\n│   ├── cli\n│   └── engine\n└── go.mod\n\n5 directories, 1 file"
	out, ok := treeCmd{}.Apply(ctx("tree", "-L", "2"), in)
	if !ok || !strings.HasPrefix(out, "[tree: 3 of the names without / are directories (tree -F marks them)]\n") {
		t.Fatalf("got\n%s", out)
	}
	// -F marks them: nothing to warn about.
	inF := ".\n├── cmd/\n│   └── lx/\n└── go.mod\n\n2 directories, 1 file"
	out, ok = treeCmd{}.Apply(ctx("tree", "-F", "-L", "2"), inF)
	if !ok || strings.Contains(out, "[tree:") || !strings.Contains(out, "cmd/lx/") {
		t.Fatalf("-F: got\n%s", out)
	}
	// --noreport: no count to compare with.
	out, ok = treeCmd{}.Apply(ctx("tree", "--noreport"), ".\n├── a\n│   └── b\n└── c")
	if !ok || !strings.Contains(out, "names without / may be directories") {
		t.Fatalf("--noreport: got\n%s", out)
	}
	// Counts agree: no note.
	out, ok = treeCmd{}.Apply(ctx("tree"), ".\n├── a\n│   └── b.go\n└── c.go\n\n1 directory, 2 files")
	if !ok || strings.Contains(out, "[tree:") {
		t.Fatalf("exact: got\n%s", out)
	}
}

// Regression: GNU ls -R prints the listed directory's own header first
// (".:" or "src:"); it was counted as a subdirectory.
func TestLsRecursiveCountsGNU(t *testing.T) {
	in := ".:\na.go\nsub\n\n./sub:\nb.go\ndeep\n\n./sub/deep:\nc.go"
	out, ok := ls{}.Apply(ctx("ls", "-R"), in)
	if !ok || !strings.HasPrefix(out, "[ls -R: 3 files in 2 subdirectories]") {
		t.Fatalf("got\n%s", out)
	}
	in = "src:\na.go\nsub\n\nsrc/sub:\nb.go"
	out, ok = ls{}.Apply(ctx("ls", "-R", "src"), in)
	if !ok || !strings.HasPrefix(out, "[ls -R: 2 files in 1 subdirectory]") {
		t.Fatalf("got\n%s", out)
	}
	// BSD: no header for the listed directory.
	in = "a.go\nsub\n\n./sub:\nb.go"
	out, ok = ls{}.Apply(ctx("ls", "-R"), in)
	if !ok || !strings.HasPrefix(out, "[ls -R: 2 files in 1 subdirectory]") {
		t.Fatalf("BSD: got\n%s", out)
	}
}

// Regression: the names-only view counted left-out names by splitting
// lines on two spaces, which miscounts quoted names holding "  ".
func TestCapNamesExact(t *testing.T) {
	var names []string
	for i := range 3000 {
		names = append(names, fmt.Sprintf("my  file %04d.txt", i))
	}
	lines, more := capNames(names, 500)
	shown := 0
	for _, ln := range lines {
		shown += strings.Count(ln, `"my  file`)
	}
	if shown+more != len(names) || more == 0 {
		t.Fatalf("shown %d + more %d != %d", shown, more, len(names))
	}
}

// Every fs filter stays well under 200ms on 50k-line inputs.
func TestHugeFast(t *testing.T) {
	var ls1, lsR, du1, tr, fd strings.Builder
	ls1.WriteString("total 99999\n")
	tr.WriteString(".\n")
	for i := range 50000 {
		fmt.Fprintf(&ls1, "-rw-r--r--  1 user  staff  %d Sep 26 00:48 file%05d.txt\n", i, i)
		if i%100 == 0 {
			fmt.Fprintf(&lsR, "\n./d%d:\n", i)
		} else {
			fmt.Fprintf(&lsR, "f%d.go\n", i)
		}
		fmt.Fprintf(&du1, "%d\t./d%d/e%d\n", i%977, i%50, i)
		fmt.Fprintf(&tr, "├── f%05d.go\n", i)
		fmt.Fprintf(&fd, "./m%d/s%d/f%d.ts\n", i%60, i%9, i)
	}
	tr.WriteString("└── last.go\n\n0 directories, 50001 files")
	for _, tc := range []struct {
		f    engine.Filter
		argv []string
		in   string
	}{
		{ls{}, []string{"ls", "-l"}, ls1.String()},
		{ls{}, []string{"ls", "-R"}, "x.go" + lsR.String()},
		{du{}, []string{"du", "."}, du1.String()},
		{treeCmd{}, []string{"tree"}, tr.String()},
		{find{}, []string{"find", "."}, fd.String()},
	} {
		in := strings.TrimSpace(tc.in)
		start := time.Now()
		_, ok := tc.f.Apply(ctx(tc.argv...), in)
		el := time.Since(start)
		if !ok {
			t.Errorf("%v bailed", tc.argv)
		}
		t.Logf("%v: %d lines in %v", tc.argv, strings.Count(in, "\n")+1, el)
		if el > testenv.Scale(time.Second) && !raceEnabled {
			t.Errorf("%v took %v", tc.argv, el)
		}
	}
}

// Regression: a listing killed by a timeout or signal printed "[N paths]"
// / "[ls -R: N files …]" as if complete. A failing run must come with the
// tool's diagnostic, else the filters bail to the generic reducer.
func TestCutShortListingBails(t *testing.T) {
	var fnd, lsr, dus strings.Builder
	for i := range 300 {
		fmt.Fprintf(&fnd, "./src/pkg%d/file.go\n", i)
		fmt.Fprintf(&dus, "%d\t./d%d\n", i, i)
	}
	lsr.WriteString("a.go\n\n./sub:\nb.go\n\n./sub2:\nc.go")
	cases := []struct {
		f    engine.Filter
		argv []string
		in   string
	}{
		{find{}, []string{"find", "."}, fnd.String()},
		{ls{}, []string{"ls", "-R"}, lsr.String()},
		{ls{}, []string{"ls", "-l"}, "-rw-r--r-- 1 user user 10 Sep 26 00:48 a.go\n-rw-r--r-- 1 user user 12 Sep 26 00:48 b.go"},
		{du{}, []string{"du", "."}, dus.String()},
		{treeCmd{}, []string{"tree"}, ".\n├── a.go\n└── b.go"},
	}
	for _, tc := range cases {
		for _, exit := range []int{1, 124, 137} {
			c := ctx(tc.argv...)
			c.Exit = exit
			if out, ok := tc.f.Apply(c, strings.TrimSpace(tc.in)); ok {
				t.Errorf("%v exit %d without a diagnostic must bail, got\n%s", tc.argv, exit, out)
			}
		}
	}
	// With the diagnostic it is a normal failing listing.
	c := ctx("find", ".")
	c.Exit = 1
	if _, ok := (find{}).Apply(c, "find: ./x: Permission denied\n"+fnd.String()); !ok {
		t.Error("find exit 1 with a diagnostic bailed")
	}
}
