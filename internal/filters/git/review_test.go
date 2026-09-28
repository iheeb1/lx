package git

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/testenv"
	"github.com/iheeb1/lx/internal/tokens"
)

func TestStatusStagedDeletionAndUntrackedSamePath(t *testing.T) {
	in := "On branch main\nChanges to be committed:\n  (use \"git restore --staged <file>...\" to unstage)\n\tdeleted:    History.md\n\n" +
		"Untracked files:\n  (use \"git add <file>...\" to include in what will be committed)\n\tHistory.md\n"
	got, ok := status{}.Apply(ctx(0, "git", "status"), in)
	want := "## main\nD  History.md\n?? History.md"
	if !ok || got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}

	in = "On branch main\nChanges to be committed:\n\tdeleted:    build.log\n\nIgnored files:\n\tbuild.log\n"
	if got, _ := (status{}).Apply(ctx(0, "git", "status", "--ignored"), in); got != "## main\nD  build.log\n!! build.log" {
		t.Errorf("ignored: got\n%s", got)
	}
}

func TestStatusGuardsErrorNamedPaths(t *testing.T) {
	var b strings.Builder
	b.WriteString("On branch fix-panic\nYour branch is ahead of 'origin/fix-panic' by 1 commit.\n  (use \"git push\" to publish your local commits)\n\n")
	b.WriteString("Changes not staged for commit:\n  (use \"git add <file>...\" to update what will be committed)\n")
	for _, p := range []string{"src/panic.go", "fail.go", "exception/handler.py", "fatal.c", "conflict.go"} {
		fmt.Fprintf(&b, "\tmodified:   %s\n", p)
	}
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "\tmodified:   internal/pkg/sub/file%02d.go\n", i)
	}
	b.WriteString("\tdeleted:    test/timeout_killed_test.go\n\nUntracked files:\n\tdenied.txt\n\nno changes added to commit (use \"git add\" and/or \"git commit -a\")\n")
	in := b.String()
	c := ctx(0, "git", "status")
	res := engine.Process(c, in, engine.Options{})
	if res.Filter != "git-status" || res.GuardAdded != 0 {
		t.Fatalf("filter %s, guard added %d:\n%s", res.Filter, res.GuardAdded, res.Output)
	}
	for _, m := range fixture.ErrorLinesMissing(in, res.Output) {
		if !statusConverted(m, res.Output) {
			t.Errorf("error line lost: %q", m)
		}
	}
	if !strings.HasPrefix(res.Output, "## fix-panic...origin/fix-panic [ahead 1]\n") {
		t.Errorf("branch line:\n%s", res.Output)
	}
}

func TestStatusKeepsUnconvertedErrorLines(t *testing.T) {
	in := "warning: could not open directory 'secret/': Permission denied\nOn branch main\nChanges not staged for commit:\n" +
		"  (use \"git add <file>...\" to update what will be committed)\n  (fatal: unable to refresh the index)\n\tmodified:   a.go\n"
	got, ok := status{}.Apply(ctx(128, "git", "status"), in)
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{"warning: could not open directory 'secret/': Permission denied", "  (fatal: unable to refresh the index)", " M a.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing from\n%s", want, got)
		}
	}
}

func TestStatusOldUpToDateWording(t *testing.T) {
	in := "On branch master\nYour branch is up-to-date with 'origin/master'.\nChanges not staged for commit:\n  (use \"git add <file>...\" to update what will be committed)\n\n\tmodified:   a.go\n\nno changes added to commit (use \"git add\" and/or \"git commit -a\")"
	got, _ := status{}.Apply(ctx(0, "git", "status"), in)
	if got != "## master...origin/master\n M a.go" {
		t.Errorf("got\n%s", got)
	}
}

