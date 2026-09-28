package engine

import (
	"fmt"
	"strings"
	"testing"
)

func TestFactorPaths(t *testing.T) {
	in := []string{"./go.mod", "./main.go", "./internal/engine/a.go", "./internal/engine/b.go", "./docs/x/y/z.md"}
	for i := 1; i <= 25; i++ {
		in = append(in, fmt.Sprintf("./node_modules/pkg%d/index.js", i))
	}
	for i := 1; i <= 7; i++ {
		in = append(in, fmt.Sprintf("./logs/run-%d.log", i))
	}
	got := strings.Join(FactorPaths(in), "\n")
	want := `go.mod  main.go
docs/x/y/
  z.md
internal/engine/
  a.go  b.go
logs/
  run-{1..7}.log
node_modules/ [25 files]`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestFactorPathsInsideHeavyDirAndEdges(t *testing.T) {
	var in []string
	for i := 0; i < 30; i++ {
		in = append(in, fmt.Sprintf("node_modules/a/f%c.js", 'a'+i%26))
	}
	got := FactorPaths(in)
	if got[0] != "node_modules/a/" {
		t.Errorf("list entirely inside node_modules was pruned: %q", got)
	}
	if out := FactorPaths(nil); len(out) != 0 {
		t.Errorf("empty: %q", out)
	}
	if out := FactorPaths([]string{"/abs/dir/f.txt"}); strings.Join(out, "|") != "/abs/dir/|  f.txt" {
		t.Errorf("absolute: %q", out)
	}

	if out := FactorPaths([]string{"./src", "./src/a.go", "./src/é.go"}); strings.Join(out, "|") != "src/|  a.go  é.go" {
		t.Errorf("dir entry: %q", out)
	}
}

func TestLooksLikePathList(t *testing.T) {
	yes := []string{"./a/b.go", "./a/c.go", "./d/e.ts", "README.md", "./x y/z.txt"}
	if !LooksLikePathList(yes) {
		t.Error("path list not recognized")
	}
	for _, no := range [][]string{
		{"PASS test/a.js", "PASS test/b.js", "PASS test/c.js", "PASS test/d.js"},
		{"src/a.go:12: x", "src/a.go:13: y", "src/b.go:1: z", "src/c.go:2: w"},
		{"one", "two"},
		{"hello world", "this is prose", "not/paths at all here", "nope"},
	} {
		if LooksLikePathList(no) {
			t.Errorf("false positive: %q", no)
		}
	}
}

func TestLsRecursivePaths(t *testing.T) {
	in := strings.Split("client\nnode\nREADME.md\n\nsrc/client:\na.ts\nb.ts\n\nsrc/node:\nc.ts\nd.ts", "\n")
	paths, ok := lsRecursivePaths(in)
	if !ok {
		t.Fatal("not recognized")
	}
	want := "src/client|src/node|src/README.md|src/client/a.ts|src/client/b.ts|src/node/c.ts|src/node/d.ts"
	if strings.Join(paths, "|") != want {
		t.Errorf("got %q", paths)
	}
}

func TestBraceGroup(t *testing.T) {
	cases := []struct {
		mask    string
		members []string
		want    string
		ok      bool
	}{
		{"vite<N>.md", []string{"vite2.md", "vite3.md", "vite4.md", "vite5.md", "vite6.md", "vite7.md"}, "vite{2..7}.md", true},
		{"t<N>.out", []string{"t1.out", "t3.out", "t9.out", "t10.out", "t12.out", "t20.out"}, "t{1,3,9,10,12,20}.out", true},
		{"log-<N>.txt", []string{"log-01.txt", "log-02.txt", "log-03.txt", "log-04.txt", "log-05.txt", "log-06.txt"}, "log-{01..06}.txt", true},
		{"a<N>", []string{"a01", "a2", "a3", "a4", "a5", "a6"}, "a{01,2,3,4,5,6}", true},
	}
	for _, tc := range cases {
		got, ok := braceGroup(tc.mask, tc.members)
		if ok != tc.ok || got != tc.want {
			t.Errorf("braceGroup(%q) = %q,%v want %q,%v", tc.mask, got, ok, tc.want, tc.ok)
		}
	}
}
