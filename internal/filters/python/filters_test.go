package python

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tokens"
)

func ctx(exit int, argv ...string) *engine.Context {
	return &engine.Context{Argv: argv, Exit: exit, Cwd: "/home/user/src/demo", Home: "/home/user"}
}

func find(argv ...string) string {
	if f := engine.Find(ctx(0, argv...)); f != nil {
		return f.Name()
	}
	return ""
}

func TestMatch(t *testing.T) {
	cases := []struct {
		want string
		argv []string
	}{
		{"pytest", []string{"pytest"}},
		{"pytest", []string{"/home/user/venv/bin/pytest", "-x", "tests"}},
		{"pytest", []string{"py.test", "-q"}},
		{"pytest", []string{"python", "-m", "pytest", "-x"}},
		{"pytest", []string{"python3", "-m", "pytest"}},
		{"pytest", []string{"python3.12", "-u", "-m", "pytest", "tests/"}},
		{"pytest", []string{"/usr/bin/python3", "-X", "dev", "-m", "pytest"}},
		{"pytest", []string{"python", "-Bm", "pytest"}},
		{"pytest", []string{"python", "-mpytest"}},
		{"pytest", []string{"uv", "run", "pytest", "-k", "x"}},
		{"pytest", []string{"uv", "run", "--with", "pytest-xdist", "--frozen", "pytest", "-n", "4"}},
		{"pytest", []string{"uv", "run", "python", "-m", "pytest"}},
		{"pytest", []string{"poetry", "run", "pytest"}},
		{"pytest", []string{"pdm", "run", "pytest"}},
		{"pytest", []string{"pipenv", "run", "python", "-m", "pytest"}},
		{"pytest", []string{"uvx", "pytest"}},
		{"pytest", []string{"bash", "-c", "pytest -x tests 2>&1"}},
		{"pytest", []string{"sh", "-c", "python -m pytest -k 'not slow'"}},
		{"", []string{"pytest", "--collect-only"}},
		{"", []string{"pytest", "--co", "-q"}},
		{"", []string{"pytest", "--version"}},
		{"", []string{"pytest", "--fixtures"}},
		{"", []string{"bash", "-c", "cd tests && pytest"}},
		{"", []string{"bash", "-c", "pytest $ARGS"}},
		{"", []string{"poetry", "install"}},

		{"pip-install", []string{"pip", "install", "requests"}},
		{"pip-install", []string{"pip3", "install", "-r", "requirements.txt"}},
		{"pip-install", []string{"pip3.11", "install", "-e", "."}},
		{"pip-install", []string{"python", "-m", "pip", "install", "-U", "pip"}},
		{"pip-install", []string{"python3", "-u", "-m", "pip", "install", "x"}},
		{"pip-install", []string{"pip", "--proxy", "http://p:3128", "install", "x"}},
		{"pip-install", []string{"/home/user/venv/bin/pip", "--disable-pip-version-check", "install", "x"}},
		{"", []string{"pip", "install", "--report", "-", "x"}},
		{"pip-uninstall", []string{"pip", "uninstall", "-y", "flask"}},
		{"pip-list", []string{"pip", "list"}},
		{"pip-list", []string{"pip", "list", "--outdated"}},
		{"", []string{"pip", "list", "--format=json"}},
		{"", []string{"pip", "list", "--format", "freeze"}},
		{"pip-show", []string{"pip", "show", "-f", "requests"}},
		{"", []string{"pip", "freeze"}},
		{"", []string{"pip", "--version"}},
		{"", []string{"pipx", "install", "x"}},

		{"mypy", []string{"mypy", "src"}},
		{"mypy", []string{"python", "-m", "mypy", "--strict", "."}},
		{"mypy", []string{"uv", "run", "mypy", "."}},
		{"", []string{"mypy", "-O", "json", "."}},
		{"", []string{"mypy", "--output=json", "."}},
		{"", []string{"mypy", "--version"}},

		{"ruff", []string{"ruff", "check", "."}},
		{"ruff", []string{"ruff", "check", "--output-format", "concise", "src"}},
		{"ruff", []string{"ruff", "--config", "ruff.toml", "check"}},
		{"ruff", []string{"python", "-m", "ruff", "check"}},
		{"ruff", []string{"uvx", "ruff", "check", "."}},
		{"", []string{"ruff", "check", "--output-format=json", "."}},
		{"", []string{"ruff", "check", "--output-format", "github"}},
		{"", []string{"ruff", "check", "--watch"}},
		{"", []string{"ruff", "check", "--diff"}},
		{"", []string{"ruff", "check", "--statistics"}},
		{"", []string{"ruff", "format", "."}},
		{"", []string{"ruff"}},

		{"python", []string{"python", "script.py"}},
		{"python", []string{"python3", "-u", "scripts/load.py", "x.json"}},
		{"python", []string{"python", "-c", "import x"}},
		{"python", []string{"python", "-m", "unittest"}},
		{"python", []string{"python", "-W", "error", "manage.py", "test"}},
		{"python", []string{"uv", "run", "scripts/fetch.py"}},
		{"python", []string{"bash", "-c", "python -c \"import requests; requests.get('http://x')\""}},
		{"", []string{"python"}},
		{"", []string{"python", "-u"}},
		{"", []string{"python", "-m"}},
		{"", []string{"node", "x.py"}},
		{"", []string{"pythonista", "x.py"}},
		{"", []string{}},
	}
	for _, tc := range cases {
		if got := find(tc.argv...); got != tc.want {
			t.Errorf("%q: filter %q, want %q", tc.argv, got, tc.want)
		}
	}
}