func TestDiffKeepsCopyLines(t *testing.T) {
	in := "diff --git a/a.go b/b.go\nsimilarity index 100%\ncopy from a.go\ncopy to b.go\n" +
		"diff --git a/c.go b/d.go\nsimilarity index 100%\nrename from c.go\nrename to d.go"
	want := "diff --git a/a.go b/b.go\nsimilarity index 100%\ncopy from a.go\ncopy to b.go\n" +
		"diff --git a/c.go b/d.go\nsimilarity index 100%"
	if got := applyDiff(t, in); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func lockDiffWithConflict(name string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\nindex 1..2 100644\n--- a/%s\n+++ b/%s\n@@ -10,6 +10,66 @@\n", name, name, name, name)
	b.WriteString(" a@^1:\n   version \"1.0.0\"\n   resolved \"x\"\n+<<<<<<< HEAD\n")
	for i := 0; i < 28; i++ {
		fmt.Fprintf(&b, "+pkg-a-long-name-%02d@^1.0.0:\n", i)
	}
	b.WriteString("+=======\n")
	for i := 0; i < 29; i++ {
		fmt.Fprintf(&b, "+pkg-b-long-name-%02d@^2.0.0:\n", i)
	}
	b.WriteString("+>>>>>>> feature\n b@^1:\n   version \"1.0.0\"\n   resolved \"y\"")
	return b.String()
}

func TestDiffConflictMarkersNeverHidden(t *testing.T) {
	for _, name := range []string{"yarn.lock", "dist/app.min.js"} {
		got := applyDiff(t, lockDiffWithConflict(name))
		for _, want := range []string{
			"[unresolved conflict markers added (3 lines), by new-file line:",
			"  13: +<<<<<<< HEAD", "  42: +=======", "  72: +>>>>>>> feature]",
		} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: %q missing from\n%s", name, want, got)
			}
		}
	}

	small := "diff --git a/a.go b/a.go\n@@ -1,2 +1,6 @@\n x\n+<<<<<<< HEAD\n+a\n+=======\n+b\n+>>>>>>> t\n y"
	if got := applyDiff(t, small); strings.Contains(got, "unresolved") {
		t.Errorf("note on a fully shown file:\n%s", got)
	}

	var md strings.Builder
	md.WriteString("diff --git a/dist/README.md b/dist/README.md\n@@ -1,0 +1,200 @@\n+Title\n+=======\n")
	for i := 0; i < 198; i++ {
		fmt.Fprintf(&md, "+generated documentation line number %d with enough words\n", i)
	}
	if got := applyDiff(t, md.String()); strings.Contains(got, "conflict") {
		t.Errorf("heading underline reported as a conflict:\n%s", got)
	}
}

func TestDiffConflictMarkersSurviveCuts(t *testing.T) {
	var b strings.Builder
	b.WriteString("diff --git a/big.go b/big.go\nindex 1..2 100644\n--- a/big.go\n+++ b/big.go\n")
	for h := 0; h < 6; h++ {
		start := h*1000 + 1
		fmt.Fprintf(&b, "@@ -%d,120 +%d,125 @@\n", start, start)
		for i := 0; i < 120; i++ {
			fmt.Fprintf(&b, "-\told%d := compute(%d, %d) // a fairly long line of code\n+\tnew%d := compute(%d, %d) // a fairly long line of code\n", i, h, i, i, h, i)
		}
		if h == 4 {
			b.WriteString("+<<<<<<< HEAD\n+x\n+=======\n+y\n+>>>>>>> topic\n")
		} else {
			b.WriteString("+a\n+b\n+c\n+d\n+e\n")
		}
	}
	got := applyDiff(t, b.String())
	if !strings.Contains(got, "[unresolved conflict markers added (3 lines), by new-file line:\n  4121: +<<<<<<< HEAD") {
		t.Errorf("markers in a cut hunk not listed:\n%s", got[:min(len(got), 600)])
	}

	var many strings.Builder
	for f := 0; f < 400; f++ {
		fmt.Fprintf(&many, "diff --git a/f%03d.go b/f%03d.go\n@@ -1,3 +1,3 @@\n-\told := value(%d) // some padding words here\n+\tnew := value(%d) // some padding words here\n more context\n", f, f, f, f)
	}
	many.WriteString("diff --git a/zz.go b/zz.go\n@@ -1,1 +1,3 @@\n+<<<<<<< HEAD\n x\n+>>>>>>> topic\n")
	got = applyDiff(t, many.String())
	if !strings.Contains(got, "zz.go +2 (2 conflict-marker lines)") {
		t.Errorf("listed file not flagged:\n%s", got[max(0, len(got)-400):])
	}
}

