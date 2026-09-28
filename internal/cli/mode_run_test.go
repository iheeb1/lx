//go:build unix

package cli

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/tee"
)

var modeReceiptRe = regexp.MustCompile(`^\[lx: [\d,]+→\d+ lines \(−\d+%\) · mode ([a-z→]+) · full output: lx show \d+\]$`)

func TestModeRun(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	quiet := []string{"LX_HEARTBEAT=off", "LX_PROMPT_IDLE=off"}
	for _, c := range []struct {
		env  []string
		args []string
		exit int
		mode string
	}{
		{nil, []string{"-m", "verify", "sh", "-c", fitScript}, 3, "verify→error"},
		{nil, []string{"--mode=error", "sh", "-c", fitScript}, 3, "error"},
		{[]string{"LX_MODE=minimal"}, []string{"sh", "-c", fitScript}, 3, "minimal"},
		{[]string{"LX_MODE=error"}, []string{"-m", "debug", "sh", "-c", fitScript}, 3, "debug"},
		{nil, []string{"-m", "verify", "sh", "-c", "seq 1 3000"}, 0, "verify"},
	} {
		p := startLx(t, append(append([]string(nil), quiet...), c.env...), c.args...)
		if code := p.wait(10 * time.Second); code != c.exit {
			t.Fatalf("%q: exit %d, want the command's %d", c.args, code, c.exit)
		}
		lines := nonEmptyLines(p.stdout.String())
		m := modeReceiptRe.FindStringSubmatch(lines[len(lines)-1])
		if m == nil || m[1] != c.mode {
			t.Fatalf("%q %q: receipt %q, want mode %s", c.env, c.args, lines[len(lines)-1], c.mode)
		}
		if c.exit != 0 && !strings.Contains(p.stdout.String(), "error: step 150 failed: disk full") {
			t.Fatalf("%q: the error line is gone:\n%s", c.args, p.stdout.String())
		}
	}

	p := startLx(t, quiet, "sh", "-c", fitScript)
	if code := p.wait(10 * time.Second); code != 3 {
		t.Fatalf("auto: exit %d", code)
	}
	lines := nonEmptyLines(p.stdout.String())
	if !receiptRe.MatchString(lines[len(lines)-1]) {
		t.Fatalf("auto receipt %q", lines[len(lines)-1])
	}
	if out, _, err := tee.Load(1); err != nil || !strings.Contains(out, "step 300 finished") {
		t.Fatalf("the full output must be stored: %v", err)
	}
}

func TestModeVerboseAndErrors(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	p := startLx(t, []string{"LX_HEARTBEAT=off", "LX_PROMPT_IDLE=off"}, "-v", "-m", "verify", "sh", "-c", fitScript)
	if code := p.wait(10 * time.Second); code != 3 || !strings.Contains(p.stderr.String(), " mode=error ") {
		t.Fatalf("exit %d, stderr %q", code, p.stderr.String())
	}
	p = startLx(t, []string{"LX_HEARTBEAT=off"}, "-v", "sh", "-c", fitScript)
	if code := p.wait(10 * time.Second); code != 3 || strings.Contains(p.stderr.String(), "mode=") {
		t.Fatalf("auto: exit %d, stderr %q", code, p.stderr.String())
	}
	p = startLx(t, nil, "-m", "bogus", "sh", "-c", "echo ran")
	if code := p.wait(10 * time.Second); code != 2 || p.stdout.String() != "" ||
		!strings.Contains(p.stderr.String(), `unknown mode "bogus"`) || !strings.Contains(p.stderr.String(), "verify") {
		t.Fatalf("-m bogus: exit %d, stdout %q, stderr %q", code, p.stdout.String(), p.stderr.String())
	}
	p = startLx(t, []string{"LX_MODE=bogus"}, "sh", "-c", "echo ran")
	if code := p.wait(10 * time.Second); code != 2 || p.stdout.String() != "" || !strings.Contains(p.stderr.String(), `LX_MODE "bogus"`) {
		t.Fatalf("LX_MODE=bogus: exit %d, stdout %q, stderr %q", code, p.stdout.String(), p.stderr.String())
	}
}

func TestPipeMode(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString("step finished, records written to the shard\n")
		if i == 200 {
			b.WriteString("error: step 200 failed: disk full\n")
		}
	}
	in := b.String()
	pipe := func(env []string, args ...string) (string, string, int) {
		cmd := exec.Command(os.Args[0], append([]string{"pipe"}, args...)...)
		cmd.Env = append(lxEnv(t), env...)
		cmd.Stdin = strings.NewReader(in)
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		return out.String(), errb.String(), code
	}
	for _, c := range []struct {
		env  []string
		args []string
		note string
	}{
		{nil, []string{"--mode", "verify", "--as", "make", "--exit", "2"}, " · mode verify→error]"},
		{nil, []string{"-m", "minimal", "--as", "make"}, " · mode minimal]"},
		{[]string{"LX_MODE=debug"}, []string{"--as", "make"}, " · mode debug]"},
		{nil, []string{"--as", "make"}, "%)]"},
	} {
		out, errOut, code := pipe(c.env, c.args...)
		lines := nonEmptyLines(out)
		if code != 0 || len(lines) == 0 || !strings.HasSuffix(lines[len(lines)-1], c.note) || !strings.Contains(out, "error: step 200 failed") {
			t.Fatalf("%q %q: exit %d, stderr %q:\n%s", c.env, c.args, code, errOut, out)
		}
		if len(c.env)+len(c.args) == 2 && strings.Contains(out, "mode") {
			t.Fatalf("auto names a mode: %q", lines[len(lines)-1])
		}
	}
	if _, errOut, code := pipe(nil, "--mode", "fast"); code != 2 || !strings.Contains(errOut, `unknown mode "fast"`) {
		t.Fatalf("--mode fast: exit %d, stderr %q", code, errOut)
	}
	if _, errOut, code := pipe([]string{"LX_MODE=fast"}); code != 2 || !strings.Contains(errOut, `unknown LX_MODE "fast"`) {
		t.Fatalf("LX_MODE=fast: exit %d, stderr %q", code, errOut)
	}

	for _, c := range []struct {
		env  []string
		args []string
		want string
	}{
		{nil, []string{"--mode="}, `unknown mode ""`},
		{nil, []string{"-m", ""}, `unknown mode ""`},
		{[]string{"LX_MODE=debug"}, []string{"--mode="}, `unknown mode ""`},
		{[]string{"LX_MODE=fast"}, []string{"--mode", "verify"}, `unknown LX_MODE "fast"`},
	} {
		if out, errOut, code := pipe(c.env, c.args...); code != 2 || out != "" || !strings.Contains(errOut, c.want) {
			t.Errorf("%q %q: exit %d, stdout %q, stderr %q", c.env, c.args, code, out, errOut)
		}
	}

	if _, errOut, code := pipe(nil, "--stats", "-m", "verify", "--as", "make", "--exit", "2"); code != 0 || !strings.Contains(errOut, " mode=error") {
		t.Errorf("--stats: exit %d, stderr %q", code, errOut)
	}
	if _, errOut, _ := pipe(nil, "--stats", "--as", "make"); strings.Contains(errOut, "mode=") {
		t.Errorf("--stats in auto names a mode: %q", errOut)
	}
}
