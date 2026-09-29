//go:build unix

package cli

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/runner"
	"github.com/iheeb1/lx/internal/tee"
	"github.com/iheeb1/lx/internal/testenv"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type lxProc struct {
	t              *testing.T
	cmd            *exec.Cmd
	stdout, stderr *syncBuf
	start          time.Time
	done           chan struct{}
	exit           int
}

func startLx(t *testing.T, env []string, args ...string) *lxProc {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "LX_") || strings.HasPrefix(kv, "CLAUDECODE=") || strings.HasPrefix(kv, "BASH_MAX_OUTPUT_LENGTH=") {
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	cmd.Env = append(cmd.Env, "LX_TEST_MAIN=1", "LX_TEE_DIR="+tee.Dir(), "LX_TRACK=0", "LX_DATA_DIR="+t.TempDir(), "LX_LAYA=0")
	cmd.Env = append(cmd.Env, env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p := &lxProc{t: t, cmd: cmd, stdout: &syncBuf{}, stderr: &syncBuf{}, done: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = p.stdout, p.stderr
	p.start = time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		err := cmd.Wait()
		p.exit = 0
		if ee, ok := err.(*exec.ExitError); ok {
			p.exit = ee.ExitCode()
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				p.exit = 128 + int(ws.Signal())
			}
		}
		close(p.done)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-p.done
	})
	return p
}

func (p *lxProc) waitFor(buf *syncBuf, substr string, within time.Duration) time.Time {
	p.t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), substr) {
			return time.Now()
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.t.Fatalf("no %q within %v\nstdout: %s\nstderr: %s", substr, within, p.stdout.String(), p.stderr.String())
	return time.Time{}
}

func (p *lxProc) wait(within time.Duration) int {
	p.t.Helper()
	within = testenv.Scale(within)
	select {
	case <-p.done:
		return p.exit
	case <-time.After(within):
		p.t.Fatalf("lx did not exit within %v\nstdout: %s\nstderr: %s", within, p.stdout.String(), p.stderr.String())
	}
	return -1
}

func seq(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	return b.String()
}

var heartbeatIDRe = regexp.MustCompile(`output so far: lx show (\d+) --tail 40\]`)

func TestHeartbeatRunSurvivesSIGKILL(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	p := startLx(t, []string{"LX_HEARTBEAT=300ms", "LX_PROMPT_IDLE=off"}, "sh", "-c", "seq 1 500; sleep 30")
	p.waitFor(p.stderr, "[lx: still running", 10*time.Second)
	time.Sleep(time.Until(p.start.Add(1500 * time.Millisecond)))
	_ = p.cmd.Process.Kill()
	p.wait(5 * time.Second)

	var beats []string
	for _, ln := range strings.Split(p.stderr.String(), "\n") {
		if strings.HasPrefix(ln, "[lx: still running after ") {
			beats = append(beats, ln)
		}
	}
	if len(beats) != 1 {
		t.Fatalf("want exactly one heartbeat line, got %q", beats)
	}
	m := heartbeatIDRe.FindStringSubmatch(beats[0])
	if m == nil || !strings.Contains(beats[0], " · 500 lines so far · this is not the result · ") {
		t.Fatalf("heartbeat line = %q", beats[0])
	}
	id, _ := strconv.Atoi(m[1])
	out, meta, err := tee.Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if out != seq(500) {
		t.Fatalf("stored partial output = %d bytes, want the 500 lines", len(out))
	}
	if !strings.HasPrefix(meta.StatusNote(), "incomplete: ") {
		t.Fatalf("StatusNote = %q (meta %+v)", meta.StatusNote(), meta)
	}
	if p.stdout.String() != "" {
		t.Fatalf("stdout = %q", p.stdout.String())
	}
}