func TestDiffDeletedFileBodiesCounted(t *testing.T) {
	var b strings.Builder
	b.WriteString("diff --git a/gone.md b/gone.md\ndeleted file mode 100644\nindex 1..0\n--- a/gone.md\n+++ /dev/null\n@@ -1,20 +0,0 @@\n")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "-line %d\n", i)
	}
	b.WriteString("diff --git a/tiny.txt b/tiny.txt\ndeleted file mode 100644\nindex 1..0\n--- a/tiny.txt\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-a\n-b")
	want := "diff --git a/gone.md b/gone.md\ndeleted file mode 100644\n[deleted file: 20 lines not shown]\n" +
		"diff --git a/tiny.txt b/tiny.txt\ndeleted file mode 100644\n@@ -1,2 +0,0 @@\n-a\n-b"
	if got := applyDiff(t, b.String()); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestCountDescNoContent(t *testing.T) {
	if got := countDesc(&fileDiff{}); got != "0" {
		t.Errorf("got %q", got)
	}
}

func TestLogStatNativeIndent(t *testing.T) {
	in := "commit 1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nAuthor: A <a@b>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\n    subject\n\n    Fixes #1\n\n a.go | 2 +-\n 1 file changed, 1 insertion(+), 1 deletion(-)\n"
	got, _ := logFilter{}.Apply(ctx(0, "git", "log", "--stat"), in)
	want := "1111111 2006-01-02 [A] subject\n    Fixes #1\n a.go +1 -1\n 1 file changed, 1 insertion(+), 1 deletion(-)"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestLogCutListsCommitsWithoutChanges(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "commit %07x%033d\nAuthor: A <a@b>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\n    subject %d\n", 0xa000000+i, 0, i)
		if i == 30 {
			b.WriteString("\n    Fixes #77\n")
		}
		fmt.Fprintf(&b, "\n src/fail%d.go | 60 ++++++++++++++++++++++++++++++\n 1 file changed, 60 insertions(+)\n\n", i)
		fmt.Fprintf(&b, "diff --git a/src/fail%d.go b/src/fail%d.go\nindex 1..2 100644\n--- a/src/fail%d.go\n+++ b/src/fail%d.go\n@@ -1,0 +1,60 @@\n", i, i, i, i)
		for k := 0; k < 60; k++ {
			fmt.Fprintf(&b, "+\tline %d of commit %d with a few words of code\n", k, i)
		}
		b.WriteString("\n")
	}
	b.WriteString("fatal: unable to read tree deadbeef\n")
	in := b.String()
	got, ok := logFilter{}.Apply(ctx(128, "git", "log", "-p", "--stat"), in)
	if !ok {
		t.Fatal("bailed")
	}
	if n := tokens.Count(got); n > logBudget {
		t.Errorf("%d tokens", n)
	}
	m := listedRe.FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("no listing marker:\n%s", got[len(got)-500:])
	}
	for i := 0; i < 40; i++ {
		sha := fmt.Sprintf("%07x", 0xa000000+i)
		if !strings.Contains(got, sha+" 2006-01-02 [A] subject") && moreCommitRe.FindString(got) == "" {
			t.Errorf("commit %d neither shown nor counted", i)
		}
	}
	if !strings.Contains(got, "    Fixes #77") {
		t.Error("kept body line of a listed commit dropped")
	}
	if !strings.HasSuffix(got, "fatal: unable to read tree deadbeef") {
		t.Errorf("fatal line lost: %q", got[len(got)-200:])
	}
	cut := got[strings.Index(got, m[0]):]
	if strings.Contains(cut, "fail3") && strings.Contains(cut, "| 60") {
		t.Errorf("stat rows of listed commits printed after the cut:\n%s", cut)
	}

	logDrops(t, in, got, fixture.ErrorLinesMissing(in, got))
}

