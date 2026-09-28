package python

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/textutil"
)

const ptHeader = "============================= test session starts ==============================\n" +
	"platform linux -- Python 3.12.1, pytest-8.3.4, pluggy-1.5.0\nrootdir: /home/user/src/demo\n"

func TestReviewWarningsSummaryTailKept(t *testing.T) {
	in := ptHeader + "collected 7 items\n\ntests/test_models.py .......                                             [100%]\n\n" +
		"=============================== warnings summary ===============================\n" +
		"tests/test_models.py::test_legacy_total[1]\ntests/test_models.py::test_legacy_total[2]\n" +
		"  /home/user/src/demo/tests/test_models.py:32: DeprecationWarning: legacy_total() is deprecated\n    assert stock.legacy_total() == 16\n\n" +
		"-- Docs: https://docs.pytest.org/en/stable/how-to/capture-warnings.html\n\n" +
		"---------- coverage: platform linux, python 3.12.1-final-0 -----------\n" +
		"Name                    Stmts   Miss  Cover\n-------------------------------------------\n" +
		"inventory/models.py        49      7    86%\n-------------------------------------------\nTOTAL                      97     47    52%\n\n" +
		"FAIL Required test coverage of 95% not reached. Total coverage: 51.55%\n" +
		"- generated xml file: /home/user/src/demo/junit.xml -\n" +
		"================= 7 passed, 2 warnings in 0.56s ================="
	out, added, ok := reducePytest(ctx(1, "pytest", "--cov"), in)
	if !ok || added != 0 {
		t.Fatalf("ok=%v added=%d\n%s", ok, added, out)
	}
	for _, want := range []string{
		"FAIL Required test coverage of 95% not reached. Total coverage: 51.55%",
		"---------- coverage: platform linux, python 3.12.1-final-0 -----------",
		"TOTAL                      97     47    52%",
		"inventory/models.py        49      7    86%",
		"- generated xml file: junit.xml -",
		"tests/test_models.py::test_legacy_total[1] (+1 more)",
		"exited 1 although",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "coverage: platform linux, python 3.12.1-final-0 ----------- (+") {
		t.Errorf("coverage header folded as a warning id:\n%s", out)
	}

	res := engine.Process(ctx(1, "pytest", "--cov"), in, engine.Options{})
	if res.GuardAdded != 0 || !strings.Contains(res.Output, "FAIL Required test coverage") {
		t.Errorf("pipeline: guard=%d\n%s", res.GuardAdded, res.Output)
	}
}

func TestReviewFailMarksKeptWhenNothingElseNamesFailures(t *testing.T) {
	in := ptHeader + "collected 30 items\n\n" +
		"tests/test_models.py ....F......FEsx                                     [ 50%]\n" +
		"tests/test_long.py ...................................................... [ 80%]\n" +
		"...F..                                                                   [ 90%]\n" +
		"tests/test_ok.py ...                                                     [100%]\n\n" +
		"=== 3 failed, 25 passed, 1 skipped, 1 xfailed, 1 error in 0.44s ==="
	out, _, ok := reducePytest(ctx(1, "pytest", "--tb=no", "-rN"), in)
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{"tests/test_models.py ....F......FEsx", "tests/test_long.py ......", "...F..", "[lx: 1 progress line hidden]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "tests/test_ok.py") {
		t.Errorf("passing file kept:\n%s", out)
	}

	with := strings.Replace(in, "=== 3 failed", "=========================== short test summary info ============================\n"+
		"FAILED tests/test_models.py::test_a\nFAILED tests/test_models.py::test_b\nFAILED tests/test_long.py::test_c\nERROR tests/test_models.py::test_d\n=== 3 failed", 1)
	out, _, _ = reducePytest(ctx(1, "pytest"), with)
	if strings.Contains(out, "....F......FEsx") || !strings.Contains(out, "[lx: 4 progress lines hidden]") {
		t.Errorf("progress kept although the summary names the failures:\n%s", out)
	}

	qq := "....F......FEsx.FF..FFF                                                  [100%]\n" +
		"=========================== short test summary info ============================\n" +
		"SKIPPED [1] tests/test_models.py:54: needs network"
	out, _, ok = reducePytest(ctx(1, "pytest", "-qq", "--tb=no", "-rs"), qq)
	if !ok || !strings.Contains(out, "....F......FEsx.FF..FFF") || !strings.HasSuffix(out, "[lx: pytest exited 1]") {
		t.Errorf("-qq run with failures reads as a pass:\n%s", out)
	}
	if out, _, _ := reducePytest(ctx(0, "pytest", "-qq", "-rs"), strings.Replace(qq, "F", ".", -1)); strings.Contains(out, "[lx:") && strings.Contains(out, "exited") {
		t.Errorf("exit note on a passing -qq run:\n%s", out)
	}
}