func apply(t *testing.T, name string, c *engine.Context, in string) (string, bool) {
	t.Helper()
	for _, f := range engine.Filters() {
		if f.Name() == name {
			return f.Apply(c, in)
		}
	}
	t.Fatalf("no filter %q", name)
	return "", false
}

var allFilters = []string{"pytest", "pip-install", "pip-uninstall", "pip-list", "pip-show", "mypy", "ruff", "python"}

func TestBailsOnUnknown(t *testing.T) {
	inputs := map[string]string{
		"empty":     "",
		"blank":     "\n\n",
		"localized": "Aucun test n'a été trouvé.\nErreur : le fichier est introuvable",
		"unrelated": "hello world\nthis is not tool output\n42",
		"json":      `{"summary": {"passed": 3}}`,
	}
	for name, in := range inputs {
		for _, f := range allFilters {
			if out, ok := apply(t, f, ctx(1, "x"), in); ok {
				t.Errorf("%s on %s: ok=true, want bail; got %q", f, name, out)
			}
		}
	}
}

func TestPytestSummaryVerbatim(t *testing.T) {
	for _, line := range []string{
		"=================== 3 passed, 1 error in 0.42s ===================",
		"==== 1 failed, 10 errors, 2 warnings in 12.01s (0:00:12) ====",
		"3 passed, 1 error in 0.42s",
		"==================== 2 passed, 1 xpassed, 1 rerun in 0.10s ====================",
		"============================ no tests ran in 0.01s =============================",
	} {
		in := "============================= test session starts ==============================\n" +
			"platform linux -- Python 3.12.1, pytest-8.3.4, pluggy-1.5.0\nrootdir: /home/user/src/demo\ncollected 4 items\n\n" +
			"tests/test_a.py ..E.                                                     [100%]\n\n" + line
		out, ok := apply(t, "pytest", ctx(1, "pytest"), in)
		if !ok || !strings.Contains(out, line) {
			t.Errorf("summary %q not verbatim in:\n%s", line, out)
		}
		if strings.Contains(out, "platform linux") || strings.Contains(out, "rootdir") {
			t.Errorf("session header kept:\n%s", out)
		}
	}
}

func TestPytestExitConsistency(t *testing.T) {
	pass := "collected 5 items\n\ntests/test_a.py .....                                   [100%]\n\n" +
		"FAIL Required test coverage of 90% not reached. Total coverage: 85.00%\n" +
		"============================== 5 passed in 0.10s ==============================="
	out, ok := apply(t, "pytest", ctx(1, "pytest", "--cov"), pass)
	if !ok || !strings.Contains(out, "exited 1 although") || !strings.Contains(out, "FAIL Required test coverage") {
		t.Errorf("pass-looking result with exit 1 not flagged:\n%s", out)
	}

	xf := "collected 2 items\n\n======================== 1 passed, 1 xfailed in 0.10s ========================"
	if out, _ := apply(t, "pytest", ctx(1, "pytest"), xf); !strings.Contains(out, "exited 1 although") {
		t.Errorf("xfailed-only run with exit 1 not flagged:\n%s", out)
	}
	errs := "collected 20 items\n\n======================== 10 passed, 10 errors in 0.10s ========================"
	if out, _ := apply(t, "pytest", ctx(1, "pytest"), errs); strings.Contains(out, "although") {
		t.Errorf("run with 10 errors flagged as pass-looking:\n%s", out)
	}
	if out, _ := apply(t, "pytest", ctx(0, "pytest"), errs); strings.Contains(out, "[lx:") && strings.Contains(out, "exit") {
		t.Errorf("unexpected exit note:\n%s", out)
	}
	five := "collected 3 items / 3 deselected / 0 selected\n\n======================= 3 deselected in 0.01s ========================"
	if out, _ := apply(t, "pytest", ctx(5, "pytest", "-k", "nope"), five); !strings.Contains(out, "exit 5") || !strings.Contains(out, "3 deselected / 0 selected") {
		t.Errorf("exit 5 not explained:\n%s", out)
	}
}

func TestPytestBailsWithoutResult(t *testing.T) {
	crash := "============================= test session starts ==============================\n" +
		"platform linux -- Python 3.12.1, pytest-8.3.4, pluggy-1.5.0\ncollected 400 items\n\n" +
		"tests/test_a.py ........................................................ [ 14%]\n" +
		"tests/test_b.py ........F..........\nKilled\n"
	if _, ok := apply(t, "pytest", ctx(137, "pytest"), crash); ok {
		t.Error("a killed run must go to the generic reducer")
	}
	usage := "ERROR: usage: pytest [options] [file_or_dir] [file_or_dir] [...]\npytest: error: unrecognized arguments: --frobnicate\n  inifile: None\n  rootdir: /home/user/src/demo\n"
	if _, ok := apply(t, "pytest", ctx(4, "pytest", "--frobnicate"), usage); ok {
		t.Error("usage error must go to the generic reducer")
	}
}

