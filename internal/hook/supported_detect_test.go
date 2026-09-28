package hook_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/hook"
	"github.com/iheeb1/lx/internal/textutil"
)

type runnerOutput struct {
	cmd      string
	exit     int
	raw      string
	detected string
	keep     []string
}

func loadPkgCapture(t *testing.T, pkg, name string) fixture.Case {
	t.Helper()
	c, err := fixture.Read(filepath.Join(fixture.Root(), "internal", "filters", pkg, "testdata"), "detect", name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func runnerOutputs(t *testing.T) []runnerOutput {
	t.Helper()
	corpus := func(cat, name string) string { return strings.TrimRight(fixture.Load(t, cat, name).Raw, "\n") }
	pkg := func(p, name string) fixture.Case { return loadPkgCapture(t, p, name) }
	nxFooter := func(target string, failed bool) string {
		if failed {
			return "\n\n——————————————————————————————————————————————\n\n >  NX   Ran target " + target + " for project app (4s)\n\n   ✖  1/1 failed\n   ✔  0/1 succeeded [0 read from cache]\n"
		}
		return "\n\n——————————————————————————————————————————————\n\n >  NX   Successfully ran target " + target + " for project app (4s)\n"
	}
	turbo := func(task, body string) string {
		var b strings.Builder
		b.WriteString("• Packages in scope: web\n• Running " + task + " in 1 packages\n• Remote caching disabled\n")
		b.WriteString("web:" + task + ": cache miss, executing 5c2a0e7f1b3d4a96\n")
		for _, ln := range strings.Split(body, "\n") {
			b.WriteString("web:" + task + ": " + ln + "\n")
		}
		b.WriteString("web:" + task + ": ERROR: command finished with error: command (/w/apps/web) /usr/local/bin/npm run " + task + " exited (1)\n")
		b.WriteString("web#" + task + ": command (/w/apps/web) /usr/local/bin/npm run " + task + " exited (1)\n\n")
		b.WriteString(" Tasks:    0 successful, 1 total\nCached:    0 cached, 1 total\n  Time:    6.5s \nFailed:    web#" + task + "\n\n")
		b.WriteString(" ERROR  run failed: command  exited (1)")
		return b.String()
	}
	goFail := corpus("go", "go-test-fail")
	eslint := corpus("node", "eslint-many-problems")
	pytest := corpus("python", "pytest-fail")
	tsc := corpus("node", "tsc-noemit-errors")
	vitest := corpus("node", "vitest-fail")
	jest := corpus("node", "jest-fail")
	mocha := corpus("node", "mocha-fail")

	rakeGo := pkg("golang", "rake-test-go-fail")
	rakeLint := pkg("jstools", "rake-lint-eslint-fail")
	composerPy := pkg("python", "composer-test-pytest-fail")
	composerTsc := pkg("jstools", "composer-typecheck-tsc-fail")
	testonly := pkg("jstest", "npm-run-testonly-vitest")

	denoTest := "running 3 tests from ./tests/url_test.ts\njoins paths ... ok (2ms)\nkeeps query ... ok (1ms)\n" +
		"normalizes slashes ... FAILED (4ms)\n\n ERRORS \n\nnormalizes slashes => ./tests/url_test.ts:14:6\n" +
		"error: AssertionError: Values are not equal.\n\n\n    [Diff] Actual / Expected\n\n\n-   \"/a//b\"\n+   \"/a/b\"\n\n" +
		"    at assertEquals (https://jsr.io/@std/assert/1.0.8/equals.ts:51:9)\n    at file:///w/tests/url_test.ts:16:3\n\n" +
		" FAILURES \n\nnormalizes slashes => ./tests/url_test.ts:14:6\n\nFAILED | 2 passed | 1 failed (41ms)\n\nerror: Test failed"
	var denoMany strings.Builder
	for i := range 40 {
		denoMany.WriteString("url helper case " + strings.Repeat("x", i%7) + " ... ok (0ms)\n")
	}

	var nextest strings.Builder
	nextest.WriteString("    Finished `test` profile [unoptimized + debuginfo] target(s) in 0.21s\n" +
		"────────────\n Nextest run ID 6a1f0b0c-2d3e-4f50-8a9b-0c1d2e3f4a5b with nextest profile: default\n" +
		"    Starting 60 tests across 2 binaries\n")
	for i := range 59 {
		nextest.WriteString("        PASS [   0.004s] demo store::tests::case_" + strings.Repeat("a", 1+i%5) + "\n")
	}

	nextest.WriteString("        FAIL [   0.005s] demo parser::tests::parses_negative\n" +
		"──── STDOUT:             demo parser::tests::parses_negative\n\nrunning 1 test\ntest parser::tests::parses_negative ... FAILED\n\n" +
		"failures:\n\nfailures:\n    parser::tests::parses_negative\n\n" +
		"test result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 59 filtered out; finished in 0.00s\n\n" +
		"──── STDERR:             demo parser::tests::parses_negative\n\n" +
		"thread 'parser::tests::parses_negative' panicked at src/parser.rs:141:9:\n" +
		"called `Result::unwrap()` on an `Err` value: ParseIntError { kind: InvalidDigit }\n\n" +
		"────────────\n     Summary [   0.210s] 60 tests run: 59 passed, 1 failed, 0 skipped\n" +
		"        FAIL [   0.005s] demo parser::tests::parses_negative\nerror: test run failed")

	rspec := "Randomized with seed 4242\n" + strings.Repeat(".", 120) + "F" + strings.Repeat(".", 40) + "\n\nFailures:\n\n" +
		"  1) Cart#total sums line items\n     Failure/Error: expect(cart.total).to eq(30)\n\n       expected: 30\n            got: 20\n\n" +
		"       (compared using ==)\n     # ./spec/cart_spec.rb:12:in 'block (2 levels) in <top (required)>'\n\n" +
		"Finished in 0.41 seconds (files took 0.2 seconds to load)\n161 examples, 1 failure\n\nFailed examples:\n\n" +
		"rspec ./spec/cart_spec.rb:9 # Cart#total sums line items\n\nRandomized with seed 4242\n\n" +
		"rake aborted!\nCommand failed with status (1): [rspec]\n/w/Rakefile:3:in 'block in <top (required)>'\nTasks: TOP => spec\n(See full trace by running task with --trace)"

	return []runnerOutput{
		{"just test", 1, "go test ./...\n" + goFail + "\nerror: Recipe `test` failed on line 2 with exit code 1", "go-test",
			[]string{"FAIL\tgithub.com/spf13/cobra\t0.655s", "error: Recipe `test` failed on line 2 with exit code 1"}},
		{"just lint", 1, "npx eslint lib examples\n" + eslint + "\nerror: Recipe `lint` failed on line 5 with exit code 1", "eslint",
			[]string{"✖ 529 problems (416 errors, 113 warnings)", "error: Recipe `lint` failed on line 5 with exit code 1"}},
		{"task test", 1, "task: [test] pytest\n" + pytest + "\ntask: Failed to run task \"test\": exit status 1", "pytest",
			[]string{"task: Failed to run task \"test\": exit status 1"}},
		{"task lint test", 2, "task: [lint] npx tsc --noEmit\n" + tsc + "\ntask: Failed to run task \"lint\": exit status 2", "tsc",
			[]string{"task: Failed to run task \"lint\": exit status 2", "task: [lint] npx tsc --noEmit"}},
		{"mise run test", 1, "[test] $ npx vitest run\n" + vitest + "\n[test] ERROR task failed", "vitest",
			[]string{"[test] ERROR task failed", " Test Files  3 failed | 10 passed (13)"}},
		{"turbo run test", 1, turbo("test", vitest), "", []string{" ERROR  run failed: command  exited (1)", "Failed:    web#test"}},
		{"turbo run build test lint", 1, turbo("lint", eslint), "", []string{" ERROR  run failed: command  exited (1)"}},
		{"nx test app", 1, "\n> nx run app:test\n\n" + jest + nxFooter("test", true), "jest",
			[]string{" >  NX   Ran target test for project app (4s)", "Tests:       10 failed, 1212 passed, 1222 total"}},
		{"nx run app:test", 9, "\n> nx run app:test\n\n" + mocha + nxFooter("test", true), "mocha",
			[]string{" >  NX   Ran target test for project app (4s)", "  9 failing"}},
		{"nx run-many -t test lint", 1, "\n> nx run app:lint\n\n" + eslint + "\n\n> nx run app:test\n\n" + jest + nxFooter("test, lint", true), "jest",
			[]string{"✖ 529 problems (416 errors, 113 warnings)", "Tests:       10 failed, 1212 passed, 1222 total"}},
		{"npx nx test app", 1, "\n> nx run app:test\n\n" + jest + nxFooter("test", true), "jest",
			[]string{"Test Suites: 3 failed, 55 passed, 58 total"}},
		{"rake test", rakeGo.Meta.ExitCode, rakeGo.Raw, "go-test", []string{"rake aborted!", "Tasks: TOP => test"}},
		{"rake spec", 1, rspec, "", []string{"161 examples, 1 failure", "rake aborted!", "rspec ./spec/cart_spec.rb:9 # Cart#total sums line items"}},
		{"rake lint", rakeLint.Meta.ExitCode, rakeLint.Raw, "eslint", []string{"✖ 222 problems (222 errors, 0 warnings)", "rake aborted!"}},
		{"deno test", 1, denoMany.String() + denoTest, "", []string{"FAILED | 2 passed | 1 failed (41ms)", "error: Test failed"}},
		{"deno check main.ts", 1, strings.Repeat("Check file:///w/src/a.ts\n", 3) + strings.Repeat("error: TS2322 [ERROR]: Type 'string' is not assignable to type 'number'.\n  const x: number = \"a\";\n        ^\n    at file:///w/src/a.ts:3:7\n\n", 8) + "Found 8 errors.", "",
			[]string{"Found 8 errors."}},
		{"deno lint", 1, strings.Repeat("error[no-unused-vars]: `x` is never used\n --> /w/src/a.ts:3:7\n  |\n3 | const x = 1;\n  |       ^\n  = hint: If this is intentional, prefix it with an underscore like `_x`\n\n", 10) + "Found 10 problems\nChecked 12 files", "",
			[]string{"Found 10 problems"}},
		{"deno task test", 1, "Task test deno test -A\n" + denoMany.String() + denoTest, "", []string{"FAILED | 2 passed | 1 failed (41ms)"}},
		{"cargo nextest run", 100, nextest.String(), "", []string{"     Summary [   0.210s] 60 tests run: 59 passed, 1 failed, 0 skipped", "error: test run failed"}},
		{"composer test", composerPy.Meta.ExitCode, composerPy.Raw, "pytest",
			[]string{"=================== 8 failed, 143 passed, 1 skipped in 0.22s ===================", "Script pytest tests/test_options.py tests/test_types.py handling the test event returned with error code 1"}},
		{"composer run-script typecheck", composerTsc.Meta.ExitCode, composerTsc.Raw, "tsc",
			[]string{"Script npx tsc --noEmit handling the typecheck event returned with error code 2"}},
		{"npm run testonly", testonly.Meta.ExitCode, testonly.Raw, "vitest", []string{" Test Files  28 passed (28)", "      Tests  867 passed | 2 skipped (869)"}},
	}
}

func TestRunnerOutputIsHandled(t *testing.T) {
	outs := runnerOutputs(t)
	covered := map[string]bool{}
	for _, ro := range outs {
		t.Run(ro.cmd, func(t *testing.T) {
			covered[ro.cmd] = true
			argv := strings.Fields(ro.cmd)
			if !hook.Supported(argv) {
				t.Fatalf("%q is not rewritten", ro.cmd)
			}
			c := &engine.Context{Argv: argv, Exit: ro.exit, Cwd: "/home/user/src/app", Home: "/home/user"}
			if f, _ := engine.Resolve(c); f != nil {
				t.Fatalf("filter %s matches %q: move it out of runnerOnly", f.Name(), ro.cmd)
			}
			clean := textutil.Clean(ro.raw)
			_, shape := engine.GenericShape(c, clean)
			if want := engine.DetectedPrefix + ro.detected; ro.detected != "" && shape != want {
				t.Errorf("shape %q, want %q", shape, want)
			} else if ro.detected == "" && strings.HasPrefix(shape, engine.DetectedPrefix) {
				t.Errorf("shape %q: no detector should claim this output", shape)
			}
			res := engine.Process(c, ro.raw, engine.Options{})
			switch res.Filter {
			case "generic", "passthrough", "normalize", engine.DetectedPrefix + ro.detected:
			default:
				t.Errorf("filter %q", res.Filter)
			}
			if res.OutTokens > res.RawTokens {
				t.Errorf("view has more tokens than the output (%d > %d)", res.OutTokens, res.RawTokens)
			}
			for _, m := range engine.ErrorMessagesMissing(clean, res.Output) {
				if !knownBlindSpot(m) {
					t.Errorf("error message missing: %q", m)
				}
			}
			if ro.exit != 0 {
				if miss := engine.AppLocationsMissing(clean, res.Output); len(miss) > 0 {
					t.Errorf("locations missing: %q", miss)
				}
			}
			for _, k := range ro.keep {
				if !strings.Contains(clean, k) {
					t.Fatalf("test bug: %q is not in the output", k)
				}
				if !strings.Contains(res.Output, k) {
					t.Errorf("line not kept verbatim: %q", k)
				}
			}
		})
	}
	for cmd := range runnerOnly {
		if !covered[cmd] {
			t.Errorf("runnerOnly[%q] has no typical output in runnerOutputs", cmd)
		}
	}
}

var knownBlindSpots = map[string]bool{
	"Summary of all failing tests":          true,
	"assert not result.exception":           true,
	"error handling":                        true,
	"error-pages":                           true,
	"on failure":                            true,
	"when an error occurs":                  true,
	"when error occurs in response handler": true,
	"at error (node_modules/supertest/lib/test.js:335:15)": true,
}

func knownBlindSpot(m string) bool { return knownBlindSpots[m] }
