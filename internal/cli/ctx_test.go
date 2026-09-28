package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/agentctx"
)

const ctxSID = "22222222-3333-4444-8555-666666666666"

var ctxTranscript = []string{
	`{"type":"user","uuid":"u1","timestamp":"2026-09-20T10:00:00Z","message":{"role":"user","content":"hushhush please make TestParseMode pass in internal/engine/modes.go"}}`,
	`{"type":"ai-title","aiTitle":"Fix ParseMode"}`,
	`{"type":"assistant","uuid":"a1","timestamp":"2026-09-20T10:01:00Z","message":{"id":"m1","model":"claude-sonnet-4-5","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":99000},"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"cd /repo && go test ./internal/engine -run TestParseMode"}}]}}`,
	`{"type":"user","uuid":"u2","timestamp":"2026-09-20T10:01:10Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"FAIL hushhush\n[lx: 307→29 lines (−71%) · full output: lx show 12]"}]}}`,
	`{"type":"system","subtype":"compact_boundary","uuid":"s1","timestamp":"2026-09-20T10:04:00Z","compactMetadata":{"trigger":"auto","preTokens":180000}}`,
	`{"type":"assistant","uuid":"a2","timestamp":"2026-09-20T10:05:00Z","message":{"id":"m2","model":"claude-sonnet-4-5","usage":{"input_tokens":500,"cache_creation_input_tokens":500,"cache_read_input_tokens":49000},"content":[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"/repo/internal/engine/modes.go","old_string":"hushhush","new_string":"x"}}]}}`,
	`{"type":"user","uuid":"u3","timestamp":"2026-09-20T10:05:10Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"ok"}]}}`,
	`{"type":"assistant","uuid":"a3","timestamp":"2026-09-20T10:06:00Z","message":{"id":"m3","model":"claude-sonnet-4-5","usage":{"input_tokens":500,"cache_creation_input_tokens":500,"cache_read_input_tokens":49000},"content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"LX_MODE=verify git status"}}]}}`,
	`{"type":"user","uuid":"u4","timestamp":"2026-09-20T10:06:10Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t3","content":"clean"}]}}`,
	`{"type":"assistant","uuid":"a4","timestamp":"2026-09-20T10:07:00Z","message":{"id":"m4","model":"claude-sonnet-4-5","usage":{"input_tokens":500,"cache_creation_input_tokens":500,"cache_read_input_tokens":49000},"content":[{"type":"text","text":"hushhush: let me run the tests again to verify ParseMode."}]}}`,
	`{"type":"assistant","uuid":"a5","timestamp":"2026-09-20T10:07:00Z","message":{"id":"m4","model":"claude-sonnet-4-5","usage":{"input_tokens":500,"cache_creation_input_tokens":500,"cache_read_input_tokens":49000},"content":[{"type":"tool_use","id":"t4","name":"Bash","input":{"command":"lx ctx"}}]}}`,
}