func TestSyncKeepsLastProgressOnFailure(t *testing.T) {
	in := "Cloning into 'y'...\nremote: Enumerating objects: 4593, done.\nReceiving objects:  45% (2067/4593), 756.01 KiB | 724.00 KiB/s"
	got, _ := syncFilter{}.Apply(ctx(130, "git", "clone", "https://x/y.git"), in)
	if got != "Cloning into 'y'...\nReceiving objects:  45% (2067/4593), 756.01 KiB | 724.00 KiB/s" {
		t.Errorf("exit 130: got\n%s", got)
	}
	got, _ = syncFilter{}.Apply(ctx(0, "git", "clone", "https://x/y.git"), in)
	if got != "Cloning into 'y'..." {
		t.Errorf("exit 0: got\n%s", got)
	}
}

func TestDiffstatCapped(t *testing.T) {
	var b strings.Builder
	b.WriteString("Updating 1234567..89abcde\nFast-forward\n")
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&b, " dir%d/file.go | 2 +-\n", i)
	}
	b.WriteString(" 1000 files changed, 1000 insertions(+), 1000 deletions(-)")
	got, _ := syncFilter{}.Apply(ctx(0, "git", "pull"), b.String())
	if !strings.Contains(got, "… +700 more files") || !strings.HasSuffix(got, " 1000 files changed, 1000 insertions(+), 1000 deletions(-)") {
		t.Errorf("got tail %q", got[max(0, len(got)-200):])
	}
	if n := tokens.Count(got); n > 5000 {
		t.Errorf("%d tokens", n)
	}
}

func TestBlameCutWithSeveralRanges(t *testing.T) {
	var b strings.Builder
	for _, r := range [][2]int{{1, 400}, {1000, 1400}} {
		for n := r[0]; n <= r[1]; n++ {
			fmt.Fprintf(&b, "%08x (Author %d 2020-01-02 03:04:05 +0000 %4d) some code on line %d with words\n", n, n%5, n, n)
		}
	}
	got, ok := blameFilter{}.Apply(ctx(0, "git", "blame", "-L", "1,400", "-L", "1000,1400", "x.go"), b.String())
	if !ok {
		t.Fatal("bailed")
	}
	if strings.Contains(got, "add -L") || !strings.Contains(got, "in several ranges]") {
		t.Errorf("tail %q", got[len(got)-200:])
	}
}

