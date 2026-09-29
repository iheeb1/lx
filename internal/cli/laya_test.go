//go:build unix

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	daemon "github.com/iheeb1/lx/integrations/laya"
	"github.com/iheeb1/lx/internal/laya"
)

type layaT struct {
	*layaEnv
	env      map[string]string
	out, err bytes.Buffer
	root     string
}

func newLayaT(t *testing.T) *layaT {
	t.Helper()
	root, err := os.MkdirTemp("", "lxc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	lt := &layaT{env: map[string]string{}, root: root}
	getenv := func(k string) string { return lt.env[k] }
	lt.layaEnv = &layaEnv{
		paths:  laya.PathsFor(getenv, root, filepath.Join(root, "data")),
		getenv: getenv, home: root, goos: "darwin",
		in: strings.NewReader(""), out: &lt.out, errw: &lt.err,
	}
	return lt
}

func (lt *layaT) run(args ...string) int {
	lt.out.Reset()
	lt.err.Reset()
	return runLaya(args, lt.layaEnv)
}

const fakePython = `#!/bin/sh
echo "$*" >> %q
pwd >> %q.cwd
case "$1" in
-c)
	case "$2" in
	*version_info*) echo %s ;;
	*laya.__version__*) echo 0.3.21 ;;
	esac ;;
-m)
	if [ "$2" = venv ]; then mkdir -p "$3/bin" && cp "$0" "$3/bin/python"; fi
	if [ "$2" = pip ]; then %s; fi ;;
esac
exit 0
`

func (lt *layaT) fakePython(t *testing.T, version, pip string) (py, calls string) {
	t.Helper()
	calls = filepath.Join(lt.root, "calls")
	py = filepath.Join(lt.root, "python3")
	if err := os.WriteFile(py, fmt.Appendf(nil, fakePython, calls, calls, version, pip), 0o700); err != nil {
		t.Fatal(err)
	}
	return py, calls
}

func readCalls(t *testing.T, path string) []string {
	b, _ := os.ReadFile(path)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestLayaUsage(t *testing.T) {
	lt := newLayaT(t)
	if lt.run() != 2 || !strings.Contains(lt.err.String(), "usage: lx laya") {
		t.Errorf("no args: %s", lt.err.String())
	}
	if lt.run("frobnicate") != 2 || !strings.Contains(lt.err.String(), `unknown command "frobnicate"`) {
		t.Errorf("unknown: %s", lt.err.String())
	}
	if lt.run("--help") != 0 || !strings.Contains(lt.out.String(), "setup [--python PATH]") {
		t.Errorf("help: %s", lt.out.String())
	}
	if lt.run("setup", "extra") != 2 || lt.run("status", "--bogus") != 2 {
		t.Error("bad arguments accepted")
	}
}

func TestLayaSetupAsksFirst(t *testing.T) {
	lt := newLayaT(t)
	py, calls := lt.fakePython(t, "3.12", "true")
	lt.in = strings.NewReader("n\n")
	if code := lt.run("setup", "--python", py); code != 1 {
		t.Fatalf("exit %d\n%s%s", code, lt.out.String(), lt.err.String())
	}
	out := lt.out.String()
	venv := "~/data/laya/venv"
	for _, want := range []string{
		"lx laya setup downloads, over the network:",
		"laya 0.3.21 and the packages it needs",
		"from pypi.org into " + venv,
		"the model convaiinnovations/laya (typed-decisions) from huggingface.co: 807 MB",
		"  " + py + " -m venv " + lt.paths.Venv + "\n",
		"  " + lt.paths.VenvPython() + " -m pip install --disable-pip-version-check laya==0.3.21\n",
		"  " + lt.paths.VenvPython() + " " + lt.paths.Script + " --fetch\n",
		"the daemon runs offline",
		"Continue? [y/N] ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "download.pytorch.org") {
		t.Error("CPU torch index on darwin")
	}
	if !strings.Contains(lt.err.String(), "cancelled, nothing downloaded") {
		t.Errorf("stderr %q", lt.err.String())
	}
	if c := readCalls(t, calls); len(c) != 1 || !strings.HasPrefix(c[0], "-c ") {
		t.Errorf("ran more than the version check: %q", c)
	}
	if _, err := os.Stat(lt.paths.Dir); err == nil {
		t.Error("declined setup wrote files")
	}

	lt.in = strings.NewReader("")
	if lt.run("setup", "--python", py) != 1 {
		t.Error("EOF on stdin counted as yes")
	}
}

func TestLayaSetup(t *testing.T) {
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			lt := newLayaT(t)
			lt.goos = goos
			py, calls := lt.fakePython(t, "3.12", "true")
			if code := lt.run("setup", "--python", py, "--yes"); code != 0 {
				t.Fatalf("exit %d\n%s%s", code, lt.out.String(), lt.err.String())
			}
			vpy := lt.paths.VenvPython()
			want := []string{"-c import sys; print('%d.%d' % sys.version_info[:2])", "-m venv " + lt.paths.Venv}
			if goos == "linux" {
				want = append(want, "-m pip install --disable-pip-version-check --index-url https://download.pytorch.org/whl/cpu torch")
			}
			want = append(want, "-m pip install --disable-pip-version-check laya==0.3.21", lt.paths.Script+" --fetch")
			if got := readCalls(t, calls); strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Errorf("calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
			for _, dir := range readCalls(t, calls+".cwd") {
				if dir != "/" {
					t.Errorf("python ran in %s, where -m venv and -m pip import that directory's venv.py or pip/", dir)
				}
			}
			b, err := os.ReadFile(lt.paths.Script)
			if err != nil || string(b) != daemon.Script {
				t.Errorf("script not written: %v", err)
			}
			if fi, _ := os.Stat(lt.paths.Script); fi.Mode().Perm() != 0o600 {
				t.Errorf("script mode %v", fi.Mode())
			}
			if fi, _ := os.Stat(lt.paths.Dir); fi.Mode().Perm() != 0o700 {
				t.Errorf("dir mode %v", fi.Mode())
			}
			if !strings.Contains(lt.out.String(), "$ "+vpy+" -m pip install") || !strings.Contains(lt.out.String(), "Laya is set up. Start it with: lx laya start") {
				t.Errorf("output:\n%s", lt.out.String())
			}
			if strings.Contains(lt.out.String(), "Continue?") {
				t.Error("--yes still asked")
			}

			os.Remove(calls)
			if code := lt.run("setup", "--python", py, "-y"); code != 0 {
				t.Fatalf("second setup: exit %d", code)
			}
			if c := readCalls(t, calls); strings.Contains(strings.Join(c, "\n"), "venv") {
				t.Errorf("re-created the venv: %q", c)
			}
		})
	}
}

