package python

import (
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

const pytestNew = `________________________ test_float_range_clamp _________________________

    def test_float_range_clamp():
        t = click.FloatRange(max=1.0, clamp=True)
>       assert t.convert("1.5", None, None) == 1.0
E       AssertionError: assert 1.5 == 1.0
E        +  where 1.5 = convert('1.5', None, None)

tests/test_types.py:61: AssertionError
`

func pytestRerun(t *testing.T, raw string) string {
	t.Helper()
	i := strings.Index(raw, "________________________________ test_counting")
	j := strings.Index(raw, "_______________ test_intrange_default_help_text[type0-1<=x<=32]")
	if i < 0 || j < 0 {
		t.Fatal("fixture changed")
	}
	cur := raw[:i] + raw[j:]
	for _, p := range [][2]string{
		{"tests/test_options.py ....F.....", "tests/test_options.py .........."},
		{"tests/test_types.py ........F.....F..FF.F.............s....", "tests/test_types.py ........F.....F..FF.F......F......s...."},
		{"FAILED tests/test_options.py::test_counting - assert \"Invalid value for '-v':...\n", ""},
		{"=========================== short test summary info", pytestNew + "=========================== short test summary info"},
		{"FAILED tests/test_types.py::test_range_fail[type6-1.5-x<1.5] - Failed: DID NO...\n",
			"FAILED tests/test_types.py::test_range_fail[type6-1.5-x<1.5] - Failed: DID NO...\nFAILED tests/test_types.py::test_float_range_clamp - AssertionError: assert 1.5 == 1.0\n"},
		{"in 0.85s", "in 0.91s"},
	} {
		if !strings.Contains(cur, p[0]) {
			t.Fatalf("fixture text %q not found", p[0])
		}
		cur = strings.Replace(cur, p[0], p[1], 1)
	}
	return cur
}

func TestDeltaPytest(t *testing.T) {
	fc := fixture.Load(t, "python", "pytest-fail")
	c := fc.Context()
	cur := pytestRerun(t, fc.Raw)
	res := engine.Process(c, cur, engine.Options{})
	delta, ok := engine.Rerun(c, engine.Prev{Ref: "lx show 8", TurnsAgo: 4, Exit: 1, Raw: fc.Raw}, cur, res.Output)
	if !ok {
		t.Fatalf("no delta; view:\n%s", res.Output)
	}
	if float64(tokens.Count(delta)) > 0.7*float64(tokens.Count(res.Output)) {
		t.Errorf("delta is not 30%% smaller than the view")
	}
	for _, want := range []string{
		"[lx: same command as lx show 8 (4 turns ago, still in your context) — only what changed:]\nnew:\n",
		"________________________ test_float_range_clamp _________________________\n",
		"E       AssertionError: assert 1.5 == 1.0\n",
		"fixed: tests/test_options.py::test_counting\n",
		"  tests/test_types.py::test_range[type8-5-4] (line 32)\n",
		"  tests/test_types.py::test_range_fail[type0-6-6 is not in the range 0<=x<=5.] (line 51)\n",
		"============ 11 failed, 616 passed, 22 skipped, 1 xfailed in 0.91s =============",
	} {
		if !strings.Contains(delta, want) {
			t.Errorf("missing %q in:\n%s", want, delta)
		}
	}
	if strings.Contains(delta, "DID NOT RAISE") {
		t.Errorf("an unchanged failure was repeated:\n%s", delta)
	}
}

func TestDeltaPytestKeys(t *testing.T) {
	fc := fixture.Load(t, "python", "pytest-fail")
	items, ok := engine.ItemsOf(fc.Context(), fc.Clean())
	if !ok || len(items) != 11 {
		t.Fatalf("%d items (ok=%v), want 11", len(items), ok)
	}
	if items[0].Key != "tests/test_normalization.py::test_option_normalization" {
		t.Fatalf("first key %q", items[0].Key)
	}

	fc = fixture.Load(t, "python", "pytest-collection-errors")
	items, _ = engine.ItemsOf(fc.Context(), fc.Clean())
	var keys []string
	for _, it := range items {
		keys = append(keys, it.Key)
	}
	if strings.Join(keys, "|") != "ERROR collecting tests/test_parser.py|ERROR collecting tests/test_utils.py" {
		t.Fatalf("keys %q", keys)
	}
}

func TestDeltaPytestShortOnly(t *testing.T) {
	out := "collected 3 items\n\nFAILED tests/test_a.py::test_x - assert 1 == 2\nFAILED tests/test_a.py::test_y[a b] - KeyError\n3 failed in 0.02s\n"
	items := pytestFilter{}.Items(&engine.Context{Argv: []string{"pytest", "-q", "--tb=no"}, Exit: 1}, out)
	if len(items) != 2 || items[0].Key != "tests/test_a.py::test_x" || items[1].Key != "tests/test_a.py::test_y[a b]" {
		t.Fatalf("items %+v", items)
	}
}
