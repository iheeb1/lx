package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	daemon "github.com/iheeb1/lx/integrations/laya"
	"github.com/iheeb1/lx/internal/hook"
	"github.com/iheeb1/lx/internal/laya"
	"github.com/iheeb1/lx/internal/track"
)

const layaUsage = `usage: lx laya <command>
  setup [--python PATH] [--yes]  create a venv, install laya and download its model (asks first:
                                 the only lx command that uses the network)
  start [--wait 2m]              start the daemon and wait until the model is loaded
  stop                           stop the daemon
  status [--json]                is it running, its model, socket, memory and last latency
                                 (exit 1 when it isn't ready)
LX_LAYA_PYTHON=PATH runs the daemon with a python that already has laya installed.
`

const (
	layaPkg      = "laya==0.3.21"
	torchCPUIdx  = "https://download.pytorch.org/whl/cpu"
	layaModelDoc = "the model convaiinnovations/laya (typed-decisions) from huggingface.co: 807 MB"
)

const daemonBusy = 3

var daemonEnv = []string{"PYTHONUNBUFFERED=1", "PYTHONDONTWRITEBYTECODE=1",
	"HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1", "HF_HUB_DISABLE_TELEMETRY=1"}

type layaEnv struct {
	paths  laya.Paths
	getenv func(string) string
	home   string
	goos   string
	in     io.Reader
	out    io.Writer
	errw   io.Writer
}

func cmdLaya(args []string) int {
	home, _ := os.UserHomeDir()
	return runLaya(args, &layaEnv{
		paths:  laya.PathsFor(os.Getenv, home, filepath.Dir(track.Path())),
		getenv: os.Getenv, home: home, goos: runtime.GOOS,
		in: os.Stdin, out: os.Stdout, errw: os.Stderr,
	})
}

func runLaya(args []string, e *layaEnv) int {
	if len(args) == 0 {
		fmt.Fprint(e.errw, layaUsage)
		return 2
	}
	switch args[0] {
	case "setup":
		return e.setup(args[1:])
	case "start":
		return e.start(args[1:])
	case "stop":
		return e.stop(args[1:])
	case "status":
		return e.status(args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(e.out, layaUsage)
		return 0
	}
	fmt.Fprintf(e.errw, "lx laya: unknown command %q\n%s", args[0], layaUsage)
	return 2
}

func (e *layaEnv) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("lx laya "+name, flag.ContinueOnError)
	fs.SetOutput(e.errw)
	return fs
}

func (e *layaEnv) fail(cmd, format string, a ...any) int {
	fmt.Fprintf(e.errw, "lx laya "+cmd+": "+format+"\n", a...)
	return 1
}

func (e *layaEnv) tilde(p string) string { return showTilde(p, e.home) }

func (e *layaEnv) ready() error {
	if runtime.GOOS == "windows" {
		return laya.ErrUnsupported
	}
	if e.paths.Socket == "" || e.paths.Script == "" {
		return errors.New("no home directory to keep it in")
	}
	return nil
}