func TestReviewNestedSessionStaysCaptured(t *testing.T) {
	in := ptHeader + "collected 2 items\n\ntests/test_plugin.py .F                                                  [100%]\n\n" +
		"=================================== FAILURES ===================================\n" +
		"_________________________________ test_plugin __________________________________\n\n" +
		"    def test_plugin(pytester):\n>       assert result.ret == 0\nE       assert 1 == 0\n\ntests/test_plugin.py:9: AssertionError\n" +
		"----------------------------- Captured stdout call -----------------------------\n" +
		"============================= test session starts ==============================\n" +
		"platform linux -- Python 3.12.1, pytest-8.3.4, pluggy-1.5.0\ncollected 3 items\n\n" +
		"test_inner.py .F.                                                        [100%]\n\n" +
		"=================================== FAILURES ===================================\n" +
		"__________________________________ test_two ____________________________________\n" +
		"E       assert 1 == 2\n" +
		"========================= 1 failed, 2 passed in 0.01s ==========================\n" +
		"=========================== short test summary info ============================\n" +
		"FAILED tests/test_plugin.py::test_plugin - assert 1 == 0\n" +
		"========================= 1 failed, 1 passed in 0.20s =========================="
	out, added, ok := reducePytest(ctx(1, "pytest"), in)
	if !ok || added != 0 {
		t.Fatalf("ok=%v added=%d\n%s", ok, added, out)
	}

	nested := "----------------------------- Captured stdout call -----------------------------\n" +
		"============================= test session starts ==============================\n" +
		"platform linux -- Python 3.12.1, pytest-8.3.4, pluggy-1.5.0\ncollected 3 items\n\n" +
		"test_inner.py .F.                                                        [100%]\n\n" +
		"=================================== FAILURES ===================================\n" +
		"__________________________________ test_two ____________________________________\n" +
		"E       assert 1 == 2\n" +
		"========================= 1 failed, 2 passed in 0.01s =========================="
	if !strings.Contains(out, nested) {
		t.Errorf("nested report not kept as captured output:\n%s", out)
	}
	if !strings.HasSuffix(out, "FAILED tests/test_plugin.py::test_plugin - assert 1 == 0\n========================= 1 failed, 1 passed in 0.20s ==========================") {
		t.Errorf("outer summary changed:\n%s", out)
	}

	broken := strings.Replace(in, "========================= 1 failed, 2 passed in 0.01s ==========================\n", "", 1)
	out, _, _ = reducePytest(ctx(1, "pytest"), broken)
	if !strings.Contains(out, "FAILED tests/test_plugin.py::test_plugin - assert 1 == 0") {
		t.Errorf("outer short summary lost:\n%s", out)
	}
}

func TestReviewCountStyleVerbose(t *testing.T) {
	var b strings.Builder
	b.WriteString(ptHeader + "collecting ... collected 40 items\n\n")
	for i := 1; i <= 40; i++ {
		st := "PASSED"
		if i == 7 {
			st = "FAILED"
		}
		fmt.Fprintf(&b, "tests/test_c.py::test_%d %s%s[%2d/40]\n", i, st, strings.Repeat(" ", 20), i)
	}
	b.WriteString("=========================== short test summary info ============================\nFAILED tests/test_c.py::test_7 - assert 0\n")
	b.WriteString("========================= 1 failed, 39 passed in 0.50s =========================")
	out, _, ok := reducePytest(ctx(1, "pytest", "-v", "-o", "console_output_style=count"), b.String())
	if !ok || !strings.Contains(out, "[lx: 39 PASSED lines hidden]") || !strings.Contains(out, "tests/test_c.py::test_7 FAILED") {
		t.Errorf("count-style -v not condensed:\n%s", out)
	}
}