func TestPytestQuietQQ(t *testing.T) {
	in := "..F.                                                                     [100%]\n" +
		"=================================== FAILURES ===================================\n" +
		"___________________________________ test_c ___________________________________\n\n" +
		"    def test_c():\n>       assert 1 == 2\nE       assert 1 == 2\n\ntests/test_a.py:9: AssertionError\n" +
		"=========================== short test summary info ============================\n" +
		"FAILED tests/test_a.py::test_c - assert 1 == 2"
	out, ok := apply(t, "pytest", ctx(1, "pytest", "-qq"), in)
	if !ok {
		t.Fatal("bailed on -qq")
	}
	for _, want := range []string{"E       assert 1 == 2", "tests/test_a.py:9: AssertionError", "FAILED tests/test_a.py::test_c - assert 1 == 2", ">       assert 1 == 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

const pytestShapes = `============================= test session starts ==============================
platform linux -- Python 3.12.1, pytest-8.3.4, pluggy-1.5.0
rootdir: /home/user/src/demo
plugins: xdist-3.6.1, cov-5.0.0
collected 7 items

tests/test_x.py .FEXF.x                                                  [100%]

==================================== ERRORS ====================================
_________________________ ERROR at setup of test_needs _________________________
file /home/user/src/demo/tests/test_x.py, line 12
  def test_needs(missing_fixture):
E       fixture 'missing_fixture' not found
>       available fixtures: cache, capfd, capsys, monkeypatch, tmp_path
>       use 'pytest --fixtures [testpath]' for help on them.

/home/user/src/demo/tests/test_x.py:12
=================================== FAILURES ===================================
__________________________________ test_fail ___________________________________
[gw0] linux -- Python 3.12.1 /home/user/venv/bin/python

    def test_fail():
        # an unrelated comment mentioning an error
        data = load()
        if not data:
            raise RuntimeError("cannot load data: timed out")
        x = compute(data)
>       assert x == 3
E       assert 2 == 3

tests/test_x.py:20: AssertionError
----------------------------- Captured stderr call -----------------------------
line 1
line 2
line 3
line 4
line 5
line 6
line 7
line 8
line 9
ERROR deep in the captured output: disk full at /var/tmp
line 11
line 12
line 13
line 14
line 15
line 16
line 17
line 18
line 19
line 20
line 21
line 22
line 23
line 24
_______________________________ test_strict_xpass ______________________________
[XPASS(strict)] should fail
=============================== warnings summary ===============================
tests/test_x.py::test_a
  /home/user/src/demo/tests/test_x.py:3: DeprecationWarning: old api
    old()

tests/test_y.py::test_b
  /home/user/src/demo/tests/test_y.py:8: DeprecationWarning: old api
    old()

tests/test_z.py::test_c
  /home/user/src/demo/tests/test_z.py:4: UserWarning: something else
    warn()

-- Docs: https://docs.pytest.org/en/stable/how-to/capture-warnings.html
=========================== short test summary info ============================
FAILED tests/test_x.py::test_fail - assert 2 == 3
FAILED tests/test_x.py::test_strict_xpass
ERROR tests/test_x.py::test_needs
== 2 failed, 3 passed, 1 xfailed, 3 warnings, 1 error in 0.31s ==`

func TestPytestShapes(t *testing.T) {
	c := ctx(1, "pytest")
	out, added, ok := reducePytest(c, pytestShapes)
	if !ok {
		t.Fatal("bailed")
	}
	if added != 0 {
		t.Errorf("own guard re-added %d lines:\n%s", added, out)
	}
	for _, want := range []string{
		"collected 7 items",
		"E       fixture 'missing_fixture' not found",
		">       available fixtures: cache, capfd, capsys, monkeypatch, tmp_path",
		"file tests/test_x.py, line 12",
		"\ntests/test_x.py:12\n",
		">       assert x == 3",
		"E       assert 2 == 3",
		"tests/test_x.py:20: AssertionError",
		"ERROR deep in the captured output: disk full at /var/tmp",
		"… 12 similar lines …",
		"[XPASS(strict)] should fail",
		"tests/test_x.py:3: DeprecationWarning: old api",
		"[lx: same warning also at tests/test_y.py:8]",
		"tests/test_z.py:4: UserWarning: something else",
		"== 2 failed, 3 passed, 1 xfailed, 3 warnings, 1 error in 0.31s ==",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, gone := range []string{"platform linux", "plugins:", "[gw0] linux", "line 15\n", "-- Docs:", "    old()", "an unrelated comment"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q should be folded:\n%s", gone, out)
		}
	}
	if got := fixture.ErrorLinesMissing(pytestShapes, out); len(got) != 1 || got[0] != "# an unrelated comment mentioning an error" {
		t.Errorf("unexpected missing error lines %q", got)
	}
}

func TestPytestLibraryFramesFolded(t *testing.T) {
	in := `collected 1 item

tests/test_api.py F                                                      [100%]

=================================== FAILURES ===================================
__________________________________ test_call ___________________________________

    def test_call():
>       api.get("/x")

tests/test_api.py:5:
_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _

self = <Client>, error = TimeoutError('connection failed')

    def get(self, path):
        if error:
            raise error
>       return self._send(path)

../venv/lib/python3.12/site-packages/httpx/_client.py:100:
_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _
../venv/lib/python3.12/site-packages/httpx/_client.py:200: in _send
    return self._transport.handle(path)  # fails when unreachable
../venv/lib/python3.12/site-packages/httpx/_transport.py:50: in handle
    raise_on_error()
_ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _ _

    def raise_on_error():
>       raise ConnectError("Connection refused")
E       httpx.ConnectError: Connection refused

../venv/lib/python3.12/site-packages/httpx/_transport.py:80: ConnectError
=========================== short test summary info ============================
FAILED tests/test_api.py::test_call - httpx.ConnectError: Connection refused
============================== 1 failed in 0.05s ===============================`
	out, added, ok := reducePytest(ctx(1, "pytest"), in)
	if !ok || added != 0 {
		t.Fatalf("ok=%v added=%d\n%s", ok, added, out)
	}
	for _, want := range []string{">       api.get(\"/x\")", "tests/test_api.py:5:", "… 3 library frames (httpx)",
		">       raise ConnectError(\"Connection refused\")", "E       httpx.ConnectError: Connection refused",
		"../venv/lib/python3.12/site-packages/httpx/_transport.py:80: ConnectError"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "self._send") || strings.Contains(out, "TimeoutError") {
		t.Errorf("library frame internals kept:\n%s", out)
	}
}

func TestPytestHuge(t *testing.T) {
	var b strings.Builder
	b.WriteString("============================= test session starts ==============================\ncollecting ... collected 50000 items\n\n")
	for i := 0; i < 50000; i++ {
		status := "PASSED"
		if i%10000 == 7 {
			status = "FAILED"
		}
		fmt.Fprintf(&b, "tests/test_m%d.py::test_case[%d] %s [%3d%%]\n", i/1000, i, status, i*100/50000)
	}
	b.WriteString("=================================== FAILURES ===================================\n")
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&b, "________________ test_case[%d] ________________\n\n    def test_case(n):\n>       assert n != %d\nE       assert %d != %d\n\ntests/test_m%d.py:4: AssertionError\n", i*10000+7, i*10000+7, i*10000+7, i*10000+7, i*10)
	}
	b.WriteString("====== 5 failed, 49995 passed in 99.00s (0:01:39) ======\n")
	in := b.String()
	start := time.Now()
	out, ok := apply(t, "pytest", ctx(1, "pytest", "-v"), in)

	if el := time.Since(start); el > 1500*time.Millisecond*raceSlowdown {
		t.Errorf("took %v", el)
	}
	if !ok || !strings.Contains(out, "[lx: 49995 PASSED lines hidden]") || strings.Count(out, "FAILED") != 5 {
		t.Fatalf("unexpected:\n%.2000s", out)
	}
	if n := tokens.Count(out); n > 2000 {
		t.Errorf("output %d tokens", n)
	}
}