func (e *layaEnv) setup(args []string) int {
	fs := e.flags("setup")
	base := fs.String("python", "", "the python (3.10 or newer) to create the venv with (default: python3 on PATH)")
	yes := fs.Bool("yes", false, "don't ask before downloading")
	fs.BoolVar(yes, "y", false, "same as --yes")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprint(e.errw, layaUsage)
		return 2
	}
	if err := e.ready(); err != nil {
		return e.fail("setup", "%v", err)
	}

	type step struct {
		what string
		argv []string
	}
	var (
		py    string
		steps []step
		gets  []string
	)
	env := absPath(e.getenv("LX_LAYA_PYTHON"))
	if env != "" && *base != "" {
		return e.fail("setup", "--python makes a venv, but LX_LAYA_PYTHON=%s would still run the daemon: unset LX_LAYA_PYTHON first", env)
	}
	if env != "" {
		v, err := pyOutput(env, "import laya; print(laya.__version__)")
		if err != nil {
			return e.fail("setup", "LX_LAYA_PYTHON=%s cannot import laya (%v): install laya into it, or unset LX_LAYA_PYTHON to let lx make its own venv", env, err)
		}
		py = env
		fmt.Fprintf(e.out, "Using LX_LAYA_PYTHON=%s (laya %s).\n", env, v)
	} else {
		b, err := e.findPython(*base)
		if err != nil {
			return e.fail("setup", "%v", err)
		}
		py = e.paths.VenvPython()
		if _, err := os.Stat(py); err != nil {
			steps = append(steps, step{"creating the venv", []string{b, "-m", "venv", e.paths.Venv}})
		}
		pip := []string{py, "-m", "pip", "install", "--disable-pip-version-check"}
		if e.goos == "linux" {
			gets = append(gets, "torch, CPU-only build, from download.pytorch.org (the default build pulls in CUDA)")
			steps = append(steps, step{"installing torch", append(pip[:len(pip):len(pip)], "--index-url", torchCPUIdx, "torch")})
		}
		gets = append(gets, "laya 0.3.21 and the packages it needs (torch, transformers, safetensors, huggingface_hub, numpy, …) from pypi.org into "+
			e.tilde(e.paths.Venv)+": about 900 MB once installed")
		steps = append(steps, step{"installing laya", append(pip, layaPkg)})
	}
	gets = append(gets, layaModelDoc+", into huggingface_hub's cache (~/.cache/huggingface, or $HF_HOME)")
	steps = append(steps, step{"downloading the model", []string{py, e.paths.Script, "--fetch"}})

	fmt.Fprintln(e.out, "lx laya setup downloads, over the network:")
	for _, g := range gets {
		fmt.Fprintln(e.out, "  - "+g)
	}
	fmt.Fprintln(e.out, "by running:")
	for _, s := range steps {
		fmt.Fprintln(e.out, "  "+hook.ShellJoin(s.argv))
	}
	fmt.Fprintln(e.out, "Nothing else in lx uses the network: the daemon runs offline, and what lx sends it never leaves this machine.")
	if !*yes && !e.confirm() {
		fmt.Fprintln(e.errw, "lx laya setup: cancelled, nothing downloaded")
		return 1
	}
	if err := writeScript(e.paths.Script); err != nil {
		return e.fail("setup", "%v", err)
	}
	for _, s := range steps {
		fmt.Fprintln(e.out, "$ "+hook.ShellJoin(s.argv))
		cmd := exec.Command(s.argv[0], s.argv[1:]...)
		cmd.Dir = "/"
		cmd.Stdout, cmd.Stderr = e.out, e.errw
		cmd.Env = append(os.Environ(), "HF_HUB_DISABLE_TELEMETRY=1")
		if err := cmd.Run(); err != nil {
			return e.fail("setup", "%s failed: %v", s.what, err)
		}
	}
	fmt.Fprintln(e.out, "Laya is set up. Start it with: lx laya start")
	return 0
}