func TestLayaSetupFailures(t *testing.T) {
	lt := newLayaT(t)
	old, _ := lt.fakePython(t, "3.9", "true")
	if lt.run("setup", "--python", old, "--yes") != 1 || !strings.Contains(lt.err.String(), "laya needs Python 3.10 or newer, found "+old+" 3.9") {
		t.Errorf("old python: %s", lt.err.String())
	}
	if lt.run("setup", "--python", filepath.Join(lt.root, "nope"), "--yes") != 1 || !strings.Contains(lt.err.String(), "--python") {
		t.Errorf("missing python: %s", lt.err.String())
	}

	py, calls := lt.fakePython(t, "3.12", "echo 'network is down' >&2; exit 7")
	if lt.run("setup", "--python", py, "--yes") != 1 || !strings.Contains(lt.err.String(), "installing laya failed: exit status 7") {
		t.Errorf("pip failure: %s", lt.err.String())
	}
	if c := readCalls(t, calls); strings.Contains(c[len(c)-1], "--fetch") {
		t.Errorf("went on after pip failed: %q", c)
	}
}

func TestLayaSetupWithEnvPython(t *testing.T) {
	lt := newLayaT(t)
	py, calls := lt.fakePython(t, "3.12", "true")
	lt.env["LX_LAYA_PYTHON"] = py
	if code := lt.run("setup", "--yes"); code != 0 {
		t.Fatalf("exit %d\n%s", code, lt.err.String())
	}
	if !strings.Contains(lt.out.String(), "Using LX_LAYA_PYTHON="+py+" (laya 0.3.21)") || strings.Contains(lt.out.String(), "pypi.org") {
		t.Errorf("output:\n%s", lt.out.String())
	}
	want := "-c import laya; print(laya.__version__)\n" + lt.paths.Script + " --fetch"
	if got := strings.Join(readCalls(t, calls), "\n"); got != want {
		t.Errorf("calls %q", got)
	}
	if got := strings.Join(readCalls(t, calls+".cwd"), " "); got != "/ /" {
		t.Errorf("python ran in %s, where import laya finds that directory's laya.py", got)
	}
	if lt.run("setup", "--yes", "--python", py) != 1 || !strings.Contains(lt.err.String(), "unset LX_LAYA_PYTHON first") {
		t.Errorf("--python with LX_LAYA_PYTHON: %s", lt.err.String())
	}
	lt.env["LX_LAYA_PYTHON"] = "/bin/false"
	if lt.run("setup", "--yes") != 1 || !strings.Contains(lt.err.String(), "LX_LAYA_PYTHON=/bin/false cannot import laya") {
		t.Errorf("broken LX_LAYA_PYTHON: %s", lt.err.String())
	}
}