func TestInterruptPrintsPartialView(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	script := `n=0; trap 'n=$((n+1)); if [ "$n" -ge 2 ]; then echo "error: cleanup failed" >&2; exit 7; fi' TERM; ` +
		`seq 1 500; while :; do sleep 0.1; done`
	p := startLx(t, []string{"LX_HEARTBEAT=200ms", "LX_PROMPT_IDLE=off"}, "sh", "-c", script)
	p.waitFor(p.stderr, "[lx: still running", 10*time.Second)
	hb := heartbeatIDRe.FindStringSubmatch(p.stderr.String())
	if hb == nil {
		t.Fatalf("heartbeat = %q", p.stderr.String())
	}

	sent := time.Now()
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	got := p.waitFor(p.stdout, "[lx: interrupted by SIGTERM after ", 10*time.Second)
	limit := 3 * time.Second
	if testenv.Race {
		limit += 2 * time.Second
	}
	if d := got.Sub(sent); d > limit {
		t.Fatalf("partial view after %v, want within %v", d, limit)
	}
	partial := p.stdout.String()
	want := "; the command is still stopping · partial view of 500 lines · output so far: lx show " + hb[1] + "]\n"
	if !strings.HasSuffix(partial, want) {
		t.Fatalf("partial view ends %q, want suffix %q", partial, want)
	}
	if !strings.Contains(partial, "500") {
		t.Fatalf("partial view lost the last line: %q", partial)
	}
	select {
	case <-p.done:
		t.Fatal("lx exited although the command is still running")
	default:
	}

	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	if code := p.wait(5 * time.Second); code != 7 {
		t.Fatalf("exit %d, want the command's 7", code)
	}
	rest := strings.TrimPrefix(p.stdout.String(), partial)
	wantRest := "[lx: printed after the partial view:]\nerror: cleanup failed\n[lx: command exited 7 after SIGTERM · full output: lx show " + hb[1] + "]\n"
	if rest != wantRest {
		t.Fatalf("after the partial view:\n%s\nwant:\n%s", rest, wantRest)
	}
	id, _ := strconv.Atoi(hb[1])
	out, meta, err := tee.Load(id)
	if err != nil || out != seq(500)+"error: cleanup failed\n" || meta.Exit != 7 || meta.StatusNote() != "" {
		t.Fatalf("stored run: %v %q %+v", err, out, meta)
	}
	if strings.Count(p.stderr.String(), "[lx: still running") != 1 {
		t.Fatalf("stderr = %q", p.stderr.String())
	}
}

func TestInterruptedReceipt(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	p := startLx(t, []string{"LX_HEARTBEAT=100ms", "LX_PROMPT_IDLE=off"}, "sh", "-c", "seq 1 20000; exec sleep 30")
	p.waitFor(p.stderr, "[lx: still running", 10*time.Second)
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	if code := p.wait(5 * time.Second); code != 143 {
		t.Fatalf("exit %d, want 143", code)
	}
	out := p.stdout.String()
	if !regexp.MustCompile(`\n\[lx: 20,00\d→\d+ lines \(−\d+%\) · full output: lx show 1 · interrupted by SIGTERM\]\n$`).MatchString(out) {
		t.Fatalf("receipt: %q", out[max(0, len(out)-200):])
	}
	if !strings.Contains(out, "\n20000\n") {
		t.Fatal("the view lost the last line")
	}
	if strings.Contains(out, "partial view") {
		t.Fatal("a partial view for a command that stopped at once")
	}
}

func TestFastRunIsUnchanged(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	p := startLx(t, nil, "sh", "-c", "echo hi; echo oops >&2; exit 3")
	if code := p.wait(10 * time.Second); code != 3 {
		t.Fatalf("exit %d", code)
	}
	if p.stdout.String() != "hi\n" || p.stderr.String() != "oops\n" {
		t.Fatalf("stdout %q stderr %q", p.stdout.String(), p.stderr.String())
	}
	if r := tee.Recent(5); len(r) != 0 {
		t.Fatalf("a small passthrough run was stored: %+v", r)
	}
}