func (e *layaEnv) confirm() bool {
	fmt.Fprint(e.out, "Continue? [y/N] ")
	line, _ := bufio.NewReader(e.in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func (e *layaEnv) findPython(base string) (string, error) {
	cands := []string{base}
	if base == "" {
		cands = []string{"python3", "python3.13", "python3.12", "python3.11", "python3.10"}
	}
	var seen []string
	for _, c := range cands {
		p, err := exec.LookPath(c)
		if err != nil {
			if base != "" {
				return "", fmt.Errorf("--python %s: %v", base, err)
			}
			continue
		}
		p = absPath(p)
		v, err := pyOutput(p, "import sys; print('%d.%d' % sys.version_info[:2])")
		if err != nil {
			seen = append(seen, c+" (does not run)")
			continue
		}
		if newEnough(v) {
			return p, nil
		}
		seen = append(seen, c+" "+v)
	}
	if len(seen) == 0 {
		return "", errors.New("no python3 on PATH: install Python 3.10 or newer, or pass --python PATH")
	}
	return "", fmt.Errorf("laya needs Python 3.10 or newer, found %s: pass --python PATH", strings.Join(seen, ", "))
}

func newEnough(v string) bool {
	maj, min, ok := strings.Cut(v, ".")
	a, err1 := strconv.Atoi(maj)
	b, err2 := strconv.Atoi(min)
	return ok && err1 == nil && err2 == nil && (a > 3 || a == 3 && b >= 10)
}

func pyOutput(py, code string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, py, "-c", code)
	cmd.Dir = "/"
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if last := tailLine(errb.String()); last != "" {
			return "", errors.New(last)
		}
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// python -m and -c import from the working directory, and the daemon runs from /.
func absPath(p string) string {
	if strings.ContainsRune(p, '/') && !filepath.IsAbs(p) {
		if abs, err := filepath.Abs(p); err == nil {
			return abs
		}
	}
	return p
}

func tailLine(s string) string {
	s = strings.TrimSpace(s)
	return s[strings.LastIndexByte(s, '\n')+1:]
}

func writeScript(path string) error {
	if b, err := os.ReadFile(path); err == nil && string(b) == daemon.Script {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".lx_laya-*.py")
	if err != nil {
		return err
	}
	_, werr := tmp.WriteString(daemon.Script)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		os.Remove(tmp.Name())
	}
	return werr
}

func (e *layaEnv) start(args []string) int {
	fs := e.flags("start")
	wait := fs.Duration("wait", 2*time.Minute, "how long to wait for the model to load (0: don't wait)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if err := e.ready(); err != nil {
		return e.fail("start", "%v", err)
	}
	py, _ := e.paths.Python(e.getenv)
	if py == "" {
		return e.fail("start", "Laya is not set up: lx laya setup installs it (or set LX_LAYA_PYTHON to a python that has laya)")
	}
	py = absPath(py)
	c := laya.Client{Socket: e.paths.Socket}
	var exited chan error
	logFrom := int64(0)
	in, err := c.Ping(time.Second)
	pid := in.Pid
	switch {
	case err == nil && in.Version != laya.Protocol:
		return e.fail("start", "a daemon from another lx version is running (pid %d): lx laya stop, then lx laya start", in.Pid)
	case err == nil && in.Loaded:
		fmt.Fprintf(e.out, "Laya is already running (pid %d)\n", in.Pid)
		return 0
	case err == nil:
		fmt.Fprintf(e.out, "Laya is starting (pid %d), loading the model…\n", in.Pid)
	default:
		if err := writeScript(e.paths.Script); err != nil {
			return e.fail("start", "%v", err)
		}
		if err := os.MkdirAll(filepath.Dir(e.paths.Socket), 0o700); err != nil {
			return e.fail("start", "%v", err)
		}
		logf, err := os.OpenFile(e.paths.Log, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return e.fail("start", "%v", err)
		}
		if fi, err := logf.Stat(); err == nil {
			logFrom = fi.Size()
		}
		cmd := exec.Command(py, e.paths.Script, "--socket", e.paths.Socket, "--pid", e.paths.Pid, "--log", e.paths.Log)
		cmd.Env = append(os.Environ(), daemonEnv...)
		cmd.Stdout, cmd.Stderr = logf, logf
		cmd.Dir = "/"
		cmd.SysProcAttr = laya.DetachAttr()
		err = cmd.Start()
		logf.Close()
		if err != nil {
			return e.fail("start", "cannot run %s: %v", py, err)
		}
		exited = make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		pid = cmd.Process.Pid
		if *wait <= 0 {
			fmt.Fprintf(e.out, "Laya started (pid %d); lx laya status shows when the model is loaded\n", pid)
			return 0
		}
		fmt.Fprintf(e.out, "Laya started (pid %d), loading the model…\n", pid)
	}

	deadline := time.Now().Add(*wait)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			tries := 1
			if ee := (*exec.ExitError)(nil); errors.As(err, &ee) && ee.ExitCode() == daemonBusy {
				tries = 5
			}
			for i := range tries {
				if other, perr := c.Ping(200 * time.Millisecond); perr == nil {
					exited, pid = nil, other.Pid
					break
				}
				if i+1 < tries {
					time.Sleep(200 * time.Millisecond)
				}
			}
			if exited == nil {
				continue
			}
			fmt.Fprintf(e.errw, "lx laya start: the daemon exited (%v) before its model was ready; its log (%s) says:\n", exitText(err), e.tilde(e.paths.Log))
			fmt.Fprint(e.errw, logTail(e.paths.Log, logFrom, 15))
			return 1
		case <-tick.C:
		}
		in, err := c.Ping(500 * time.Millisecond)
		switch {
		case err == nil && in.Loaded:
			fmt.Fprintf(e.out, "Laya is running (pid %d): model %s, socket %s\n", in.Pid, in.Model, e.tilde(e.paths.Socket))
			return 0
		case err == nil:
			pid = in.Pid
		case exited == nil && pid > 0 && !processAlive(pid):
			fmt.Fprintf(e.errw, "lx laya start: the daemon (pid %d) exited before its model was ready; its log (%s) ends:\n", pid, e.tilde(e.paths.Log))
			fmt.Fprint(e.errw, logTail(e.paths.Log, logFrom, 15))
			return 1
		}
		if time.Now().After(deadline) {
			return e.fail("start", "the model is still loading after %v; lx laya status shows when it is ready (log: %s)", *wait, e.tilde(e.paths.Log))
		}
	}
}

