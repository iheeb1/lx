package search

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
)

func TestAbsoluteArgv0Diagnostics(t *testing.T) {
	c := ctx("/usr/bin/grep", "-r", "x")
	c.Exit = 2
	in := "a.go:x := 1\n/usr/bin/grep: warning: recursive search of stdin\nb.go:x = 2"
	out, ok := matches{}.Apply(c, in)
	if !ok || !strings.HasPrefix(out, "/usr/bin/grep: warning: recursive search of stdin\n") {
		t.Fatalf("got\n%s", out)
	}
	c = ctx("/usr/bin/grep", "-rn", "x", "nosuch", "src")
	c.Exit = 2
	in = "/usr/bin/grep: nosuch: No such file or directory\nsrc/a.go:1:x := 1\nsrc/b.go:2:x = 2"
	if out, ok = (matches{}).Apply(c, in); !ok || !strings.HasPrefix(out, "/usr/bin/grep: nosuch: No such file or directory\n") {
		t.Fatalf("got ok=%v\n%s", ok, out)
	}
	var b strings.Builder
	for i := range 30 {
		fmt.Fprintf(&b, "grep: img/%d.png: binary file matches\n", i)
	}
	b.WriteString("src/a.go:1:PNG")
	out, ok = matches{}.Apply(ctx("grep", "-rn", "PNG", "img", "src"), b.String())
	if !ok || !strings.Contains(out, "… +10 more binary files matched") || !strings.Contains(out, "grep: img/19.png: binary file matches") {
		t.Fatalf("GNU 3.5 binary notes: got\n%s", out)
	}

	out, ok = matches{}.Apply(ctx("rg", "-n", "x"), "a.go:1:x\nimg.png: binary file matches (found \"\\0\" byte around offset 7)\nb.go:2:x")
	if !ok || !strings.HasPrefix(out, "img.png: binary file matches (found \"\\0\" byte around offset 7)\n") {
		t.Fatalf("rg binary: got\n%s", out)
	}
}

func TestLineOrderBails(t *testing.T) {
	bail := []struct {
		argv []string
		in   string
	}{
		{[]string{"grep", "-rn", "ERROR", "logs"}, "logs/12:30:00.log:1:ERROR a\nlogs/12:30:00.log:3:ERROR b"},
		{[]string{"bash", "-c", "grep -rn x . | sort"}, "./a.go:10:x\n./a.go:9:x"},
		{[]string{"grep", "-n", "x", "a.go"}, "10:x\n9:x"},
		{[]string{"grep", "-rn", "-C1", "x", "."}, "./a.go-5-y\n./a.go:4:x"},
	}
	for _, tc := range bail {
		if out, ok := (matches{}).Apply(ctx(tc.argv...), tc.in); ok {
			t.Errorf("%q must bail on out-of-order lines, got\n%s", tc.argv, out)
		}
	}
	keep := []struct {
		argv []string
		in   string
	}{
		{[]string{"grep", "-rno", "x.", "."}, "./a.go:1:xa\n./a.go:1:xb\n./a.go:2:xc"},
		{[]string{"rg", "--vimgrep", "x"}, "a.go:1:5:x x\na.go:1:7:x x"},
		{[]string{"rg", "-n", "--column", "x"}, "a.go:1:5:x x\na.go:1:7:x x"},

		{[]string{"grep", "-rn", "x", "a", "b"}, "a/x.go:3:x\nb/y.go:1:x\nb/y.go:2:x"},
	}
	for _, tc := range keep {
		if out, ok := (matches{}).Apply(ctx(tc.argv...), tc.in); !ok {
			t.Errorf("%q bailed on valid output", tc.argv)
		} else if strings.Contains(tc.in, "1:5:") && !strings.Contains(out, "1:5:x x") {
			t.Errorf("%q dropped the column:\n%s", tc.argv, out)
		}
	}
}

func TestColumnKept(t *testing.T) {
	out, ok := matches{}.Apply(ctx("rg", "--vimgrep", "x"), "a.go:1:5:  x x\na.go:1:7:  x x\nb.go:3:1:x")
	want := "a.go\n1:5:x x\n1:7:x x\n\nb.go:3:1:x"
	if !ok || out != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}

	out, ok = matches{}.Apply(ctx("rg", "--column", "-n", "x", "a.go"), "1:5:x x\n2:1:x")
	if !ok || out != "1:5:x x\n2:1:x" {
		t.Fatalf("got\n%s", out)
	}
}

