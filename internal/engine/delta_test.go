package engine

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/tokens"
)

func failure(name string, line int, msg string) Item {
	return Item{
		Key:   "pkg " + name,
		Block: fmt.Sprintf("--- FAIL: %s (0.01s)\n    thing_test.go:%d: %s", name, line, msg),
	}
}

func viewOf(items []Item, pad int, summary string) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.Block + "\n")
	}
	for i := 0; i < pad; i++ {
		fmt.Fprintf(&b, "    helper output line %d for context\n", i)
	}
	b.WriteString(summary)
	return b.String()
}

func rawOf(items []Item) string {
	return viewOf(items, 1, "FAIL\tpkg\t0.2s")
}

func TestDeltaSections(t *testing.T) {
	a := failure("TestA", 10, "got 1, want 2")
	b := failure("TestB", 20, "nil map")
	c := failure("TestC", 30, "timeout waiting for lock")
	d := failure("TestD", 40, "boom")
	prev := Prev{Ref: "lx show 7", TurnsAgo: 3, Raw: rawOf([]Item{a, b, c}), Items: []Item{a, b, c}}
	cur := []Item{b, c, d}
	view := viewOf(cur, 40, "FAIL\tpkg\t0.3s")
	out, ok := Delta(prev, cur, view)
	if !ok {
		t.Fatal("no delta")
	}
	want := "[lx: same command as lx show 7 (3 turns ago, still in your context) — only what changed:]\n" +
		"new:\n" + d.Block + "\n" +
		"fixed: pkg TestA\n" +
		"still failing:\n  pkg TestB (thing_test.go:20)\n  pkg TestC (thing_test.go:30)\n" +
		"FAIL\tpkg\t0.3s"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestDeltaChangedAndMoved(t *testing.T) {
	a := failure("TestA", 10, "got 1, want 2")
	b := failure("TestB", 20, "nil map")
	prev := Prev{Ref: "lx show 7", TurnsAgo: 1, Raw: rawOf([]Item{a, b}), Items: []Item{a, b}}
	a2 := failure("TestA", 10, "got 3, want 2")
	b2 := failure("TestB", 24, "nil map")
	b2.Block = strings.Replace(b2.Block, "(0.01s)", "(0.02s)", 1)
	out, ok := Delta(prev, []Item{a2, b2}, viewOf([]Item{a2, b2}, 40, "FAIL\tpkg\t0.3s"))
	if !ok {
		t.Fatal("no delta")
	}
	if !strings.Contains(out, "\nchanged:\n"+a2.Block+"\n") {
		t.Errorf("a new message for a failing test must be shown in full:\n%s", out)
	}
	if !strings.Contains(out, "still failing:\n  pkg TestB (thing_test.go:24)\n") || strings.Contains(out, "nil map") {
		t.Errorf("a failure that only moved and took longer is unchanged:\n%s", out)
	}
}

func TestDeltaRefusals(t *testing.T) {
	a := failure("TestA", 10, "got 1, want 2")
	b := failure("TestB", 20, "nil map")
	c := failure("TestC", 30, "boom")
	d := failure("TestD", 40, "bang")
	prev := Prev{Ref: "lx show 1", Raw: rawOf([]Item{a, b}), Items: []Item{a, b}}

	if _, ok := Delta(Prev{Ref: "lx show 1"}, nil, "some view"); ok {
		t.Error("nothing to compare")
	}
	if _, ok := Delta(prev, []Item{a, c, d}, viewOf([]Item{a, c, d}, 80, "")); ok {
		t.Error("more than half of the failures are new")
	}
	if _, ok := Delta(prev, []Item{a, b, c}, viewOf([]Item{a, b, c}, 0, "")); ok {
		t.Error("a delta that is not 30% smaller than the view must not replace it")
	}
	view := viewOf([]Item{a, b}, 40, "") + "\nfatal: connection refused while tearing down"
	if _, ok := Delta(prev, []Item{a, b}, view); ok {
		t.Error("an error line that is new and belongs to no item must keep the full view")
	}
	prev.Raw += "\nfatal: connection refused while tearing down"
	if _, ok := Delta(prev, []Item{a, b}, view); !ok {
		t.Error("the same stray error line as last time is not news")
	}
}

func TestDeltaDuplicateKeys(t *testing.T) {
	diag := func(line int) Item {
		return Item{Key: "src/a.ts: error TS2554: Expected 2 arguments, but got 1.", Block: fmt.Sprintf("src/a.ts(%d,5): error TS2554: Expected 2 arguments, but got 1.", line)}
	}
	var others []Item
	for i := range 6 {
		others = append(others, failure(fmt.Sprintf("Test%d", i), i, "bad"))
	}
	prev := Prev{Ref: "lx show 2", TurnsAgo: 1, Items: append([]Item{diag(10), diag(20), diag(30)}, others...)}
	prev.Raw = rawOf(prev.Items)
	cur := append([]Item{diag(10), diag(30)}, others...)
	out, ok := Delta(prev, cur, viewOf(cur, 60, "Found 2 errors in 1 file."))
	if !ok {
		t.Fatal("no delta")
	}
	for _, w := range []string{
		"fixed: src/a.ts: error TS2554: Expected 2 arguments, but got 1. (1 of 3)\n",
		"  src/a.ts: error TS2554: Expected 2 arguments, but got 1. ×2 (lines 10, 30)\n",
		"Found 2 errors in 1 file.",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
	cur = append([]Item{diag(10), diag(20), diag(30), diag(40)}, others...)
	out, ok = Delta(prev, cur, viewOf(cur, 60, ""))
	if !ok || !strings.Contains(out, "\nchanged:\n"+diag(10).Block+"\n"+diag(20).Block+"\n"+diag(30).Block+"\n"+diag(40).Block+"\n") {
		t.Fatalf("one more of the same error: every instance is shown (ok=%v):\n%s", ok, out)
	}
}

func TestDeltaLongStillList(t *testing.T) {
	var prev, cur []Item
	for i := range 60 {
		prev = append(prev, failure(fmt.Sprintf("Test%02d", i), 100+i, "bad"))
		line := 100 + i
		if i == 42 {
			line = 150
		}
		if i != 7 {
			cur = append(cur, failure(fmt.Sprintf("Test%02d", i), line, "bad"))
		}
	}
	out, ok := Delta(Prev{Ref: "lx show 3", TurnsAgo: 2, Raw: rawOf(prev), Items: prev}, cur, viewOf(cur, 100, ""))
	if !ok {
		t.Fatal("no delta")
	}
	want := "fixed: pkg Test07\nstill failing:\n  pkg Test42 (thing_test.go:150)\n  … +58 more, at the same lines as before"
	if !strings.Contains(out, want) {
		t.Fatalf("missing %q in:\n%s", want, out)
	}
}

func TestSame(t *testing.T) {
	view := "--- FAIL: TestA (0.01s)\n" + strings.Repeat("    detail line that the model already has\n", 30) + "FAIL\tpkg\t0.3s\nok  \tpkg/sub\t(cached)"
	prev := Prev{Ref: "lx show 12", TurnsAgo: 1}
	out, ok := Same(prev, view, false)
	if !ok || out != "[lx: output identical to lx show 12 (1 turn ago, still in your context)]\nFAIL\tpkg\t0.3s\nok  \tpkg/sub\t(cached)" {
		t.Fatalf("got ok=%v:\n%s", ok, out)
	}
	out, _ = Same(prev, view, true)
	if !strings.HasPrefix(out, "[lx: output identical to lx show 12 apart from timings (1 turn ago, still in your context)]\n") {
		t.Fatalf("got:\n%s", out)
	}
	if _, ok := Same(prev, "ok  \tpkg\t0.1s", false); ok {
		t.Fatal("a one-line view is shorter than the identical note")
	}
}

func TestSameExceptTimings(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"ok  \tpkg\t0.12s", "ok  \tpkg\t0.31s", true},
		{"Tests: 3 passed\nTime:        2.137 s", "Tests: 3 passed\nTime:        2.9 s", true},
		{"✓ renders (5 ms)", "✓ renders (12 ms)", true},
		{"=== 3 passed in 0.85s ===", "=== 3 passed in 1.02s ===", true},
		{"started 2026-09-20T10:01:00Z", "started 2026-09-20T10:04:31Z", true},
		{"expected 2, got 3", "expected 2, got 4", false},
		{"a.go:10: bad", "a.go:11: bad", false},
		{"one\ntwo", "one", false},
	} {
		if got := SameExceptTimings(tc.a, tc.b); got != tc.want {
			t.Errorf("SameExceptTimings(%q, %q) = %v", tc.a, tc.b, got)
		}
	}
}