func TestReviewXdistStartLines(t *testing.T) {
	in := ptHeader + "created: 2/2 workers\n2 workers [3 items]\n\nscheduling tests via LoadScheduling\n\n" +
		"tests/test_a.py::test_a \ntests/test_a.py::test_fail_b \n[gw0] [ 33%] PASSED tests/test_a.py::test_a \n" +
		"tests/test_a.py::test_c \n[gw1] [ 66%] FAILED tests/test_a.py::test_fail_b \n[gw0] [100%] PASSED tests/test_a.py::test_c \n\n" +
		"========================= 1 failed, 2 passed in 1.00s =========================="
	out, added, ok := reducePytest(ctx(1, "pytest", "-n", "2", "-v"), textutil.Clean(in))
	if !ok || added != 0 {
		t.Fatalf("ok=%v added=%d\n%s", ok, added, out)
	}
	want := "2 workers [3 items]\n[gw1] [ 66%] FAILED tests/test_a.py::test_fail_b\n[lx: 2 PASSED lines hidden]\n[lx: 3 xdist test-start lines hidden]\n[lx: 1 progress line hidden]\n" +
		"========================= 1 failed, 2 passed in 1.00s =========================="
	if out != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}

	plain := ptHeader + "collected 1 item\n\ntests/test_a.py::test_a starting\nPASSED\n\n==== 1 passed in 0.01s ===="
	if out, _, _ := reducePytest(ctx(0, "pytest", "-v", "-s"), plain); !strings.Contains(out, "tests/test_a.py::test_a starting") {
		t.Errorf("-s output dropped:\n%s", out)
	}
}

func TestReviewCrashDump(t *testing.T) {
	var b strings.Builder
	b.WriteString(ptHeader + "collected 2 items\n\ncrash/test_crash.py .Fatal Python error: Segmentation fault\n\n")
	b.WriteString("Current thread 0x00000001f6149d80 (most recent call first):\n")
	b.WriteString("  File \"/usr/lib/python3.12/ctypes/__init__.py\", line 509 in string_at\n")
	b.WriteString("  File \"/home/user/src/demo/crash/test_crash.py\", line 9 in test_segfault\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "  File \"/home/user/venv/lib/python3.12/site-packages/pluggy/_callers.py\", line %d in _multicall\n", 100+i)
	}
	b.WriteString("  File \"/home/user/venv/bin/pytest\", line 8 in <module>")
	in := b.String()
	out, added, ok := reducePytest(ctx(139, "pytest"), in)
	if !ok || added != 0 {
		t.Fatalf("ok=%v added=%d\n%s", ok, added, out)
	}
	for _, want := range []string{"crash/test_crash.py .Fatal Python error: Segmentation fault", "Current thread 0x00000001f6149d80 (most recent call first):",
		`File "/usr/lib/python3.12/ctypes/__init__.py", line 509 in string_at`, `File "crash/test_crash.py", line 9 in test_segfault`,
		"… 30 library frames (pluggy)", "[lx: pytest exited 139]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	if _, _, ok := reducePytest(ctx(137, "pytest"), ptHeader+"collected 2 items\n\ncrash/test_crash.py ."); ok {
		t.Error("a run killed without a dump must bail")
	}
}

func TestReviewInternalErrorFolded(t *testing.T) {
	var b strings.Builder
	b.WriteString(ptHeader + "collected 2 items\n\nie/test_ie.py ..\nINTERNALERROR> Traceback (most recent call last):\n")
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "INTERNALERROR>   File \"/home/user/venv/lib/python3.12/site-packages/pluggy/_callers.py\", line %d, in _multicall\nINTERNALERROR>     raise exception\n", 100+i)
	}
	b.WriteString("INTERNALERROR>   File \"/home/user/src/demo/ie/conftest.py\", line 3, in pytest_runtest_logreport\n" +
		"INTERNALERROR>     raise RuntimeError(\"plugin bug in logreport\")\nINTERNALERROR> RuntimeError: plugin bug in logreport\n\n" +
		"============================== 2 passed in 0.02s ===============================")
	out, added, ok := reducePytest(ctx(3, "pytest"), b.String())
	if !ok || added != 0 {
		t.Fatalf("ok=%v added=%d\n%s", ok, added, out)
	}
	want := "INTERNALERROR> Traceback (most recent call last):\nINTERNALERROR>   … 20 library frames (pluggy)\n" +
		"INTERNALERROR>   File \"ie/conftest.py\", line 3, in pytest_runtest_logreport\n" +
		"INTERNALERROR>     raise RuntimeError(\"plugin bug in logreport\")\nINTERNALERROR> RuntimeError: plugin bug in logreport"
	if !strings.Contains(out, want) || !strings.Contains(out, "exited 3 although") {
		t.Errorf("got:\n%s", out)
	}
}

