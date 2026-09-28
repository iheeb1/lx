package build

import (
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

func cargoCase(t *testing.T, name string) fixture.Case {
	t.Helper()
	fc, err := fixture.Read("testdata", "synthetic", name)
	if err != nil {
		t.Fatal(err)
	}
	return fc
}

func replaceAll(t *testing.T, s string, pairs ...string) string {
	t.Helper()
	for i := 0; i < len(pairs); i += 2 {
		if !strings.Contains(s, pairs[i]) {
			t.Fatalf("fixture text %q not found", pairs[i])
		}
		s = strings.Replace(s, pairs[i], pairs[i+1], 1)
	}
	return s
}

func cargoDelta(t *testing.T, fc fixture.Case, cur string) string {
	t.Helper()
	c := fc.Context()
	res := engine.Process(c, cur, engine.Options{})
	delta, ok := engine.Rerun(c, engine.Prev{Ref: "lx show 9", TurnsAgo: 1, Exit: c.Exit, Raw: fc.Raw}, cur, res.Output)
	if !ok {
		t.Fatalf("no delta; view:\n%s", res.Output)
	}
	if float64(tokens.Count(delta)) > 0.7*float64(tokens.Count(res.Output)) {
		t.Errorf("delta is not 30%% smaller than the view:\n%s", delta)
	}
	return delta
}

func expect(t *testing.T, got string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
}

const cargoNewFailure = `---- config::tests::fails_on_bad_port stdout ----

thread 'config::tests::fails_on_bad_port' panicked at src/config.rs:88:9:
expected Err for port 70000, got Ok(4464)
note: run with ` + "`RUST_BACKTRACE=1`" + ` environment variable to display a backtrace


failures:
    config::tests::fails_on_bad_port
    store::tests::iter_order
`

func TestDeltaCargoTest(t *testing.T) {
	fc := cargoCase(t, "cargo-test-fail")
	i := strings.Index(fc.Raw, "---- parser::tests::parses_negative stdout ----")
	j := strings.Index(fc.Raw, "---- store::tests::iter_order stdout ----")
	cur := fc.Raw[:i] + fc.Raw[j:]
	cur = replaceAll(t, cur,
		"test parser::tests::parses_negative ... FAILED", "test parser::tests::parses_negative ... ok",
		"test config::tests::fails_on_bad_port ... ok", "test config::tests::fails_on_bad_port ... FAILED",
		"\n\nfailures:\n    parser::tests::parses_negative\n    store::tests::iter_order\n", "\n"+cargoNewFailure,
		"finished in 0.04s", "finished in 0.05s")
	delta := cargoDelta(t, fc, cur)
	expect(t, delta,
		"new:\n---- config::tests::fails_on_bad_port stdout ----\n",
		"expected Err for port 70000, got Ok(4464)",
		"fixed: parser::tests::parses_negative\n",
		"still failing:\n  store::tests::iter_order (src/store.rs:212)\n",
		"test result: FAILED. 13 passed; 2 failed; 1 ignored; 0 measured; 0 filtered out; finished in 0.05s",
		"error: test failed, to rerun pass `--lib`")
	if strings.Contains(delta, "rows must come back in key order") {
		t.Errorf("an unchanged failure was repeated:\n%s", delta)
	}
}

func TestDeltaCargoBuild(t *testing.T) {
	fc := cargoCase(t, "cargo-build-errors")
	items, ok := engine.ItemsOf(fc.Context(), fc.Clean())
	if !ok || len(items) != 4 {
		t.Fatalf("%d items (ok=%v), want 1 warning and 3 errors", len(items), ok)
	}
	if it := items[1]; it.Key != "src/main.rs: error[E0308]: mismatched types" || it.Loc != "src/main.rs:18" {
		t.Fatalf("item %+v", it)
	}
	cur := replaceAll(t, fc.Raw,
		"error[E0425]: cannot find value `retries` in this scope\n  --> src/main.rs:24:31\n   |\n24 |     for attempt in 0..retries {\n   |                       ^^^^^^^ help: a local variable with a similar name exists: `retry`\n\n", "",
		"--> src/net.rs:9:14", "--> src/net.rs:11:14",
		"due to 3 previous errors", "due to 2 previous errors")
	c := fc.Context()
	view := engine.Process(c, cur, engine.Options{}).Output
	prevItems, _ := engine.ItemsOf(c, fc.Clean())
	curItems, _ := engine.ItemsOf(c, cur)
	delta, ok := engine.Delta(engine.Prev{Ref: "lx show 9", Raw: fc.Clean(), Items: prevItems}, curItems, view+"\n"+view)
	if !ok {
		t.Fatal("no delta")
	}
	expect(t, delta,
		"fixed: src/main.rs: error[E0425]: cannot find value `retries` in this scope\n",
		"  src/net.rs: error[E0599]: no method named `connect_timeout` found for struct `Client` in the current scope (line 11)\n",
		"error: could not compile `demo` (bin \"demo\") due to 2 previous errors; 1 warning emitted")
}
