package golang

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

func applyFocus(t *testing.T, fc fixture.Case, focus *engine.Focus) string {
	t.Helper()
	c := fc.Context()
	c.Focus = focus
	f := engine.Find(c)
	if f == nil {
		t.Fatal("no filter")
	}
	got, ok := f.Apply(c, fc.Clean())
	if !ok {
		t.Fatal("filter bailed")
	}
	return got
}

func failLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(ln, "--- FAIL: ") {
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

func TestFocusFailingTestFirst(t *testing.T) {
	for _, name := range []string{"go-test-fail", "go-test-fail-json"} {
		fc := fixture.Load(t, "go", name)
		base := applyFocus(t, fc, nil)
		if first := failLines(base)[0]; !strings.Contains(first, "TestMinimumNArgs_WithLessArgs ") {
			t.Fatalf("%s: unexpected first failure without focus: %s", name, first)
		}
		for _, focus := range []*engine.Focus{
			focusTerms("TestFind"),
			focusTerms("Find"),
			{Files: []string{"/home/user/src/cobra/command_test.go"}},
		} {
			got := applyFocus(t, fc, focus)
			fails := failLines(got)
			if len(fails) < 2 || fails[0] != "--- FAIL: TestFind (0.00s)" {
				t.Errorf("%s %+v: focused test not first:\n%s", name, *focus, got)
			}
			sameLines(t, base, got)
		}
		if got := applyFocus(t, fc, focusTerms("nothing_here", "TestMinimumNArgs_WithLessArgs")); got != base {
			t.Errorf("%s: a focus that matches the first failure must not change the view", name)
		}
	}
}

func TestFocusFailingPackageFirst(t *testing.T) {
	in := strings.Join([]string{
		"--- FAIL: TestAlpha (0.00s)",
		"    alpha_test.go:10: bad alpha",
		"FAIL",
		"FAIL\texample.com/app/alpha\t0.01s",
		"ok  \texample.com/app/mid\t0.02s",
		"--- FAIL: TestBeta (0.00s)",
		"    beta_test.go:20: bad beta",
		"FAIL",
		"FAIL\texample.com/app/beta\t0.01s",
		"FAIL",
	}, "\n")
	c := ctx(1, "go", "test", "./...")
	base, ok := testText{}.Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	c.Focus = &engine.Focus{Files: []string{"/home/user/src/app/beta/beta_test.go"}}
	got, _ := testText{}.Apply(c, in)
	want := strings.Join([]string{
		"--- FAIL: TestBeta (0.00s)",
		"    beta_test.go:20: bad beta",
		"FAIL",
		"FAIL\texample.com/app/beta\t0.01s",
		"ok  \texample.com/app/mid\t0.02s",
		"--- FAIL: TestAlpha (0.00s)",
		"    alpha_test.go:10: bad alpha",
		"FAIL",
		"FAIL\texample.com/app/alpha\t0.01s",
		"FAIL",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	sameLines(t, base, got)
}

func TestFocusSubtestsStayWithParent(t *testing.T) {
	in := strings.Join([]string{
		"=== RUN   TestOne",
		"    one_test.go:5: one failed",
		"--- FAIL: TestOne (0.00s)",
		"=== RUN   TestTwo",
		"=== RUN   TestTwo/empty",
		"    two_test.go:9: empty failed",
		"=== RUN   TestTwo/full",
		"--- FAIL: TestTwo (0.00s)",
		"    --- FAIL: TestTwo/empty (0.00s)",
		"    --- PASS: TestTwo/full (0.00s)",
		"FAIL",
		"FAIL\texample.com/app\t0.01s",
		"FAIL",
	}, "\n")
	c := ctx(1, "go", "test", "-v", ".")
	c.Focus = focusTerms("empty")
	got, ok := testText{}.Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	i2, i2e, i1 := strings.Index(got, "--- FAIL: TestTwo "), strings.Index(got, "--- FAIL: TestTwo/empty"), strings.Index(got, "--- FAIL: TestOne")
	if i2 < 0 || i1 < 0 || !(i2 < i2e && i2e < i1) {
		t.Errorf("TestTwo and its subtest should come first, together:\n%s", got)
	}
}

func TestFocusStrayBetweenSubtests(t *testing.T) {
	in := strings.Join([]string{
		"=== RUN   TestA",
		"=== RUN   TestA/x",
		"    a_test.go:5: bad x",
		"=== RUN   TestA/y",
		"    a_test.go:6: bad y",
		"=== RUN   TestB",
		"    b_test.go:9: bad b",
		"--- FAIL: TestA (0.00s)",
		"    --- FAIL: TestA/x (0.00s)",
		"leaked by a goroutine",
		"    --- FAIL: TestA/y (0.00s)",
		"--- FAIL: TestB (0.00s)",
		"FAIL",
		"FAIL\texample.com/app\t0.01s",
		"FAIL",
	}, "\n")
	c := ctx(1, "go", "test", "-v", ".")
	base, ok := testText{}.Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	c.Focus = focusTerms("TestB")
	if got, _ := (testText{}).Apply(c, in); got != base {
		t.Errorf("TestA/y would end up under TestB:\n%s", got)
	}
}

func TestFocusKeepsEverything(t *testing.T) {
	all := append(append([]corpusCase(nil), corpus...), captures...)
	for i, cc := range all {
		if !strings.HasPrefix(cc.filter, "go-test") {
			continue
		}
		t.Run(cc.name, func(t *testing.T) {
			var fc fixture.Case
			if i < len(corpus) {
				fc = fixture.Load(t, cc.cat, cc.name)
			} else {
				fc = loadCapture(t, cc.cat, cc.name)
			}
			ref := fc.Clean()
			if cc.filter == "go-test-json" {
				ref = decodeEvents(ref)
			}
			var last string
			for _, ln := range strings.Split(ref, "\n") {
				if m := resultRe.FindStringSubmatch(ln); m != nil && m[2] == "FAIL" && m[1] == "" {
					last, _, _ = strings.Cut(m[3], "/")
				}
			}
			base := applyFocus(t, fc, nil)
			if got := applyFocus(t, fc, focusTerms("TestNothingLikeThis")); got != base {
				t.Error("an unmatched focus changed the view")
			}
			if last == "" {
				return
			}
			got := applyFocus(t, fc, focusTerms(last))
			sameLines(t, base, got)
			checkFidelity(t, cc, fc, ref, got)
			if fails := failLines(got); len(fails) == 0 || !strings.HasPrefix(fails[0], "--- FAIL: "+last+" (") {
				t.Errorf("focused test %s not first", last)
			}
		})
	}
}

func heavyFocus() *engine.Focus {
	f := &engine.Focus{}
	for i := range 30 {
		f.Terms = append(f.Terms, engine.FocusTerm{Text: fmt.Sprintf("TestCase%d", i*37), Weight: 1})
	}
	f.Terms = append(f.Terms, engine.FocusTerm{Text: "connection refused", Weight: 1})
	for i := range 20 {
		f.Files = append(f.Files, fmt.Sprintf("/home/user/src/app/pkg%d/file%d_test.go", i, i))
	}
	return f
}

func TestFocusHugeIsFast(t *testing.T) {
	var b strings.Builder
	for i := range 20000 {
		fmt.Fprintf(&b, "--- FAIL: TestCase%d (0.00s)\n    file%d_test.go:%d: got %d, want %d: connection refused\n", i, i%50, i, i, i+1)
	}
	b.WriteString("FAIL\nFAIL\tex.com/big\t1.0s\nFAIL")
	for name, in := range map[string]string{"failures": b.String(), "errors": distinctErrors(50000), "nesting": deepNesting(20000)} {
		t.Run(name, func(t *testing.T) {
			c := ctx(1, "go", "test")
			c.Focus = heavyFocus()
			start := time.Now()
			if _, ok := (testText{}).Apply(c, in); !ok {
				t.Fatal("bailed")
			}
			if d := time.Since(start); d > testenv.Scale(3*time.Second) {
				t.Errorf("took %v", d)
			}
		})
	}
}

func TestFocusThroughProcess(t *testing.T) {
	fc := fixture.Load(t, "go", "go-test-fail")
	res := engine.Process(fc.Context(), fc.Raw, engine.Options{Focus: focusTerms("TestFind")})
	if res.Filter != "go-test" {
		t.Fatalf("filter %s", res.Filter)
	}
	if fails := failLines(res.Output); len(fails) == 0 || fails[0] != "--- FAIL: TestFind (0.00s)" {
		t.Errorf("focused test not first:\n%s", res.Output)
	}
	if res.GuardAdded != 0 {
		t.Errorf("guard re-added %d lines", res.GuardAdded)
	}
	plain := engine.Process(fc.Context(), fc.Raw, engine.Options{})
	if len(failLines(plain.Output)) != len(failLines(res.Output)) {
		t.Error("a failure went missing")
	}
}