func TestLayaNotSetUp(t *testing.T) {
	lt := newLayaT(t)
	if lt.run("status") != 1 || lt.out.String() != "laya: not set up (optional): lx laya setup installs it\n" {
		t.Errorf("status: %q", lt.out.String())
	}
	if lt.run("start") != 1 || !strings.Contains(lt.err.String(), "Laya is not set up: lx laya setup installs it") {
		t.Errorf("start: %q", lt.err.String())
	}
	if lt.run("stop") != 0 || lt.out.String() != "Laya is not running\n" {
		t.Errorf("stop: %q", lt.out.String())
	}
	var st layaStatus
	if lt.run("status", "--json") != 1 || json.Unmarshal(lt.out.Bytes(), &st) != nil || st.SetUp || st.Running || st.Socket != lt.paths.Socket {
		t.Errorf("status --json: %s", lt.out.String())
	}
}

func TestLayaStopStaleSocket(t *testing.T) {
	lt := newLayaT(t)
	os.MkdirAll(filepath.Dir(lt.paths.Socket), 0o700)
	ln, err := net.Listen("unix", lt.paths.Socket)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	os.WriteFile(lt.paths.Pid, []byte("999999999\n"), 0o600)
	if lt.run("status") != 1 || !strings.Contains(lt.out.String(), "laya: not answering on ~/") {
		t.Errorf("status: %q", lt.out.String())
	}
	if lt.run("stop") != 0 || lt.out.String() != "Laya is not running (removed a stale socket)\n" {
		t.Errorf("stop: %q %q", lt.out.String(), lt.err.String())
	}
	if _, err := os.Lstat(lt.paths.Socket); err == nil {
		t.Error("stale socket kept")
	}
	if _, err := os.Stat(lt.paths.Pid); err == nil {
		t.Error("stale pid file kept")
	}
}

func TestLayaStopRefusesAStranger(t *testing.T) {
	lt := newLayaT(t)
	os.MkdirAll(filepath.Dir(lt.paths.Pid), 0o700)
	os.WriteFile(lt.paths.Pid, fmt.Appendf(nil, "%d\n", os.Getpid()), 0o600)
	if lt.run("stop") != 1 || !strings.Contains(lt.err.String(), "is not lx's Laya daemon; not touching it") {
		t.Errorf("stop: %q %q", lt.out.String(), lt.err.String())
	}

	t.Setenv("PATH", lt.root)
	want := "cannot tell whether pid"
	if _, err := os.Stat("/proc/self/cmdline"); err == nil {
		want = "is not lx's Laya daemon"
	}
	if lt.run("stop") != 1 || !strings.Contains(lt.err.String(), want) {
		t.Errorf("stop without ps: %q, want %q", lt.err.String(), want)
	}
}