func TestReviewWarningCapKeepsErrors(t *testing.T) {
	var b strings.Builder
	b.WriteString("collected 40 items\n\ntests/test_w.py ........................................ [100%]\n\n")
	b.WriteString("=============================== warnings summary ===============================\n")
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&b, "tests/test_w.py::test_%d\n  /home/user/src/demo/tests/test_w.py:%d: UserWarning: distinct warning %d\n    warn()\n\n", i, i+1, i)
	}
	b.WriteString("tests/test_w.py::test_del\n  /home/user/venv/lib/python3.12/site-packages/_pytest/unraisableexception.py:85: PytestUnraisableExceptionWarning: Exception ignored in: <function Conn.__del__ at 0x7f00>\n" +
		"  \n  Traceback (most recent call last):\n    File \"/home/user/src/demo/conn.py\", line 10, in __del__\n      self.sock.close()\n  AttributeError: 'Conn' object has no attribute 'sock'\n  \n    warnings.warn(pytest.PytestUnraisableExceptionWarning(msg))\n\n")
	b.WriteString("-- Docs: https://docs.pytest.org/en/stable/how-to/capture-warnings.html\n")
	b.WriteString("======================= 40 passed, 26 warnings in 0.20s ========================")
	out, added, ok := reducePytest(ctx(0, "pytest"), b.String())
	if !ok || added != 0 {
		t.Fatalf("ok=%v added=%d\n%s", ok, added, out)
	}
	for _, want := range []string{"AttributeError: 'Conn' object has no attribute 'sock'", "PytestUnraisableExceptionWarning: Exception ignored in",
		"[lx: +5 more warning groups (5 test/location entries)]", "distinct warning 19"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "distinct warning 20") || strings.Contains(out, "error lines from the full output") {
		t.Errorf("cap not applied, or guard fired:\n%s", out)
	}
}

func TestReviewCapturedCriticalKept(t *testing.T) {
	var b strings.Builder
	b.WriteString("collected 1 item\n\ntests/test_l.py F                                                        [100%]\n\n" +
		"=================================== FAILURES ===================================\n" +
		"___________________________________ test_l _____________________________________\n\n" +
		"    def test_l():\n>       assert run() == 0\nE       assert 3 == 0\n\ntests/test_l.py:5: AssertionError\n" +
		"------------------------------ Captured log call -------------------------------\n")
	for i := 0; i < 60; i++ {
		if i == 30 {
			b.WriteString("CRITICAL app.db:db.py:88 replica lag above threshold; writes disabled\n")
			continue
		}
		fmt.Fprintf(&b, "INFO     app.worker:worker.py:%d step %s done\n", 10+i%7, strings.Repeat("x", i%5+1))
	}
	b.WriteString("=========================== short test summary info ============================\nFAILED tests/test_l.py::test_l - assert 3 == 0\n")
	b.WriteString("============================== 1 failed in 0.05s ===============================")
	out, _, ok := reducePytest(ctx(1, "pytest"), b.String())
	if !ok || !strings.Contains(out, "CRITICAL app.db:db.py:88 replica lag above threshold; writes disabled") || !strings.Contains(out, "lines hidden …") {
		t.Errorf("CRITICAL log line lost or log not capped:\n%s", out)
	}
}

func TestReviewWindowsOutput(t *testing.T) {
	in := strings.Join([]string{
		"============================= test session starts =============================",
		"platform win32 -- Python 3.12.1, pytest-8.3.4, pluggy-1.5.0",
		"rootdir: C:\\Users\\dev\\proj",
		"collected 3 items",
		"",
		"tests\\test_a.py .F.                                                    [100%]",
		"",
		"================================== FAILURES ===================================",
		"___________________________________ test_b ____________________________________",
		"",
		"    def test_b():",
		">       json.loads(\"{\")",
		"",
		"tests\\test_a.py:9: ",
		"_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _",
		"C:\\Python312\\Lib\\json\\__init__.py:346: in loads",
		"    return _default_decoder.decode(s)",
		"C:\\Python312\\Lib\\json\\decoder.py:337: in decode",
		"    obj, end = self.raw_decode(s, idx=_w(s, 0).end())",
		"_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _",
		"",
		"    def raw_decode(self, s, idx=0):",
		">           raise JSONDecodeError(\"Expecting property name enclosed in double quotes\", s, err.value) from None",
		"E           json.decoder.JSONDecodeError: Expecting property name enclosed in double quotes: line 1 column 2 (char 1)",
		"",
		"C:\\Python312\\Lib\\json\\decoder.py:353: JSONDecodeError",
		"=========================== short test summary info ===========================",
		"FAILED tests\\test_a.py::test_b - json.decoder.JSONDecodeError: Expecting property name enclosed in double quotes...",
		"========================= 1 failed, 2 passed in 0.12s =========================",
	}, "\r\n") + "\r\n"
	c := &engine.Context{Argv: []string{"C:\\venv\\Scripts\\pytest.exe"}, Exit: 1, Cwd: "C:\\Users\\dev\\proj"}
	c.Argv[0] = "pytest"
	res := engine.Process(c, in, engine.Options{})
	if res.Filter != "pytest" || res.GuardAdded != 0 {
		t.Fatalf("filter=%s guard=%d\n%s", res.Filter, res.GuardAdded, res.Output)
	}
	for _, want := range []string{"tests\\test_a.py:9:", "… 2 library frames (json)", "E           json.decoder.JSONDecodeError: Expecting property name",
		"C:\\Python312\\Lib\\json\\decoder.py:353: JSONDecodeError", "========================= 1 failed, 2 passed in 0.12s ========================="} {
		if !strings.Contains(res.Output, want) {
			t.Errorf("missing %q in:\n%s", want, res.Output)
		}
	}
	if strings.Contains(res.Output, "\r") {
		t.Error("carriage returns left in the output")
	}
}