func TestNormLine(t *testing.T) {
	same := [][2]string{
		{"    args_test.go:73: bad", "args_test.go:75: bad"},
		{"src/a.ts(10,5): error TS2554: x", "src/a.ts(12,1): error TS2554: x"},
		{"    > 28 |   expect(x).toBe(1);", "> 31 | expect(x).toBe(1);"},
		{"  1) Store #set:", "  4) Store #set:"},
		{"panic({0x1028db7e0?, 0x7f594249d110?})", "panic({0x10aa00000?, 0x7f0000000000?})"},
		{"goroutine 109 [running]:", "goroutine 7 [running]:"},
		{"(node:45926) [DEP0169] DeprecationWarning", "(node:7) [DEP0169] DeprecationWarning"},
	}
	for _, p := range same {
		if normLine(p[0]) != normLine(p[1]) {
			t.Errorf("%q and %q should compare equal: %q vs %q", p[0], p[1], normLine(p[0]), normLine(p[1]))
		}
	}
	for _, p := range [][2]string{{"Expected: 2060", "Expected: 2061"}, {"got 3 items", "got 4 items"}} {
		if normLine(p[0]) == normLine(p[1]) {
			t.Errorf("%q and %q must differ", p[0], p[1])
		}
	}
}

func TestKeyName(t *testing.T) {
	for key, want := range map[string]string{
		"test/a.test.js › Suite › does thing":      "does thing",
		"test/a.test.ts > suite > //foo//":         "//foo//",
		"tests/test_x.py::TestA::test_b[1-2]":      "test_b[1-2]",
		"src/a.ts: error TS2554: Expected 2 args.": "Expected 2 args.",
		"github.com/x/y TestFind/[child]":          "TestFind/[child]",
		"lib/a.js: error  Unexpected var  no-var":  "error Unexpected var no-var",
	} {
		if got := keyName(key); got != want {
			t.Errorf("keyName(%q) = %q, want %q", key, got, want)
		}
	}
}

