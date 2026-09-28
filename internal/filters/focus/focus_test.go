package focus

import (
	"fmt"
	"math"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
)

func terms(ts ...string) *engine.Focus {
	f := &engine.Focus{}
	for _, t := range ts {
		f.Terms = append(f.Terms, engine.FocusTerm{Text: t, Weight: 1})
	}
	return f
}

func TestNameForms(t *testing.T) {
	fx := New(terms("parseConfig"))
	for _, name := range []string{"TestParseConfig", "TestParseConfig/empty", "Test_parseConfig", "test_parseConfig"} {
		if fx.Score(NameForms(name), "") == 0 {
			t.Errorf("%s should match parseConfig", name)
		}
	}
	for _, name := range []string{"TestReparseConfig", "TestParseConfigs", "Testing"} {
		if fx.Score(NameForms(name), "") != 0 {
			t.Errorf("%s should not match parseConfig", name)
		}
	}
}

func TestFileMentions(t *testing.T) {
	fx := New(&engine.Focus{Files: []string{"/home/u/app/internal/cli/run.go"}})
	cases := map[string]bool{
		"run.go:12: undefined: x":                    true,
		"    internal/cli/run.go:40 +0x1d":           true,
		"/home/u/app/internal/cli/run.go:3":          true,
		"../cli/run.go:3":                            true,
		"./internal/cli/run.go:3":                    true,
		"internal/engine/run.go:40":                  false,
		"rerun.go:1":                                 false,
		"run.gopher":                                 false,
		"/home/u/other/internal/cli/run.go:4":        false,
		"/home/u/app/internal/other/run.go:4":        false,
		"file:///home/u/app/internal/cli/run.go:1:2": true,
	}
	for s, want := range cases {
		if got := fx.Score("", s) > 0; got != want {
			t.Errorf("%q: got %v, want %v", s, got, want)
		}
	}
	for p, want := range map[string]bool{
		"internal/cli/run.go":   true,
		"./internal/cli/run.go": true,
		`internal\cli\run.go`:   true,
		"cli/run.go":            true,
		"engine/run.go":         false,
		"":                      false,
	} {
		if fx.File(p) != want {
			t.Errorf("File(%q) = %v", p, !want)
		}
	}
}

func TestTermsNeedBoundaries(t *testing.T) {
	fx := New(terms("cart", "Total", "a.b"))
	for s, want := range map[string]float64{
		"cart_test failed":    0,
		"the Cart is empty":   1,
		"subtotal":            0,
		"Total: 3":            1,
		"Totally":             0,
		"x = a.b(1)":          1,
		"in cart and a.b too": 2,
	} {
		if got := fx.Score("", s); got != want {
			t.Errorf("%q: %v, want %v", s, got, want)
		}
	}
	if got := fx.Score("Total", ""); got != 2 {
		t.Errorf("a term in the name counts twice, got %v", got)
	}
}

func TestNewIgnoresJunk(t *testing.T) {
	if New(nil) != nil || New(&engine.Focus{}) != nil || New(terms("ab", " ", "a\nbc")) != nil {
		t.Error("a focus with nothing usable should be nil")
	}
	f := &engine.Focus{Terms: []engine.FocusTerm{{Text: "alpha", Weight: math.NaN()}, {Text: "beta", Weight: math.Inf(1)}, {Text: "gamma", Weight: -2}}}
	if got := New(f).Score("", "alpha beta gamma"); got != 3 {
		t.Errorf("bad weights should count as 1, got %v", got)
	}
	big := &engine.Focus{}
	for i := range 500 {
		big.Terms = append(big.Terms, engine.FocusTerm{Text: fmt.Sprintf("term%d", i), Weight: 1})
		big.Files = append(big.Files, fmt.Sprintf("/src/f%d.go", i))
	}
	fx := New(big)
	if len(fx.terms) != maxItems || len(fx.files) != maxItems {
		t.Errorf("%d terms and %d files kept, want at most %d", len(fx.terms), len(fx.files), maxItems)
	}
}

func TestRank(t *testing.T) {
	if Rank(3, func(int) float64 { return 0 }) != nil {
		t.Error("equal scores should keep the order")
	}
	got := Rank(4, func(k int) float64 { return []float64{0, 2, 0, 1}[k] })
	if fmt.Sprint(got) != "[1 3 0 2]" {
		t.Errorf("got %v", got)
	}
}
