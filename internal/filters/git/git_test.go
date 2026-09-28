package git

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/testenv"
	"github.com/iheeb1/lx/internal/tokens"
)

func ctx(exit int, argv ...string) *engine.Context {
	return &engine.Context{Argv: argv, Exit: exit, Cwd: "/home/user/src/app", Home: "/home/user"}
}

func find(t *testing.T, c *engine.Context) engine.Filter {
	t.Helper()
	f := engine.Find(c)
	if f == nil {
		t.Fatalf("no filter for %q", c.Argv)
	}
	return f
}

func TestMatch(t *testing.T) {
	cases := []struct {
		argv string
		want string
	}{
		{"git log", "git-log"},
		{"/usr/local/bin/git log --stat", "git-log"},
		{"git.exe log", "git-log"},
		{"git -C /repo -c color.ui=always --no-pager log -p -n 3", "git-log"},
		{"git log --pretty=oneline", "git-log"},
		{"git log --format=%H", ""},
		{"git log --pretty=format:%h", ""},
		{"git show HEAD", "git-show"},
		{"git show HEAD:README.md", "git-show"},
		{"git diff", "git-diff"},
		{"git diff --cached -- src/", "git-diff"},
		{"git diff --name-only", ""},
		{"git diff --shortstat", ""},
		{"git stash show -p", "git-diff"},
		{"git stash list", "git-stash"},
		{"git blame gin.go", "git-blame"},
		{"git blame --porcelain gin.go", ""},
		{"git blame -p gin.go", ""},
		{"git blame --line-porcelain gin.go", ""},
		{"git branch -a", "git-branch"},
		{"git branch -r --sort=-committerdate", "git-branch"},
		{"git branch -d old", ""},
		{"git branch -vv", "git-list"},
		{"git branch -av", "git-list"},
		{"git branch -D old", ""},
		{"git branch -dr origin/x", ""},
		{"git branch -m old new", ""},
		{"git branch new-feature", "git-branch"},
		{"git push origin main", "git-sync"},
		{"git pull --rebase", "git-sync"},
		{"git fetch --all --prune", "git-sync"},
		{"git clone https://example.com/x.git", "git-sync"},
		{"git merge feature", "git-merge"},
		{"git merge-base a b", ""},
		{"git commit -m x", "git-commit"},
		{"git tag", "git-tag"},
		{"git tag -n", "git-list"},
		{"git tag -n5 -l 'v1*'", "git-list"},
		{"git tag -a v1 -m x", "git-tag"},
		{"git tag -d v1", ""},
		{"git remote -v", "git-remote"},
		{"git remote add origin x", ""},
		{"git status", "git-status"},
		{"git status -sb", ""},
		{"git status -s", ""},
		{"git status -v", "git-status"},
		{"git reflog", "git-list"},
		{"git reflog show feature", "git-list"},
		{"git reflog expire --all", ""},
		{"git worktree list", "git-list"},
		{"git worktree add ../x", ""},
		{"git remote show origin", "git-list"},
		{"git shortlog -sn", "git-list"},
		{"git submodule status", "git-list"},
		{"git submodule update --init", ""},
		{"git clean -n -d", "git-list"},
		{"git rm -r --cached dist", "git-list"},
		{"git rev-parse HEAD", ""},
		{"gitk", ""},
		{"hg log", ""},
	}
	for _, tc := range cases {
		c := ctx(0, strings.Fields(tc.argv)...)
		f := engine.Find(c)
		got := ""
		if f != nil {
			got = f.Name()
		}
		if engine.MachineReadable(c) && got != "" {
			t.Errorf("%q: machine-readable but %s matched", tc.argv, got)
		}
		if got != tc.want && (tc.want != "" || strings.HasPrefix(got, "git-")) {
			t.Errorf("%q: filter %q, want %q", tc.argv, got, tc.want)
		}
	}
}

var allFilters = [][]string{
	{"git", "log"}, {"git", "log", "--stat"}, {"git", "log", "-p"}, {"git", "show"}, {"git", "diff"},
	{"git", "blame", "f.go"}, {"git", "branch", "-a"}, {"git", "push"}, {"git", "pull"}, {"git", "fetch"},
	{"git", "clone", "u"}, {"git", "merge", "x"}, {"git", "commit"}, {"git", "tag"}, {"git", "remote", "-v"},
	{"git", "status"}, {"git", "stash", "list"}, {"git", "stash", "show", "-p"},
	{"git", "reflog"}, {"git", "branch", "-vv"}, {"git", "tag", "-n"},
}

func TestEmptyAndOneLine(t *testing.T) {
	for _, argv := range allFilters {
		c := ctx(0, argv...)
		f := find(t, c)
		if got, ok := f.Apply(c, ""); ok {
			t.Errorf("%s: empty output accepted: %q", f.Name(), got)
		}
		for _, ln := range []string{
			"fatal: not a git repository (or any of the parent directories): .git",
			"error: pathspec 'nope' did not match any file(s) known to git",
			"hello",
		} {
			c := ctx(128, argv...)
			if got, ok := f.Apply(c, ln); ok && !strings.Contains(got, ln) {
				t.Errorf("%s: one-line %q became %q", f.Name(), ln, got)
			}
		}
	}
}