func TestDeltaProperties(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	msgs := []string{"got 1, want 2", "nil pointer dereference", "timeout", "unexpected EOF", "mismatch"}
	used := 0
	defer func() {
		if used < 80 {
			t.Errorf("only %d of 200 rounds produced a delta", used)
		}
	}()
	for round := 0; round < 200; round++ {
		var prev, cur []Item
		for i := 0; i < 12; i++ {
			name, msg := fmt.Sprintf("Test%d", i), msgs[i%len(msgs)]
			was := r.Intn(3) > 0
			if was {
				prev = append(prev, failure(name, 10*i, msg))
			}
			if r.Intn(10) == 0 {
				msg = msgs[r.Intn(len(msgs))]
			}
			if was && r.Intn(5) > 0 || !was && r.Intn(5) == 0 {
				cur = append(cur, failure(name, 10*i+r.Intn(2), msg))
			}
		}
		view := viewOf(cur, r.Intn(120), "FAIL\tpkg\t1.1s")
		out, ok := Delta(Prev{Ref: "lx show 4", TurnsAgo: 2, Raw: rawOf(prev), Items: prev}, cur, view)
		if !ok {
			continue
		}
		used++
		old := map[string]string{}
		for _, it := range prev {
			old[it.Key] = normBlock(it.Block)
		}
		shown := 0
		for _, it := range cur {
			if b, seen := old[it.Key]; seen && b == normBlock(it.Block) {
				continue
			}
			shown++
			if !strings.Contains(out, it.Block+"\n") {
				t.Fatalf("round %d: new or changed %q is not shown in full:\n%s", round, it.Key, out)
			}
		}
		if 2*shown > len(cur) {
			t.Fatalf("round %d: %d of %d items are new, yet a delta was used", round, shown, len(cur))
		}
		if float64(tokens.Count(out)) > 0.7*float64(tokens.Count(view)) {
			t.Fatalf("round %d: delta is not 30%% smaller", round)
		}
		for k := range old {
			found := false
			for _, it := range cur {
				found = found || it.Key == k
			}
			if !found && !strings.Contains(out, k) {
				t.Fatalf("round %d: fixed %q is not reported:\n%s", round, k, out)
			}
		}
		if !strings.HasSuffix(out, "FAIL\tpkg\t1.1s") {
			t.Fatalf("round %d: summary line missing:\n%s", round, out)
		}
	}
}