func TestFailuresStayVisible(t *testing.T) {
	pad := strings.Repeat("remote: Counting objects: 100% (5/5), done.\n", 3)
	cases := []struct {
		name string
		argv []string
		exit int
		in   string
		must []string
	}{
		{"push rejected by a hook after an up-to-date line", []string{"git", "push"}, 1,
			pad + "Everything up-to-date\nremote: error: GH006: Protected branch update failed for refs/heads/main.\nremote: error: Changes must be made through a pull request.\nTo github.com:o/r.git\n ! [remote rejected] main -> main (protected branch hook declined)\nerror: failed to push some refs to 'github.com:o/r.git'",
			[]string{" ! [remote rejected] main -> main (protected branch hook declined)", "error: failed to push some refs"}},
		{"push secret scanning", []string{"git", "push"}, 1,
			pad + "remote: error: GH013: Repository rule violations found for refs/heads/main.\nremote:\nremote: - GITHUB PUSH PROTECTION\nremote:     Resolve the following violations before pushing again\nremote:     - Push cannot contain secrets\nTo github.com:o/r.git\n ! [remote rejected] main -> main (push declined due to repository rule violations)\nerror: failed to push some refs to 'github.com:o/r.git'",
			[]string{"remote:     - Push cannot contain secrets", "(push declined due to repository rule violations)"}},
		{"pull refused", []string{"git", "pull"}, 128,
			pad + "From github.com:o/r\n * branch            main       -> FETCH_HEAD\nhint: You have divergent branches and need to specify how to reconcile them.\nfatal: Need to specify how to reconcile divergent branches.",
			[]string{"fatal: Need to specify how to reconcile divergent branches."}},
		{"pull rebase conflict", []string{"git", "pull", "--rebase"}, 1,
			pad + "Auto-merging a.go\nCONFLICT (content): Merge conflict in a.go\nerror: could not apply 1234567... change a\nhint: Resolve all conflicts manually, mark them as resolved with\nCould not apply 1234567... change a",
			[]string{"CONFLICT (content): Merge conflict in a.go", "error: could not apply 1234567... change a"}},
		{"merge conflict on many files", []string{"git", "merge", "topic"}, 1,
			strings.Repeat("Auto-merging src/file.go\n", 1) + "Auto-merging a.go\nAuto-merging b.go\nAuto-merging c.go\nAuto-merging d.go\nAuto-merging e.go\nAuto-merging f.go\nAuto-merging g.go\nAuto-merging h.go\nAuto-merging i.go\nAuto-merging j.go\nAuto-merging k.go\nAuto-merging l.go\nAuto-merging m.go\nAuto-merging n.go\nAuto-merging o.go\nAuto-merging p.go\nAuto-merging q.go\nAuto-merging r.go\nAuto-merging s.go\nAuto-merging t.go\nAuto-merging u.go\nAuto-merging v.go\nCONFLICT (modify/delete): x.go deleted in topic and modified in HEAD.  Version HEAD of x.go left in tree.\nAutomatic merge failed; fix conflicts and then commit the result.",
			[]string{"CONFLICT (modify/delete): x.go deleted in topic and modified in HEAD.  Version HEAD of x.go left in tree.", "Automatic merge failed; fix conflicts and then commit the result."}},
		{"commit hook failed after passing hooks", []string{"git", "commit", "-m", "x"}, 1,
			strings.Repeat("check yaml...............................................................Passed\n", 6) +
				"flake8...................................................................Failed\n- hook id: flake8\n- exit code: 1\n\na.py:1:1: F401 'os' imported but unused\na.py:9:80: E501 line too long (88 > 79 characters)",
			[]string{"flake8...................................................................Failed", "a.py:1:1: F401 'os' imported but unused"}},
		{"clone interrupted", []string{"git", "clone", "https://x/y.git"}, 130,
			"Cloning into 'y'...\n" + pad + "Receiving objects:  45% (2067/4593), 756.01 KiB | 724.00 KiB/s",
			[]string{"Receiving objects:  45% (2067/4593)"}},
		{"diff of a lockfile with an unresolved merge", []string{"git", "diff"}, 0,
			lockDiffWithConflict("package-lock.json"), []string{"+<<<<<<< HEAD", "+>>>>>>> feature"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ctx(tc.exit, tc.argv...)
			res := engine.Process(c, tc.in, engine.Options{})
			for _, m := range tc.must {
				if !strings.Contains(res.Output, m) {
					t.Errorf("%q not visible (filter %s):\n%s", m, res.Filter, res.Output)
				}
			}
			if miss := fixture.ErrorLinesMissing(tc.in, res.Output); len(miss) > 0 {
				t.Errorf("error lines missing: %q", miss)
			}
			if res.GuardAdded != 0 {
				t.Errorf("guard re-added %d lines:\n%s", res.GuardAdded, res.Output)
			}
			if c.Failed() {
				if n, m := len(passWordRe.FindAllString(res.Output, -1)), len(passWordRe.FindAllString(tc.in, -1)); n > m {
					t.Errorf("pass-like words added (%d > %d):\n%s", n, m, res.Output)
				}
			}
		})
	}
}