func exitText(err error) string {
	if err == nil {
		return "status 0"
	}
	return err.Error()
}

func logTail(path string, from int64, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() < from {
		from = 0
	}
	f.Seek(max(from, 0), io.SeekStart)
	b, _ := io.ReadAll(io.LimitReader(f, 1<<20))
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 1 && lines[0] == "" {
		return "  (nothing)\n"
	}
	return "  " + strings.Join(lines, "\n  ") + "\n"
}

func (e *layaEnv) stop(args []string) int {
	if err := e.flags("stop").Parse(args); err != nil {
		return 2
	}
	if err := e.ready(); err != nil {
		return e.fail("stop", "%v", err)
	}
	c := laya.Client{Socket: e.paths.Socket}
	in, perr := c.Ping(time.Second)
	pid := in.Pid
	if perr == nil && c.Shutdown(2*time.Second) == nil && waitExit(pid, c, 10*time.Second) {
		fmt.Fprintf(e.out, "Laya stopped (pid %d)\n", pid)
		return 0
	}
	if pid == 0 {
		pid = readPid(e.paths.Pid)
	}
	if pid > 0 && processAlive(pid) {
		cmdline, err := processCommand(pid)
		if err != nil {
			return e.fail("stop", "cannot tell whether pid %d is lx's Laya daemon (%v); stop it yourself", pid, err)
		}
		if !strings.Contains(cmdline, "lx_laya.py") {
			return e.fail("stop", "pid %d (from %s) is not lx's Laya daemon; not touching it", pid, e.tilde(e.paths.Pid))
		}
		p, _ := os.FindProcess(pid)
		p.Signal(syscall.SIGTERM)
		if !waitExit(pid, c, 5*time.Second) {
			p.Kill()
			if !waitExit(pid, c, 2*time.Second) {
				return e.fail("stop", "pid %d is still running after SIGKILL", pid)
			}
		}
		e.cleanStale()
		fmt.Fprintf(e.out, "Laya stopped (pid %d)\n", pid)
		return 0
	}
	removed, left := e.cleanStale()
	switch {
	case left != nil:
		return e.fail("stop", "%s: %v, and no running daemon is recorded in %s; stop that process yourself",
			e.tilde(e.paths.Socket), left, e.tilde(e.paths.Pid))
	case removed:
		fmt.Fprintln(e.out, "Laya is not running (removed a stale socket)")
	default:
		fmt.Fprintln(e.out, "Laya is not running")
	}
	return 0
}