func TestReviewIsLibPath(t *testing.T) {
	for p, want := range map[string]bool{
		"/usr/lib/python3.12/json/decoder.py":                          true,
		"/usr/lib64/python3.9/asyncio/base_events.py":                  true,
		"/opt/pypy/lib/pypy3.10/json/decoder.py":                       true,
		"/home/user/.pyenv/versions/3.13.0t/lib/python3.13t/json/x.py": true,
		"../../venv/lib/python3.9/site-packages/urllib3/connection.py": true,
		"/usr/lib/python3/dist-packages/yaml/__init__.py":              true,
		"C:\\Python312\\Lib\\json\\decoder.py":                         true,
		"<frozen importlib._bootstrap>":                                true,
		"/home/user/src/mono/lib/python/billing/invoice.py":            false,
		"/home/user/src/app/lib/python_utils/retry.py":                 false,
		"src/lib/pythonic.py":                                          false,
		"tests/test_models.py":                                         false,
	} {
		if got := isLibPath(p); got != want {
			t.Errorf("isLibPath(%q) = %v, want %v", p, got, want)
		}
	}
	in := "Traceback (most recent call last):\n" +
		"  File \"/home/user/src/mono/lib/python/billing/cli.py\", line 40, in <module>\n    main()\n" +
		"  File \"/home/user/src/mono/lib/python/billing/cli.py\", line 30, in main\n    run()\n" +
		"  File \"/home/user/src/mono/lib/python/billing/invoice.py\", line 12, in run\n    total(x)\n" +
		"  File \"/home/user/src/mono/lib/python/billing/invoice.py\", line 5, in total\n    raise ValueError(\"negative amount\")\n" +
		"ValueError: negative amount"
	out, _ := foldPyTracebacks(strings.Split(in, "\n"))
	if strings.Join(out, "\n") != in {
		t.Errorf("project frames folded:\n%s", strings.Join(out, "\n"))
	}
}

func TestReviewExceptionGroupFolded(t *testing.T) {
	var b strings.Builder
	b.WriteString("  + Exception Group Traceback (most recent call last):\n  |   File \"/home/user/src/demo/tg.py\", line 17, in <module>\n  |     asyncio.run(main())\n")
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&b, "  |   File \"/usr/lib/python3.13/asyncio/runners.py\", line %d, in run\n  |     return runner.run(main)\n  |            ~~~~~~~~~~^^^^^^\n", 100+i)
	}
	b.WriteString("  |   File \"/usr/lib/python3.13/asyncio/taskgroups.py\", line 173, in _aexit\n  |     raise BaseExceptionGroup(\n" +
		"  | ExceptionGroup: unhandled errors in a TaskGroup (1 sub-exception)\n  +-+---------------- 1 ----------------\n" +
		"    | Traceback (most recent call last):\n    |   File \"/home/user/src/demo/tg.py\", line 7, in fetch\n" +
		"    |     raise ConnectionError(f\"worker {n} lost connection\")\n    | ConnectionError: worker 1 lost connection\n    +------------------------------------")
	in := b.String()
	out, ok := apply(t, "python", ctx(1, "python", "tg.py"), in)
	if !ok {
		t.Fatal("bailed on an exception-group traceback")
	}
	for _, want := range []string{"  |   … 4 library frames (asyncio)", `  |   File "/usr/lib/python3.13/asyncio/taskgroups.py", line 173, in _aexit`,
		"  | ExceptionGroup: unhandled errors in a TaskGroup (1 sub-exception)", "    | ConnectionError: worker 1 lost connection",
		`    |   File "tg.py", line 7, in fetch`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if missing := fixture.ErrorLinesMissing(in, out); len(missing) != 0 {
		t.Errorf("error lines missing: %q", missing)
	}
}