func TestPipInstallShapes(t *testing.T) {
	in := `Looking in indexes: https://pypi.org/simple, https://pkgs.example.com/simple
DEPRECATION: Legacy editable install of demo==0.1 (from -e .) (setup.py develop) is deprecated. pip 25.3 will enforce this behaviour change.
Collecting requests
  Downloading requests-2.32.5-py3-none-any.whl (64 kB)
     ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 64.7/64.7 kB 2.1 MB/s eta 0:00:00
WARNING: Retrying (Retry(total=4, connect=None, read=None, redirect=None, status=None)) after connection broken by 'ReadTimeoutError("HTTPSConnectionPool(host='pypi.org', port=443): Read timed out. (read timeout=15)")': /simple/idna/
Requirement already satisfied: idna<4,>=2.5 in /home/user/venv/lib/python3.12/site-packages (from requests) (3.7)
DEPRECATION: Legacy editable install of demo==0.1 (from -e .) (setup.py develop) is deprecated. pip 25.3 will enforce this behaviour change.
Installing collected packages: requests
Successfully installed requests-2.32.5

[notice] A new release of pip is available: 24.0 -> 26.0.1
[notice] To update, run: pip install --upgrade pip`
	out, ok := apply(t, "pip-install", ctx(0, "pip", "install", "requests"), in)
	if !ok {
		t.Fatal("bailed")
	}
	want := `Looking in indexes: https://pypi.org/simple, https://pkgs.example.com/simple
DEPRECATION: Legacy editable install of demo==0.1 (from -e .) (setup.py develop) is deprecated. pip 25.3 will enforce this behaviour change.
[lx: hid 1 Collecting, 1 Downloading, 1 progress lines; 1 dependency already satisfied]
WARNING: Retrying (Retry(total=4, connect=None, read=None, redirect=None, status=None)) after connection broken by 'ReadTimeoutError("HTTPSConnectionPool(host='pypi.org', port=443): Read timed out. (read timeout=15)")': /simple/idna/
Successfully installed requests-2.32.5
[notice] A new release of pip is available: 24.0 -> 26.0.1
[notice] To update, run: pip install --upgrade pip`
	if out != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}