func fakeListener(t *testing.T, path string, reply func(n int) string) net.Listener {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o700)
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for n := 0; ; n++ {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 4096)
				c.Read(buf)
				if r := reply(n); r != "" {
					c.Write([]byte(r + "\n"))
				} else {
					c.Read(buf)
				}
			}()
		}
	}()
	return ln
}

func TestLayaStopKeepsASilentDaemon(t *testing.T) {
	lt := newLayaT(t)
	fakeListener(t, lt.paths.Socket, func(int) string { return "" })
	if lt.run("stop") != 1 || !strings.Contains(lt.err.String(), "stop that process yourself") {
		t.Errorf("stop: %q %q", lt.out.String(), lt.err.String())
	}
	if _, err := os.Lstat(lt.paths.Socket); err != nil {
		t.Errorf("removed the socket of a daemon that is still listening: %v", err)
	}
}

func TestLayaStartNoticesADeadDaemon(t *testing.T) {
	lt := newLayaT(t)
	lt.env["LX_LAYA_PYTHON"] = "/bin/sh"
	gone := exec.Command("/bin/sh", "-c", "exit 0")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	var ln net.Listener
	ln = fakeListener(t, lt.paths.Socket, func(int) string {
		defer ln.Close()
		return fmt.Sprintf(`{"ok":true,"model":"m","loaded":false,"version":1,"pid":%d}`, gone.Process.Pid)
	})
	start := time.Now()
	if lt.run("start", "--wait", "10s") != 1 || !strings.Contains(lt.err.String(), fmt.Sprintf("the daemon (pid %d) exited before its model was ready", gone.Process.Pid)) {
		t.Errorf("start: %q %q", lt.out.String(), lt.err.String())
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("waited %v for a daemon that was gone", d)
	}
}

const fakeLauncher = `import importlib.util, sys
spec = importlib.util.spec_from_file_location("lx_laya", sys.argv[1])
m = importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)

class Fake:
    name = "fake/model"
    version = "0"
    def load(self):
        pass
    def predict(self, texts, qs):
        return [{"answers": {"keep": {"choice": "B" if "noise" in t else "A", "answer_confidence": 0.9}}} for t in texts]

sys.exit(m.main(sys.argv[2:], model=Fake()))
`

func python3(t *testing.T) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	if v, err := pyOutput(py, "import sys; print('%d.%d' % sys.version_info[:2])"); err != nil || !(newEnough(v) || v == "3.9" || v == "3.8") {
		t.Skipf("python3 %s is too old to run the daemon", v)
	}
	return py
}

func (lt *layaT) stopAtEnd(t *testing.T) {
	t.Cleanup(func() {
		lt.run("stop")
		if pid := readPid(lt.paths.Pid); pid > 0 && processAlive(pid) {
			if cmd, err := processCommand(pid); err == nil && strings.Contains(cmd, "lx_laya.py") {
				p, _ := os.FindProcess(pid)
				p.Kill()
			}
		}
	})
}

