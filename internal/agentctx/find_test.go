package agentctx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"/Users/me/app":                "-Users-me-app",
		"/Users/me/projects/app":       "-Users-me-projects-app",
		"/home/me/my_app.v2":           "-home-me-my-app-v2",
		"/tmp/café":                    "-tmp-caf-",
		"/tmp/a b/😀":                   "-tmp-a-b---",
		`C:\Users\me\app`:              "C--Users-me-app",
		"/srv/odoo-salla-integration/": "-srv-odoo-salla-integration-",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func claudeHome(t *testing.T, project string) (home, path string) {
	t.Helper()
	home = t.TempDir()
	path = filepath.Join(home, ".claude", "projects", Slug(project), testSID+".jsonl")
	copyFile(t, "testdata/claude/main.jsonl", path)
	return home, path
}

func TestLocateClaude(t *testing.T) {
	home, path := claudeHome(t, "/work/app")
	base := map[string]string{"HOME": home, "CLAUDE_CODE_SESSION_ID": testSID, "CLAUDECODE": "1"}
	for _, cwd := range []string{"/work/app", "/work/app/internal/deep", "/elsewhere", ""} {
		src, err := Locate(envOf(base), cwd)
		if err != nil || src.Path != path || src.Agent != ClaudeCode || src.SessionID != testSID {
			t.Fatalf("cwd %q: %+v %v", cwd, src, err)
		}
	}

	cfg := t.TempDir()
	other := filepath.Join(cfg, "projects", "-some-project", testSID+".jsonl")
	copyFile(t, "testdata/claude/main.jsonl", other)
	env := map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": cfg, "CLAUDE_CODE_SESSION_ID": testSID, "LX_CONTEXT_WINDOW": "1m"}
	src, err := Locate(envOf(env), "/work/app")
	if err != nil || src.Path != other || src.Window != 1000000 {
		t.Fatalf("CLAUDE_CONFIG_DIR wins: %+v %v", src, err)
	}
}

func TestLocateReasons(t *testing.T) {
	home, _ := claudeHome(t, "/work/app")
	cases := []struct {
		env  map[string]string
		want string
		is   error
	}{
		{map[string]string{"HOME": home}, "not running under a coding agent", ErrNoAgent},
		{map[string]string{"HOME": home, "CLAUDECODE": "1"}, "CLAUDE_CODE_SESSION_ID is not set", ErrNoID},
		{map[string]string{"HOME": home, "CLAUDE_CODE_SESSION_ID": testSID, "LX_CONTEXT": "0"}, "LX_CONTEXT=0", ErrDisabled},
		{map[string]string{"HOME": home, "CLAUDE_CODE_SESSION_ID": "../../etc/passwd"}, "not a session id", nil},
		{map[string]string{"HOME": home, "CLAUDE_CODE_SESSION_ID": "99999999-0000-4000-8000-000000000000"}, "no transcript for claude-code session", nil},
		{map[string]string{"HOME": home, "CODEX_THREAD_ID": "019a0000-0000-7000-8000-000000000000"}, "no transcript for codex session", nil},
	}
	for _, c := range cases {
		_, err := Locate(envOf(c.env), "/work/app")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err %v, want %q", c.env, err, c.want)
		}
		if c.is != nil && !errors.Is(err, c.is) {
			t.Errorf("%v: err %v is not %v", c.env, err, c.is)
		}
		if _, ok := Find(envOf(c.env), "/work/app"); ok {
			t.Errorf("%v: Find reported a session", c.env)
		}
	}
	for _, v := range []string{"0", "off", "false", " no "} {
		if !Disabled(envOf(map[string]string{"LX_CONTEXT": v})) {
			t.Errorf("LX_CONTEXT=%q should disable", v)
		}
	}
	if Disabled(envOf(map[string]string{"LX_CONTEXT": "1"})) {
		t.Error("LX_CONTEXT=1 disables nothing")
	}
}

func TestLocateSkipsNonRegularFiles(t *testing.T) {
	home := t.TempDir()
	p := filepath.Join(home, ".claude", "projects", Slug("/w"), testSID+".jsonl")
	os.MkdirAll(p, 0o755)
	if _, err := Locate(envOf(map[string]string{"HOME": home, "CLAUDE_CODE_SESSION_ID": testSID}), "/w"); err == nil {
		t.Fatal("a directory is not a transcript")
	}
}

func TestLocateCodex(t *testing.T) {
	home := t.TempDir()
	id := "01a0deae-5150-79f3-b313-52a0160a0ef6"
	started, _ := uuidTime(id)
	day := dayDir(filepath.Join(home, ".codex", "sessions"), started)
	path := filepath.Join(day, "rollout-2026-09-26T18-06-01-"+id+".jsonl")
	copyFile(t, "testdata/codex/rollout.jsonl", path)
	copyFile(t, "testdata/codex/rollout.jsonl", filepath.Join(day, "rollout-2026-09-26T18-06-01-01a0deae-0000-79f3-b313-52a0160a0ef6.jsonl"))
	src, err := Locate(envOf(map[string]string{"HOME": home, "CODEX_THREAD_ID": id}), "/repo")
	if err != nil || src.Path != path || src.Agent != Codex {
		t.Fatalf("%+v %v", src, err)
	}
	s := load(t, src)
	if s.SessionID != id || s.Agent != Codex || len(s.Runs) == 0 {
		t.Fatalf("%+v", s)
	}

	codexHome := t.TempDir()
	v4 := "5b2c7d3e-1111-4222-8333-444455556666"
	p2 := filepath.Join(codexHome, "sessions", "2026", "08", "01", "rollout-2026-08-01T10-00-00-"+v4+".jsonl")
	copyFile(t, "testdata/codex/rollout.jsonl", p2)
	os.MkdirAll(filepath.Join(codexHome, "sessions", "2026", "09", "27"), 0o755)
	src, err = Locate(envOf(map[string]string{"HOME": home, "CODEX_HOME": codexHome, "CODEX_THREAD_ID": v4}), "")
	if err != nil || src.Path != p2 {
		t.Fatalf("scan fallback: %+v %v", src, err)
	}
}

