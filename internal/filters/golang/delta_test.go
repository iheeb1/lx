package golang

import (
	"fmt"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

func rerun(t *testing.T, c *engine.Context, prev, cur string) (delta, view string, ok bool) {
	t.Helper()
	res := engine.Process(c, cur, engine.Options{})
	delta, ok = engine.Rerun(c, engine.Prev{Ref: "lx show 12", TurnsAgo: 2, Exit: c.Exit, Raw: prev}, cur, res.Output)
	if ok && float64(tokens.Count(delta)) > 0.7*float64(tokens.Count(res.Output)) {
		t.Errorf("delta is not 30%% smaller than the view:\n%s", delta)
	}
	return delta, res.Output, ok
}

func mustContain(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

func mustNotContain(t *testing.T, got string, bad ...string) {
	t.Helper()
	for _, b := range bad {
		if strings.Contains(got, b) {
			t.Errorf("unexpected %q in:\n%s", b, got)
		}
	}
}

const (
	cobraFixed = "--- FAIL: TestMinimumNArgs_WithLessArgs (0.00s)\n    args_test.go:73: Expected \"requires at least 2 arg(s), only received 1\", got \"requires at least 2 arg(s), received 1\"\n"
	cobraNew   = "--- FAIL: TestFindAlias (0.01s)\n    command_test.go:2910: alias \"ls\" resolved to <nil>, want list\n"
)

func cobraRerun(prev string) string {
	cur := strings.Replace(prev, cobraFixed, "", 1)
	cur = strings.Replace(cur, "--- FAIL: TestFind (0.00s)\n", cobraNew+"--- FAIL: TestFind (0.00s)\n", 1)
	return strings.Replace(cur, "\t0.655s", "\t0.702s", 1)
}

func TestDeltaGoTestFixLoop(t *testing.T) {
	fc := fixture.Load(t, "go", "go-test-fail")
	delta, view, ok := rerun(t, fc.Context(), fc.Raw, cobraRerun(fc.Raw))
	if !ok {
		t.Fatalf("no delta for a fix-loop rerun; view:\n%s", view)
	}
	mustContain(t, delta,
		"[lx: same command as lx show 12 (2 turns ago, still in your context) — only what changed:]\n",
		"new:\n"+strings.TrimRight(cobraNew, "\n"),
		"fixed: github.com/spf13/cobra TestMinimumNArgs_WithLessArgs\n",
		"still failing:\n  github.com/spf13/cobra TestMinimumNArgs_WithLessArgs_WithValid (args_test.go:73)\n",
		"  github.com/spf13/cobra TestFind/[child_-f_child] (command_test.go:2876)\n",
		"FAIL\tgithub.com/spf13/cobra\t0.702s",
		"ok  \tgithub.com/spf13/cobra/doc\t(cached)")
	mustNotContain(t, delta, "Wrong args", "Usage:", "--- FAIL: TestMinimumNArgs_WithLessArgs_WithValid (")
}

func TestDeltaGoTestSameFailures(t *testing.T) {
	fc := fixture.Load(t, "go", "go-test-fail")
	c := fc.Context()

	delta, _, ok := rerun(t, c, fc.Raw, fc.Raw)
	if !ok || !strings.HasPrefix(delta, "[lx: output identical to lx show 12 (2 turns ago, still in your context)]\n") {
		t.Fatalf("identical output: ok=%v\n%s", ok, delta)
	}
	mustContain(t, delta, "FAIL\tgithub.com/spf13/cobra\t0.655s")

	delta, _, ok = rerun(t, c, fc.Raw, strings.Replace(fc.Raw, "\t0.655s", "\t0.71s", 1))
	if !ok || !strings.HasPrefix(delta, "[lx: output identical to lx show 12 apart from timings (2 turns ago, still in your context)]\n") {
		t.Fatalf("timing-only change: ok=%v\n%s", ok, delta)
	}
	mustContain(t, delta, "FAIL\tgithub.com/spf13/cobra\t0.71s")

	moved := strings.Replace(fc.Raw, "command_test.go:2876: Wrong args", "command_test.go:2881: Wrong args", 1)
	delta, _, ok = rerun(t, c, fc.Raw, moved)
	if !ok {
		t.Fatal("a failure that only moved lines is not new")
	}
	mustContain(t, delta, "TestFind/[child_-f_child] (command_test.go:2881)")
	mustNotContain(t, delta, "\nnew:\n", "\nchanged:\n", "\nfixed:")
}

func TestDeltaGoTestChangedMessage(t *testing.T) {
	fc := fixture.Load(t, "go", "go-test-fail")
	cur := strings.Replace(fc.Raw, "Got: [-f child]", "Got: [child]", 1)
	delta, _, ok := rerun(t, fc.Context(), fc.Raw, cur)
	if !ok {
		t.Fatal("no delta")
	}
	mustContain(t, delta, "changed:\n--- FAIL: TestFind/[child_-f_child] (0.00s)\n    command_test.go:2876: Wrong args\n        Expected: [child -f child]\n        Got: [child]\n")
}

func TestDeltaGoTestNewLooseError(t *testing.T) {
	fc := fixture.Load(t, "go", "go-test-fail")
	for _, extra := range []string{"panic: close of closed channel in cleanup hook", "cleanup: could not remove /tmp/x: permission denied"} {
		cur := strings.Replace(cobraRerun(fc.Raw), "FAIL\nFAIL\tgithub.com/spf13/cobra", extra+"\nFAIL\nFAIL\tgithub.com/spf13/cobra", 1)
		if delta, _, ok := rerun(t, fc.Context(), fc.Raw, cur); ok && !strings.Contains(delta, extra) {
			t.Fatalf("a new error line was hidden:\n%s", delta)
		}
	}
}

func TestDeltaGoTestBuildErrors(t *testing.T) {
	fc := fixture.Load(t, "go", "go-test-build-failed")
	c := fc.Context()
	items, ok := engine.ItemsOf(c, fc.Clean())
	if !ok || len(items) != 7 {
		t.Fatalf("got %d items (ok=%v), want the 7 compiler errors", len(items), ok)
	}
	if items[0].Key != "./auth.go: c.AbortWithStatus undefined (type *Context has no field or method AbortWithStatus)" || items[0].Loc != "./auth.go:60" {
		t.Fatalf("first item %+v", items[0])
	}
	cur := strings.Replace(fc.Raw, "./gin.go:692:2: declared and not used: requestStart\n", "", 1)
	cur = strings.Replace(cur, "./utils.go:15:2:", "./utils.go:16:2:", 1)
	if _, _, ok := rerun(t, c, fc.Raw, cur); ok {
		t.Fatal("the build-failed view is already short: a delta must not replace it unless it is 30% smaller")
	}
	d, ok := engine.Delta(engine.Prev{Ref: "lx show 3", TurnsAgo: 1, Raw: fc.Clean(), Items: items}, mustItems(t, c, cur), strings.Repeat(fc.Clean()+"\n", 3))
	if !ok {
		t.Fatal("no delta")
	}
	mustContain(t, d, "fixed: ./gin.go: declared and not used: requestStart\n",
		"  ./auth.go: c.AbortWithStatus undefined (type *Context has no field or method AbortWithStatus) ×2 (lines 60, 109)\n",
		"  ./utils.go: \"strconv\" imported and not used (line 16)\n")
}

func mustItems(t *testing.T, c *engine.Context, raw string) []engine.Item {
	t.Helper()
	items, ok := engine.ItemsOf(c, raw)
	if !ok {
		t.Fatal("no items")
	}
	return items
}

func TestDeltaGoTestPanic(t *testing.T) {
	fc := fixture.Load(t, "go", "go-test-panic")
	items := mustItems(t, fc.Context(), fc.Clean())
	var keys []string
	for _, it := range items {
		keys = append(keys, it.Key)
	}
	want := "github.com/spf13/cobra panic: runtime error: index out of range [0] with length 0 [recovered, repanicked]"
	if !strings.Contains(strings.Join(keys, "\n"), want) {
		t.Fatalf("keys %q, want the panic %q", keys, want)
	}
}

func TestDeltaGoTestJSON(t *testing.T) {
	fc := fixture.Load(t, "go", "go-test-fail-json")
	c := fc.Context()
	var kept []string
	for _, ln := range strings.Split(fc.Raw, "\n") {
		if !strings.Contains(ln, `"Test":"TestMinimumNArgs_WithLessArgs"`) {
			kept = append(kept, ln)
		}
	}
	cur := strings.Join(kept, "\n")
	delta, view, ok := rerun(t, c, fc.Raw, cur)
	if !ok {
		t.Fatalf("no delta; view:\n%s", view)
	}
	mustContain(t, delta, "fixed: github.com/spf13/cobra TestMinimumNArgs_WithLessArgs\n",
		"  github.com/spf13/cobra TestFind/[child_-f_child] (command_test.go:2876)\n")
	mustNotContain(t, delta, "\nnew:\n", "\nchanged:\n")
}

func manyFailures(n, skip, add int, word string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		if i == skip {
			continue
		}
		if i == add {
			fmt.Fprintf(&b, "--- FAIL: TestAdded (0.00s)\n    added_test.go:9: %s nil map in the new case\n", word)
		}
		fmt.Fprintf(&b, "--- FAIL: TestCase%03d (0.00s)\n", i)
		for k := 0; k < 6; k++ {
			fmt.Fprintf(&b, "    case_test.go:%d: %s field %d of record %d: got %q, want %q\n", 100+i, word, k, i, "alpha-beta-gamma", "alpha-beta-delta")
		}
	}
	return b.String() + "FAIL\nFAIL\texample.com/big\t0.512s\nFAIL\n"
}

func TestDeltaGoTestCutEarlierView(t *testing.T) {
	argv := []string{"go", "test", "./..."}
	modes := []engine.Mode{engine.ModeAuto, engine.ModeError, engine.ModeDebug}
	used := 0
	for _, tc := range []struct {
		word string
		n    int
	}{{"mismatch in", 40}, {"mismatch in", 160}, {"error: mismatch in", 90}} {
		prev, cur := manyFailures(tc.n, -1, -1, tc.word), manyFailures(tc.n, 3, tc.n/2, tc.word)
		for _, pm := range modes {
			shown := engine.Process(&engine.Context{Argv: argv, Exit: 1}, prev, engine.Options{Mode: pm}).Output
			for _, cm := range modes {
				c := &engine.Context{Argv: argv, Exit: 1}
				view := engine.Process(c, cur, engine.Options{Mode: cm}).Output
				delta, ok := engine.Rerun(c, engine.Prev{Ref: "lx show 1", TurnsAgo: 1, Exit: 1, Raw: prev}, cur, view)
				if !ok {
					continue
				}
				used++
				for _, ln := range strings.Split(view, "\n") {
					ln = strings.TrimSpace(ln)
					if engine.Classify(ln) != engine.Normal && !strings.Contains(delta, ln) && !strings.Contains(shown, ln) {
						t.Errorf("%q n=%d %v→%v: %q is in the usual view, not in the delta, and was never shown:\n%s", tc.word, tc.n, pm, cm, ln, delta)
					}
				}
			}
		}
	}
	if used == 0 {
		t.Error("no delta at all")
	}
}