func TestLayaStartStatusStop(t *testing.T) {
	py := python3(t)
	lt := newLayaT(t)
	launcher := filepath.Join(lt.root, "launch.py")
	os.WriteFile(launcher, []byte(fakeLauncher), 0o600)
	wrapper := filepath.Join(lt.root, "fakepy")
	os.WriteFile(wrapper, fmt.Appendf(nil, "#!/bin/sh\nexec %q -B %q \"$@\"\n", py, launcher), 0o700)
	lt.env["LX_LAYA_PYTHON"] = wrapper
	lt.stopAtEnd(t)

	if code := lt.run("start", "--wait", "20s"); code != 0 {
		t.Fatalf("start: exit %d\n%s%s\nlog:\n%s", code, lt.out.String(), lt.err.String(), logTail(lt.paths.Log, 0, 30))
	}
	if !strings.Contains(lt.out.String(), "Laya is running (pid ") || !strings.Contains(lt.out.String(), "model fake/model, socket ~/") {
		t.Errorf("start output %q", lt.out.String())
	}
	for path, mode := range map[string]os.FileMode{lt.paths.Socket: 0o600, lt.paths.Pid: 0o600, lt.paths.Log: 0o600, filepath.Dir(lt.paths.Socket): 0o700} {
		if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != mode {
			t.Errorf("%s: %v %v, want %v", path, fi.Mode().Perm(), err, mode)
		}
	}

	vs, err := laya.Client{Socket: lt.paths.Socket}.Judge(laya.Request{Family: "log", Items: []laya.Item{{Text: "noise"}, {Text: "ERROR x"}}}, 2*time.Second)
	if err != nil || len(vs) != 2 || vs[0] != (laya.Verdict{Keep: false, Confidence: 0.9}) || !vs[1].Keep {
		t.Fatalf("judge: %+v %v", vs, err)
	}

	if code := lt.run("status"); code != 0 {
		t.Fatalf("status: exit %d\n%s", code, lt.out.String())
	}
	out := lt.out.String()
	for _, want := range []string{"laya: running (pid ", "  model   fake/model (laya 0), loaded in ", "  socket  ~/", ", ping ", "  memory  ", "  last    2 items in ", "  python  ~/fakepy\n", "  log     ~/"} {
		if !strings.Contains(out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	var st layaStatus
	if lt.run("status", "--json") != 0 || json.Unmarshal(lt.out.Bytes(), &st) != nil || !st.Ready || st.Daemon == nil || st.Daemon.Last.Items != 2 {
		t.Errorf("status --json: %s", lt.out.String())
	}

	if code := lt.run("start"); code != 0 || !strings.Contains(lt.out.String(), "Laya is already running") {
		t.Errorf("second start: %d %q", code, lt.out.String())
	}

	pid := st.Daemon.Pid
	if code := lt.run("stop"); code != 0 || lt.out.String() != fmt.Sprintf("Laya stopped (pid %d)\n", pid) {
		t.Fatalf("stop: %d %q %q", code, lt.out.String(), lt.err.String())
	}
	if processAlive(pid) {
		t.Error("daemon still alive")
	}
	for _, p := range []string{lt.paths.Socket, lt.paths.Pid} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s left behind", p)
		}
	}
	if lt.run("status") != 1 || !strings.Contains(lt.out.String(), "laya: not running (set up with ~/fakepy)") {
		t.Errorf("status after stop: %q", lt.out.String())
	}
}

func TestLayaStartWaitsForTheDaemonThatWon(t *testing.T) {
	if !strings.Contains(daemon.Script, fmt.Sprintf("\nEXIT_BUSY = %d\n", daemonBusy)) {
		t.Fatalf("lx_laya.py doesn't exit %d when another daemon runs", daemonBusy)
	}
	lt := newLayaT(t)
	busy := filepath.Join(lt.root, "busypy")
	os.WriteFile(busy, fmt.Appendf(nil, "#!/bin/sh\nexit %d\n", daemonBusy), 0o700)
	lt.env["LX_LAYA_PYTHON"] = busy
	fakeListener(t, lt.paths.Socket, func(n int) string {
		if n < 2 {
			return ""
		}
		return `{"ok":true,"model":"fake/model","loaded":true,"version":1,"pid":42}`
	})
	if code := lt.run("start", "--wait", "10s"); code != 0 || !strings.Contains(lt.out.String(), "Laya is running (pid 42)") {
		t.Errorf("start: %d %q %q", code, lt.out.String(), lt.err.String())
	}
}

func TestLayaStartDaemonDies(t *testing.T) {
	lt := newLayaT(t)
	bad := filepath.Join(lt.root, "badpy")
	os.WriteFile(bad, []byte("#!/bin/sh\necho 'ModuleNotFoundError: No module named torch' >&2\nexit 1\n"), 0o700)
	lt.env["LX_LAYA_PYTHON"] = bad
	if code := lt.run("start", "--wait", "10s"); code != 1 {
		t.Fatalf("exit %d", code)
	}
	e := lt.err.String()
	if !strings.Contains(e, "the daemon exited (exit status 1) before its model was ready") || !strings.Contains(e, "  ModuleNotFoundError: No module named torch\n") {
		t.Errorf("stderr:\n%s", e)
	}
	wd, _ := os.Getwd()
	rel, err := filepath.Rel(wd, bad)
	if err != nil {
		t.Fatal(err)
	}
	lt.env["LX_LAYA_PYTHON"] = rel
	lt.run("start", "--wait", "10s")
	if !strings.Contains(lt.err.String(), "the daemon exited (exit status 1)") {
		t.Errorf("relative LX_LAYA_PYTHON=%s: %s", rel, lt.err.String())
	}
	if n := strings.Count(lt.err.String(), "ModuleNotFoundError"); n != 1 {
		t.Errorf("log tail repeats an earlier start (%d):\n%s", n, lt.err.String())
	}
}

