package search

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/filters/fs"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/testenv"
)

func searchWith(t *testing.T, c *engine.Context, in string, focus *engine.Focus) string {
	t.Helper()
	c.Focus = focus
	got, ok := matches{}.Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	checkMatches(t, fs.Effective(c), in, got)
	return got
}

func firstGroup(got string) string {
	for _, ln := range strings.Split(got, "\n") {
		if ln != "" && !strings.HasPrefix(ln, "[") {
			return ln
		}
	}
	return ""
}

func TestFocusSearchFileFirst(t *testing.T) {
	fc := fixture.Load(t, "search", "grep-rn-luxon-duration")
	base := searchWith(t, fc.Context(), fc.Clean(), nil)
	if firstGroup(base) != "src/duration.js" {
		t.Fatalf("unexpected first file without focus: %s", firstGroup(base))
	}
	got := searchWith(t, fc.Context(), fc.Clean(), &engine.Focus{Files: []string{"/home/user/src/luxon/test/duration/math.test.js"}})
	if firstGroup(got) != "test/duration/math.test.js" {
		t.Errorf("focused file not first:\n%s", got)
	}
	if !strings.Contains(got, ", focused first") {
		t.Errorf("header should say the files are not in grep's order:\n%s", got[:200])
	}
}

func TestFocusSearchPicksFocusedMatches(t *testing.T) {
	fc := fixture.Load(t, "search", "grep-rn-luxon-duration")
	base := searchWith(t, fc.Context(), fc.Clean(), nil)
	if strings.Contains(base, "659:") {
		t.Fatal("the mapUnits lines are not expected in the capped view without focus")
	}
	got := searchWith(t, fc.Context(), fc.Clean(), focusTerms("mapUnits"))
	if firstGroup(got) != "src/duration.js" {
		t.Errorf("focused file not first:\n%s", got)
	}
	block := got[:strings.Index(got, "… +")]
	for _, want := range []string{"659:", "660:", "1:", "217:"} {
		if !strings.Contains(block, "\n  "+want) && !strings.Contains(block, "\n"+want) {
			t.Errorf("line %s missing from the focused file's block:\n%s", want, block)
		}
	}
}

func TestFocusSearchContextRuns(t *testing.T) {
	var b strings.Builder
	for _, f := range []string{"a.go", "b.go", "c.go", "d.go"} {
		for i := 1; i <= 40; i++ {
			n := i * 10
			word := "match"
			if f == "c.go" && i == 30 {
				word = "needle"
			}
			fmt.Fprintf(&b, "%s-%d-before %d\n%s:%d:x := pattern(%s)\n%s-%d-after %d\n--\n", f, n-1, i, f, n, word, f, n+1, i)
		}
	}
	in := strings.TrimSuffix(b.String(), "--\n")
	c := ctx("rg", "-n", "-C1", "pattern")
	base := searchWith(t, ctx("rg", "-n", "-C1", "pattern"), in, nil)
	got := searchWith(t, c, in, focusTerms("needle"))
	if firstGroup(got) != "c.go" {
		t.Fatalf("focused file not first:\n%s", got)
	}
	block := got[strings.Index(got, "\nc.go\n"):]
	block = block[:strings.Index(block, "more in this file")]
	for _, want := range []string{"299-before 30", "300:x := pattern(needle)", "301-after 30", "10:x := pattern(match)"} {
		if !strings.Contains(block, want) {
			t.Errorf("%q missing:\n%s", want, block)
		}
	}
	if !strings.Contains(block, "\n--\n299-before 30") {
		t.Errorf("the focused run should be set apart:\n%s", block)
	}
	if strings.Count(got, "pattern(") != strings.Count(base, "pattern(") {
		t.Errorf("focus changed how many matches are shown")
	}
}

func TestFocusSearchUnmatched(t *testing.T) {
	fc := fixture.Load(t, "search", "grep-rn-luxon-duration")
	base := searchWith(t, fc.Context(), fc.Clean(), nil)
	for _, focus := range []*engine.Focus{
		focusTerms("zzzNothing"),
		{Files: []string{"/home/user/src/luxon/src/missing.js"}},
	} {
		if got := searchWith(t, fc.Context(), fc.Clean(), focus); got != base {
			t.Errorf("%+v: a focus matching nothing new must not change the view", *focus)
		}
	}
}

func TestFocusSearchKeepsCounts(t *testing.T) {
	var cases []fixture.Case
	for _, tc := range corpusCases {
		if tc.filter == "search" {
			cases = append(cases, fixture.Load(t, "search", tc.name))
		}
	}
	for _, tc := range extraCases {
		if tc.filter == "search" {
			fc, err := fixture.Read("testdata/corpus", "search", tc.name)
			if err != nil {
				t.Fatal(err)
			}
			cases = append(cases, fc)
		}
	}
	for _, fc := range cases {
		t.Run(fc.Name, func(t *testing.T) {
			clean := fc.Clean()
			base, ok := (matches{}).Apply(fc.Context(), clean)
			if !ok {
				return
			}
			c := fc.Context()
			c.Focus = focusTerms("zzzNothingLikeThis")
			if got, _ := (matches{}).Apply(c, clean); got != base {
				t.Error("an unmatched focus changed the view")
			}
			lines := strings.Split(strings.TrimSpace(clean), "\n")
			path, _, _ := strings.Cut(lines[len(lines)-1], ":")
			focus := &engine.Focus{Files: []string{"/somewhere/" + path}}
			if mid := strings.Fields(lines[len(lines)/2]); len(mid) > 1 {
				focus.Terms = []engine.FocusTerm{{Text: mid[len(mid)-1], Weight: 1}}
			}
			searchWith(t, fc.Context(), clean, focus)
		})
	}
}

func focusTerms(ts ...string) *engine.Focus {
	f := &engine.Focus{}
	for _, t := range ts {
		f.Terms = append(f.Terms, engine.FocusTerm{Text: t, Weight: 1})
	}
	return f
}

func TestFocusHugeIsFast(t *testing.T) {
	var b strings.Builder
	for i := range 50000 {
		fmt.Fprintf(&b, "./pkg%d/file%d.go:%d:\tvalue := compute(%d) // Context parseThing%d\n", i%100, i%5000, i, i, i%40)
	}
	f := &engine.Focus{}
	for i := range 30 {
		f.Terms = append(f.Terms, engine.FocusTerm{Text: fmt.Sprintf("parseThing%d", i), Weight: 1})
	}
	for i := range 20 {
		f.Files = append(f.Files, fmt.Sprintf("/home/user/src/app/pkg%d/file%d.go", i, i*7))
	}
	c := ctx("grep", "-rn", "Context", ".")
	c.Focus = f
	start := time.Now()
	if _, ok := (matches{}).Apply(c, strings.TrimSpace(b.String())); !ok {
		t.Fatal("bailed")
	}
	if d := time.Since(start); d > testenv.Scale(3*time.Second) {
		t.Errorf("took %v", d)
	}
}