func TestUnknownOutputBails(t *testing.T) {
	cases := []struct {
		argv []string
		out  string
	}{
		{[]string{"git", "log"}, "just some text\nthat is not a log"},
		{[]string{"git", "show", "HEAD^{tree}"}, "README.md\nsrc/\ngo.mod"},
		{[]string{"git", "diff"}, "Usage: git diff [<options>]"},
		{[]string{"git", "blame", "x"}, "4b68a5f1 x.go 12 some other layout"},
		{[]string{"git", "branch"}, "* main\n  dev  abc1234 [origin/dev] subject"},

		{[]string{"git", "merge", "topic"}, "Fusion automatique de a.go\nCONFLIT (contenu) : Conflit de fusion dans a.go\nLa fusion automatique a échoué ; réglez les conflits et validez le résultat."},
		{[]string{"git", "pull"}, "Mise à jour 1a2b3c4..5d6e7f8\nAvance rapide\n a.go | 2 +-\n 1 fichier modifié, 1 insertion(+), 1 suppression(-)"},
		{[]string{"git", "commit"}, "Sur la branche main\nrien à valider, la copie de travail est propre"},
		{[]string{"git", "tag"}, "error: tag 'x' not found."},
		{[]string{"git", "remote", "-v"}, "origin https://x (fetch)"},
	}
	for _, tc := range cases {
		c := ctx(1, tc.argv...)
		f := find(t, c)
		if got, ok := f.Apply(c, tc.out); ok {
			t.Errorf("%s accepted unknown output %q:\n%s", f.Name(), tc.out, got)
		}
	}
}

var passWordRe = regexp.MustCompile(`(?i)\b(?:ok|pass(?:ed)?|success(?:ful(?:ly)?)?|clean|up.to.date|done|no (?:errors|changes|conflicts))\b`)

func TestFailingRunsAddNoVerdict(t *testing.T) {
	cases := []struct {
		argv []string
		out  string
	}{
		{[]string{"git", "push"}, "Everything up-to-date\nerror: failed to push some refs to 'origin'"},
		{[]string{"git", "push"}, "To github.com:o/r.git\n ! [rejected]        main -> main (non-fast-forward)\nerror: failed to push some refs to 'github.com:o/r.git'\nhint: Updates were rejected because the tip of your current branch is behind\nhint: its remote counterpart."},
		{[]string{"git", "merge", "x"}, "Already up to date.\nfatal: refusing to merge unrelated histories"},
		{[]string{"git", "pull"}, "From x\n * branch main -> FETCH_HEAD\nerror: Your local changes to the following files would be overwritten by merge:\n\ta.go\nPlease commit your changes or stash them before you merge.\nAborting"},
		{[]string{"git", "diff", "--exit-code"}, "diff --git a/a b/a\nindex 1..2 100644\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n-x\n+y"},
		{[]string{"git", "commit"}, "black....................................................................Failed\n- hook id: black\n- files were modified by this hook\n\nreformatted a.py\n\nAll done! ✨ 🍰 ✨\n1 file reformatted.\nflake8...................................................................Passed"},
	}
	for _, tc := range cases {
		c := ctx(1, tc.argv...)
		f := find(t, c)
		got, ok := f.Apply(c, tc.out)
		if !ok {
			continue
		}
		if n, m := len(passWordRe.FindAllString(got, -1)), len(passWordRe.FindAllString(tc.out, -1)); n > m {
			t.Errorf("%s added pass-like words (%d > %d):\n%s", f.Name(), n, m, got)
		}
		if miss := fixture.ErrorLinesMissing(tc.out, got); len(miss) > 0 {
			t.Errorf("%s dropped error lines %q", f.Name(), miss)
		}
	}
}

func TestHugeOutputs(t *testing.T) {
	type gen struct {
		name string
		argv []string
		out  func() string
		max  int
	}
	var b strings.Builder
	gens := []gen{
		{"log", []string{"git", "log"}, func() string {
			b.Reset()
			for i := 0; i < 10000; i++ {
				fmt.Fprintf(&b, "commit %040x\nAuthor: A U Thor <a@example.com>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\n    subject %d\n\n", i+1, i)
			}
			return b.String()
		}, 8000},
		{"diff-one-hunk", []string{"git", "diff"}, func() string {
			b.Reset()
			b.WriteString("diff --git a/big.txt b/big.txt\nindex 1..2 100644\n--- a/big.txt\n+++ b/big.txt\n@@ -1,25000 +1,25000 @@\n")
			for i := 0; i < 25000; i++ {
				fmt.Fprintf(&b, "-old line %d\n+new line %d\n", i, i)
			}
			return b.String()
		}, 8000},
		{"diff-many-files", []string{"git", "diff"}, func() string {
			b.Reset()
			for i := 0; i < 5000; i++ {
				fmt.Fprintf(&b, "diff --git a/f%d.go b/f%d.go\nindex 1..2 100644\n--- a/f%d.go\n+++ b/f%d.go\n@@ -1,2 +1,2 @@\n-a%d\n+b%d\n c\n", i, i, i, i, i, i)
			}
			return b.String()
		}, 8000},
		{"lockfile", []string{"git", "diff"}, func() string {
			b.Reset()
			b.WriteString("diff --git a/package-lock.json b/package-lock.json\nindex 1..2 100644\n--- a/package-lock.json\n+++ b/package-lock.json\n@@ -1,50000 +1,50000 @@\n")
			for i := 0; i < 10000; i++ {
				fmt.Fprintf(&b, "     \"node_modules/p%d\": {\n-      \"version\": \"1.0.%d\",\n+      \"version\": \"1.1.%d\",\n       \"dev\": true\n     },\n", i, i, i)
			}
			return b.String()
		}, 8000},
		{"blame", []string{"git", "blame", "x.go"}, func() string {
			b.Reset()
			for i := 1; i <= 50000; i++ {
				fmt.Fprintf(&b, "%08x (Some Author %d 2020-01-02 03:04:05 +0000 %5d) code line %d\n", i/3, i%7, i, i)
			}
			return b.String()
		}, 8000},
		{"fetch-tags", []string{"git", "fetch"}, func() string {
			b.Reset()
			b.WriteString("From https://example.com/r\n")
			for i := 0; i < 50000; i++ {
				fmt.Fprintf(&b, " * [new tag]         v%d.%d.%d -> v%d.%d.%d\n", i/1000, i/10%100, i%10, i/1000, i/10%100, i%10)
			}
			return b.String()
		}, 0},
		{"branches", []string{"git", "branch", "-a"}, func() string {
			b.Reset()
			b.WriteString("* main\n")
			for i := 0; i < 50000; i++ {
				fmt.Fprintf(&b, "  remotes/origin/feature-%d\n", i)
			}
			return b.String()
		}, 0},
		{"log-stat", []string{"git", "log", "--stat"}, func() string {
			b.Reset()
			for i := 0; i < 5000; i++ {
				fmt.Fprintf(&b, "commit %040x\nAuthor: A <a@b>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\n    s%d\n\n a/b%d.go | 3 +--\n 1 file changed, 1 insertion(+), 2 deletions(-)\n\n", i+1, i, i)
			}
			return b.String()
		}, 8000},
	}
	for _, g := range gens {
		t.Run(g.name, func(t *testing.T) {
			in := g.out()
			c := ctx(0, g.argv...)
			f := find(t, c)
			start := time.Now()
			got, ok := f.Apply(c, in)
			el := time.Since(start)
			if !ok {
				t.Fatal("bailed")
			}
			if el > testenv.Scale(5*time.Second) {
				t.Errorf("took %v", el)
			}
			if g.max > 0 {
				if n := tokens.Count(got); n > g.max {
					t.Errorf("output %d tokens > %d", n, g.max)
				}
			}
			t.Logf("%d lines → %d tokens in %v", strings.Count(in, "\n"), tokens.Count(got), el.Round(time.Millisecond))
		})
	}
}

