//go:build unix

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestFailingRunFitsClaudeCode(t *testing.T) {
	dir := t.TempDir()
	gen := filepath.Join(dir, "gen.sh")
	script := `awk -v code="$1" 'BEGIN {
  srand(7)
  n = split("the of and to in is for that with on as by this from are be or it we can use not all new one may has was but if its also each when you your into see run set configuration repository dependency implementation documentation performance compatibility initialization authentication serialization transformation environment deployment middleware subscription notification", w, " ")
  for (i = 0; i < 900; i++) {
    line = ""
    for (j = 6 + int(rand() * 10); j > 0; j--) line = line w[1 + int(rand() * n)] " "
    print line
    if (i % 60 == 7) printf "src/module_%d.c:%d:5: error: use of undeclared identifier x%d\n", i, i, i
  }
  exit code
}'
`
	if err := os.WriteFile(gen, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"CLAUDECODE": "1", "CLAUDE_CONFIG_DIR": t.TempDir(), "CLAUDE_PROJECT_DIR": dir, "LX_TEE_DIR": t.TempDir()}
	for k, v := range env {
		t.Setenv(k, v)
	}
	hostcapUnset(t, "LX_MAX_CHARS")
	hostcapUnset(t, "BASH_MAX_OUTPUT_LENGTH")
	failRoom := hostCharCapFor(1) + 1 + receiptRoom
	if failRoom >= hostCharCapFor(0) {
		t.Skipf("managed settings on this machine leave no room between the caps (%d, %d)", hostCharCapFor(1), hostCharCapFor(0))
	}
	for _, exit := range []int{0, 1, 3} {
		p := startLx(t, []string{"CLAUDECODE=1", "CLAUDE_CONFIG_DIR=" + env["CLAUDE_CONFIG_DIR"], "CLAUDE_PROJECT_DIR=" + dir,
			"LX_TEE_DIR=" + env["LX_TEE_DIR"], "LX_HEARTBEAT=off", "LX_PROMPT_IDLE=off", "LX_CONTEXT=0"}, "sh", gen, strconv.Itoa(exit))
		if code := p.wait(20 * time.Second); code != exit {
			t.Fatalf("exit %d, want %d", code, exit)
		}
		out := p.stdout.String() + p.stderr.String()
		limit, capc := hostLimitsFor(exit)
		if len(out) > capc+1+receiptRoom || len(out) > limit*9/10 {
			t.Errorf("exit %d: %d chars reach the agent, over the %d-char cap (limit %d)", exit, len(out), capc, limit)
		}
		if exit == 0 && len(out) <= failRoom {
			t.Errorf("a passing run got %d chars, no more than a failing one may", len(out))
		}
		for i := 7; i < 900; i += 60 {
			if want := fmt.Sprintf("src/module_%d.c:%d:5: error: use of undeclared identifier x%d", i, i, i); !strings.Contains(out, want) {
				t.Errorf("exit %d: lost %q", exit, want)
			}
		}
		if !strings.Contains(lastLine(out), "full output: lx show ") {
			t.Errorf("exit %d: no receipt: %q", exit, lastLine(out))
		}
	}
}