func TestUUIDTime(t *testing.T) {
	got, ok := uuidTime("01a0deae-5150-79f3-b313-52a0160a0ef6")
	if !ok || !got.Equal(time.UnixMilli(0x01a0deae5150)) {
		t.Fatalf("%v %v", got, ok)
	}
	for _, id := range []string{"5b2c7d3e-1111-4222-8333-444455556666", "nothex", ""} {
		if _, ok := uuidTime(id); ok {
			t.Errorf("%q is not a v7 uuid", id)
		}
	}
}

func TestParseWindow(t *testing.T) {
	cases := map[string]int{"200000": 200000, "200k": 200000, "1m": 1000000, "1M": 1000000, "1.5m": 1500000,
		"1_000_000": 1000000, "": 0, "abc": 0, "-5": 0, "0": 0, "1e12": 0}
	for in, want := range cases {
		if got := parseWindow(in); got != want {
			t.Errorf("parseWindow(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestCurrent(t *testing.T) {
	home, _ := claudeHome(t, "/work/app")
	env := envOf(map[string]string{"HOME": home, "CLAUDE_CODE_SESSION_ID": testSID})
	s := Current(env, "/work/app", []string{"go", "test"})
	if s == nil || s.Model != "claude-sonnet-4-5" || s.Focus() == nil {
		t.Fatalf("%+v", s)
	}
	if Current(envOf(map[string]string{"HOME": home}), "/work/app", nil) != nil {
		t.Fatal("no session, no snapshot")
	}
	var nilSnap *Snapshot
	if nilSnap.Focus() != nil || nilSnap.InferMode() != 0 {
		t.Fatal("a nil snapshot has no focus and the auto mode")
	}

	home, path := claudeHome(t, "/work/app")
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(path, old, old)
	env = envOf(map[string]string{"HOME": home, "CLAUDE_CODE_SESSION_ID": testSID})
	if Current(env, "/work/app", []string{"make"}) != nil {
		t.Fatal("a session idle for two hours that didn't issue this command is stale")
	}
	if Current(env, "/work/app", []string{"go", "test", "./internal/engine/..."}) == nil {
		t.Fatal("its pending call proves it is the caller")
	}
}

func TestLocateNestedAgents(t *testing.T) {
	home, claudePath := claudeHome(t, "/work/app")
	id := "01a0deae-5150-79f3-b313-52a0160a0ef6"
	started, _ := uuidTime(id)
	codexPath := filepath.Join(dayDir(filepath.Join(home, ".codex", "sessions"), started), "rollout-2026-09-26T18-06-01-"+id+".jsonl")
	copyFile(t, "testdata/codex/rollout.jsonl", codexPath)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(claudePath, old, old)
	env := envOf(map[string]string{"HOME": home, "CLAUDE_CODE_SESSION_ID": testSID, "CODEX_THREAD_ID": id})

	src, err := Locate(env, "/work/app")
	if err != nil || src.Agent != Codex || !src.Nested {
		t.Fatalf("the inner, active agent wins: %+v %v", src, err)
	}
	src.Argv = []string{"go", "test", "./internal/...", "-run", "TestCodexPending"}
	if s := load(t, src); !s.Caller || !s.Runs[1].InContext {
		t.Fatalf("pending call found: caller %v runs %+v", s.Caller, s.Runs)
	}
	src.Argv = []string{"make"}
	if s := load(t, src); s.Caller || s.Runs[1].InContext {
		t.Fatalf("nested and not found: caller %v runs %+v", s.Caller, s.Runs)
	}

	os.Chtimes(claudePath, time.Now(), time.Now())
	os.Chtimes(codexPath, old, old)
	if src, err := Locate(env, "/work/app"); err != nil || src.Agent != ClaudeCode || !src.Nested {
		t.Fatalf("%+v %v", src, err)
	}
	os.Remove(claudePath)
	if src, err := Locate(env, "/work/app"); err != nil || src.Agent != Codex || !src.Nested {
		t.Fatalf("only the codex transcript exists: %+v %v", src, err)
	}
	if src, err := Locate(envOf(map[string]string{"HOME": home, "CODEX_THREAD_ID": id}), ""); err != nil || src.Nested {
		t.Fatalf("codex alone: %+v %v", src, err)
	}
}

func TestLocateScanPrefersTheLiveCopy(t *testing.T) {
	home, stale := claudeHome(t, "/work/old")
	live := filepath.Join(home, ".claude", "projects", Slug("/work/new"), testSID+".jsonl")
	copyFile(t, "testdata/claude/main.jsonl", live)
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(stale, old, old)
	src, err := Locate(envOf(map[string]string{"HOME": home, "CLAUDE_CODE_SESSION_ID": testSID}), "/tmp")
	if err != nil || src.Path != live {
		t.Fatalf("got %q, want the copy being written %q (%v)", src.Path, live, err)
	}
}