func TestPromptNotice(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	p := startLx(t, []string{"LX_HEARTBEAT=off", "LX_PROMPT_IDLE=300ms"}, "sh", "-c", `printf 'Proceed? [y/N] '; sleep 1`)
	if code := p.wait(10 * time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := "[lx: the command may be waiting for input: \"Proceed? [y/N] \" — nothing is answering it]\n"
	if p.stderr.String() != want {
		t.Fatalf("stderr = %q, want %q", p.stderr.String(), want)
	}
	if !strings.Contains(p.stdout.String(), "Proceed? [y/N]") {
		t.Fatalf("the prompt itself must still be shown: %q", p.stdout.String())
	}
}

func TestHeartbeatWithoutTee(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	p := startLx(t, []string{"LX_HEARTBEAT=200ms", "LX_PROMPT_IDLE=off", "LX_TEE=0"}, "sh", "-c", "echo 'error: first'; sleep 0.6; echo done")
	if code := p.wait(10 * time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := regexp.MustCompile(`^\[lx: still running after 0\.[2-9]s · 1 line so far \(1 error line\) · this is not the result\]\n$`)
	if !want.MatchString(p.stderr.String()) {
		t.Fatalf("stderr = %q", p.stderr.String())
	}
	if p.stdout.String() != "error: first\ndone\n" {
		t.Fatalf("stdout = %q", p.stdout.String())
	}
}

func TestHeartbeatRunAlwaysStored(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	p := startLx(t, []string{"LX_HEARTBEAT=100ms", "LX_PROMPT_IDLE=off"}, "sh", "-c", "echo start; sleep 0.4; echo end")
	if code := p.wait(10 * time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	hb := heartbeatIDRe.FindStringSubmatch(p.stderr.String())
	if hb == nil {
		t.Fatalf("stderr = %q", p.stderr.String())
	}
	id, _ := strconv.Atoi(hb[1])
	out, m, err := tee.Load(id)
	if err != nil || out != "start\nend\n" || m.StatusNote() != "" || m.Exit != 0 {
		t.Fatalf("Load(%d) = %q %+v %v", id, out, m, err)
	}
	if p.stdout.String() != "start\nend\n" {
		t.Fatalf("stdout = %q", p.stdout.String())
	}
}

func TestEnvDuration(t *testing.T) {
	cases := map[string]time.Duration{
		"":      30 * time.Second,
		"0":     0,
		"off":   0,
		"OFF":   0,
		"45s":   45 * time.Second,
		"300ms": 300 * time.Millisecond,
		"90":    90 * time.Second,
		"1.5":   1500 * time.Millisecond,
		"-5s":   0,
		"soon":  30 * time.Second,
	}
	for v, want := range cases {
		t.Setenv("LX_TEST_DURATION", v)
		if got := envDuration("LX_TEST_DURATION", 30*time.Second); got != want {
			t.Errorf("%q: %v, want %v", v, got, want)
		}
	}
}

func TestFormatting(t *testing.T) {
	for d, want := range map[time.Duration]string{
		300 * time.Millisecond:                  "0.3s",
		2 * time.Second:                         "2.0s",
		30 * time.Second:                        "30s",
		2*time.Minute + 3*time.Second:           "2m03s",
		time.Hour + 4*time.Minute + time.Second: "1h04m",
		59*time.Minute + 59*time.Second + 600e6: "1h00m",
	} {
		if got := fmtElapsed(d); got != want {
			t.Errorf("fmtElapsed(%v) = %q, want %q", d, got, want)
		}
	}
	for n, want := range map[int]string{0: "0 lines", 1: "1 line", 999: "999 lines", 1204: "1,204 lines", 1234567: "1,234,567 lines"} {
		if got := plural(n, "line", "lines"); got != want {
			t.Errorf("plural(%d) = %q, want %q", n, got, want)
		}
	}
	if countLines(nil) != 0 || countLines([]byte("a")) != 1 || countLines([]byte("a\nb\n")) != 2 || countLines([]byte("a\nb")) != 2 {
		t.Error("countLines")
	}
}

func TestCountErrorLines(t *testing.T) {
	out := "ok\nerror: one\n\x1b[31mFAIL\x1b[0m pkg\nall 0 errors\n"
	if n, w := countErrorLines([]byte(out)); n != 2 || w {
		t.Fatalf("got %d (windowed %v), want 2", n, w)
	}
	big := strings.Repeat("error: old\n", 10) + strings.Repeat("x\n", errScanWindow/2) + "error: new\n"
	if n, w := countErrorLines([]byte(big)); n != 1 || !w {
		t.Fatalf("windowed count = %d (%v), want 1 in the last 1 MB", n, w)
	}
}

func TestLiveRunCallbacks(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	t.Setenv("LX_MAX_CHARS", "0")
	var so, se bytes.Buffer
	l := &liveRun{argv: []string{"make"}, cwd: "/w", start: time.Now(), stdout: &so, stderr: &se}
	sofar := []byte(strings.Repeat("compiling\n", 1203) + "error: boom\n")
	w := l.heartbeat(30*time.Second, sofar)
	if w == nil || l.spool == nil || !l.hbPrinted {
		t.Fatal("heartbeat did not reserve a run")
	}
	want := "[lx: still running after 30s · 1,204 lines so far (1 error line) · this is not the result · output so far: lx show 1 --tail 40]\n"
	if se.String() != want {
		t.Fatalf("heartbeat line %q, want %q", se.String(), want)
	}
	if out, m, _ := tee.Load(1); out != string(sofar) || m.State != tee.StateRunning || m.Argv[0] != "make" {
		t.Fatalf("spooled %d bytes, meta %+v", len(out), m)
	}

	se.Reset()
	l.prompt("Password: ")
	if !strings.HasPrefix(se.String(), `[lx: the command may be waiting for input: "Password: "`) {
		t.Fatalf("prompt line %q", se.String())
	}

	if w := l.interrupt(syscall.SIGTERM, 123*time.Second, sofar); w != nil {
		t.Fatalf("interrupt returned a second spool %T", w)
	}
	if !l.partialShown || l.partialRaw != string(sofar) || l.partialSig != "SIGTERM" {
		t.Fatalf("partial state %+v", l)
	}
	if !strings.HasSuffix(so.String(), "\n[lx: interrupted by SIGTERM after 2m03s; the command is still stopping · partial view of 1,204 lines · output so far: lx show 1]\n") ||
		!strings.Contains(so.String(), "error: boom") {
		t.Fatalf("partial view %q", so.String())
	}

	t.Setenv("LX_TEE", "0")
	se.Reset()
	l2 := &liveRun{argv: []string{"make"}, start: time.Now(), stdout: &so, stderr: &se}
	if w := l2.heartbeat(time.Second, nil); w != nil {
		t.Fatalf("heartbeat without tee returned %T", w)
	}
	if strings.Contains(se.String(), "lx show") || !strings.Contains(se.String(), "0 lines so far") {
		t.Fatalf("heartbeat line %q", se.String())
	}
	so.Reset()
	if w := l2.interrupt(os.Interrupt, time.Second, nil); w != nil {
		t.Fatalf("interrupt without tee returned %T", w)
	}
	if so.String() != "[lx: interrupted by SIGINT after 1.0s; the command is still stopping · no output so far]\n" {
		t.Fatalf("empty partial view %q", so.String())
	}
}

func TestInterruptViewsFitHostCap(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	t.Setenv("LX_MAX_CHARS", "8000")
	var so, se bytes.Buffer
	l := &liveRun{argv: []string{"make"}, cwd: "/w", start: time.Now(), stdout: &so, stderr: &se}
	var before, after strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&before, "step %d: cc -O2 -c src/module_%d.c -o build/module_%d.o (%d warnings suppressed)\n", i, i*7, i*7, i%5)
		fmt.Fprintf(&after, "teardown %d: releasing lock %x for worker-%d\n", i, i*977, i%13)
	}
	after.WriteString("error: cleanup failed: state lock still held\n")
	sofar := []byte(before.String())
	l.interrupt(syscall.SIGTERM, 3*time.Second, sofar)
	partial := so.Len()
	if partial > 8000*3/4 {
		t.Fatalf("partial view is %d bytes, want at most 3/4 of the cap", partial)
	}
	res := runner.Result{Output: before.String() + after.String(), ExitCode: 143, Interrupted: "SIGTERM"}
	l.finishAfterPartial(&engine.Context{Argv: l.argv, Cwd: "/w", Exit: 143}, res)
	if so.Len() > 8000 {
		t.Fatalf("partial view + rest = %d bytes, over the 8000-char host limit", so.Len())
	}
	rest := so.String()[partial:]
	if !regexp.MustCompile(`^\[lx: printed after the partial view \(2,001→\d+ lines\):\]\n`).MatchString(rest) ||
		!strings.Contains(rest, "error: cleanup failed: state lock still held") ||
		!strings.HasSuffix(rest, "[lx: command exited 143 after SIGTERM · full output: lx show 1]\n") {
		t.Fatalf("rest view:\n%s", rest)
	}
	if out, m, err := tee.Load(1); err != nil || out != res.Output || m.Exit != 143 {
		t.Fatalf("stored run: %v, %d bytes, %+v", err, len(out), m)
	}
}

func TestInterruptViewsFitHostCapWorstCase(t *testing.T) {
	var before, after strings.Builder
	for i := 0; i < 800; i++ {
		fmt.Fprintf(&before, "src/module_%d.c:%d:5: error: use of undeclared identifier 'x%d'\n", i, i, i)
		fmt.Fprintf(&after, "cleanup/worker_%d.go:%d: error: lock %x still held by worker-%d\n", i, i, i*977, i)
	}
	for _, limit := range []int{1000, 8000} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("LX_TEE_DIR", dir)
			t.Setenv("LX_MAX_CHARS", strconv.Itoa(limit))
			os.WriteFile(dir+"/.seq", []byte("1073741000\n"), 0o600)
			var so, se bytes.Buffer
			l := &liveRun{argv: []string{"make"}, cwd: "/w", start: time.Now(), stdout: &so, stderr: &se}
			l.interrupt(syscall.SIGHUP, 64*time.Minute, []byte(before.String()))
			res := runner.Result{Output: before.String() + after.String(), ExitCode: 129, Interrupted: "SIGHUP"}
			l.finishAfterPartial(&engine.Context{Argv: l.argv, Cwd: "/w", Exit: 129}, res)
			out := so.String()
			if !strings.Contains(out, "lx show 1073741001]") {
				t.Fatalf("no 10-digit id in %q", out[max(0, len(out)-300):])
			}
			if !strings.HasSuffix(out, "[lx: command exited 129 after SIGHUP · full output: lx show 1073741001]\n") {
				t.Fatalf("no exit line: %q", out[max(0, len(out)-300):])
			}
			if limit < 2000 {

				if len(out) > limit {
					t.Fatalf("partial view + rest = %d bytes, over the host limit %d", len(out), limit)
				}
				return
			}
			if capc := hostCharCap(); len(out) > capc {
				t.Fatalf("partial view + rest = %d bytes, over the cap %d (limit %d)", len(out), capc, limit)
			}
			if !strings.Contains(out, "cleanup/worker_799.go:799: error") {
				t.Fatalf("the last error printed while stopping is missing:\n%s", out)
			}
		})
	}
}