func TestInvertKeepsIndent(t *testing.T) {
	in := "key = 1\n  nested = 2\n\tindented = 3"
	out, ok := matches{}.Apply(ctx("grep", "-v", "^#", "app.conf"), in)
	if !ok || out != in {
		t.Fatalf("got %q", out)
	}
	out, ok = matches{}.Apply(ctx("grep", "-rv", "^#", "conf"), "conf/a.yml:root:\nconf/a.yml:  child: 1\nconf/b.yml:x: 1")
	if !ok || !strings.Contains(out, "conf/a.yml\n  root:\n    child: 1") {
		t.Fatalf("got\n%s", out)
	}
}

func TestFewHeavyHitsShown(t *testing.T) {
	in := "./src/a.go:1:TODO x\n./node_modules/dep/index.js:1:TODO y"
	out, ok := matches{}.Apply(ctx("grep", "-rn", "TODO", "."), in)
	if !ok || strings.Contains(out, "summarized") || !strings.Contains(out, "./node_modules/dep/index.js:1:TODO y") {
		t.Fatalf("got\n%s", out)
	}
	var b strings.Builder
	b.WriteString("./src/a.go:1:TODO x\n")
	for i := range minHeavy + 1 {
		fmt.Fprintf(&b, "./node_modules/dep%d/index.js:1:TODO y\n", i)
	}
	out, ok = matches{}.Apply(ctx("grep", "-rn", "TODO", "."), strings.TrimSpace(b.String()))
	if !ok || !strings.Contains(out, "summarized") || !strings.Contains(out, "./node_modules/: 6 matches in 6 files") {
		t.Fatalf("got\n%s", out)
	}
}

func TestOperandPrefixStrict(t *testing.T) {
	bail := []struct {
		argv []string
		in   string
	}{
		{[]string{"grep", "-r", "x", "src"}, "src2/a.go:x\nsrc2/b.go:x"},
		{[]string{"grep", "-r", "x", "."}, "note: x\nsee also: x"},
	}
	for _, tc := range bail {
		if out, ok := (matches{}).Apply(ctx(tc.argv...), tc.in); ok {
			t.Errorf("%q must bail, got\n%s", tc.argv, out)
		}
	}
	for _, tc := range []struct {
		argv []string
		in   string
	}{
		{[]string{"grep", "-r", "x", "src/"}, "src//a.go:x\nsrc/b.go:x"},
		{[]string{"grep", "-r", "x", "."}, "./a.go:x\n./b/c.go:x"},
		{[]string{"grep", "-r", "x", "a.go", "b.go"}, "a.go:x\nb.go:x"},
		{[]string{"grep", "-rn", "x", "../lib"}, "../lib/a.go:1:x\n../lib/b.go:2:x"},
	} {
		if out, ok := (matches{}).Apply(ctx(tc.argv...), tc.in); !ok {
			t.Errorf("%q bailed on valid output\n%s", tc.argv, out)
		}
	}
}

