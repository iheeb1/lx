package python

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

func focusTerms(ts ...string) *engine.Focus {
	f := &engine.Focus{}
	for _, t := range ts {
		f.Terms = append(f.Terms, engine.FocusTerm{Text: t, Weight: 1})
	}
	return f
}

func pytestWith(t *testing.T, fc fixture.Case, focus *engine.Focus) string {
	t.Helper()
	c := fc.Context()
	c.Focus = focus
	got, _, ok := reducePytest(c, fc.Clean())
	if !ok {
		t.Fatal("bailed")
	}
	return got
}

func blockNames(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if m := ptBlockRe.FindStringSubmatch(ln); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

func summaryLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(ln, "FAILED ") || strings.HasPrefix(ln, "ERROR ") {
			out = append(out, ln)
		}
	}
	return out
}

func sameLines(t *testing.T, want, got string) {
	t.Helper()
	a, b := strings.Split(want, "\n"), strings.Split(got, "\n")
	slices.Sort(a)
	slices.Sort(b)
	if !slices.Equal(a, b) {
		t.Errorf("focus changed what is shown, not just the order:\n--- without focus:\n%s\n--- with focus:\n%s", want, got)
	}
}

func TestFocusPytestFailureFirst(t *testing.T) {
	fc := fixture.Load(t, "python", "pytest-fail")
	base := pytestWith(t, fc, nil)
	if names := blockNames(base); names[0] != "test_option_normalization" {
		t.Fatalf("unexpected first block: %v", names)
	}
	cases := []struct {
		focus *engine.Focus
		first string
		sum   string
	}{
		{focusTerms("test_counting"), "test_counting", "FAILED tests/test_options.py::test_counting "},
		{focusTerms("counting"), "test_counting", "FAILED tests/test_options.py::test_counting "},
		{focusTerms("range_fail"), "test_range_fail[type0-6-6 is not in the range 0<=x<=5.]", "FAILED tests/test_types.py::test_range_fail[type0-"},
		{&engine.Focus{Files: []string{"/home/user/src/click/tests/test_types.py"}}, "test_range[type8-5-4]", "FAILED tests/test_types.py::test_range[type8-5-4]"},
	}
	for _, tc := range cases {
		got := pytestWith(t, fc, tc.focus)
		if names := blockNames(got); names[0] != tc.first {
			t.Errorf("%+v: first block %q, want %q", *tc.focus, names[0], tc.first)
		}
		if s := summaryLines(got); !strings.HasPrefix(s[0], tc.sum) {
			t.Errorf("%+v: first summary line %q, want prefix %q", *tc.focus, s[0], tc.sum)
		}
		if len(blockNames(got)) != len(blockNames(base)) || len(summaryLines(got)) != len(summaryLines(base)) {
			t.Errorf("%+v: a failure went missing", *tc.focus)
		}
		sameLines(t, base, got)
	}
	if got := pytestWith(t, fc, focusTerms("test_option_normalization")); got != base {
		t.Error("focusing the first failure must not change the view")
	}
}

func TestFocusPytestKeepsEverything(t *testing.T) {
	for _, f := range loadFixtures(t) {
		if expectFind(f.Name) != "pytest" {
			continue
		}
		t.Run(f.source+"/"+f.Name, func(t *testing.T) {
			c := f.Context()
			flt := engine.Find(c)
			base, ok := flt.Apply(c, f.Clean())
			if !ok {
				return
			}
			c.Focus = focusTerms("zzzNothingLikeThis")
			if got, _ := flt.Apply(c, f.Clean()); got != base {
				t.Error("an unmatched focus changed the view")
			}
			names := blockNames(base)
			focus := focusTerms("nothing_like_this")
			if len(names) > 0 {
				focus = focusTerms(strings.Fields(names[len(names)-1])[0])
			}
			c.Focus = focus
			got, ok := flt.Apply(c, f.Clean())
			if !ok {
				t.Fatal("bailed with a focus")
			}
			sameLines(t, base, got)
			checkFidelity(t, f, flt, f.Clean(), got)
			if len(names) == 0 && got != base {
				t.Error("an unmatched focus changed the view")
			}
		})
	}
}

func TestFocusHugeIsFast(t *testing.T) {
	var b strings.Builder
	b.WriteString("============================= test session starts ==============================\ncollected 5000 items\n\n")
	b.WriteString("tests/test_big.py " + strings.Repeat("F", 5000) + " [100%]\n\n")
	b.WriteString("=================================== FAILURES ===================================\n")
	for i := range 5000 {
		fmt.Fprintf(&b, "________________________________ test_case_%d ________________________________\n\n    def test_case_%d():\n>       assert compute(%d) == %d\nE       assert %d == %d\n\ntests/test_big.py:%d: AssertionError\n", i, i, i, i+1, i, i+1, i*3)
	}
	b.WriteString("=========================== short test summary info ============================\n")
	for i := range 5000 {
		fmt.Fprintf(&b, "FAILED tests/test_big.py::test_case_%d - assert %d == %d\n", i, i, i+1)
	}
	b.WriteString("============================== 5000 failed in 9.99s ==============================\n")
	f := &engine.Focus{}
	for i := range 30 {
		f.Terms = append(f.Terms, engine.FocusTerm{Text: fmt.Sprintf("case_%d", i*97), Weight: 1})
	}
	for i := range 20 {
		f.Files = append(f.Files, fmt.Sprintf("/home/user/src/app/tests/test_other%d.py", i))
	}
	c := ctx(1, "pytest")
	c.Focus = f
	start := time.Now()
	if _, _, ok := reducePytest(c, b.String()); !ok {
		t.Fatal("bailed")
	}
	if d := time.Since(start); d > testenv.Scale(3*time.Second) {
		t.Errorf("took %v", d)
	}
}
