//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUnstorableRunPrintsPlainOutput(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through read-only directories")
	}
	store := filepath.Join(t.TempDir(), "runs")
	os.MkdirAll(store, 0o700)
	os.Chmod(store, 0o500)
	t.Cleanup(func() { os.Chmod(store, 0o700) })
	script := `i=0; while [ $i -lt 3000 ]; do echo "INFO request $i served in 3ms"; i=$((i+1)); done; echo "error: boom"`
	p := startLx(t, []string{"LX_TEE_DIR=" + store, "LX_HEARTBEAT=off", "LX_PROMPT_IDLE=off", "LX_MAX_CHARS=0"}, "sh", "-c", script)
	if code := p.wait(20 * time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	var want strings.Builder
	for i := 0; i < 3000; i++ {
		want.WriteString("INFO request " + strconv.Itoa(i) + " served in 3ms\n")
	}
	want.WriteString("error: boom\n")
	if got := p.stdout.String(); got != want.String() {
		t.Fatalf("a run lx could not store printed a view (%d bytes):\n%s", len(got), got[max(0, len(got)-400):])
	}
}

func TestSandboxedRunUsesFallbackStore(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through read-only directories")
	}
	home, tmp := t.TempDir(), t.TempDir()
	os.Chmod(home, 0o500)
	t.Cleanup(func() { os.Chmod(home, 0o700) })
	env := []string{"LX_TEE_DIR=", "HOME=" + home, "XDG_CACHE_HOME=", "TMPDIR=" + tmp, "LX_HEARTBEAT=off", "LX_PROMPT_IDLE=off", "LX_MAX_CHARS=0"}
	script := `i=0; while [ $i -lt 3000 ]; do echo "INFO request $i served in 3ms"; i=$((i+1)); done; echo "error: boom"`
	p := startLx(t, env, "sh", "-c", script)
	if code := p.wait(20 * time.Second); code != 0 {
		t.Fatalf("exit %d", code)
	}
	m := regexp.MustCompile(`full output: lx show (\d+)\]\n$`).FindStringSubmatch(p.stdout.String())
	if m == nil {
		t.Fatalf("no lx show pointer:\n%s", p.stdout.String())
	}
	show := startLx(t, env, "show", m[1], "--tail", "1")
	show.wait(10 * time.Second)
	if !strings.Contains(show.stdout.String(), "error: boom") {
		t.Fatalf("lx show %s: %s %s", m[1], show.stdout.String(), show.stderr.String())
	}
	if _, err := os.Stat(filepath.Join(tmp, "lx-"+strconv.Itoa(os.Getuid()), "runs", m[1]+".log")); err != nil {
		t.Fatal("the run is not in the fallback store:", err)
	}
}