func TestReviewFaulthandlerKeepsFirstFrame(t *testing.T) {
	in := "Fatal Python error: Segmentation fault\n\nCurrent thread 0x00007f (most recent call first):\n" +
		"  File \"/usr/lib/python3.12/ctypes/__init__.py\", line 509 in string_at\n" +
		"  File \"/usr/lib/python3.12/runpy.py\", line 88 in _run_code\n" +
		"  File \"/usr/lib/python3.12/runpy.py\", line 198 in _run_module_as_main\n" +
		"  File \"/usr/lib/python3.12/runpy.py\", line 300 in main"
	out, ok := apply(t, "python", ctx(139, "python", "-X", "faulthandler", "x.py"), in)
	if !ok {
		t.Fatal("bailed on a faulthandler dump")
	}
	if !strings.Contains(out, "line 509 in string_at") || !strings.Contains(out, "… 3 library frames (runpy)") || strings.Contains(out, "line 300 in main") {
		t.Errorf("got:\n%s", out)
	}
}

func TestReviewMypyLocationNotesKept(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&b, "ign.py:%d: error: Argument 1 to \"f\" has incompatible type \"str\"; expected \"int\"  [arg-type]\n", i)
		fmt.Fprintf(&b, "ign.py:%d: note: Error code \"arg-type\" not covered by \"type: ignore[attr-defined]\" comment\n", i)
		fmt.Fprintf(&b, "ign.py:%d: note: See https://mypy.rtfd.io/en/stable/_refs.html#code-arg-type for more info\n", i)
	}
	b.WriteString("Found 30 errors in 1 file (checked 1 source file)")
	in := b.String()
	out, ok := apply(t, "mypy", ctx(1, "mypy", "ign.py"), in)
	if !ok || strings.Count(out, "not covered by") != 30 || strings.Count(out, "See https://") != 1 || !strings.Contains(out, "[lx: 29 repeated notes hidden]") {
		t.Errorf("got:\n%s", out)
	}
	res := engine.Process(ctx(1, "mypy", "ign.py"), in, engine.Options{})
	if res.GuardAdded != 0 || res.Filter != "mypy" {
		t.Errorf("filter=%s guard=%d", res.Filter, res.GuardAdded)
	}
}

func TestReviewPipErrorWordPackageNotHidden(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&b, "Collecting dep%d\n  Downloading dep%d-1.0-py3-none-any.whl (10 kB)\n", i, i)
	}
	b.WriteString("Collecting pytest-error-for-skips\n  Downloading pytest_error_for_skips-2.0.2-py2.py3-none-any.whl (4.2 kB)\n")
	b.WriteString("Installing collected packages: pytest-error-for-skips\nSuccessfully installed pytest-error-for-skips-2.0.2")
	in := b.String()
	res := engine.Process(ctx(0, "pip", "install", "-r", "r.txt"), in, engine.Options{})
	if res.Filter != "pip-install" || res.GuardAdded != 0 || !strings.Contains(res.Output, "Collecting pytest-error-for-skips") ||
		!strings.Contains(res.Output, "hid 20 Collecting, 21 Downloading lines") {
		t.Errorf("filter=%s guard=%d\n%s", res.Filter, res.GuardAdded, res.Output)
	}
	un := "Found existing installation: pytest-error-for-skips 2.0.2\nUninstalling pytest-error-for-skips-2.0.2:\n  Successfully uninstalled pytest-error-for-skips-2.0.2"
	if out, _ := apply(t, "pip-uninstall", ctx(0, "pip", "uninstall", "-y", "pytest-error-for-skips"), un); !strings.Contains(out, "Found existing installation: pytest-error-for-skips 2.0.2") {
		t.Errorf("error-class line hidden:\n%s", out)
	}
}

func TestReviewFileURLNotRelativized(t *testing.T) {
	in := "Processing /home/user/src/badpkg\nWARNING: Discarding file:///home/user/src/badpkg. Command errored out with exit status 1: python setup.py egg_info Check the logs for full command output.\n" +
		"ERROR: Command errored out with exit status 1: python setup.py egg_info Check the logs for full command output."
	out, _ := apply(t, "pip-install", ctx(1, "pip", "install", "../badpkg"), in)
	if !strings.Contains(out, "file:///home/user/src/badpkg") || strings.Contains(out, "file://~") {
		t.Errorf("file URL rewritten:\n%s", out)
	}
}

func TestReviewRuffExitNote(t *testing.T) {
	out, ok := apply(t, "ruff", ctx(1, "ruff", "check", "--fix", "--exit-non-zero-on-fix"), "All checks passed!")
	if !ok || out != "All checks passed!\n[lx: ruff exited 1]" {
		t.Errorf("got %q", out)
	}
	if out, _ := apply(t, "ruff", ctx(0, "ruff", "check"), "All checks passed!"); out != "All checks passed!" {
		t.Errorf("got %q", out)
	}
}

