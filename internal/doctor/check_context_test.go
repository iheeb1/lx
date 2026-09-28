package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/agentctx"
)

const ctxSID = "44444444-5555-4666-8777-888888888888"

func contextChecks(e Env, env map[string]string) []Check {
	e.Getenv = func(k string) string { return env[k] }
	var out []Check
	for _, c := range Run(e).Checks {
		if c.ID == "context" {
			out = append(out, c)
		}
	}
	return out
}

func TestContextCheck(t *testing.T) {
	e := emptyEnv(t)
	cfg := filepath.Join(e.Home, "claude-config")
	path := filepath.Join(cfg, "projects", agentctx.Slug(e.Cwd), ctxSID+".jsonl")
	os.MkdirAll(filepath.Dir(path), 0o700)
	line := `{"type":"assistant","message":{"id":"m1","model":"claude-sonnet-4-5","usage":{"input_tokens":1000,"cache_read_input_tokens":20000},"content":[{"type":"text","text":"secret words"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	session := func(kv ...string) map[string]string {
		m := map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_SESSION_ID": ctxSID, "CLAUDE_CONFIG_DIR": cfg}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	for _, c := range []struct {
		env    map[string]string
		status string
		msg    []string
		fix    string
	}{
		{map[string]string{}, "", nil, ""},
		{session(), OK, []string{"claude-code session 44444444…: transcript readable (1 KiB, reads the last 1 KiB); model claude-sonnet-4-5, window 200,000 tokens (model table), 10% used"}, ""},
		{session("LX_CONTEXT_WINDOW", "1m"), OK, []string{"window 1,000,000 tokens (LX_CONTEXT_WINDOW), 2% used"}, ""},
		{session("LX_CONTEXT_WINDOW", "lots"), Warn, []string{"LX_CONTEXT_WINDOW=lots is not a token count"}, "LX_CONTEXT_WINDOW=200k"},
		{session("LX_CONTEXT", "0"), Skip, []string{"LX_CONTEXT=0"}, ""},
		{map[string]string{"LX_CONTEXT": "0"}, Skip, []string{"LX_CONTEXT=0"}, ""},
		{map[string]string{"CLAUDECODE": "1"}, Skip, []string{"CLAUDE_CODE_SESSION_ID"}, ""},
		{session("CLAUDE_CODE_SESSION_ID", "99999999-0000-4000-8000-000000000000"), Warn, []string{"no transcript for claude-code session 99999999…", "lx works without context"}, "CLAUDE_CONFIG_DIR"},
		{map[string]string{"CODEX_THREAD_ID": "019a0000-0000-7000-8000-000000000000", "CODEX_HOME": cfg}, Warn, []string{"no transcript for codex session"}, "CODEX_HOME"},
		{session("CLAUDE_CODE_SESSION_ID", "../x"), Warn, []string{"not a session id"}, ""},
	} {
		got := contextChecks(e, c.env)
		if c.status == "" {
			if len(got) != 0 {
				t.Errorf("%v: %+v, want no context check outside an agent", c.env, got)
			}
			continue
		}
		if len(got) != 1 || got[0].Status != c.status || !strings.Contains(got[0].Fix, c.fix) {
			t.Errorf("%v: %+v, want status %s, fix with %q", c.env, got, c.status, c.fix)
			continue
		}
		for _, m := range c.msg {
			if !strings.Contains(got[0].Message, m) {
				t.Errorf("%v: message %q lacks %q", c.env, got[0].Message, m)
			}
		}
		if strings.Contains(got[0].Message, "secret") {
			t.Errorf("transcript text in the report: %q", got[0].Message)
		}
	}
}

func TestContextCheckUnreadableTranscript(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads anything")
	}
	e := emptyEnv(t)
	path := filepath.Join(e.Home, ".claude", "projects", agentctx.Slug(e.Cwd), ctxSID+".jsonl")
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path, []byte("{}\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	got := contextChecks(e, map[string]string{"CLAUDE_CODE_SESSION_ID": ctxSID, "HOME": e.Home})
	if len(got) != 1 || got[0].Status != Warn || !strings.Contains(got[0].Message, "can't read the transcript ~/.claude/projects/") {
		t.Fatalf("%+v", got)
	}
}