func TestHugeLogCountsEveryCommit(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&b, "commit %040x\nAuthor: A <a@b>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\n    subject %d\n\n", i+1, i)
	}
	b.WriteString("fatal: unable to read tree 1234567\n")
	got, ok := logFilter{}.Apply(ctx(128, "git", "log"), b.String())
	if !ok {
		t.Fatal("bailed")
	}
	m := moreCommitRe.FindStringSubmatch(got)
	if m == nil || m[2] != "3000" {
		t.Fatalf("no exact cut marker: %q", got[len(got)-200:])
	}
	shown := strings.Count(got, "[A] subject")
	if fmt.Sprint(3000-shown) != m[1] {
		t.Errorf("marker says %s more, %d shown of 3000", m[1], shown)
	}
	if !strings.Contains(got, "fatal: unable to read tree 1234567") {
		t.Error("trailing fatal line cut with the commits")
	}
}

func TestParseStatRow(t *testing.T) {
	cases := []struct{ in, want string }{
		{" context.go      |  3 +--", "+1 -2"},
		{" routes_test.go | 97 ++++++++++++++++++++++++++++++++++++++++++++++++++++++++++", "+97"},
		{" old.go | 12 ------", "-12"},
		{" routergroup.go      | 38 ++++++++++++++++++++++++++++++++----", "±38"},
		{" a.go => b.go | 0", "0"},
		{" img.png | Bin 0 -> 1234 bytes", "Bin 0 -> 1234 bytes"},
		{" img.png | Bin", "Bin"},
	}
	for _, tc := range cases {
		r, ok := parseStatRow(tc.in)
		if !ok || r.desc() != tc.want {
			t.Errorf("%q → %q ok=%v, want %q", tc.in, r.desc(), ok, tc.want)
		}
	}
	for _, bad := range []string{"context.go | 3 +--", " no bar here", " x | abc"} {
		if _, ok := parseStatRow(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestResolveStat(t *testing.T) {
	var rows []statRow
	for _, ln := range []string{
		" docs/doc.md         | 41 +++++++++++++++++++++++++++++++++++++++++",
		" routergroup.go      | 38 ++++++++++++++++++++++++++++++++----",
		" routes_test.go      | 55 +++++++++++++++++++++++++++++++++++++++++++++++++++++++",
		" img.png             | Bin 0 -> 5 bytes",
	} {
		r, _ := parseStatRow(ln)
		rows = append(rows, r)
	}
	resolveStat(rows, " 4 files changed, 130 insertions(+), 4 deletions(-)")
	if got := rows[1].desc(); got != "+34 -4" {
		t.Errorf("resolved %q", got)
	}

	a, _ := parseStatRow(" a.go | 38 ++++++++++++++++++++++++++++++++----")
	b, _ := parseStatRow(" b.go | 38 ++++++++++++++++++++++++++++++++----")
	two := []statRow{a, b}
	resolveStat(two, " 2 files changed, 68 insertions(+), 8 deletions(-)")
	if two[0].exact || two[1].exact {
		t.Error("guessed a split")
	}

	one := []statRow{a}
	resolveStat(one, " 3 files changed, 34 insertions(+), 4 deletions(-)")
	if one[0].exact {
		t.Error("used a summary for other rows")
	}
}

func TestRenderStatGroupsByDir(t *testing.T) {
	rows := []statRow{
		{path: "a.go", n: 1, ins: 1, exact: true},
		{path: "pkg/b.go", n: 2, ins: 1, del: 1, exact: true},
		{path: "pkg/c.go", n: 3, del: 3, exact: true},
		{path: "x/{old.go => new.go}", n: 0, exact: true},
		{path: ".../deep/path.go", n: 4, ins: 4, exact: true},
	}
	got := strings.Join(renderStat(rows, "", 0), "\n")
	want := "a.go +1, pkg/{b.go +1 -1, c.go -3}, x/{old.go => new.go} 0, .../deep/path.go +4"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func applyDiff(t *testing.T, in string) string {
	t.Helper()
	got, ok := diffFilter{}.Apply(ctx(0, "git", "diff"), in)
	if !ok {
		t.Fatalf("bailed on\n%s", in)
	}
	return got
}

func TestDiffHeaders(t *testing.T) {
	in := `diff --git a/a.go b/a.go
index 1111111..2222222 100644
--- a/a.go
+++ b/a.go
@@ -1,3 +1,3 @@ func f() {
 x

-y
+z
\ No newline at end of file
diff --git a/old name.txt b/new name.txt
similarity index 90%
rename from old name.txt
rename to new name.txt
index 1..2 100644
--- a/old name.txt
+++ b/new name.txt
@@ -1 +1 @@
-p
+q
diff --git a/img.png b/img.png
new file mode 100644
index 0000000..3333333
Binary files /dev/null and b/img.png differ
diff --git a/run.sh b/run.sh
old mode 100644
new mode 100755
diff --git a/e.txt b/e.txt
new file mode 100644
index 0000000..e69de29
diff --git --no-prefix x.go x.go
--- x.go
+++ x.go
@@ -1 +1 @@
-1
+2`
	want := `diff --git a/a.go b/a.go
@@ -1,3 +1,3 @@ func f() {
 x

-y
+z
\ No newline at end of file
diff --git a/old name.txt b/new name.txt
similarity index 90%
rename from old name.txt
rename to new name.txt
@@ -1 +1 @@
-p
+q
Binary files /dev/null and b/img.png differ
diff --git a/run.sh b/run.sh
old mode 100644
new mode 100755
diff --git a/e.txt b/e.txt
new file mode 100644
diff --git --no-prefix x.go x.go
@@ -1 +1 @@
-1
+2`
	if got := applyDiff(t, in); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestDiffKeepsForeignLines(t *testing.T) {
	in := "warning: in the working copy of 'a.txt', LF will be replaced by CRLF the next time Git touches it\n" +
		"diff --git a/a.txt b/a.txt\nindex 1..2 100644\n--- a/a.txt\n+++ b/a.txt\n@@ -1,2 +1,2 @@\n-a\n+b\n c\n" +
		"error: some interleaved stderr line\n@@ -10 +10 @@\n-x\n+y\n"
	got := applyDiff(t, in)
	for _, ln := range strings.Split(in, "\n") {
		if strings.HasPrefix(ln, "index ") || strings.HasPrefix(ln, "--- ") || strings.HasPrefix(ln, "+++ ") {
			continue
		}
		if !strings.Contains(got, ln) {
			t.Errorf("line %q lost:\n%s", ln, got)
		}
	}
}

func TestDiffHunkCounting(t *testing.T) {
	in := "diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ -1,4 +1,4 @@\n a\n\n-b\n+c\n\nnot part of the hunk"
	got := applyDiff(t, in)
	if !strings.HasSuffix(got, "+c\n\nnot part of the hunk") {
		t.Errorf("got\n%s", got)
	}
	d, _ := parseDiffDoc(strings.Split(in, "\n"))
	if h := d.parts[0].file.hunks[0]; len(h.body) != 5 || h.adds != 1 || h.dels != 1 {
		t.Errorf("hunk body %q adds %d dels %d", h.body, h.adds, h.dels)
	}

	short := "diff --git a/a b/a\n@@ -1,10 +1,10 @@\n a\n-b"
	if got := applyDiff(t, short); got != short {
		t.Errorf("truncated hunk changed:\n%s", got)
	}
}

func TestDiffCombined(t *testing.T) {
	in := `diff --cc a.go
index 1,2..0000000
--- a/a.go
+++ b/a.go
@@@ -1,3 -1,3 +1,7 @@@
  x
++<<<<<<< HEAD
 +y
++=======
+ z
++>>>>>>> topic
  w
* Unmerged path b.go`
	want := `diff --cc a.go
@@@ -1,3 -1,3 +1,7 @@@
  x
++<<<<<<< HEAD
 +y
++=======
+ z
++>>>>>>> topic
  w
* Unmerged path b.go`
	if got := applyDiff(t, in); got != want {
		t.Errorf("got\n%s", got)
	}
}

func TestDiffIdenticalHunks(t *testing.T) {
	var b strings.Builder
	for _, p := range []string{"a.go", "pkg/b.go", "pkg/c.go", "d.go"} {
		hunk := "@@ -1 +1 @@\n-// Copyright 2020\n+// Copyright 2026"
		if p == "d.go" {
			hunk = "@@ -1 +1 @@\n-x\n+y"
		}
		fmt.Fprintf(&b, "diff --git a/%s b/%s\nindex 1..2 100644\n--- a/%s\n+++ b/%s\n%s\n", p, p, p, p, hunk)
	}
	got := applyDiff(t, b.String())
	want := `diff --git a/a.go b/a.go
[same hunks also in 2 files: pkg/{b.go, c.go}]
@@ -1 +1 @@
-// Copyright 2020
+// Copyright 2026
diff --git a/d.go b/d.go
@@ -1 +1 @@
-x
+y`
	if got != want {
		t.Errorf("got\n%s", got)
	}
}

func TestDiffGitBinaryPatchIsVerbatim(t *testing.T) {
	in := "diff --git a/x.bin b/x.bin\nindex 1..2 100644\nGIT binary patch\nliteral 5\nMcmZ?b\n\nliteral 0\nHcmV?d00001\n"
	got, ok := diffFilter{}.Apply(ctx(0, "git", "diff"), in)
	if ok && got != in {
		t.Errorf("binary patch reshaped:\n%s", got)
	}
	got, ok = diffFilter{}.Apply(ctx(0, "git", "diff", "--binary"), in)
	if !ok || got != in {
		t.Errorf("--binary not verbatim")
	}
}

func TestDiffVerbatimFlags(t *testing.T) {
	in := "diff --git a/a b/a\nindex 1..2 100644\n--- a/a\n+++ b/a\n@@ -1 +1 @@\n[-x-]{+y+}"
	for _, flag := range []string{"--word-diff", "--word-diff=plain", "--color-words", "--stat", "--full-index"} {
		got, ok := diffFilter{}.Apply(ctx(0, "git", "diff", flag), in)
		if !ok || got != in {
			t.Errorf("%s: not verbatim: %q", flag, got)
		}
	}
}

func TestDiffLevelsKeepFileList(t *testing.T) {
	var b strings.Builder

	b.WriteString("diff --git a/gone.txt b/gone.txt\ndeleted file mode 100644\nindex 1..0\n--- a/gone.txt\n+++ /dev/null\n@@ -1,300 +0,0 @@\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "-deleted line %d with some words in it\n", i)
	}
	b.WriteString("diff --git a/new.txt b/new.txt\nnew file mode 100644\nindex 0..1\n--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1,500 @@\n")
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&b, "+new line %d with some words in it\n", i)
	}
	var names []string
	for f := 0; f < 60; f++ {
		name := fmt.Sprintf("src/mod%d/file%d.go", f, f)
		names = append(names, name)
		fmt.Fprintf(&b, "diff --git a/%s b/%s\nindex 1..2 100644\n--- a/%s\n+++ b/%s\n", name, name, name, name)
		for h := 0; h < 3; h++ {
			fmt.Fprintf(&b, "@@ -%d,6 +%d,6 @@ func f%d() {\n", h*100+1, h*100+1, h)
			for i := 0; i < 3; i++ {
				fmt.Fprintf(&b, "-\told := compute(%d, %d, %d)\n+\tnew := compute(%d, %d, %d)\n", f, h, i, f, h, i)
			}
		}
	}
	got := applyDiff(t, b.String())
	if n := tokens.Count(got); n > diffBudget+200 {
		t.Errorf("output %d tokens", n)
	}
	for _, n := range append(names, "gone.txt", "new.txt") {
		if !strings.Contains(got, n) {
			t.Errorf("file %s not named", n)
		}
	}
	for _, marker := range []string{"[deleted file: 300 lines not shown]", "more lines of this new file not shown]"} {
		if !strings.Contains(got, marker) {
			t.Errorf("marker %q missing", marker)
		}
	}
}

func TestLockfileSummaries(t *testing.T) {
	cases := []struct {
		name, body string
		want       []string
	}{
		{"go.sum", `@@ -1,4 +1,4 @@
-github.com/a/b v1.0.0 h1:AAAA=
-github.com/a/b v1.0.0/go.mod h1:BBBB=
+github.com/a/b v1.1.0 h1:CCCC=
+github.com/a/b v1.1.0/go.mod h1:DDDD=
 github.com/c/d v0.1.0 h1:EEEE=
+github.com/e/f v2.0.0+incompatible h1:FFFF=
-github.com/g/h v0.0.1/go.mod h1:GGGG=`, []string{"changed (1): github.com/a/b v1.0.0→v1.1.0", "added (1): github.com/e/f@v2.0.0+incompatible", "removed (1): github.com/g/h@v0.0.1"}},
		{"Cargo.lock", `@@ -10,8 +10,8 @@
 [[package]]
 name = "serde"
-version = "1.0.1"
+version = "1.0.2"
 source = "registry+https://github.com/rust-lang/crates.io-index"
+
+[[package]]
+name = "anyhow"
+version = "1.0.80"`, []string{"changed (1): serde 1.0.1→1.0.2", "added (1): anyhow@1.0.80"}},
		{"yarn.lock", `@@ -1,8 +1,8 @@
-"@babel/core@^7.0.0", "@babel/core@^7.1.0":
-  version "7.1.0"
+"@babel/core@^7.0.0", "@babel/core@^7.1.0", "@babel/core@^7.2.0":
+  version "7.2.0"
   resolved "https://registry.yarnpkg.com/x"
-left-pad@^1.0.0:
-  version "1.3.0"`, []string{"changed (1): @babel/core 7.1.0→7.2.0", "removed (1): left-pad@1.3.0"}},
		{"pnpm-lock.yaml", `@@ -1,6 +1,6 @@
-  /lodash@4.17.20:
+  /lodash@4.17.21:
   '@esbuild/aix-ppc64@0.25.12':
-  '@esbuild/aix-ppc64@0.27.2':
+  '@vue/shared@3.4.0(typescript@5.0.0)':`, []string{"changed (1): lodash 4.17.20→4.17.21", "added (1): @vue/shared@3.4.0", "removed (1): @esbuild/aix-ppc64@0.27.2"}},
		{"package-lock.json", `@@ -100,7 +100,7 @@
     "node_modules/a": {
-      "version": "1.0.0",
+      "version": "2.0.0",
       "dev": true
@@ -200,3 +200,3 @@
       "dev": true,
-      "version": "9.9.9"
+      "version": "9.9.10"`, []string{"changed (1): a 1.0.0→2.0.0", "(+2 version lines outside the shown context)"}},
	}
	for _, tc := range cases {
		body := tc.body
		n := strings.Count(body, "\n") + 1
		pad := strings.Repeat(" padding line that keeps the lockfile hunk large enough to summarize\n", 40)
		in := fmt.Sprintf("diff --git a/%s b/%s\nindex 1..2 100644\n--- a/%s\n+++ b/%s\n%s\n%s", tc.name, tc.name, tc.name, tc.name,
			growHunk(body, 40), strings.TrimSuffix(pad, "\n"))
		_ = n
		got := applyDiff(t, in)
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q missing from\n%s", tc.name, w, got)
			}
		}
		if strings.Contains(got, "padding line") {
			t.Errorf("%s: lockfile body shown", tc.name)
		}
	}
}

func growHunk(body string, k int) string {
	lines := strings.Split(body, "\n")
	last := -1
	for i, ln := range lines {
		if strings.HasPrefix(ln, "@@ ") {
			last = i
		}
	}
	m := regexp.MustCompile(`^@@ -(\d+),(\d+) \+(\d+),(\d+) @@`).FindStringSubmatch(lines[last])
	var a, b, c, d int
	fmt.Sscan(m[1], &a)
	fmt.Sscan(m[2], &b)
	fmt.Sscan(m[3], &c)
	fmt.Sscan(m[4], &d)

	b, d = 0, 0
	for _, ln := range lines[last+1:] {
		switch {
		case strings.HasPrefix(ln, "+"):
			d++
		case strings.HasPrefix(ln, "-"):
			b++
		default:
			b++
			d++
		}
	}
	lines[last] = fmt.Sprintf("@@ -%d,%d +%d,%d @@", a, b+k, c, d+k)
	return strings.Join(lines, "\n")
}

func TestSmallLockfileStaysVerbatim(t *testing.T) {
	in := "diff --git a/package-lock.json b/package-lock.json\nindex 1..2 100644\n--- a/package-lock.json\n+++ b/package-lock.json\n@@ -1,4 +1,4 @@\n {\n   \"name\": \"x\",\n-  \"version\": \"1.0.0\",\n+  \"version\": \"1.0.1\","
	if got := applyDiff(t, in); !strings.Contains(got, `+  "version": "1.0.1",`) {
		t.Errorf("small lockfile edit hidden:\n%s", got)
	}
}

func TestGeneratedFileSummary(t *testing.T) {
	var b strings.Builder
	b.WriteString("diff --git a/dist/app.min.js b/dist/app.min.js\nindex 1..2 100644\n--- a/dist/app.min.js\n+++ b/dist/app.min.js\n@@ -1 +1 @@\n")
	fmt.Fprintf(&b, "-%s\n+%s", strings.Repeat("var a=1;", 300), strings.Repeat("var b=2;", 300))
	got := applyDiff(t, b.String())
	if got != "diff --git a/dist/app.min.js b/dist/app.min.js\n[generated file: +1 -1 lines in 1 hunk not shown]" {
		t.Errorf("got\n%s", got)
	}
}

func TestLogRendering(t *testing.T) {
	in := `commit 1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa (HEAD -> main, origin/main)
Author: Jane Doe <jane@example.com>
Date:   Fri Sep 25 08:01:37 2026 +0200

    feat!: new API
    that spans two lines

    Some rationale that is dropped.
    BREAKING CHANGE: the old API is gone.
    Fixes #12
    Closes org/repo#34
    This reverts commit abcdef0.
    Signed-off-by: Jane Doe <jane@security.example.com>
    - Fix CVE-2026-1234 in the parser,
      found by fuzzing
    - unrelated item

commit 2222222bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
Merge: 1111111 3333333
Author: Bot <bot@example.com>
Date:   Thu Sep 24 10:00:00 2026 -0400

    Merge pull request #7 from x/y

    Add the thing
`
	got, ok := logFilter{}.Apply(ctx(0, "git", "log"), in)
	if !ok {
		t.Fatal("bailed")
	}
	want := `1111111 (HEAD -> main, origin/main) 2026-09-25 [Jane Doe] feat!: new API that spans two lines [+3 body lines]
    BREAKING CHANGE: the old API is gone.
    Fixes #12
    Closes org/repo#34
    This reverts commit abcdef0.
    - Fix CVE-2026-1234 in the parser,
      found by fuzzing
2222222 2026-09-24 [Bot] Merge pull request #7 from x/y [merge 1111111 3333333]
    Add the thing`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestLogDates(t *testing.T) {
	for in, want := range map[string]string{
		"Fri Sep 25 08:01:37 2026 +0200":  "2026-09-25",
		"Fri Sep 25 23:59:59 2026 -1100":  "2026-09-25",
		"Fri, 25 Sep 2026 08:01:37 +0200": "2026-09-25",
		"2026-09-25 08:01:37 +0200":       "2026-09-25",
		"2026-09-25T08:01:37+02:00":       "2026-09-25",
		"3 days ago":                      "3 days ago",
		"1695621697 +0200":                "1695621697 +0200",
	} {
		if got := shortDate(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestLogAbbrevUnique(t *testing.T) {
	in := "commit abcdef1234567890000000000000000000000000\nAuthor: A <a>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\n    one\n\n" +
		"commit abcdef1299999999999999999999999999999999\nAuthor: A <a>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\n    two\n"
	got, _ := logFilter{}.Apply(ctx(0, "git", "log"), in)
	if !strings.Contains(got, "abcdef123 ") || !strings.Contains(got, "abcdef129 ") {
		t.Errorf("ambiguous abbreviations:\n%s", got)
	}
}

func TestLogUserFormatsVerbatim(t *testing.T) {
	in := "* 1234567 (HEAD -> main) subject\n* 89abcde other"
	for _, flags := range [][]string{{"--oneline"}, {"--graph"}, {"--pretty=fuller"}, {"--format=short"}} {
		argv := append([]string{"git", "log"}, flags...)
		got, ok := logFilter{}.Apply(ctx(0, argv...), in)
		if !ok || got != in {
			t.Errorf("%v: not verbatim", flags)
		}
	}

	med := "commit 1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nAuthor: A <a>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\n    s"
	if got, _ := (logFilter{}).Apply(ctx(0, "git", "log", "--pretty=medium"), med); got != "1111111 2006-01-02 [A] s" {
		t.Errorf("--pretty=medium: %q", got)
	}
}

func TestShowKeepsMessageAndBlobs(t *testing.T) {
	in := "commit 1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nAuthor: A <a@b>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\n    subject\n\n    para one\n    Signed-off-by: A <a@b>\n\n" +
		"diff --git a/x b/x\nindex 1..2 100644\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b"
	got, _ := showFilter{}.Apply(ctx(0, "git", "show"), in)
	want := "1111111 2006-01-02 [A <a@b>] subject\n    para one\n    Signed-off-by: A <a@b>\ndiff --git a/x b/x\n@@ -1 +1 @@\n-a\n+b"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	blob := "package main\n\nfunc main() {}\n"
	if got, ok := (showFilter{}).Apply(ctx(0, "git", "show", "HEAD:main.go"), blob); !ok || got != blob {
		t.Error("blob not verbatim")
	}
}

func TestSyncKeepsRejectionsAndGroupsRefs(t *testing.T) {
	in := `To github.com:o/r.git
 ! [rejected]        main -> main (non-fast-forward)
 * [new branch]      a -> a
 * [new branch]      b -> b
 * [new branch]      c -> c
 * [new branch]      fix-failing-build -> fix-failing-build
 * [new branch]      d -> d
error: failed to push some refs to 'github.com:o/r.git'
hint: Updates were rejected because the tip of your current branch is behind`
	got, ok := syncFilter{}.Apply(ctx(1, "git", "push"), in)
	if !ok {
		t.Fatal("bailed")
	}
	for _, ln := range []string{
		" ! [rejected]        main -> main (non-fast-forward)",
		" * [new branch]      fix-failing-build -> fix-failing-build",
		"error: failed to push some refs to 'github.com:o/r.git'",
		"hint: Updates were rejected because the tip of your current branch is behind",
	} {
		if !strings.Contains(got, ln) {
			t.Errorf("%q lost:\n%s", ln, got)
		}
	}
	if m := fixture.ErrorLinesMissing(in, got); len(m) > 0 {
		t.Errorf("error lines missing: %q", m)
	}
}

func TestSyncDropsOnlyNoise(t *testing.T) {
	in := `remote: Enumerating objects: 5, done.
remote: Counting objects: 100% (5/5), done.
remote: Total 3 (delta 2), reused 3 (delta 2), pack-reused 0
Unpacking objects: 100% (3/3), 1.2 KiB | 300.00 KiB/s, done.
remote:
remote: Create a pull request for 'feat' on GitHub by visiting:
remote:      https://github.com/o/r/pull/new/feat
From github.com:o/r
   1a2b3c4..5d6e7f8  main       -> origin/main
Updating 1a2b3c4..5d6e7f8
Fast-forward
 a.go | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)`
	got, _ := syncFilter{}.Apply(ctx(0, "git", "pull"), in)
	want := `remote: Create a pull request for 'feat' on GitHub by visiting:
remote:      https://github.com/o/r/pull/new/feat
From github.com:o/r
   1a2b3c4..5d6e7f8  main       -> origin/main
Updating 1a2b3c4..5d6e7f8
Fast-forward
 a.go +1 -1
 1 file changed, 1 insertion(+), 1 deletion(-)`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestMergeAutoMerging(t *testing.T) {
	in := "Auto-merging a.go\nAuto-merging src/fail.go\nAuto-merging my file.go\nCONFLICT (content): Merge conflict in a.go\nAuto-merging b.go\nAutomatic merge failed; fix conflicts and then commit the result."
	got, _ := mergeFilter{}.Apply(ctx(1, "git", "merge", "x"), in)

	want := "Auto-merging (3): a.go src/fail.go b.go\nAuto-merging my file.go\nCONFLICT (content): Merge conflict in a.go\nAutomatic merge failed; fix conflicts and then commit the result."
	if got != want {
		t.Errorf("got\n%s", got)
	}
}

func TestCommitHooks(t *testing.T) {
	in := `black....................................................................Passed
isort....................................................................Passed
mypy.................................................(no files to check)Skipped
flake8...................................................................Failed
- hook id: flake8
- exit code: 1

a.py:1:1: F401 'os' imported but unused`
	got, ok := commitFilter{}.Apply(ctx(1, "git", "commit", "-m", "x"), in)
	if !ok {
		t.Fatal("bailed")
	}
	want := "[pre-commit: 2 passed, 1 skipped hooks not shown]\nflake8...................................................................Failed\n- hook id: flake8\n- exit code: 1\na.py:1:1: F401 'os' imported but unused"
	if got != want {
		t.Errorf("got\n%s", got)
	}
}

func TestBlameShapes(t *testing.T) {
	in := `4b68a5f1 (Jane Doe  2022-05-28 10:42:28 +0800 1) a
4b68a5f1 (Jane Doe  2022-05-28 10:42:28 +0800 2)
^15216a0 (Old Dev   2014-06-18 01:42:34 +0200 3) 	b
4b68a5f1 (Jane Doe  2022-05-28 10:42:28 +0800 4) c`
	got, _ := blameFilter{}.Apply(ctx(0, "git", "blame", "x"), in)
	want := "4b68a5f1 (Jane Doe 2022-05-28)\n 1) a\n 2)\n^15216a0 (Old Dev 2014-06-18) 3) \tb\n4b68a5f1 4) c"
	if got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}

	withFile := "4b68a5f1 old.go (Jane 2022-05-28 10:42:28 +0800 1) a\n4b68a5f1 old.go (Jane 2022-05-28 10:42:28 +0800 2) b\n5c5c5c5c new.go (Bob  2023-01-01 00:00:00 +0000 3) c"
	got, ok := blameFilter{}.Apply(ctx(0, "git", "blame", "x"), withFile)
	if !ok || !strings.Contains(got, "4b68a5f1 old.go (Jane 2022-05-28)\n 1) a\n 2) b") {
		t.Errorf("filename column: %q", got)
	}

	if _, ok := (blameFilter{}).Apply(ctx(0, "git", "blame", "x"), "4b68a5f1 (Jane 2022-05-28 1) a"); !ok {
		t.Error("short date bailed")
	}

	porc := "4b68a5f1 1 1 2\nauthor Jane\nauthor-time 1653705748\n\ta\n4b68a5f1 2 2\n\tb"
	for _, flag := range []string{"-p", "--line-porcelain", "--incremental"} {
		if got, ok := (blameFilter{}).Apply(ctx(0, "git", "blame", flag, "x"), porc); !ok || got != porc {
			t.Errorf("%s: porcelain reshaped", flag)
		}
	}
}

func TestBranchShapes(t *testing.T) {
	in := "* (HEAD detached at 1a2b3c4)\n  dev\n+ wt-branch\n  main\n  remotes/origin/HEAD -> origin/main\n  remotes/origin/a\n  remotes/origin/b\n  remotes/origin/c\n  remotes/origin/d\n  remotes/upstream/main"
	got, ok := branchFilter{}.Apply(ctx(0, "git", "branch", "-a"), in)
	want := "* (HEAD detached at 1a2b3c4)\n+ wt-branch\n  dev\n  main\n  remotes/origin/HEAD -> origin/main\n  remotes/origin/ (4):\n    a b c d\n  remotes/upstream/main"
	if !ok || got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestBraceRuns(t *testing.T) {
	got := strings.Join(braceRuns([]string{"v1.0.0", "v1.0.1", "v1.0.2", "v1.1.0", "v2.0.0", "v2.0.1", "release", "a.b", "a.c", "a.d"}), " ")
	want := "v1.0.{0,1,2} v1.1.0 v2.0.0 v2.0.1 release a.{b,c,d}"
	if got != want {
		t.Errorf("got %s", got)
	}
}

func TestRemoteV(t *testing.T) {
	in := "origin\thttps://x/r.git (fetch)\norigin\thttps://x/r.git (push)\nfork\tgit@y:r.git (fetch)\nfork\tno_push (push)"
	got, _ := remoteFilter{}.Apply(ctx(0, "git", "remote", "-v"), in)
	want := "origin\thttps://x/r.git (fetch, push)\nfork\tgit@y:r.git (fetch)\nfork\tno_push (push)"
	if got != want {
		t.Errorf("got\n%s", got)
	}
}

func TestPathologicalShapes(t *testing.T) {
	var b strings.Builder
	rep := func(n int, format string, args ...func(int) any) string {
		b.Reset()
		for i := 0; i < n; i++ {
			vals := make([]any, len(args))
			for k, a := range args {
				vals[k] = a(i)
			}
			fmt.Fprintf(&b, format, vals...)
		}
		return b.String()
	}
	id := func(i int) any { return i }
	odd := func(i int) any { return i % 2 }
	cases := []struct {
		name string
		argv []string
		out  string
	}{
		{"go.sum one module many versions", []string{"git", "diff"},
			"diff --git a/go.sum b/go.sum\n@@ -1,20000 +1,20000 @@\n" + rep(20000, "-example.com/m v1.0.%d h1:x=\n+example.com/m v2.0.%d h1:y=\n", id, id)},
		{"status many renames", []string{"git", "status"},
			"On branch main\nChanges to be committed:\n" + rep(20000, "\trenamed:    a%d -> b%d\n", id, id) +
				"\nChanges not staged for commit:\n" + rep(20000, "\tmodified:   b%d\n", id)},
		{"one commit huge body", []string{"git", "log"},
			"commit 1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nAuthor: A <a>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\n    s\n\n" + rep(50000, "    body line %d fixes #%d\n", id, id)},
		{"identical hunks in many files", []string{"git", "diff"},
			rep(20000, "diff --git a/f%d b/f%d\n@@ -1 +1 @@\n-x\n+y\n", id, id)},
		{"blame alternating commits", []string{"git", "blame", "x"},
			rep(50000, "%08d (A 2020-01-01 00:00:00 +0000 %d) x\n", odd, id)},
		{"many local branches", []string{"git", "branch"}, rep(50000, "  b%d\n", id)},
		{"many create mode lines", []string{"git", "commit"},
			"[main 1234567] x\n" + rep(50000, " create mode 10064%d f%d\n", odd, id)},
		{"many auto-merging lines", []string{"git", "merge", "x"}, rep(50000, "Auto-merging f%d\n", id)},
		{"alternating ref kinds", []string{"git", "fetch"},
			"From x\n" + rep(50000, " * [new tag]         t%d -> t%d\n   1234567..89abcde  b%d -> origin/b%d\n", id, id, id, id)},
		{"stat rows", []string{"git", "pull"}, "Updating 1..2\nFast-forward\n" + rep(50000, " d%d/f.go | 2 +-\n", id)},
		{"one huge line", []string{"git", "diff"},
			"diff --git a/x b/x\n@@ -1 +1 @@\n-" + strings.Repeat("a", 2<<20) + "\n+" + strings.Repeat("b", 2<<20)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ctx(0, tc.argv...)
			f := find(t, c)
			start := time.Now()
			got, _ := f.Apply(c, tc.out)
			if el := time.Since(start); el > testenv.Scale(3*time.Second) {
				t.Errorf("%s took %v", f.Name(), el)
			}
			t.Logf("%d bytes → %d bytes in %v", len(tc.out), len(got), time.Since(start).Round(time.Millisecond))
		})
	}
}

func TestLogSignatureLinesKept(t *testing.T) {
	in := "commit 1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\ngpg: Signature made Mon Jan  2 15:04:05 2006\ngpg: Good signature from \"A <a@b>\"\nAuthor: A <a@b>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\n    subject"
	got, ok := logFilter{}.Apply(ctx(0, "git", "log", "--show-signature"), in)
	want := "1111111 2006-01-02 [A] subject\n    gpg: Signature made Mon Jan  2 15:04:05 2006\n    gpg: Good signature from \"A <a@b>\""
	if !ok || got != want {
		t.Errorf("got\n%s", got)
	}
}

func TestLongOnelineLogKeepsNewest(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, "%07x commit subject number %d\n", i, i)
	}
	b.WriteString("fatal: bad object deadbeef")
	got, ok := logFilter{}.Apply(ctx(128, "git", "log", "--oneline"), b.String())
	if !ok || !strings.HasPrefix(got, "0000000 commit subject number 0\n") {
		t.Fatalf("head lost")
	}
	if n := tokens.Count(got); n > logBudget {
		t.Errorf("%d tokens", n)
	}
	if !regexp.MustCompile(`\[… \+\d+ more lines not shown \(20001 total\)\]`).MatchString(got) || !strings.HasSuffix(got, "fatal: bad object deadbeef") {
		t.Errorf("tail:\n%s", got[len(got)-300:])
	}
}

func TestShowAnnotatedTag(t *testing.T) {
	in := "tag v1.0\nTagger: A <a@b>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\nRelease 1.0\n\ncommit 1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nAuthor: A <a@b>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\n    subject\n"
	got, ok := showFilter{}.Apply(ctx(0, "git", "show", "v1.0"), in)
	want := "tag v1.0\nTagger: A <a@b>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\nRelease 1.0\n\n1111111 2006-01-02 [A <a@b>] subject"
	if !ok || got != want {
		t.Errorf("got\n%q\nwant\n%q", got, want)
	}
}