func ctxSetup(t *testing.T, cwd string) map[string]string {
	t.Helper()
	home := t.TempDir()
	p := filepath.Join(home, ".claude", "projects", agentctx.Slug(cwd), ctxSID+".jsonl")
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(strings.Join(ctxTranscript, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"HOME": home, "CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": ctxSID}
}

func ctxExec(env map[string]string, cwd string, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	now := time.Date(2026, 9, 20, 10, 34, 0, 0, time.UTC)
	code := runCtx(args, &out, &errb, func(k string) string { return env[k] }, cwd, now)
	return code, out.String(), errb.String()
}

func TestCtxText(t *testing.T) {
	env := ctxSetup(t, "/repo")
	code, out, errs := ctxExec(env, "/repo")
	if code != 0 || errs != "" {
		t.Fatalf("exit %d: %s", code, errs)
	}
	for _, want := range []string{
		"agent       claude-code, session " + ctxSID + "\n",
		"caller      confirmed\n",
		"model       claude-sonnet-4-5\n",
		"context     50,000 of 200,000 tokens (25.0%), window from the model table\n",
		"compacted   1× in the part read, last 30m ago\n",
		"title       Fix ParseMode\n",
		"mode        verify, inferred from the latest assistant message\n",
		"ParseMode 1.0",
		"TestParseMode 0.8",
		"files       internal/engine/modes.go (edit, 2 turns ago)\n",
		"runs        git status, 1 turn ago (+1 not in context)\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "hushhush") {
		t.Fatalf("transcript content leaked:\n%s", out)
	}
}

func TestCtxJSON(t *testing.T) {
	env := ctxSetup(t, "/repo")
	env["LX_MODE"] = "debug"
	code, out, _ := ctxExec(env, "/repo/internal", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(out, "hushhush") {
		t.Fatalf("transcript content leaked:\n%s", out)
	}
	var v struct {
		Agent       string  `json:"agent"`
		Used        int     `json:"context_used"`
		Window      int     `json:"context_window"`
		Percent     float64 `json:"context_pct"`
		Compactions int     `json:"compactions"`
		Mode        string  `json:"mode"`
		ModeEnv     string  `json:"lx_mode"`
		Terms       []struct {
			Text   string  `json:"text"`
			Weight float64 `json:"weight"`
		} `json:"focus_terms"`
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
		Runs []struct {
			Command   string `json:"command"`
			LxID      int    `json:"lx_id"`
			InContext bool   `json:"in_context"`
			Failed    bool   `json:"failed"`
		} `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if v.Agent != "claude-code" || v.Used != 50000 || v.Window != 200000 || v.Percent != 25 || v.Compactions != 1 ||
		v.Mode != "verify" || v.ModeEnv != "debug" || len(v.Terms) == 0 {
		t.Fatalf("%+v", v)
	}
	if len(v.Files) != 1 || v.Files[0].Path != "engine/modes.go" {
		t.Fatalf("files %+v", v.Files)
	}
	if len(v.Runs) != 2 || v.Runs[0].Command != "git status" || v.Runs[1].Command != "go test" ||
		v.Runs[1].LxID != 12 || v.Runs[1].InContext || !v.Runs[1].Failed {
		t.Fatalf("runs %+v", v.Runs)
	}
}

func TestCtxCallerNotConfirmed(t *testing.T) {
	env := ctxSetup(t, "/repo")
	p := filepath.Join(env["HOME"], ".claude", "projects", agentctx.Slug("/repo"), ctxSID+".jsonl")
	lines := append(append([]string(nil), ctxTranscript[:len(ctxTranscript)-1]...), "")
	os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o600)
	defer func(n int) { ctxRetries = n }(ctxRetries)
	ctxRetries = 1
	code, out, _ := ctxExec(env, "/repo")
	if code != 0 || !strings.Contains(out, "caller      not confirmed") || !strings.Contains(out, "runs        none (+2 not in context)\n") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	_, out, _ = ctxExec(env, "/repo", "--json")
	if !strings.Contains(out, `"caller": false`) || strings.Contains(out, `"in_context": true`) {
		t.Fatalf("%s", out)
	}
}

func TestCtxNoSession(t *testing.T) {
	env := ctxSetup(t, "/repo")
	cases := []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"HOME": env["HOME"]}, "not running under a coding agent"},
		{map[string]string{"HOME": env["HOME"], "CLAUDECODE": "1"}, "CLAUDE_CODE_SESSION_ID is not set"},
		{map[string]string{"HOME": env["HOME"], "CLAUDE_CODE_SESSION_ID": "33333333-0000-4000-8000-000000000000"}, "no transcript for claude-code session"},
		{map[string]string{"HOME": env["HOME"], "CLAUDE_CODE_SESSION_ID": ctxSID, "LX_CONTEXT": "0"}, "LX_CONTEXT=0"},
	}
	for _, c := range cases {
		code, out, errs := ctxExec(c.env, "/repo")
		if code != 1 || out != "" || !strings.Contains(errs, c.want) {
			t.Errorf("%v: exit %d out %q err %q, want %q", c.env, code, out, errs, c.want)
		}
	}
	if code, _, _ := ctxExec(env, "/repo", "extra"); code != 2 {
		t.Errorf("unexpected argument: exit %d", code)
	}
	if code, _, _ := ctxExec(env, "/repo", "--nope"); code != 2 {
		t.Errorf("unknown flag: exit %d", code)
	}
}

func TestCtxRunKey(t *testing.T) {
	cases := map[string]string{
		"go test ./...":                           "go test",
		"cd /repo && npm run build":               "npm run",
		"FOO=1 BAR=2 pytest -x tests/secret.py":   "pytest",
		"export X=1; git -C /secret log":          "git log",
		"for f in *.go; do gofmt -l $f; done":     "for",
		"python3 - <<'EOF'\nprint('secret')\nEOF": "python3",
		"": "sh",
	}
	for cmd, want := range cases {
		if got := ctxRunKey(cmd); got != want {
			t.Errorf("ctxRunKey(%q) = %q, want %q", cmd, got, want)
		}
	}
}
