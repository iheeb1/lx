package build

import (
	"fmt"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
)

func TestCargoTestDetectorCapture(t *testing.T) {
	fc, err := fixture.Read("testdata", "detect", "just-test-cargo-fail")
	if err != nil {
		t.Fatal(err)
	}
	clean := fc.Clean()
	if !detectCargoTest(clean) {
		t.Fatal("cargo-test detector did not fire")
	}
	c := fc.Context()
	out, shape := engine.GenericShape(c, clean)
	if shape != "detected:cargo-test" {
		t.Fatalf("shape %q", shape)
	}
	direct, ok := cargoTest{}.Apply(&engine.Context{Argv: []string{"cargo", "test"}, Exit: c.Exit, Cwd: c.Cwd, Home: c.Home}, clean)
	if !ok || direct != out {
		t.Fatal("detected view differs from the cargo-test filter's own view")
	}
	fixture.Golden(t, "detect", fc.Name, out)
	if miss := fixture.ErrorLinesMissing(clean, out); len(miss) > 0 {
		t.Errorf("error lines missing: %q", miss)
	}
	if miss := fixture.AppLocationsMissing(clean, out); len(miss) > 0 {
		t.Errorf("locations missing: %q", miss)
	}
	for _, ln := range []string{"cargo test", "error: Recipe `test` failed on line 2 with exit code 101"} {
		if !strings.Contains(out, ln) {
			t.Errorf("runner line not kept: %q", ln)
		}
	}
	for _, ln := range strings.Split(clean, "\n") {
		if testResultRe.MatchString(ln) && !strings.Contains(out, ln) {
			t.Errorf("result line not kept verbatim: %q", ln)
		}
	}

	for _, f := range loadFixtures(t) {
		want := strings.HasPrefix(f.Name, "cargo-test")
		if got := detectCargoTest(f.Clean()); got != want {
			t.Errorf("%s: detector fired = %v, want %v", f.Name, got, want)
		}
	}
}

func TestCargoTestDetectorRejects(t *testing.T) {
	for name, s := range map[string]string{
		"bench": "running 2 tests\ntest bench_a ... bench:       1,234 ns/iter (+/- 56)\ntest bench_b ... bench:         789 ns/iter (+/- 12)\n\n" +
			"test result: ok. 0 passed; 0 failed; 0 ignored; 2 measured; 0 filtered out; finished in 1.02s\n",
		"--list": "tests::adds: test\ntests::subtracts: test\n\n2 tests, 0 benchmarks\n",
		"nextest": "    Starting 2 tests across 1 binary\n        PASS [   0.004s] demo tests::adds\n        FAIL [   0.005s] demo tests::subtracts\n" +
			"------------\n     Summary [   0.010s] 2 tests run: 1 passed, 1 failed, 0 skipped\n        FAIL [   0.005s] demo tests::subtracts\nerror: test run failed\n",
		"deno":              "running 2 tests from ./a_test.ts\nadds ... ok (1ms)\n\nok | 2 passed | 0 failed (25ms)\n",
		"result line only":  "test result: ok. 2 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.00s\n",
		"running line only": "running 2 tests\ntest a ... ok\ntest b ... ok\n",
	} {
		if detectCargoTest(s) {
			t.Errorf("%s: detector fired on\n%s", name, s)
		}
	}
}

func nextestFail(outHeader func(stream, test string) string) string {
	var b strings.Builder
	b.WriteString("    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.21s\n" +
		"────────────\n Nextest run ID 6a1f0b0c-2d3e-4f50-8a9b-0c1d2e3f4a5b with nextest profile: default\n" +
		"    Starting 60 tests across 2 binaries\n")
	for i := range 58 {
		fmt.Fprintf(&b, "        PASS [   0.004s] demo store::tests::case_%d\n", i)
	}
	for _, name := range []string{"parser::tests::parses_negative", "parser::tests::parses_hex"} {
		fmt.Fprintf(&b, "        FAIL [   0.005s] demo %s\n%s\n\nrunning 1 test\ntest %s ... FAILED\n\n"+
			"failures:\n\nfailures:\n    %s\n\n"+
			"test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 59 filtered out; finished in 0.00s\n\n"+
			"%s\n\nthread '%s' panicked at src/parser.rs:141:9:\n"+
			"called `Result::unwrap()` on an `Err` value: ParseIntError { kind: InvalidDigit }\n"+
			"note: run with `RUST_BACKTRACE=1` environment variable to display a backtrace\n\n",
			name, outHeader("STDOUT", name), name, name, outHeader("STDERR", name), name)
	}
	b.WriteString("────────────\n     Summary [   0.210s] 60 tests run: 58 passed, 2 failed, 0 skipped\n" +
		"        FAIL [   0.005s] demo parser::tests::parses_negative\n" +
		"        FAIL [   0.005s] demo parser::tests::parses_hex\nerror: test run failed\n")
	return b.String()
}

func TestCargoTestDetectorRejectsNextest(t *testing.T) {
	current := nextestFail(func(stream, test string) string { return "──── " + stream + ":             demo " + test })
	older := nextestFail(func(stream, test string) string {
		return "--- " + stream + ":              demo " + test + " ---"
	})
	if !engine.HasLinePrefix(current, "running ", runningNRe.MatchString) || !engine.HasLinePrefix(current, "test result: ", testResultRe.MatchString) {
		t.Fatal("test bug: the nextest report lacks libtest's lines")
	}
	for name, s := range map[string]string{"nextest": current, "nextest (older headers)": older} {
		if detectCargoTest(s) {
			t.Errorf("%s: cargo-test detector fired", name)
		}
		c := &engine.Context{Argv: []string{"cargo", "nextest", "run"}, Exit: 100}
		if _, det, ok := engine.Detect(c, s); ok {
			t.Errorf("%s: detector %s rendered nextest's report", name, det)
		}
	}

	libtest := "running 1 test\ntest a ... FAILED\n\ntest result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 3 filtered out; finished in 0.00s\n"
	if !detectCargoTest(libtest) {
		t.Fatal("test bug: libtest's lines alone must be detected")
	}
	for _, ln := range []string{
		" Nextest run ID 6a1f0b0c-2d3e-4f50-8a9b-0c1d2e3f4a5b with nextest profile: ci",
		"    Starting 1 test across 1 binary",
		"    Starting 60 tests across 2 binaries (4 tests skipped)",
		"     Summary [   0.010s] 1 test run: 0 passed, 1 failed, 0 skipped",
		"──── STDOUT:             demo a",
		"──── STDERR:             demo a",
		"--- STDOUT:              demo a ---",
	} {
		if detectCargoTest(ln + "\n" + libtest) {
			t.Errorf("cargo-test detector fired with nextest's line %q", ln)
		}
	}
}