func TestItemsOfWithoutIdentities(t *testing.T) {
	if _, ok := ItemsOf(&Context{Argv: []string{"definitely-not-a-known-tool"}}, "x"); ok {
		t.Fatal("no filter, no items")
	}
	if _, ok := ItemsOf(nil, "x"); ok {
		t.Fatal("nil context")
	}
}

type dropTool struct{}

func (dropTool) Name() string                                { return "delta-drop-test" }
func (dropTool) Match(c *Context) bool                       { return c.Name() == "lxdroptool" }
func (dropTool) Apply(c *Context, out string) (string, bool) { return out, true }
func (dropTool) Items(c *Context, out string) []Item {
	var items []Item
	for _, ln := range strings.Split(out, "\n") {
		if ln == "[cut]" {
			break
		}
		if name, _, ok := strings.Cut(strings.TrimPrefix(ln, "FAIL "), ":"); ok && strings.HasPrefix(ln, "FAIL ") {
			items = append(items, Item{Key: name, Block: ln})
		}
	}
	return items
}

func init() { Register(dropTool{}) }

func dropOutput(lines ...string) string {
	var b strings.Builder
	for _, ln := range lines {
		b.WriteString(ln + "\n")
	}
	for i := range 60 {
		fmt.Fprintf(&b, "    trace line %d of a long report\n", i)
	}
	return b.String() + "FAIL\tpkg\t0.2s"
}

func TestRerunRefusesAFalseFix(t *testing.T) {
	c := &Context{Argv: []string{"lxdroptool"}, Exit: 1}
	prev := dropOutput("FAIL alpha_case: boom", "FAIL beta_case: nil map", "FAIL gamma_case: timeout", "FAIL delta_case: bad input")
	p := Prev{Ref: "lx show 3", TurnsAgo: 1, Exit: 1, Raw: prev}

	fixed := dropOutput("FAIL omega_case: index out of range", "FAIL alpha_case: boom", "FAIL gamma_case: timeout", "FAIL delta_case: bad input")
	out, ok := Rerun(c, p, fixed, fixed)
	if !ok || !strings.Contains(out, "fixed: beta_case\n") {
		t.Fatalf("a real fix (ok=%v):\n%s", ok, out)
	}
	missed := dropOutput("FAIL omega_case: index out of range", "FAIL alpha_case: boom", "[cut]", "FAIL beta_case: nil map", "FAIL gamma_case: timeout", "FAIL delta_case: bad input")
	if out, ok := Rerun(c, p, missed, missed); ok {
		t.Fatalf("beta_case still fails in the output, yet:\n%s", out)
	}
}

