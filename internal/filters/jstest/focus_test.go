package jstest

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

func viewWith(t *testing.T, fc fixture.Case, focus *engine.Focus) string {
	t.Helper()
	c := fc.Context()
	c.Focus = focus
	f := engine.Find(c)
	if f == nil {
		t.Fatal("no filter")
	}
	got, ok := f.Apply(c, fc.Clean())
	if !ok {
		t.Fatal("bailed")
	}
	return got
}

func linesWith(s string, keep func(string) bool) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		if keep(ln) {
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

func isJestFail(ln string) bool   { return strings.HasPrefix(ln, "FAIL ") }
func isJestBullet(ln string) bool { return strings.HasPrefix(ln, "  ● ") }
func isVitestFail(ln string) bool { return vFailRe.MatchString(ln) }
func isMochaFail(ln string) bool  { return mochaDetailRe.MatchString(ln) }

func TestFocusJestSuiteFirst(t *testing.T) {
	fc := fixture.Load(t, "node", "jest-fail")
	base := viewWith(t, fc, nil)
	if first := linesWith(base, isJestFail)[0]; first != "FAIL test/info/listers.test.js" {
		t.Fatalf("unexpected first suite without focus: %s", first)
	}

	got := viewWith(t, fc, &engine.Focus{Files: []string{"/home/user/src/luxon/test/datetime/tokenParse.test.js"}})
	if first := linesWith(got, isJestFail)[0]; first != "FAIL test/datetime/tokenParse.test.js" {
		t.Errorf("focused suite not first: %s\n%s", first, got)
	}
	sameLines(t, base, got)

	got = viewWith(t, fc, focusTerms("twoDigitCutoffYear"))
	suites, bullets := linesWith(got, isJestFail), linesWith(got, isJestBullet)
	if suites[0] != "FAIL test/datetime/tokenParse.test.js" || !strings.Contains(bullets[0], "twoDigitCutoffYear") {
		t.Errorf("focused suite and test not first:\n%s", got)
	}
	if len(bullets) != len(linesWith(base, isJestBullet)) {
		t.Error("a failure went missing")
	}
}

func TestFocusVitestFailureFirst(t *testing.T) {
	fc := fixture.Load(t, "node", "vitest-fail")
	base := viewWith(t, fc, nil)
	for _, tc := range []struct {
		focus *engine.Focus
		want  string
	}{
		{&engine.Focus{Files: []string{"test/query.test.ts"}}, " FAIL  test/query.test.ts > withQuery"},
		{focusTerms("encodeQueryValue"), " FAIL  test/encoding.test.ts > encodeQueryValue"},
	} {
		got := viewWith(t, fc, tc.focus)
		fails := linesWith(got, isVitestFail)
		if !strings.HasPrefix(fails[0], tc.want) {
			t.Errorf("%+v: first failure %q, want %q", *tc.focus, fails[0], tc.want)
		}
		if len(fails) != len(linesWith(base, isVitestFail)) {
			t.Errorf("%+v: a failure went missing", *tc.focus)
		}
		sameLines(t, base, got)
	}
}

func TestFocusVitestStackedFailuresStayTogether(t *testing.T) {
	in := strings.Join([]string{
		" RUN  v1.6.0 /home/user/src/app",
		"",
		" ❯ test/a.test.ts (3 tests | 3 failed) 5ms",
		"",
		"⎯⎯⎯⎯⎯⎯⎯ Failed Tests 3 ⎯⎯⎯⎯⎯⎯⎯",
		"",
		" FAIL  test/a.test.ts > one",
		"AssertionError: one broke",
		" ❯ test/a.test.ts:3:10",
		"",
		"⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯[1/2]⎯",
		"",
		" FAIL  test/a.test.ts > two",
		" FAIL  test/a.test.ts > three",
		"TypeError: shared cause",
		" ❯ test/a.test.ts:9:10",
		"",
		"⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯[2/2]⎯",
		"",
		" Test Files  1 failed (1)",
		"      Tests  3 failed (3)",
		"   Duration  1.2s",
	}, "\n")
	c := ctx(1, "vitest", "run")
	c.Focus = focusTerms("three")
	got, ok := vitestFilter{}.Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	two, three, shared, one := strings.Index(got, "> two"), strings.Index(got, "> three"), strings.Index(got, "shared cause"), strings.Index(got, "> one")
	if !(two >= 0 && two < three && three < shared && shared < one) {
		t.Errorf("stacked failures should move together, ahead of the rest:\n%s", got)
	}
}

func TestFocusMochaFailureFirst(t *testing.T) {
	fc, err := fixture.Read("testdata", "captures", "mocha-mixed")
	if err != nil {
		t.Fatal(err)
	}
	base := viewWith(t, fc, nil)
	for _, tc := range []struct {
		focus *engine.Focus
		want  string
	}{
		{focusTerms("hooks"), "  6) hooks"},
		{&engine.Focus{Files: []string{"/home/user/src/mochaproj/lib/store.js"}}, "  2) Store"},
		{focusTerms("sorted keys"), "  3) Store"},
	} {
		got := viewWith(t, fc, tc.focus)
		fails := linesWith(got, isMochaFail)
		if fails[0] != tc.want {
			t.Errorf("%+v: first failure %q, want %q\n%s", *tc.focus, fails[0], tc.want, got)
		}
		if len(fails) != len(linesWith(base, isMochaFail)) {
			t.Errorf("%+v: a failure went missing", *tc.focus)
		}
		sameLines(t, base, got)
	}
}

func TestFocusKeepsEverything(t *testing.T) {
	for _, cc := range corpus {
		t.Run(cc.name, func(t *testing.T) {
			fc := load(t, cc)
			clean := fc.Clean()
			base := viewWith(t, fc, nil)
			if got := viewWith(t, fc, focusTerms("zzzNothingLikeThis")); got != base {
				t.Error("an unmatched focus changed the view")
			}
			var titles []string
			switch cc.filter {
			case "jest", "npm-test", "vitest", "mocha":
				titles = linesWith(base, func(ln string) bool { return isJestBullet(ln) || isVitestFail(ln) || isMochaFail(ln) })
			}
			focus := focusTerms("nothing_like_this")
			if len(titles) > 0 {
				words := strings.Fields(titles[len(titles)-1])
				focus = focusTerms(words[len(words)-1])
			}
			c := fc.Context()
			c.Focus = focus
			f := engine.Find(c)
			r, ok := render(f, c, clean)
			if !ok {
				t.Fatal("bailed with a focus")
			}
			got := r.out
			if len(titles) == 0 && got != base {
				t.Error("an unmatched focus changed the view")
			}
			if !strings.Contains(base, "[lx: many failures") && !strings.Contains(base, "[lx: very many failures") {
				sameLines(t, base, got)
			}
			for _, ti := range titles {
				if !strings.Contains(got, ti) {
					t.Errorf("failure missing with a focus: %s", ti)
				}
			}
			benign := map[string]bool{}
			for _, b := range r.benignLines() {
				benign[b] = true
			}
			for _, m := range fixture.ErrorLinesMissing(clean, got) {
				if !benign[m] {
					t.Errorf("error line dropped: %q", m)
				}
			}
			if c.Exit != 0 {
				for _, loc := range fixture.LocationsMissing(clean, got) {
					if !libLocation(clean, loc) && !inLines(r.quiet, loc) {
						t.Errorf("location dropped: %s", loc)
					}
				}
			}
		})
	}
}

func withSecondError(in, after string) string {
	lines := strings.Split(in, "\n")
	out := make([]string, 0, len(lines)*2)
	n := 0
	for _, ln := range lines {
		out = append(out, ln)
		if strings.Contains(ln, after) {
			out = append(out, fmt.Sprintf("%sError: secondary failure %d", ln[:len(ln)-len(strings.TrimLeft(ln, " "))], n))
			n++
		}
	}
	return strings.Join(out, "\n")
}

func TestFocusManyFailuresKeepErrorLines(t *testing.T) {
	for _, n := range []int{100, 300} {
		for _, tc := range []struct {
			f    engine.Filter
			c    *engine.Context
			in   string
			term string
		}{
			{jestFilter{}, ctx(1, "jest"), withSecondError(synthJest(n), "expect(received)"), fmt.Sprintf("computes value %d", n-5)},
			{vitestFilter{}, ctx(1, "vitest", "run"), withSecondError(synthVitest(n), "AssertionError: expected"), fmt.Sprintf("case %d", n-5)},
			{mochaFilter{}, ctx(n, "mocha"), withSecondError(synthMocha(n), "AssertionError [ERR_ASSERTION]"), fmt.Sprintf("case %d", n-5)},
		} {
			base, ok := tc.f.Apply(tc.c, tc.in)
			if !ok {
				t.Fatalf("%s: bailed", tc.f.Name())
			}
			c := *tc.c
			c.Focus = focusTerms(tc.term)
			got, _ := tc.f.Apply(&c, tc.in)
			have := map[string]bool{}
			for _, ln := range strings.Split(got, "\n") {
				have[ln] = true
			}
			for _, ln := range strings.Split(base, "\n") {
				if engine.IsError(ln) && !have[ln] {
					t.Errorf("%s, %d failures: focus on %q dropped %q", tc.f.Name(), n, tc.term, ln)
					break
				}
			}
		}
	}
}

func heavyFocus() *engine.Focus {
	f := &engine.Focus{}
	for i := range 30 {
		f.Terms = append(f.Terms, engine.FocusTerm{Text: fmt.Sprintf("computes value %d", i*37), Weight: 1})
	}
	for i := range 20 {
		f.Files = append(f.Files, fmt.Sprintf("/home/user/src/proj/test/suite%d.test.js", i*11))
	}
	return f
}

func TestFocusHugeIsFast(t *testing.T) {
	for name, tc := range map[string]struct {
		f  engine.Filter
		in string
	}{
		"jest":   {jestFilter{}, synthJest(3000)},
		"vitest": {vitestFilter{}, synthVitest(3000)},
		"mocha":  {mochaFilter{}, synthMocha(3000)},
	} {
		t.Run(name, func(t *testing.T) {
			c := ctx(1, name)
			c.Focus = heavyFocus()
			start := time.Now()
			if _, ok := tc.f.Apply(c, tc.in); !ok {
				t.Fatal("bailed")
			}
			if d := time.Since(start); d > testenv.Scale(3*time.Second) {
				t.Errorf("took %v", d)
			}
		})
	}
}
