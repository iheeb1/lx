//go:build unix

package cli

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tee"
)

func TestParseFitFlag(t *testing.T) {
	t.Setenv("LX_RAW", "")
	t.Setenv("LX_OFF", "")
	t.Setenv("LX_BUDGET", "")
	for args, want := range map[string]struct {
		n   int
		cut engine.Cut
	}{
		"--fit 40 go test ./...":           {40, engine.CutEither},
		"--fit=7 go test":                  {7, engine.CutEither},
		"-b 300 --fit 5 -v -- git status":  {5, engine.CutEither},
		"--fit 05 ls":                      {5, engine.CutEither},
		"--fit 3 --fit 9 ls":               {9, engine.CutEither},
		"--fit tail:40 go test ./...":      {40, engine.CutTail},
		"--fit=head:12 git log":            {12, engine.CutHead},
		"--fit head:3 --fit 9 ls":          {9, engine.CutEither},
		"go test ./... --fit 3":            {},
		"--budget 100 git log --fit 3 foo": {},
	} {
		o, argv, err := parseRunFlags(strings.Fields(args))
		if err != nil || o.fit != want.n || o.fitCut != want.cut || len(argv) == 0 {
			t.Errorf("%q: fit %d cut %d argv %q err %v, want %+v", args, o.fit, o.fitCut, argv, err, want)
		}
		if eo := o.engineOptions(); eo.MaxLines != want.n || eo.Cut != want.cut {
			t.Errorf("%q: engine options %+v", args, eo)
		}
	}
	for _, args := range []string{
		"--fit", "--fit git status", "--fit 0 ls", "--fit -3 ls", "--fit +3 ls", "--fit 4x ls",
		"--fit= ls", "--fit=0 ls", "--fit 99999999999999999999 ls", "--fit 1.5 ls", "--fit ' 3' ls",
		"--fit tail: ls", "--fit tail:0 ls", "--fit head:-3 ls", "--fit TAIL:3 ls", "--fit both:3 ls",
		"--fit tail:3:3 ls", "--fit=head: ls", "--fit :3 ls",
	} {
		if o, _, err := parseRunFlags(strings.Fields(args)); err == nil {
			t.Errorf("%q: accepted (fit %d)", args, o.fit)
		}
	}
	if code := Main([]string{"--fit", "git", "status"}); code != 2 {
		t.Errorf("lx --fit git status: exit %d, want 2 (nothing run)", code)
	}
}

func TestFitRoom(t *testing.T) {
	for _, c := range []struct{ fit, used, want int }{{0, 3, 0}, {10, 0, 10}, {10, 2, 8}, {3, 5, 1}} {
		if got := fitRoom(c.fit, c.used); got != c.want {
			t.Errorf("fitRoom(%d, %d) = %d, want %d", c.fit, c.used, got, c.want)
		}
	}
	o := runOpts{fit: 12}
	if eo := o.engineOptions(); eo.MaxLines != 12 {
		t.Errorf("engine options %+v", eo)
	}
}

const fitScript = `i=0; while [ $i -lt 300 ]; do i=$((i+1)); echo "step $i finished, 12 records written"; ` +
	`if [ $i = 150 ]; then echo "error: step 150 failed: disk full" >&2; fi; done; exit 3`

var receiptRe = regexp.MustCompile(`^\[lx: [\d,]+→\d+ lines \(−\d+%\) · full output: lx show \d+\]$`)

func nonEmptyLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func TestFitRun(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	p := startLx(t, []string{"LX_HEARTBEAT=off", "LX_PROMPT_IDLE=off"}, "--fit", "12", "sh", "-c", fitScript)
	if code := p.wait(10 * time.Second); code != 3 {
		t.Fatalf("exit %d, want the command's 3", code)
	}
	lines := nonEmptyLines(p.stdout.String())
	if len(lines)+len(nonEmptyLines(p.stderr.String())) > 12 {
		t.Fatalf("%d lines on stdout, stderr %q:\n%s", len(lines), p.stderr.String(), p.stdout.String())
	}
	if !strings.Contains(p.stdout.String(), "error: step 150 failed: disk full") ||
		!receiptRe.MatchString(lines[len(lines)-1]) || !strings.HasSuffix(lines[len(lines)-1], "lx show 1]") {
		t.Fatalf("view:\n%s", p.stdout.String())
	}
	if out, _, err := tee.Load(1); err != nil || !strings.Contains(out, "step 300 finished") {
		t.Fatalf("the full output must be stored: %v", err)
	}

	p = startLx(t, []string{"LX_HEARTBEAT=off", "LX_PROMPT_IDLE=off"}, "--fit", "40", "sh", "-c", "seq 1 37")
	if code := p.wait(10 * time.Second); code != 0 || p.stdout.String() != seq(37) {
		t.Fatalf("exit %d, stdout %q", code, p.stdout.String())
	}
}

