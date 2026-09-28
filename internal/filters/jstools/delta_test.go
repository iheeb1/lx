package jstools

import (
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

func deltaOf(t *testing.T, fc fixture.Case, cur string) string {
	t.Helper()
	c := fc.Context()
	res := engine.Process(c, cur, engine.Options{})
	delta, ok := engine.Rerun(c, engine.Prev{Ref: "lx show 21", TurnsAgo: 2, Exit: c.Exit, Raw: fc.Raw}, cur, res.Output)
	if !ok {
		t.Fatalf("no delta; view:\n%s", res.Output)
	}
	if float64(tokens.Count(delta)) > 0.7*float64(tokens.Count(res.Output)) {
		t.Errorf("delta is not 30%% smaller than the view:\n%s", delta)
	}
	return delta
}

func edit(t *testing.T, s string, pairs ...string) string {
	t.Helper()
	for i := 0; i < len(pairs); i += 2 {
		if !strings.Contains(s, pairs[i]) {
			t.Fatalf("fixture text %q not found", pairs[i])
		}
		s = strings.Replace(s, pairs[i], pairs[i+1], 1)
	}
	return s
}

func has(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

func TestDeltaTSC(t *testing.T) {
	fc := fixture.Load(t, "node", "tsc-noemit-errors")
	cur := edit(t, fc.Raw,
		"src/util/reach.ts(21,18): error TS7006: Parameter '_part' implicitly has an 'any' type.\n", "",
		"src/util/reach.ts(21,25): error TS7006:", "src/util/reach.ts(22,25): error TS7006:",
		"src/util/reach.ts(21,36): error TS7006: Parameter 'isArray' implicitly has an 'any' type.\n",
		"src/util/reach.ts(22,36): error TS7006: Parameter 'isArray' implicitly has an 'any' type.\nsrc/util/reach.ts(30,5): error TS2322: Type 'string' is not assignable to type 'number'.\n")
	delta := deltaOf(t, fc, cur)
	has(t, delta,
		"new:\nsrc/util/reach.ts(30,5): error TS2322: Type 'string' is not assignable to type 'number'.\n",
		"fixed: src/util/reach.ts: error TS7006: Parameter '_part' implicitly has an 'any' type.\n",
		"  src/util/reach.ts: error TS7006: Parameter 'isBracket' implicitly has an 'any' type. (line 22)\n",
		"  src/string.ts: error TS2554: Expected 2 arguments, but got 1. ×5 (lines 40, 284, 291, …)\n")
	if strings.Contains(delta, "Types of property 'coerce' are incompatible") {
		t.Errorf("an unchanged elaboration was repeated")
	}
}

func TestDeltaESLint(t *testing.T) {
	fc := fixture.Load(t, "node", "eslint-many-problems")
	cur := edit(t, fc.Raw,
		"    8:1   error    Unexpected var, use let or const instead  no-var\n", "",
		"   50:30  warning  Unexpected function expression            prefer-arrow-callback\n",
		"   50:30  warning  Unexpected function expression            prefer-arrow-callback\n   60:7   error    'unused' is assigned a value but never used  no-unused-vars\n",
		"✖ 529 problems (416 errors, 113 warnings)", "✖ 529 problems (416 errors, 113 warnings)")
	delta := deltaOf(t, fc, cur)
	has(t, delta,
		"new:\nexamples/auth/index.js:60:7  error  'unused' is assigned a value but never used  no-unused-vars\n",
		"fixed: examples/auth/index.js: error  Unexpected var, use let or const instead  no-var (1 of 9)",
		"✖ 529 problems (416 errors, 113 warnings)")
}

func TestDeltaESLintItems(t *testing.T) {
	fc := fixture.Load(t, "node", "eslint-many-problems")
	items, ok := engine.ItemsOf(fc.Context(), fc.Clean())
	if !ok || len(items) != 529 {
		t.Fatalf("%d items (ok=%v), want all 529 problems", len(items), ok)
	}
	if it := items[0]; it.Block != "examples/auth/index.js:7:1  error  Unexpected var, use let or const instead  no-var" || it.Loc != "examples/auth/index.js:7" {
		t.Fatalf("first item %+v", it)
	}
}