func TestLayaRealSmoke(t *testing.T) {
	if os.Getenv("LX_LAYA_REAL") != "1" {
		t.Skip("LX_LAYA_REAL=1 runs the real model")
	}
	py := os.Getenv("LX_LAYA_PYTHON")
	if py == "" {
		t.Fatal("LX_LAYA_REAL=1 needs LX_LAYA_PYTHON: a python with laya installed and its model downloaded")
	}
	lt := newLayaT(t)
	lt.env["LX_LAYA_PYTHON"] = py
	lt.stopAtEnd(t)
	start := time.Now()
	if code := lt.run("start", "--wait", "3m"); code != 0 {
		t.Fatalf("start: exit %d\n%s%s\nlog:\n%s", code, lt.out.String(), lt.err.String(), logTail(lt.paths.Log, 0, 40))
	}
	t.Logf("started in %v: %s", time.Since(start).Round(time.Millisecond), strings.TrimSpace(lt.out.String()))

	var noise []string
	for i := range 12 {
		noise = append(noise, fmt.Sprintf("081109 2036%02d 148 INFO dfs.DataNode$PacketResponder: PacketResponder 1 for block blk_%d terminating", i, 3886504906413966+i))
	}
	items := []laya.Item{
		{Text: strings.Join(noise, "\n")},
		{Text: "--- FAIL: TestParse (0.00s)\n    parse_test.go:41: got \"a\", want \"b\"\nFAIL\nexit status 1"},
		{Text: "Downloading lodash-4.17.21.tgz\nDownloading react-18.2.0.tgz\nDownloading react-dom-18.2.0.tgz"},
		{Text: "panic: runtime error: index out of range [3] with length 3\n\ngoroutine 1 [running]:\nmain.main()\n\t/app/main.go:12 +0x1d"},
	}
	t0 := time.Now()
	vs, err := laya.Client{Socket: lt.paths.Socket}.Judge(laya.Request{Family: "log", Task: "fix the failing TestParse", Items: items}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("judged %d chunks in %v: %+v", len(items), time.Since(t0).Round(time.Millisecond), vs)
	if len(vs) != len(items) || !vs[1].Keep || !vs[3].Keep {
		t.Errorf("verdicts %+v: the failure and the panic must be kept", vs)
	}
	for i, v := range vs {
		if v.Confidence <= 0 {
			t.Errorf("verdict %d unjudged: %+v", i, v)
		}
	}

	short, err := laya.Client{Socket: lt.paths.Socket}.Judge(laya.Request{Family: "lines", Items: make([]laya.Item, 64)}, 400*time.Millisecond)
	if err != nil {
		t.Logf("a 400 ms deadline on 64 items: %v (the caller falls back)", err)
	} else {
		unjudged := 0
		for _, v := range short {
			if v.Keep && v.Confidence == 0 {
				unjudged++
			}
		}
		t.Logf("a 400 ms deadline on 64 items: %d left unjudged (keep)", unjudged)
		if unjudged == 0 {
			t.Error("the deadline didn't cut the batch")
		}
	}

	if code := lt.run("status"); code != 0 {
		t.Errorf("status: exit %d\n%s", code, lt.out.String())
	}
	t.Logf("status:\n%s", lt.out.String())
	if code := lt.run("stop"); code != 0 {
		t.Errorf("stop: exit %d %s", code, lt.err.String())
	}
	if _, err := os.Lstat(lt.paths.Socket); err == nil {
		t.Error("socket left behind")
	}
}