func TestFitThroughHeadAndTail(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	for _, cut := range []string{"head -n 10", "tail -n 10", "head -10", "tail -10"} {
		sh := `"$LX" --fit 10 sh -c '` + fitScript + `' 2>&1 | ` + cut
		cmd := exec.Command("sh", "-c", sh)
		cmd.Env = append(lxEnv(t), "LX="+os.Args[0], "LX_HEARTBEAT=off", "LX_PROMPT_IDLE=off")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", cut, err)
		}
		lines := nonEmptyLines(string(out))
		if len(lines) > 10 || !strings.Contains(string(out), "error: step 150 failed: disk full") ||
			!receiptRe.MatchString(lines[len(lines)-1]) {
			t.Fatalf("| %s shows:\n%s", cut, out)
		}
	}
}

func TestFitThroughHeadAndTailEnds(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	for _, c := range []struct{ fit, cut, want, notWant string }{
		{"head:12", "head -n 12", "step 7 finished", "step 300 finished"},
		{"tail:12", "tail -n 12", "step 300 finished", "step 1 finished"},
	} {
		sh := `"$LX" --fit ` + c.fit + ` sh -c '` + fitScript + `' 2>&1 | ` + c.cut
		cmd := exec.Command("sh", "-c", sh)
		cmd.Env = append(lxEnv(t), "LX="+os.Args[0], "LX_HEARTBEAT=off", "LX_PROMPT_IDLE=off")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", c.cut, err)
		}
		lines := nonEmptyLines(string(out))
		if len(lines) > 12 || !strings.Contains(string(out), "error: step 150 failed: disk full") ||
			!receiptRe.MatchString(lines[len(lines)-1]) {
			t.Fatalf("| %s shows:\n%s", c.cut, out)
		}
		if !strings.Contains(string(out), c.want+",") || strings.Contains(string(out), c.notWant+",") {
			t.Errorf("--fit %s | %s: want %q, not %q:\n%s", c.fit, c.cut, c.want, c.notWant, out)
		}
	}
}

func lxEnv(t *testing.T) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "LX_") || strings.HasPrefix(kv, "CLAUDECODE=") || strings.HasPrefix(kv, "BASH_MAX_OUTPUT_LENGTH=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "LX_TEST_MAIN=1", "LX_TEE_DIR="+tee.Dir(), "LX_TRACK=0", "LX_DATA_DIR="+t.TempDir())
}

func TestFitLeavesRoomForNotices(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	p := startLx(t, []string{"LX_HEARTBEAT=200ms", "LX_PROMPT_IDLE=off"}, "--fit", "10", "sh", "-c", "sleep 0.6; "+fitScript)
	if code := p.wait(10 * time.Second); code != 3 {
		t.Fatalf("exit %d", code)
	}
	notices, view := nonEmptyLines(p.stderr.String()), nonEmptyLines(p.stdout.String())
	if len(notices) != 1 || !strings.HasPrefix(notices[0], "[lx: still running") {
		t.Fatalf("stderr = %q", p.stderr.String())
	}
	if len(notices)+len(view) > 10 || !strings.HasPrefix(view[len(view)-1], "[lx: ") {
		t.Fatalf("%d notice + %d view lines, over the cut of 10:\n%s", len(notices), len(view), p.stdout.String())
	}
}

func TestFitInterrupt(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	script := `n=0; trap 'n=$((n+1)); if [ "$n" -ge 2 ]; then seq 1 200; echo "error: cleanup failed" >&2; exit 7; fi' TERM; ` +
		`seq 1 500; while :; do sleep 0.1; done`
	p := startLx(t, []string{"LX_HEARTBEAT=200ms", "LX_PROMPT_IDLE=off"}, "--fit", "10", "sh", "-c", script)
	p.waitFor(p.stderr, "[lx: still running", 10*time.Second)
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	p.waitFor(p.stdout, "[lx: interrupted by SIGTERM", 10*time.Second)
	partial := p.stdout.String()
	if n := len(nonEmptyLines(p.stderr.String())) + len(nonEmptyLines(partial)); n > 10 || !strings.Contains(partial, "500") {
		t.Fatalf("notices + partial view = %d lines:\n%s", n, partial)
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	if code := p.wait(5 * time.Second); code != 7 {
		t.Fatalf("exit %d, want 7", code)
	}
	rest := nonEmptyLines(strings.TrimPrefix(p.stdout.String(), partial))
	if len(rest) > 10 || !strings.Contains(strings.Join(rest, "\n"), "error: cleanup failed") ||
		!strings.HasPrefix(rest[len(rest)-1], "[lx: command exited 7 after SIGTERM") {
		t.Fatalf("rest (%d lines):\n%s", len(rest), strings.Join(rest, "\n"))
	}
}

func TestFitLiveOptions(t *testing.T) {
	t.Setenv("LX_MAX_CHARS", "0")
	l := &liveRun{opts: runOpts{fit: 20}, stderr: &noticeCounter{w: &strings.Builder{}}}
	l.stderr.Write([]byte("[lx: still running]\n"))
	if got := l.partialOptions().MaxLines; got != 18 {
		t.Errorf("partial view room %d, want 18", got)
	}
	if got := l.restOptions().MaxLines; got != 18 {
		t.Errorf("rest view room %d, want 18", got)
	}
	l = &liveRun{opts: runOpts{}}
	if l.partialOptions().MaxLines != 0 || l.restOptions().MaxLines != 0 {
		t.Error("no --fit: no line cut")
	}
}