func TestLiveLinesFitTheirRoom(t *testing.T) {
	const id = "1073741823"
	if n := len(interruptedLine("SIGTERM", 999*time.Hour, 9_999_999, id)); n > liveLineRoom {
		t.Errorf("interrupted line is %d bytes, room %d", n, liveLineRoom)
	}
	header := "[lx: the full view, now that the command has exited (9,999,999→9,999,999 lines):]\n"
	if n := len(header) + len(exitedLine(255, "SIGTERM", id)); n > liveLineRoom {
		t.Errorf("header + exit line are %d bytes, room %d", n, liveLineRoom)
	}
}

func TestRestViewSaysCondensedWithoutTee(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	t.Setenv("LX_TEE", "0")
	t.Setenv("LX_MAX_CHARS", "0")
	var so, se bytes.Buffer
	l := &liveRun{argv: []string{"make"}, cwd: "/w", start: time.Now(), stdout: &so, stderr: &se}
	l.interrupt(syscall.SIGTERM, 3*time.Second, []byte("building\n"))
	partial := so.Len()
	after := strings.Repeat("waiting for worker to drain\n", 5000) + "error: drain timed out\n"
	res := runner.Result{Output: "building\n" + after, ExitCode: 1, Interrupted: "SIGTERM"}
	l.finishAfterPartial(&engine.Context{Argv: l.argv, Cwd: "/w", Exit: 1}, res)
	rest := so.String()[partial:]
	if !regexp.MustCompile(`^\[lx: printed after the partial view \(5,001→\d+ lines\):\]\n`).MatchString(rest) {
		t.Fatalf("a condensed rest view without a receipt must say so:\n%s", rest)
	}
	if !strings.Contains(rest, "error: drain timed out") || !strings.HasSuffix(rest, "[lx: command exited 1 after SIGTERM]\n") {
		t.Fatalf("rest view:\n%s", rest)
	}
}