func waitExit(pid int, c laya.Client, d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if _, err := c.Ping(200 * time.Millisecond); err != nil && (pid <= 0 || !processAlive(pid)) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func (e *layaEnv) cleanStale() (removed bool, left error) {
	if fi, err := os.Lstat(e.paths.Socket); err == nil && fi.Mode().Type() == os.ModeSocket {
		_, err := laya.Client{Socket: e.paths.Socket}.Ping(200 * time.Millisecond)
		switch {
		case laya.Refused(err):
			left = os.Remove(e.paths.Socket)
			removed = left == nil
		case err != nil:
			left = err
		default:
			left = errors.New("a daemon still answers there")
		}
	}
	if pid := readPid(e.paths.Pid); pid > 0 && !processAlive(pid) {
		os.Remove(e.paths.Pid)
	}
	return removed, left
}

func readPid(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func processCommand(pid int) (string, error) {
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); err == nil {
		return strings.ReplaceAll(string(b), "\x00", " "), nil
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return "", fmt.Errorf("ps: %w", err)
	}
	return string(out), nil
}

type layaStatus struct {
	Ready   bool       `json:"ready"`
	Running bool       `json:"running"`
	SetUp   bool       `json:"set_up"`
	Python  string     `json:"python,omitempty"`
	Socket  string     `json:"socket"`
	Log     string     `json:"log"`
	PingMS  float64    `json:"ping_ms,omitempty"`
	Error   string     `json:"error,omitempty"`
	Daemon  *laya.Info `json:"daemon,omitempty"`
}

func (e *layaEnv) status(args []string) int {
	fs := e.flags("status")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	st := layaStatus{Socket: e.paths.Socket, Log: e.paths.Log}
	st.Python, _ = e.paths.Python(e.getenv)
	st.SetUp = st.Python != ""
	c := laya.Client{Socket: e.paths.Socket}
	t := time.Now()
	_, err := c.Ping(time.Second)
	lat := time.Since(t)
	if err == nil {
		in, serr := c.Status(2 * time.Second)
		if serr == nil {
			st.Running, st.Daemon = true, &in
			st.Ready = in.Loaded && in.Version == laya.Protocol
			st.PingMS = float64(lat.Microseconds()) / 1000
		} else {
			err = serr
		}
	}
	if err != nil {
		if fi, serr := os.Stat(e.paths.Socket); serr == nil && fi.Mode().Type() == os.ModeSocket {
			st.Error = err.Error()
		}
	}
	code := 0
	if !st.Ready {
		code = 1
	}
	if *asJSON {
		b, _ := json.MarshalIndent(st, "", "  ")
		fmt.Fprintln(e.out, string(b))
		return code
	}
	w := e.out
	in := st.Daemon
	switch {
	case in == nil && st.Error != "":
		fmt.Fprintf(w, "laya: not answering on %s: %s\n", e.tilde(e.paths.Socket), st.Error)
		fmt.Fprintln(w, "restart it: lx laya stop && lx laya start")
		return code
	case in == nil && st.SetUp:
		fmt.Fprintf(w, "laya: not running (set up with %s)\n", e.tilde(st.Python))
		fmt.Fprintln(w, "start it: lx laya start")
		return code
	case in == nil:
		fmt.Fprintln(w, "laya: not set up (optional): lx laya setup installs it")
		return code
	case in.Version != laya.Protocol:
		fmt.Fprintf(w, "laya: pid %d speaks protocol %d, this lx %d: lx laya stop && lx laya start\n", in.Pid, in.Version, laya.Protocol)
		return code
	case !in.Loaded:
		fmt.Fprintf(w, "laya: starting (pid %d, up %s), loading the model\n", in.Pid, fmtElapsed(secs(in.UptimeS)))
		return code
	}
	fmt.Fprintf(w, "laya: running (pid %d, up %s)\n", in.Pid, fmtElapsed(secs(in.UptimeS)))
	model := in.Model
	if in.Laya != "" {
		model += " (laya " + in.Laya + ")"
	}
	fmt.Fprintf(w, "  model   %s, loaded in %s\n", model, fmtElapsed(time.Duration(in.LoadMS)*time.Millisecond))
	fmt.Fprintf(w, "  socket  %s, ping %s\n", e.tilde(e.paths.Socket), fmtMS(lat))
	if in.RSS > 0 {
		mem := layaBytes(in.RSS)
		if in.RSSPeak {
			mem += " (peak)"
		}
		fmt.Fprintf(w, "  memory  %s\n", mem)
	}
	if l := in.Last; l != nil {
		judged := ""
		if l.Judged < l.Items {
			judged = fmt.Sprintf(" (%d before the deadline)", l.Judged)
		}
		ago := showAgo(time.Now(), time.Now().Add(-secs(l.AgoS)))
		fmt.Fprintf(w, "  last    %s%s in %s (model %s), %s\n", plural(l.Items, "item", "items"), judged,
			fmtMS(time.Duration(l.TotalMS)*time.Millisecond), fmtMS(time.Duration(l.ModelMS)*time.Millisecond), ago)
	} else {
		fmt.Fprintln(w, "  last    no requests yet")
	}
	if st.Python != "" {
		fmt.Fprintf(w, "  python  %s\n", e.tilde(st.Python))
	}
	fmt.Fprintf(w, "  log     %s\n", e.tilde(e.paths.Log))
	return code
}

func secs(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

func fmtMS(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return "<1 ms"
	case d < time.Second:
		return strconv.FormatInt(d.Milliseconds(), 10) + " ms"
	}
	return fmtElapsed(d)
}

func layaBytes(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%d MB", n>>20)
}