func TestSearchFast(t *testing.T) {
	var plain, ctxt, long, bare strings.Builder
	for i := range 50000 {
		fmt.Fprintf(&plain, "./pkg%d/file%d.go:%d:\tvalue := compute(%d) // Context\n", i%100, i%5000, i, i)
		f := i / 100
		if i%3 == 0 {
			fmt.Fprintf(&ctxt, "./pkg/file%d.go:%d:\tvalue := compute(%d) // Context\n", f, i, i)
		} else {
			fmt.Fprintf(&ctxt, "./pkg/file%d.go-%d-\tother(%d)\n", f, i, i)
		}
		fmt.Fprintf(&long, "./pkg%d/f.min.js:%d:%sContext%s\n", i%100, i, strings.Repeat("x", 300), strings.Repeat("y", 300))
		fmt.Fprintf(&bare, "%d:\tvalue := compute(%d) // Context\n", i+1, i)
	}
	for _, tc := range []struct {
		name string
		argv []string
		in   string
	}{
		{"grep -rn", []string{"grep", "-rn", "Context", "."}, plain.String()},
		{"grep -rn -C1", []string{"grep", "-rn", "-C1", "Context", "."}, ctxt.String()},
		{"long lines", []string{"grep", "-rn", "Context", "."}, long.String()},
		{"single file", []string{"grep", "-n", "Context", "a.go"}, bare.String()},
		{"rg --files", []string{"rg", "--files"}, strings.ReplaceAll(plain.String(), ":", "/")},
	} {
		in := strings.TrimSpace(tc.in)
		var f engine.Filter = matches{}
		if tc.name == "rg --files" {
			f = rgFiles{}
		}
		start := time.Now()
		_, ok := f.Apply(ctx(tc.argv...), in)
		el := time.Since(start)
		if !ok {
			t.Errorf("%s bailed", tc.name)
		}
		t.Logf("%s: 50k lines in %v", tc.name, el)
		if el > time.Second && !raceEnabled {
			t.Errorf("%s took %v", tc.name, el)
		}
	}
}

func TestCutShortSearchBails(t *testing.T) {
	var b strings.Builder
	for i := range 400 {
		fmt.Fprintf(&b, "./src/f%d.go:%d:x\n", i%50, i+1)
	}
	b.WriteString("Binary file ./img.png matches\n./src/f1.go:999:x")
	for _, exit := range []int{2, 124, 130, 137, 143} {
		c := ctx("grep", "-rn", "x", ".")
		c.Exit = exit
		if out, ok := (matches{}).Apply(c, b.String()); ok {
			t.Errorf("exit %d without a diagnostic must bail, got\n%s", exit, out[:min(len(out), 200)])
		}
		res := engine.Process(c, b.String(), engine.Options{})
		if res.Filter == "search" || strings.Contains(res.Output, "matches in") {
			t.Errorf("exit %d: Process used %s:\n%s", exit, res.Filter, res.Output[:min(len(res.Output), 300)])
		}
	}

	c := ctx("grep", "-rn", "x", ".")
	c.Exit = 2
	if _, ok := (matches{}).Apply(c, "grep: ./locked: Permission denied\n"+b.String()); !ok {
		t.Error("exit 2 with a diagnostic bailed")
	}

	c = ctx("rg", "--files")
	c.Exit = 137
	if out, ok := (rgFiles{}).Apply(c, "a.go\nb/c.go"); ok {
		t.Errorf("rg --files killed: got\n%s", out)
	}
}

func TestRgFilesNoteFlood(t *testing.T) {
	var b strings.Builder
	for i := range 500 {
		fmt.Fprintf(&b, "rg: /proc/%d/fd: Permission denied (os error 13)\n", i)
	}
	b.WriteString("home/user/a.go\nhome/user/b.go")
	c := ctx("rg", "--files", "/")
	c.Exit = 2
	out, ok := rgFiles{}.Apply(c, b.String())
	if !ok || !strings.Contains(out, "… +470 more: Permission denied (os error 13) ×470") || strings.Count(out, "\n") > 40 {
		t.Fatalf("got\n%s", out)
	}
}

func TestUngroup(t *testing.T) {
	got := "grep: nosuch: No such file or directory\nrender/json.go\n57:func (r JSON) Render()\n78-}\n\nrender/a.go:3:x\n[narrow the search: …]"
	want := "grep: nosuch: No such file or directory\nrender/json.go\nrender/json.go:57:func (r JSON) Render()\nrender/json.go:78-}\n\nrender/a.go:3:x\n[narrow the search: …]\n"
	if u := ungroup(got); u != want {
		t.Fatalf("got\n%q\nwant\n%q", u, want)
	}
	fc, err := fixture.Read("testdata/corpus", "search", "grep-missing-dir")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := matches{}.Apply(fc.Context(), fc.Clean())
	if len(fixture.LocationsMissing(fc.Clean(), out)) == 0 {
		t.Fatal("grouped output unexpectedly keeps every literal location; ungroup is not exercised")
	}
	if miss := fixture.LocationsMissing(fc.Clean(), ungroup(out)); len(miss) > 0 {
		t.Fatalf("locations missing after ungroup: %q", miss)
	}
}