func TestPipInstallFailureKeepsEverythingAfterError(t *testing.T) {
	in := `Collecting numpy==1.19.0
  Downloading numpy-1.19.0.zip (7.3 MB)
  Installing build dependencies: started
  Installing build dependencies: finished with status 'done'
Building wheels for collected packages: numpy
  Building wheel for numpy (pyproject.toml): started
  Building wheel for numpy (pyproject.toml): finished with status 'error'
  error: subprocess-exited-with-error

  × Building wheel for numpy (pyproject.toml) did not run successfully.
  │ exit code: 1
  ╰─> [2 lines of output]
      Running from numpy source directory.
      RuntimeError: Broken toolchain: cannot link a simple C program
      [end of output]

  note: This error originates from a subprocess, and is likely not a problem with pip.
  ERROR: Failed building wheel for numpy
Failed to build numpy
ERROR: Could not build wheels for numpy, which is required to install pyproject.toml-based projects`
	out, ok := apply(t, "pip-install", ctx(1, "pip", "install", "numpy==1.19.0"), in)
	if !ok {
		t.Fatal("bailed")
	}
	if missing := fixture.ErrorLinesMissing(in, out); len(missing) > 0 {
		t.Errorf("missing error lines %q in:\n%s", missing, out)
	}
	for _, want := range []string{"Building wheel for numpy (pyproject.toml): finished with status 'error'", "│ exit code: 1", "Running from numpy source directory.", "Failed to build numpy"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestPipExitWithoutError(t *testing.T) {
	in := "Collecting x\n  Downloading x-1.0.tar.gz (10 kB)\nSuccessfully installed x-1.0"
	out, _ := apply(t, "pip-install", ctx(137, "pip", "install", "x"), in)
	if !strings.Contains(out, "[lx: pip exited 137]") {
		t.Errorf("exit not reported:\n%s", out)
	}
}

func TestPipInstallHuge(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 25000; i++ {
		fmt.Fprintf(&b, "Requirement already satisfied: pkg%d>=1.0 in /home/user/venv/lib/python3.12/site-packages (from app) (1.%d)\n", i, i)
		fmt.Fprintf(&b, "Collecting dep%d\n", i)
	}
	b.WriteString("Successfully installed dep0-1.0")
	start := time.Now()
	out, ok := apply(t, "pip-install", ctx(0, "pip", "install", "-r", "r.txt"), b.String())
	if time.Since(start) > 3*time.Second*raceSlowdown || !ok {
		t.Fatalf("ok=%v in %v", ok, time.Since(start))
	}
	if !strings.Contains(out, "hid 25000 Collecting lines; 25000 dependencies already satisfied") {
		t.Errorf("unexpected:\n%s", out)
	}
}

func TestPipUninstall(t *testing.T) {
	in := "Found existing installation: Flask 3.1.3\nUninstalling Flask-3.1.3:\n  Successfully uninstalled Flask-3.1.3\nWARNING: Skipping nope as it is not installed."
	out, ok := apply(t, "pip-uninstall", ctx(0, "pip", "uninstall", "-y", "flask", "nope"), in)
	if !ok || out != "Successfully uninstalled Flask-3.1.3\nWARNING: Skipping nope as it is not installed." {
		t.Errorf("got %q", out)
	}
}

func TestPipShowFiles(t *testing.T) {
	var b strings.Builder
	b.WriteString("Name: demo\nVersion: 1.0\nLocation: /home/user/venv/lib/python3.12/site-packages\nRequires: \nRequired-by: error-reporter\nFiles:\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "  demo/errors/mod%02d.py\n", i)
	}
	b.WriteString("  demo-1.0.dist-info/RECORD")
	out, ok := apply(t, "pip-show", ctx(0, "pip", "show", "-f", "demo"), b.String())
	if !ok {
		t.Fatal("bailed")
	}
	if !strings.Contains(out, "Required-by: error-reporter") || !strings.Contains(out, "Location: ~/venv/lib/python3.12/site-packages") {
		t.Errorf("fields changed:\n%s", out)
	}
	if strings.Count(out, "\n") > 20 {
		t.Errorf("file list not factored:\n%s", out)
	}
}

func TestPipList(t *testing.T) {
	in := "Package    Version Editable project location\n---------- ------- -------------------------\ndemo       0.1     /home/user/src/demo\nrequests   2.32.5"
	out, ok := apply(t, "pip-list", ctx(0, "pip", "list"), in)
	if !ok || !strings.Contains(out, "requests   2.32.5") {
		t.Errorf("got %q", out)
	}
	if _, ok := apply(t, "pip-list", ctx(0, "pip", "list"), "Package\n"); ok {
		t.Error("header without ruler must bail")
	}
}

func TestMypyNotes(t *testing.T) {
	in := `src/a.py:3: error: Library stubs not installed for "requests"  [import-untyped]
src/a.py:3: note: Hint: "python3 -m pip install types-requests"
src/a.py:3: note: See https://mypy.readthedocs.io/en/stable/running_mypy.html#missing-imports
src/b.py:3: error: Library stubs not installed for "requests"  [import-untyped]
src/b.py:3: note: Hint: "python3 -m pip install types-requests"
src/b.py:3: note: See https://mypy.readthedocs.io/en/stable/running_mypy.html#missing-imports
src/b.py:10: note: Revealed type is "builtins.int"
src/b.py:20: note: Revealed type is "builtins.int"
src/c.py:1: error: Function is missing a return type annotation  [no-untyped-def]
src/c.py:1: note: Use "-> None" if function does not return a value
src/c.py:5: error: Function is missing a return type annotation  [no-untyped-def]
src/c.py:5: note: Use "-> None" if function does not return a value
Found 4 errors in 3 files (checked 3 source files)`
	out, ok := apply(t, "mypy", ctx(1, "mypy", "src"), in)
	if !ok {
		t.Fatal("bailed")
	}
	if strings.Count(out, "Hint:") != 1 || strings.Count(out, "See https://") != 1 || strings.Count(out, `Use "-> None"`) != 1 {
		t.Errorf("advice notes not deduplicated:\n%s", out)
	}
	if strings.Count(out, "Revealed type") != 2 {
		t.Errorf("reveal_type notes must all stay:\n%s", out)
	}
	if strings.Count(out, ": error: ") != 4 || !strings.HasSuffix(out, "[lx: 3 repeated notes hidden]\nFound 4 errors in 3 files (checked 3 source files)") {
		t.Errorf("errors or summary changed:\n%s", out)
	}
}

func TestMypyExitMismatch(t *testing.T) {
	out, ok := apply(t, "mypy", ctx(2, "mypy", "."), "Success: no issues found in 3 source files")
	if !ok || !strings.Contains(out, "[lx: mypy exited 2]") {
		t.Errorf("got %q", out)
	}
}

func TestMypyHuge(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50000; i++ {
		fmt.Fprintf(&b, "src/m%d.py:%d: error: Function is missing a return type annotation  [no-untyped-def]\n", i/100, i%100)
		if i%2 == 0 {
			fmt.Fprintf(&b, "src/m%d.py:%d: note: Use \"-> None\" if function does not return a value\n", i/100, i%100)
		}
	}
	b.WriteString("Found 50000 errors in 500 files (checked 500 source files)")
	start := time.Now()
	out, ok := apply(t, "mypy", ctx(1, "mypy", "."), b.String())
	if time.Since(start) > 3*time.Second*raceSlowdown || !ok {
		t.Fatalf("ok=%v in %v", ok, time.Since(start))
	}
	if strings.Count(out, ": error: ") != 50000 || !strings.Contains(out, "[lx: 24999 repeated notes hidden]") {
		t.Errorf("errors dropped or notes kept")
	}
}

const ruffOldFull = `src/app.py:1:8: F401 [*] ` + "`os`" + ` imported but unused
  |
1 | import os
  |        ^^ F401
2 | import sys
  |
  = help: Remove unused import: ` + "`os`" + `

src/app.py:9:5: E722 Do not use bare ` + "`except`" + `
   |
 7 |     try:
 8 |         run()
 9 |     except:
   |     ^^^^^^ E722
10 |         raise RuntimeError("cannot run: error")
   |

src/bad.py:3:12: E999 SyntaxError: Expected ')', found newline
  |
1 | import os
2 |
3 | def broken(:
  |            ^ E999
  |

Found 3 errors.
[*] 1 fixable with the ` + "`--fix`" + ` option.`

func TestRuffOldFormat(t *testing.T) {
	out, ok := apply(t, "ruff", ctx(1, "ruff", "check", "."), ruffOldFull)
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{"src/app.py:1:8: F401 [*] `os` imported but unused", "1 | import os", "  = help: Remove unused import: `os`",
		"src/app.py:9:5: E722 Do not use bare `except`", " 9 |     except:", "src/bad.py:3:12: E999 SyntaxError: Expected ')', found newline",
		"3 | def broken(:", "Found 3 errors.", "[*] 1 fixable with the `--fix` option."} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "import sys") || strings.Contains(out, "raise RuntimeError") {
		t.Errorf("context lines kept:\n%s", out)
	}
	if strings.Contains(out, "error lines from the full output") {
		t.Errorf("own guard fired on snippet source:\n%s", out)
	}
}

func TestRuffSnippetCap(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&b, "F841 Local variable `x%d` is assigned to but never used\n --> src/m.py:%d:5\n  |\n%d |     x%d = error_count()\n  |     ^^ F841\n  |\nhelp: Remove assignment to unused variable `x%d`\n\n", i, i, i, i, i)
	}
	b.WriteString("Found 30 errors.\nNo fixes available (30 hidden fixes can be enabled with the `--unsafe-fixes` option).")
	out, ok := apply(t, "ruff", ctx(1, "ruff", "check"), b.String())
	if !ok {
		t.Fatal("bailed")
	}
	if n := strings.Count(out, "= error_count()"); n != 10 {
		t.Errorf("%d snippets shown, want 10:\n%s", n, out)
	}
	if strings.Count(out, "is assigned to but never used") != 30 || strings.Count(out, "--> src/m.py:") != 30 {
		t.Errorf("diagnostics dropped:\n%s", out)
	}
	if !strings.Contains(out, "[lx: hid source snippets of 20 diagnostics]") {
		t.Errorf("no marker:\n%s", out)
	}
}