func TestPipelinePanicKeepsOutputAndExit(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	env := []string{"LX_TEST_PANIC=1", "LX_HEARTBEAT=off", "LX_PROMPT_IDLE=off"}
	p := startLx(t, env, "sh", "-c", "seq 1 3000; echo 'error: the real failure' >&2; exit 3")
	if code := p.wait(10 * time.Second); code != 3 {
		t.Fatalf("exit %d, want the command's 3\nstderr: %s", code, p.stderr.String())
	}
	if p.stdout.String() != seq(3000) || !strings.Contains(p.stderr.String(), "error: the real failure\n") ||
		!strings.Contains(p.stderr.String(), "lx: internal error while condensing") {
		t.Fatalf("stdout %d bytes, stderr %q", len(p.stdout.String()), p.stderr.String())
	}

	script := `n=0; trap 'n=$((n+1)); if [ "$n" -ge 2 ]; then echo "error: stopping failed"; exit 6; fi' TERM; ` +
		`seq 1 50; while :; do sleep 0.1; done`
	p = startLx(t, env, "sh", "-c", script)
	time.Sleep(300 * time.Millisecond)
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	p.waitFor(p.stdout, "[lx: interrupted by SIGTERM after ", 10*time.Second)
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	if code := p.wait(10 * time.Second); code != 6 {
		t.Fatalf("exit %d, want the command's 6\nstdout: %s\nstderr: %s", code, p.stdout.String(), p.stderr.String())
	}
	out := p.stdout.String()
	if !strings.HasPrefix(out, seq(50)) || !strings.Contains(out, "error: stopping failed\n[lx: command exited 6 after SIGTERM") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestSelfCommandFallsBackToAbsolutePath(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	got := selfCommand()
	if got == "lx" || !filepath.IsAbs(got) {
		t.Fatalf("selfCommand() = %q with lx off PATH; want this binary's absolute path", got)
	}
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "lx"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if got := selfCommand(); got != "lx" {
		t.Fatalf("selfCommand() = %q with lx on PATH; want lx", got)
	}
}