func TestFiftyThousandLinesFast(t *testing.T) {
	gen := func(n int, f func(b *strings.Builder, i int)) string {
		var b strings.Builder
		for i := 0; i < n; i++ {
			f(&b, i)
		}
		return b.String()
	}
	cases := []struct {
		argv []string
		in   string
	}{
		{[]string{"git", "log"}, gen(10000, func(b *strings.Builder, i int) {
			fmt.Fprintf(b, "commit %040x\nAuthor: A U Thor <a@example.com>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\n    fix: subject %d (#%d)\n", i+1, i, i)
		})},
		{[]string{"git", "log", "--stat"}, gen(6250, func(b *strings.Builder, i int) {
			fmt.Fprintf(b, "commit %040x\nAuthor: A <a@b>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\n    s%d\n\n a/b%d.go | 3 +--\n 1 file changed, 1 insertion(+), 2 deletions(-)\n", i+1, i, i)
		})},
		{[]string{"git", "diff"}, gen(6250, func(b *strings.Builder, i int) {
			fmt.Fprintf(b, "diff --git a/f%d.go b/f%d.go\nindex 1..2 100644\n--- a/f%d.go\n+++ b/f%d.go\n@@ -1,2 +1,2 @@\n-a%d\n+b%d\n c\n", i, i, i, i, i, i)
		})},
		{[]string{"git", "show"}, "commit 1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\nAuthor: A <a@b>\nDate:   Mon Jan 2 15:04:05 2006 -0700\n\n    s\n\ndiff --git a/package-lock.json b/package-lock.json\n@@ -1,50000 +1,50000 @@\n" +
			gen(10000, func(b *strings.Builder, i int) {
				fmt.Fprintf(b, "     \"node_modules/p%d\": {\n-      \"version\": \"1.0.%d\",\n+      \"version\": \"1.1.%d\",\n       \"dev\": true\n     },\n", i, i, i)
			})},
		{[]string{"git", "blame", "x.go"}, gen(50000, func(b *strings.Builder, i int) {
			fmt.Fprintf(b, "%08x (Some Author 2020-01-02 03:04:05 +0000 %5d) code line %d\n", i/7, i+1, i)
		})},
		{[]string{"git", "status"}, "On branch main\nChanges not staged for commit:\n" + gen(50000, func(b *strings.Builder, i int) {
			fmt.Fprintf(b, "\tmodified:   src/pkg%d/file%d.go\n", i%100, i)
		})},
		{[]string{"git", "branch", "-a"}, "* main\n" + gen(50000, func(b *strings.Builder, i int) { fmt.Fprintf(b, "  remotes/origin/feature-%d\n", i) })},
		{[]string{"git", "fetch"}, "From https://example.com/r\n" + gen(50000, func(b *strings.Builder, i int) {
			fmt.Fprintf(b, " * [new tag]         v%d.%d.%d -> v%d.%d.%d\n", i/1000, i/10%100, i%10, i/1000, i/10%100, i%10)
		})},
		{[]string{"git", "pull"}, "Updating 1..2\nFast-forward\n" + gen(50000, func(b *strings.Builder, i int) { fmt.Fprintf(b, " d%d/f.go | 2 +-\n", i) })},
		{[]string{"git", "merge", "x"}, gen(50000, func(b *strings.Builder, i int) { fmt.Fprintf(b, "Auto-merging src/f%d.go\n", i) })},
		{[]string{"git", "commit"}, "[main 1234567] x\n" + gen(50000, func(b *strings.Builder, i int) { fmt.Fprintf(b, " create mode 100644 f%d\n", i) })},
		{[]string{"git", "tag"}, gen(50000, func(b *strings.Builder, i int) { fmt.Fprintf(b, "v%d.%d.%d\n", i/1000, i/10%100, i%10) })},
		{[]string{"git", "log", "--oneline"}, gen(50000, func(b *strings.Builder, i int) { fmt.Fprintf(b, "%07x subject %d\n", i, i) })},
	}
	for _, tc := range cases {
		c := ctx(0, tc.argv...)
		f := find(t, c)
		best := time.Hour
		for run := 0; run < 5; run++ {
			start := time.Now()
			f.Apply(c, tc.in)
			best = min(best, time.Since(start))
		}
		t.Logf("%-12s %-10s %6d lines %v", f.Name(), strings.Join(tc.argv[1:], " "), strings.Count(tc.in, "\n"), best.Round(time.Millisecond))
		if best > testenv.Scale(200*time.Millisecond) {
			t.Errorf("%s on %d lines took %v", f.Name(), strings.Count(tc.in, "\n"), best)
		}
	}
}

func TestStatusShortFormatKept(t *testing.T) {
	var b strings.Builder
	b.WriteString("## master...origin/master\n M src/a.c\n")
	for i := 12; i < 32; i++ {
		fmt.Fprintf(&b, "?? tests/inputs/test%d\n", i)
	}
	in := strings.TrimSuffix(b.String(), "\n")
	for _, argv := range [][]string{{"git", "status", "-sb"}, {"git", "status", "-bs"}, {"git", "status", "-suno"}} {
		c := ctx(0, argv...)
		res := engine.Process(c, in, engine.Options{})
		if res.Output != in || strings.Contains(res.Output, "similar lines") {
			t.Errorf("%v: %s\n%s", argv, res.Filter, res.Output)
		}
	}

	v := "On branch main\nChanges to be committed:\n\tmodified:   a.go\n\ndiff --git a/a.go b/a.go\nindex 1..2 100644\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-x\n+y"
	if got, ok := (status{}).Apply(ctx(0, "git", "status", "-v"), v); !ok || got != v {
		t.Errorf("-v: %q", got)
	}
}