func TestRuffHuge(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50000; i++ {
		fmt.Fprintf(&b, "src/m%d.py:%d:1: E501 Line too long (120 > 88)\n", i/100, i%100+1)
	}
	b.WriteString("Found 50000 errors.")
	start := time.Now()
	out, ok := apply(t, "ruff", ctx(1, "ruff", "check", "--output-format", "concise"), b.String())
	if time.Since(start) > 3*time.Second*raceSlowdown || !ok || strings.Count(out, "E501") != 50000 {
		t.Fatalf("ok=%v in %v", ok, time.Since(start))
	}
}

func TestScriptTraceback311(t *testing.T) {
	in := `starting
Traceback (most recent call last):
  File "/home/user/src/demo/app.py", line 40, in <module>
    main()
  File "/home/user/src/demo/app.py", line 30, in main
    walk(tree)
  File "/home/user/src/demo/app.py", line 12, in walk
    return walk(node.child)
           ^^^^^^^^^^^^^^^^
  [Previous line repeated 995 more times]
  File "/usr/lib/python3.12/json/__init__.py", line 346, in loads
    return _default_decoder.decode(s)
           ^^^^^^^^^^^^^^^^^^^^^^^^^^
  File "/usr/lib/python3.12/json/decoder.py", line 337, in decode
    obj, end = self.raw_decode(s, idx=_w(s, 0).end())
               ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
  File "/usr/lib/python3.12/json/decoder.py", line 353, in raw_decode
    obj, end = self.scan_once(s, idx)
               ^^^^^^^^^^^^^^^^^^^^^^
RecursionError: maximum recursion depth exceeded while decoding a JSON object from a unicode string`
	out, ok := apply(t, "python", ctx(1, "python", "app.py"), in)
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{"starting", `File "app.py", line 12, in walk`, "[Previous line repeated 995 more times]",
		"… 2 library frames (json)", `File "/usr/lib/python3.12/json/decoder.py", line 353, in raw_decode`,
		"RecursionError: maximum recursion depth exceeded while decoding a JSON object from a unicode string"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestScriptWithoutTracebackBails(t *testing.T) {
	if _, ok := apply(t, "python", ctx(0, "python", "x.py"), "hello\nworld\nERROR: something printed by the program"); ok {
		t.Error("plain program output must go to the generic reducer")
	}
}

func TestScriptHuge(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 50000; i++ {
		fmt.Fprintf(&b, "2026-09-26 10:00:%02d INFO worker processed item %d in %dms\n", i%60, i, i%97)
	}
	b.WriteString("Traceback (most recent call last):\n  File \"/home/user/src/demo/w.py\", line 3, in <module>\n    run()\nValueError: bad item 49999\n")
	start := time.Now()
	out, ok := apply(t, "python", ctx(1, "python", "w.py"), b.String())
	if el := time.Since(start); el > 5*time.Second*raceSlowdown || !ok {
		t.Fatalf("ok=%v in %v", ok, el)
	}
	if !strings.Contains(out, "ValueError: bad item 49999") || tokens.Count(out) > 3000 {
		t.Errorf("unexpected (%d tokens):\n%.3000s", tokens.Count(out), out)
	}
}

func TestShellUnwrap(t *testing.T) {
	cases := map[string][]string{
		`pytest -x`:                  {"pytest", "-x"},
		`pytest -k "not slow" 2>&1`:  {"pytest", "-k", "not slow"},
		`python -c 'print("a; b")'`:  {"python", "-c", `print("a; b")`},
		`python -c "print(\"x\")"`:   {"python", "-c", `print("x")`},
		`pytest; echo done`:          nil,
		`pytest | tail`:              nil,
		`pytest > out.txt`:           nil,
		`pytest $(cat args)`:         nil,
		`python -c "print('$HOME')"`: nil,
		`pytest tests/*.py`:          nil,
		`pytest 'unterminated`:       nil,
	}
	for s, want := range cases {
		got := unwrapShell([]string{"bash", "-c", s})
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") || (got == nil) != (want == nil) {
			t.Errorf("%q: got %q, want %q", s, got, want)
		}
	}
	if unwrapShell([]string{"bash", "-x", "pytest"}) != nil || unwrapShell([]string{"fish", "-c", "pytest"}) != nil {
		t.Error("only sh/bash/zsh/dash -c are unwrapped")
	}
}

func TestPytestXdist(t *testing.T) {
	in := `============================= test session starts ==============================
platform linux -- Python 3.12.1, pytest-8.3.4, pluggy-1.5.0
rootdir: /home/user/src/demo
plugins: xdist-3.6.1
created: 4/4 workers
4 workers [300 items]

........................................................................ [ 24%]
.....................F.................................................. [ 48%]
........................................................................ [ 72%]
............................................................................. [100%]
=================================== FAILURES ===================================
__________________________________ test_slow ___________________________________
[gw2] linux -- Python 3.12.1 /home/user/venv/bin/python

    def test_slow():
>       assert fetch() == 1
E       assert 0 == 1
E        +  where 0 = fetch()

tests/test_s.py:8: AssertionError
=========================== short test summary info ============================
FAILED tests/test_s.py::test_slow - assert 0 == 1
======================== 1 failed, 299 passed in 3.02s =========================`
	out, ok := apply(t, "pytest", ctx(1, "pytest", "-n", "4"), in)
	if !ok {
		t.Fatal("bailed")
	}
	want := `4 workers [300 items]
[lx: 5 progress lines hidden]
=================================== FAILURES ===================================
__________________________________ test_slow ___________________________________
    def test_slow():
>       assert fetch() == 1
E       assert 0 == 1
E        +  where 0 = fetch()
tests/test_s.py:8: AssertionError
=========================== short test summary info ============================
FAILED tests/test_s.py::test_slow - assert 0 == 1
======================== 1 failed, 299 passed in 3.02s =========================`
	if out != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
	v := "4 workers [3 items]\n\n[gw0] [ 33%] PASSED tests/test_a.py::test_a \n[gw1] [ 66%] FAILED tests/test_a.py::test_b \n[gw0] [100%] PASSED tests/test_a.py::test_c \n\n========================= 1 failed, 2 passed in 1.00s =========================="
	out, _ = apply(t, "pytest", ctx(1, "pytest", "-n", "2", "-v"), v)
	if !strings.Contains(out, "[gw1] [ 66%] FAILED tests/test_a.py::test_b") || strings.Contains(out, "test_c") || !strings.Contains(out, "[lx: 2 PASSED lines hidden]") {
		t.Errorf("xdist -v:\n%s", out)
	}
}

func TestPytestWarningsSummary(t *testing.T) {
	var b strings.Builder
	b.WriteString("collected 40 items\n\ntests/test_w.py ........................................ [100%]\n\n")
	b.WriteString("=============================== warnings summary ===============================\n")

	b.WriteString("tests/test_w.py::test_del\n  /home/user/venv/lib/python3.12/site-packages/_pytest/unraisableexception.py:85: PytestUnraisableExceptionWarning: Exception ignored in: <function Conn.__del__ at 0x7f00>\n  \n  Traceback (most recent call last):\n    File \"/home/user/src/demo/conn.py\", line 10, in __del__\n      self.sock.close()\n  AttributeError: 'Conn' object has no attribute 'sock'\n  \n    warnings.warn(pytest.PytestUnraisableExceptionWarning(msg))\n\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "tests/test_w.py::test_%d\n  /home/user/src/demo/tests/test_w.py:%d: UserWarning: distinct warning %d\n    warn()\n\n", i, i+1, i)
	}
	b.WriteString("-- Docs: https://docs.pytest.org/en/stable/how-to/capture-warnings.html\n")
	b.WriteString("======================= 40 passed, 31 warnings in 0.20s ========================")
	out, added, ok := reducePytest(ctx(0, "pytest"), b.String())
	if !ok || added != 0 {
		t.Fatalf("ok=%v added=%d\n%s", ok, added, out)
	}
	for _, want := range []string{"PytestUnraisableExceptionWarning: Exception ignored in", "AttributeError: 'Conn' object has no attribute 'sock'",
		`File "conn.py", line 10, in __del__`, "tests/test_w.py:1: UserWarning: distinct warning 0", "[lx: +11 more warning groups (11 test/location entries)]",
		"40 passed, 31 warnings in 0.20s"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "distinct warning 25") || strings.Contains(out, "warnings.warn(pytest") {
		t.Errorf("cap or source-line drop not applied:\n%s", out)
	}
}

func TestPytestDurationsCapped(t *testing.T) {
	var b strings.Builder
	b.WriteString("collected 60 items\n\ntests/test_d.py ............................................................ [100%]\n\n")
	b.WriteString("============================= slowest durations ==============================\n")
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "%d.%02ds call     tests/test_d.py::test_%d\n", 60-i, i, i)
	}
	b.WriteString("============================= 60 passed in 90.00s (0:01:30) ==============================")
	out, ok := apply(t, "pytest", ctx(0, "pytest", "--durations=0"), b.String())
	if !ok || !strings.Contains(out, "tests/test_d.py::test_24\n[lx: +35 more lines]") || strings.Contains(out, "test_25\n") {
		t.Errorf("durations not capped at 25:\n%s", out)
	}
}