func TestReviewHugeShapes(t *testing.T) {
	var b strings.Builder
	b.WriteString(ptHeader + "created: 8/8 workers\n8 workers [25000 items]\n\n")
	for i := 0; i < 25000; i++ {
		fmt.Fprintf(&b, "tests/test_m%d.py::test_%d \n[gw%d] [%3d%%] PASSED tests/test_m%d.py::test_%d \n", i/100, i, i%8, i*100/25000, i/100, i)
	}
	b.WriteString("============================ 25000 passed in 60.00s ============================")
	start := time.Now()
	out, ok := apply(t, "pytest", ctx(0, "pytest", "-n", "8", "-v"), b.String())
	if el := time.Since(start); el > 2*time.Second*raceSlowdown || !ok || !strings.Contains(out, "[lx: 25000 xdist test-start lines hidden]") {
		t.Fatalf("ok=%v in %v:\n%.500s", ok, el, out)
	}

	b.Reset()
	b.WriteString("collected 1 item\n=== FAILURES ===\n____ t ____\n---- Captured stdout call ----\n")
	for i := 0; i < 25000; i++ {
		b.WriteString("=== test session starts ===\nx\n")
	}
	b.WriteString("=== 1 failed in 1.00s ===")
	start = time.Now()
	out, ok = apply(t, "pytest", ctx(1, "pytest"), b.String())
	if el := time.Since(start); el > 2*time.Second*raceSlowdown || !ok || !strings.HasSuffix(out, "=== 1 failed in 1.00s ===") {
		t.Fatalf("ok=%v in %v", ok, el)
	}
	b.Reset()
	b.WriteString("  + Exception Group Traceback (most recent call last):\n")
	for i := 0; i < 50000; i++ {
		fmt.Fprintf(&b, "  |   File \"/usr/lib/python3.13/asyncio/x%d.py\", line %d, in f\n", i%10, i)
	}
	b.WriteString("  | ExceptionGroup: boom (1 sub-exception)")
	start = time.Now()
	out, ok = apply(t, "python", ctx(1, "python", "x.py"), b.String())
	if el := time.Since(start); el > 2*time.Second*raceSlowdown || !ok || !strings.Contains(out, "… 49999 library frames (asyncio)") {
		t.Fatalf("ok=%v in %v:\n%.500s", ok, el, out)
	}
}

func TestReviewWrappers(t *testing.T) {
	cases := []struct {
		want string
		argv []string
	}{
		{"pytest", []string{"env", "PYTHONPATH=src", "pytest", "-x"}},
		{"pytest", []string{"env", "-u", "CI", "-i", "PATH=/usr/bin", "python3", "-m", "pytest"}},
		{"pytest", []string{"timeout", "600", "pytest"}},
		{"pytest", []string{"timeout", "-s", "KILL", "--preserve-status", "10m", "python", "-m", "pytest"}},
		{"pytest", []string{"nice", "-n", "5", "pytest"}},
		{"pytest", []string{"nohup", "pytest", "-q"}},
		{"pytest", []string{"coverage", "run", "-m", "pytest", "-x"}},
		{"pytest", []string{"python", "-m", "coverage", "run", "--source", "src", "-m", "pytest"}},
		{"pytest", []string{"uv", "run", "coverage", "run", "--branch", "-m", "pytest"}},
		{"pytest", []string{"bash", "-c", "PYTHONPATH=src pytest -q"}},
		{"pytest", []string{"pipx", "run", "pytest"}},
		{"mypy", []string{"timeout", "60", "env", "MYPY_CACHE_DIR=/tmp/m", "mypy", "src"}},
		{"pip-install", []string{"env", "PIP_INDEX_URL=https://x", "pip", "install", "x"}},
		{"python", []string{"coverage", "run", "scripts/load.py"}},
		{"", []string{"coverage", "report"}},
		{"", []string{"python", "-m", "coverage", "html"}},
		{"", []string{"env"}},
		{"", []string{"timeout", "60"}},
		{"", []string{"env", "-S", "pytest -x"}},
	}
	for _, tc := range cases {
		if got := find(tc.argv...); got != tc.want {
			t.Errorf("%q: filter %q, want %q", tc.argv, got, tc.want)
		}
	}
}