func TestDeltaShortNameExplainsNothing(t *testing.T) {
	x := Item{Key: "src/main.rs: warning: unused variable: `x`", Block: "warning: unused variable: `x`\n  --> src/main.rs:3:9", Loc: "src/main.rs:3"}
	items := []Item{x}
	for i := range 6 {
		items = append(items, failure(fmt.Sprintf("Test%d", i), i, "bad"))
	}
	prev := Prev{Ref: "lx show 2", TurnsAgo: 1, Raw: rawOf(items), Items: items}
	view := viewOf(items, 60, "") + "\nerror: cannot move out of `x` in the drop handler"
	if out, ok := Delta(prev, items, view); ok {
		t.Fatalf("a new error that only shares a short name with a failure must keep the full view:\n%s", out)
	}
}

func TestRerunSeenBudget(t *testing.T) {
	for _, tc := range []struct {
		budget   int
		mode     Mode
		prevExit int
		want     int
	}{
		{0, ModeAuto, 1, DefaultBudget},
		{8000, ModeAuto, 1, 8000},
		{12000, ModeError, 1, 8000},
		{16000, ModeDebug, 1, 8000},
		{8000, ModeAuto, 0, 4000},
		{4000, ModeVerify, 0, 4000},
		{2000, ModeMinimal, 0, 2000},
	} {
		if got := seenBudget(&Context{Budget: tc.budget, Mode: tc.mode}, tc.prevExit); got != tc.want {
			t.Errorf("seenBudget(%d, %v, exit %d) = %d, want %d", tc.budget, tc.mode, tc.prevExit, got, tc.want)
		}
	}
}

func TestRerunKeepsAFailureItemsMissed(t *testing.T) {
	c := &Context{Argv: []string{"lxdroptool"}, Exit: 1}
	prev := dropOutput("FAIL alpha_case: boom", "FAIL beta_case: nil map", "FAIL gamma_case: timeout", "FAIL delta_case: bad input", "[cut]")
	cur := dropOutput("FAIL alpha_case: boom", "FAIL gamma_case: timeout", "FAIL delta_case: bad input", "[cut]",
		"  ● Header › renders the title", "    Expected: \"Shop\"", "    Received: \"\"")
	if out, ok := Rerun(c, Prev{Ref: "lx show 3", TurnsAgo: 1, Exit: 1, Raw: prev}, cur, cur); ok {
		t.Fatalf("a failure no item describes must keep the full view:\n%s", out)
	}
	counts := strings.Replace(cur, "  ● Header › renders the title\n    Expected: \"Shop\"\n    Received: \"\"\n", "collected 1106 items\n", 1)
	prev = strings.Replace(prev, "FAIL alpha_case", "collected 1105 items\nFAIL alpha_case", 1)
	if out, ok := Rerun(c, Prev{Ref: "lx show 3", TurnsAgo: 1, Exit: 1, Raw: prev}, counts, counts); !ok || !strings.Contains(out, "fixed: beta_case\n") {
		t.Fatalf("a count that changed is not news (ok=%v):\n%s", ok, out)
	}
}

func TestSameExceptTimingsKeepsChangedAssertions(t *testing.T) {
	cases := []struct {
		a, b string
		same bool
	}{
		{"ok  \tgithub.com/x/y\t0.412s", "ok  \tgithub.com/x/y\t0.388s", true},
		{"--- FAIL: TestX (0.00s)", "--- FAIL: TestX (0.01s)", true},
		{"    x_test.go:12: want 30s, got 45s", "    x_test.go:12: want 30s, got 60s", false},
		{"timeout: expected 5s but took 9s", "timeout: expected 5s but took 12s", false},
		{"Error: request took 300ms", "Error: request took 900ms", false},
		{"retrying in 2s", "retrying in 4s", true},
	}
	for _, tc := range cases {
		if got := SameExceptTimings(tc.a, tc.b); got != tc.same {
			t.Errorf("SameExceptTimings(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.same)
		}
	}
}
