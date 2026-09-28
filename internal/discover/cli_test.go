package discover

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runMain(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = Main(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestMainFlags(t *testing.T) {
	all := []string{"--days", "100000", "--dir", fidelityDir}

	code, out, _ := runMain(t, all...)
	if code != 0 || strings.Contains(out, "Acted-on") || !strings.Contains(out, "Host spills") || !strings.Contains(out, "Pipelines cutting") {
		t.Errorf("plain: exit %d\n%s", code, out)
	}

	code, out, _ = runMain(t, append(all, "--fidelity")...)
	if code != 0 || !strings.Contains(out, "Acted-on fidelity") || strings.Contains(out, "unit170") {
		t.Errorf("--fidelity: exit %d\n%s", code, out)
	}

	code, out, _ = runMain(t, append(all, "--fidelity", "--json")...)
	var rep Report
	if code != 0 || json.Unmarshal([]byte(out), &rep) != nil || rep.ActedOn == nil || rep.ActedOn.Examples != nil {
		t.Errorf("--fidelity --json: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "examples") {
		t.Error("examples in JSON without --examples")
	}

	code, out, _ = runMain(t, append(all, "--examples")...)
	if code != 0 || !strings.Contains(out, "grep  src/mod3/unit170.go:182  (filter)") {
		t.Errorf("--examples: exit %d\n%s", code, out)
	}
	code, out, _ = runMain(t, append(all, "--examples", "--json")...)

	if code != 0 || json.Unmarshal([]byte(out), &rep) != nil || rep.ActedOn == nil || len(rep.ActedOn.Examples) != 2 {
		t.Errorf("--examples --json: exit %d\n%s", code, out)
	}
	reasons := map[string]int{}
	for _, m := range rep.ActedOn.Examples {
		reasons[m.Reason]++
	}
	if reasons["filter"] != 2 || reasons["cut"] != 0 {
		t.Errorf("example reasons = %v", reasons)
	}
}

func TestMainUsage(t *testing.T) {
	if code, _, errOut := runMain(t, "fidelity"); code != 2 || !strings.Contains(errOut, "unexpected argument") {
		t.Errorf("stray argument: exit %d, %q", code, errOut)
	}
	if code, _, _ := runMain(t, "--nope"); code != 2 {
		t.Errorf("unknown flag: exit %d", code)
	}
	if code, _, errOut := runMain(t, "-h"); code != 0 || !strings.Contains(errOut, "--fidelity") {
		t.Errorf("-h: exit %d, %q", code, errOut)
	}
}

func TestMainDefaultDir(t *testing.T) {
	base := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", base)
	if code, out, _ := runMain(t); code != 0 || !strings.Contains(out, "no Bash calls found") {
		t.Errorf("empty config dir: exit %d\n%s", code, out)
	}
	proj := filepath.Join(base, "projects", "-x")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(fidelityDir, "b-go-test-edit", "session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), src, 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runMain(t, "--fidelity"); code != 0 || !strings.Contains(out, "1 Bash calls") || !strings.Contains(out, "go test     1") {
		t.Errorf("CLAUDE_CONFIG_DIR: exit %d\n%s", code, out)
	}
}
