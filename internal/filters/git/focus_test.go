package git

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/testenv"
)

func gitWith(t *testing.T, fc fixture.Case, files ...string) string {
	t.Helper()
	c := fc.Context()
	if len(files) > 0 {
		c.Focus = &engine.Focus{Files: files}
	}
	got, ok := engine.Find(c).Apply(c, fc.Clean())
	if !ok {
		t.Fatal("bailed")
	}
	return got
}

func diffHeads(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(ln, "diff --git ") || strings.HasPrefix(ln, "commit ") {
			out = append(out, ln)
		}
	}
	return out
}

func TestFocusShowFileFirst(t *testing.T) {
	fc := fixture.Load(t, "git", "git-show-big-commit")
	base := gitWith(t, fc)
	got := gitWith(t, fc, "/home/user/src/gin-json-codec/render/json.go")
	heads := diffHeads(got)
	if heads[0] != "diff --git a/render/json.go b/render/json.go" {
		t.Errorf("focused file not first: %q", heads[:3])
	}
	a, b := diffHeads(base), heads
	slices.Sort(a)
	slices.Sort(b)
	if !slices.Equal(a, b) {
		t.Error("a file went missing")
	}
	x, y := strings.Split(base, "\n"), strings.Split(got, "\n")
	slices.Sort(x)
	slices.Sort(y)
	if !slices.Equal(x, y) {
		t.Error("focus changed the content, not just the order")
	}
	if again := gitWith(t, fc, "/elsewhere/not/in/this/commit.go"); again != base {
		t.Error("a focus on a file outside the diff changed the view")
	}
}

func TestFocusLogKeepsCommitOrder(t *testing.T) {
	fc := loadCase(t, corpusCase{name: "git-log-p-3", local: true})
	base := gitWith(t, fc)
	got := gitWith(t, fc, "tree.go")
	order := []string{
		"1bd0ecf ", "a/context.go ", "a/context_test.go ",
		"\n3b08cd7 ", "a/tree.go ", "a/routes_test.go ",
		"\nd8f2d58 ", "a/gin.go ", "a/gin_test.go ",
	}
	last := -1
	for _, s := range order {
		i := strings.Index(got, s)
		if i <= last {
			t.Errorf("%q out of order", s)
		}
		last = i
	}
	if len(got) != len(base) {
		t.Errorf("focus changed the size of the view: %d → %d bytes", len(base), len(got))
	}
}

func bigDiff(files, lines int) string {
	var b strings.Builder
	for i := 1; i <= files; i++ {
		fmt.Fprintf(&b, "diff --git a/f%d.go b/f%d.go\nindex 1111111..2222222 100644\n--- a/f%d.go\n+++ b/f%d.go\n@@ -0,0 +1,%d @@\n", i, i, i, i, lines)
		for k := 1; k <= lines; k++ {
			fmt.Fprintf(&b, "+\tv%d := compute(%d, %d)\n", k, i, k)
		}
	}
	return b.String()
}

func TestFocusDiffKeptWhole(t *testing.T) {
	in := bigDiff(6, 320)
	base := applyDiff(t, in)
	if strings.Count(base, "compute(4,") == 320 {
		t.Fatal("the test needs a diff the budget cuts")
	}
	c := ctx(0, "git", "diff")
	c.Focus = &engine.Focus{Files: []string{"/repo/f4.go"}}
	got, ok := diffFilter{}.Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	if heads := diffHeads(got); heads[0] != "diff --git a/f4.go b/f4.go" {
		t.Errorf("focused file not first: %q", heads)
	}
	if n := strings.Count(got, "compute(4,"); n != 320 {
		t.Errorf("focused file cut to %d of 320 lines:\n%s", n, got)
	}
	for i := 1; i <= 6; i++ {
		if !strings.Contains(got, fmt.Sprintf("f%d.go", i)) {
			t.Errorf("f%d.go missing", i)
		}
	}
	if !fitsBudget(strings.Split(got, "\n"), diffBudget) {
		t.Error("over budget")
	}
}

func TestFocusDiffTooBigForBudget(t *testing.T) {
	in := bigDiff(4, 2000)
	c := ctx(0, "git", "diff")
	c.Focus = &engine.Focus{Files: []string{"f3.go"}}
	got, ok := diffFilter{}.Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	if heads := diffHeads(got); heads[0] != "diff --git a/f3.go b/f3.go" {
		t.Errorf("focused file not first: %q", heads)
	}
	for i := 1; i <= 4; i++ {
		if !strings.Contains(got, fmt.Sprintf("f%d.go", i)) {
			t.Errorf("f%d.go missing", i)
		}
	}
	if !fitsBudget(strings.Split(got, "\n"), diffBudget) {
		t.Error("over budget")
	}
}

func TestFocusGitKeepsEverything(t *testing.T) {
	for _, cc := range corpusCases {
		if cc.filter != "git-diff" && cc.filter != "git-show" && cc.filter != "git-log" || cc.bail {
			continue
		}
		t.Run(cc.name, func(t *testing.T) {
			fc := loadCase(t, cc)
			base := gitWith(t, fc)
			if got := gitWith(t, fc, "some/file/nowhere.go"); got != base {
				t.Error("an unmatched focus changed the view")
			}
			heads := diffHeads(base)
			var last string
			for _, h := range heads {
				if strings.HasPrefix(h, "diff --git ") {
					last = h[strings.LastIndex(h, " b/")+3:]
				}
			}
			if last == "" {
				return
			}
			got := gitWith(t, fc, "/repo/"+last)
			a, b := diffHeads(got), heads
			slices.Sort(a)
			slices.Sort(b)
			if !slices.Equal(a, b) {
				t.Errorf("files or commits went missing:\n%q\n%q", a, b)
			}
			if !strings.Contains(got, "b/"+last) {
				t.Errorf("focused file %s missing", last)
			}
			if !fitsBudget(strings.Split(got, "\n"), diffBudget) && fitsBudget(strings.Split(base, "\n"), diffBudget) {
				t.Error("focus pushed the view over budget")
			}
		})
	}
}

func TestFocusHugeIsFast(t *testing.T) {
	in := bigDiff(3000, 15)
	var files []string
	for i := range 20 {
		files = append(files, fmt.Sprintf("/repo/f%d.go", i*131))
	}
	c := ctx(0, "git", "diff")
	c.Focus = &engine.Focus{Files: files}
	start := time.Now()
	if _, ok := (diffFilter{}).Apply(c, in); !ok {
		t.Fatal("bailed")
	}
	if d := time.Since(start); d > testenv.Scale(3*time.Second) {
		t.Errorf("took %v", d)
	}
}