func TestListingsKeptWhole(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "%012x HEAD@{%d}: commit (amend): wip: parser rework\n", 0xa7530e6bcb01+i*7919, i)
	}
	in := strings.TrimSuffix(b.String(), "\n")
	c := ctx(0, "git", "reflog")
	res := engine.Process(c, in, engine.Options{})
	if res.Output != in {
		t.Errorf("reflog changed (%s):\n%s", res.Filter, res.Output)
	}

	b.Reset()
	for i := 0; i < 4000; i++ {
		fmt.Fprintf(&b, "%012x HEAD@{%d}: commit (amend): wip: parser rework %d\n", 0xa7530e6bcb01+i*7919, i, i)
	}
	b.WriteString("fatal: log for 'HEAD' only has 4000 entries")
	got, ok := listFilter{}.Apply(c, b.String())
	if !ok || !strings.HasPrefix(got, fmt.Sprintf("%012x HEAD@{0}:", 0xa7530e6bcb01)) ||
		!strings.HasSuffix(got, "fatal: log for 'HEAD' only has 4000 entries") || !strings.Contains(got, " more lines not shown (4001 total)]") {
		t.Errorf("cut: head %q tail %q", got[:60], got[len(got)-120:])
	}
	if _, ok := (listFilter{}).Apply(c, ""); ok {
		t.Error("empty output accepted")
	}
}

func TestWrapItemsEmptyItem(t *testing.T) {
	if got := wrapItems("", []string{""}, ", ", 10); len(got) != 1 {
		t.Errorf("got %q", got)
	}
	in := "diff --git \n@@ -1 +1 @@\ndiff --git \n@@ -1 +1 @@"
	if _, ok := (diffFilter{}).Apply(ctx(0, "git", "diff"), in); !ok {
		t.Error("bailed")
	}
}

func TestSyncProgressOnly(t *testing.T) {
	in := "remote: Enumerating objects: 5, done.\nremote: Total 3 (delta 2), reused 3 (delta 2), pack-reused 0\nUnpacking objects: 100% (3/3), 1.2 KiB | 300.00 KiB/s, done."
	if got, ok := (syncFilter{}).Apply(ctx(0, "git", "fetch"), in); !ok || got != "[3 lines of transfer progress hidden]" {
		t.Errorf("got %q ok=%v", got, ok)
	}
}

func TestCleanDryRunKeepsEveryFile(t *testing.T) {
	var b strings.Builder
	for i := 41; i <= 64; i++ {
		fmt.Fprintf(&b, "Would remove tests/inputs/test%d.out\n", i)
	}
	in := strings.TrimSuffix(b.String(), "\n")
	res := engine.Process(ctx(0, "git", "clean", "-n", "-d"), in, engine.Options{})
	if res.Output != in {
		t.Errorf("%s:\n%s", res.Filter, res.Output)
	}
}

func TestKeepBodyPrefilterExact(t *testing.T) {
	lines := []string{
		"Fixes #123", "fixes owner/repo#9", "Closes: https://github.com/o/r/issues/4", "see #12",
		"BREAKING CHANGE: x", "Revert \"y\"", "This reverts commit abc.", "security: bump", "CVE-2024-1",
		"GHSA-xxxx", "deprecate foo", "vulnerability", "Merge pull request #1234 from x/y", "(#1234)",
		"Refs: #7", "related to #8", "nothing here", "http://x.com/issues/3 fixed", "resolved #99",
	}
	for _, ln := range lines {
		if got, want := keepBody(ln), keepBodyRe.MatchString(ln); got != want {
			t.Errorf("keepBody(%q) = %v, keepBodyRe = %v", ln, got, want)
		}
	}
}