func TestReviewStream(t *testing.T) {
	cases := []struct {
		stream bool
		argv   []string
	}{
		{true, []string{"pytest", "-f"}},
		{true, []string{"python", "-m", "pytest", "--looponfail", "tests"}},
		{false, []string{"pytest", "-x"}},
		{true, []string{"python", "-i", "script.py"}},
		{true, []string{"python", "-m", "http.server", "8000"}},
		{true, []string{"python3", "-m", "uvicorn", "app:app", "--reload"}},
		{true, []string{"python", "manage.py", "runserver"}},
		{true, []string{"uv", "run", "python", "-m", "flask", "run"}},
		{true, []string{"python", "app.py", "--reload"}},
		{false, []string{"python", "manage.py", "test"}},
		{false, []string{"python", "-c", "import this"}},
		{false, []string{"python", "-m", "unittest"}},
		{false, []string{"python", "scripts/load.py", "bad.json"}},
	}
	for _, tc := range cases {
		c := ctx(0, tc.argv...)
		f := engine.Find(c)
		if f == nil {
			t.Errorf("%q: no filter", tc.argv)
			continue
		}
		s, ok := f.(engine.Streamer)
		if got := ok && s.Stream(c); got != tc.stream {
			t.Errorf("%q (%s): Stream = %v, want %v", tc.argv, f.Name(), got, tc.stream)
		}
	}
}

func TestReviewMypyCrashAfterHiddenNote(t *testing.T) {
	in := `src/a.py:3: error: Library stubs not installed for "requests"  [import-untyped]
src/a.py:3: note: Hint: "python3 -m pip install types-requests"
src/b.py:3: error: Library stubs not installed for "requests"  [import-untyped]
src/b.py:3: note: Hint: "python3 -m pip install types-requests"
Traceback (most recent call last):
  File "/home/user/venv/lib/python3.12/site-packages/mypy/checker.py", line 100, in check
    self.visit(node)
AssertionError: Cannot find component 'x' for 'y.x'
src/c.py:9: error: INTERNAL ERROR -- Please try using mypy master on GitHub:
https://mypy.readthedocs.io/en/stable/common_issues.html#using-a-development-mypy-build`
	for _, argv := range [][]string{{"mypy", "src"}, {"mypy", "--pretty", "src"}} {
		out, ok := apply(t, "mypy", ctx(2, argv...), in)
		for _, want := range []string{"Traceback (most recent call last):", `  File "/home/user/venv/lib/python3.12/site-packages/mypy/checker.py", line 100, in check`,
			"AssertionError: Cannot find component 'x' for 'y.x'", "src/c.py:9: error: INTERNAL ERROR"} {
			if !ok || !strings.Contains(out, want) {
				t.Errorf("%v: missing %q in:\n%s", argv, want, out)
			}
		}
	}

	pretty := "a.py:1: error: Library stubs not installed for \"requests\"\n[import-untyped]\n    import requests\n    ^\n" +
		"a.py:1: note: (or run \"mypy --install-types\" to install all missing\nstub packages)\n" +
		"b.py:1: error: Library stubs not installed for \"requests\"\n[import-untyped]\n    import requests\n    ^\n" +
		"b.py:1: note: (or run \"mypy --install-types\" to install all missing\nstub packages)\n" +
		"Found 2 errors in 2 files (checked 2 source files)"
	out, _ := apply(t, "mypy", ctx(1, "mypy", "--pretty", "."), pretty)
	if strings.Count(out, "stub packages)") != 1 || !strings.Contains(out, "[lx: 1 repeated note hidden]") {
		t.Errorf("pretty continuation:\n%s", out)
	}
}

func TestReviewRuffSecondarySpans(t *testing.T) {
	in := "F811 Redefinition of unused `f` from line 6\n  --> multi.py:19:5\n   |\n19 | def f():\n   |     ^ `f` redefined here\n20 |     return 2\n   |\n" +
		"  ::: lib/other.py:6:5\n   |\n 6 | def f(x: List[int] = []):\n   |     - previous definition of `f` here\n 7 |     try:\n   |\nhelp: Remove definition: `f`\n\n"
	var b strings.Builder
	for i := 0; i < 11; i++ {
		b.WriteString(in)
	}
	b.WriteString("Found 11 errors.")
	out, ok := apply(t, "ruff", ctx(1, "ruff", "check"), b.String())
	if !ok {
		t.Fatal("bailed")
	}
	if strings.Count(out, "  ::: lib/other.py:6:5") != 11 || strings.Count(out, "- previous definition of `f` here") != 10 ||
		strings.Count(out, " 6 | def f(x: List[int] = []):") != 10 || strings.Contains(out, "7 |     try:") {
		t.Errorf("secondary spans:\n%s", out)
	}
}

func TestReviewSubtestFailCount(t *testing.T) {
	in := "collected 1 item\n\nt.py ,u,                                                      [100%]\n\n" +
		"========================= 1 passed, 1 subtests failed, 2 subtests passed in 0.10s ========================="
	out, _, ok := reducePytest(ctx(1, "pytest"), in)
	if !ok || strings.Contains(out, "although") {
		t.Errorf("subtest failure read as a pass:\n%s", out)
	}
}
