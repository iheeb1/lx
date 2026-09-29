package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/hook"
)

func TestParseHookArgs(t *testing.T) {
	cases := []struct {
		args  []string
		agent string
		o     hook.HookOptions
	}{
		{nil, "claude", hook.HookOptions{}},
		{[]string{"claude"}, "claude", hook.HookOptions{}},
		{[]string{"gemini"}, "gemini", hook.HookOptions{}},
		{[]string{"codex", "--prefix", "/x/lx"}, "codex", hook.HookOptions{Prefix: "/x/lx"}},
		{[]string{"claude", "--readonly"}, "claude", hook.HookOptions{ReadOnly: true}},
		{[]string{"--readonly"}, "claude", hook.HookOptions{ReadOnly: true}},
		{[]string{"claude", "--prefix", "/a b/lx", "--readonly"}, "claude", hook.HookOptions{ReadOnly: true, Prefix: "/a b/lx"}},
		{[]string{"claude", "--prefix=/x/lx"}, "claude", hook.HookOptions{Prefix: "/x/lx"}},

		{[]string{"claude", "--future-flag", "x", "--prefix"}, "claude", hook.HookOptions{}},

		{[]string{"claude", "--prefix", "--readonly"}, "claude", hook.HookOptions{ReadOnly: true}},
	}
	for _, c := range cases {
		agent, o := parseHookArgs(c.args)
		if agent != c.agent || o != c.o {
			t.Errorf("parseHookArgs(%q) = %q %+v, want %q %+v", c.args, agent, o, c.agent, c.o)
		}
	}
}

func TestRewriteVerbose(t *testing.T) {
	cases := []struct {
		args           []string
		code           int
		stdout, stderr string
	}{
		{[]string{"git status"}, 0, "lx git status\n", ""},
		{[]string{"git", "status"}, 0, "lx git status\n", ""},
		{[]string{"echo hi"}, 1, "", ""},
		{[]string{"-v", "cd x && FOO=1 go test ./... && git log --grep='a b'"}, 0,
			"cd x && FOO=1 lx go test ./... && lx git log --grep='a b'\n",
			"target: go test ./...\ntarget: git log '--grep=a b'\n"},
		{[]string{"-v", "git status > out.txt"}, 1, "", "unchanged: no supported command\n"},
		{[]string{"--verbose", "echo $(date)"}, 1, "", "unchanged: command substitution\n"},
		{[]string{"-v"}, 2, "", "usage: lx rewrite [-v] '<shell command>'\n"},
		{nil, 2, "", "usage: lx rewrite [-v] '<shell command>'\n"},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		code := runRewrite(c.args, &out, &errb)
		if code != c.code || out.String() != c.stdout || errb.String() != c.stderr {
			t.Errorf("rewrite %q: code %d stdout %q stderr %q\nwant %d %q %q",
				c.args, code, out.String(), errb.String(), c.code, c.stdout, c.stderr)
		}
	}
}

func TestInitFlags(t *testing.T) {
	onPath := func() (string, error) { return "/usr/local/bin/lx", nil }
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	for _, c := range []struct {
		args []string
		msg  string
	}{
		{[]string{"--agent", "codex", "--readonly"}, "--readonly is for Claude Code"},
		{[]string{"--agent", "codex", "--no-readonly"}, "--readonly is for Claude Code"},
		{[]string{"--agent", "cursor", "--portable"}, "--portable works with"},
		{[]string{"--readonly", "--no-readonly"}, "contradict"},
		{[]string{"--agent", "emacs"}, "unknown agent"},
	} {
		var out, errb bytes.Buffer
		if code := runInit(c.args, &out, &errb, onPath); code != 2 || !strings.Contains(errb.String(), c.msg) {
			t.Errorf("init %q: %d %q", c.args, code, errb.String())
		}
	}

	dir := t.TempDir()
	t.Chdir(dir)
	var out, errb bytes.Buffer
	if code := runInit([]string{"--agent", "codex", "--project", "--portable"}, &out, &errb, onPath); code != 0 {
		t.Fatalf("codex portable: %d %s", code, errb.String())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".codex", "hooks.json")); !strings.Contains(string(b),
		`"sh -c 'command -v lx >/dev/null 2>&1 || exit 0; exec lx hook codex'"`) {
		t.Errorf(".codex/hooks.json:\n%s", b)
	}
	if code := runInit([]string{"--project", "--portable", "--readonly"}, &out, &errb, onPath); code != 0 {
		t.Fatalf("claude portable: %d %s", code, errb.String())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".claude", "settings.json")); !strings.Contains(string(b),
		`"sh -c 'command -v lx >/dev/null 2>&1 || exit 0; exec lx hook claude --readonly'"`) {
		t.Errorf(".claude/settings.json:\n%s", b)
	}
	if code := runInit([]string{"--agent", "codex", "--project", "--uninstall"}, &out, &errb, onPath); code != 0 {
		t.Fatalf("codex uninstall: %d %s", code, errb.String())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".codex", "hooks.json")); strings.Contains(string(b), "lx") {
		t.Errorf("uninstall left:\n%s", b)
	}

	out.Reset()
	if code := runInit([]string{"--agent", "codex", "--dry-run"}, &out, &errb, onPath); code != 0 ||
		!strings.Contains(out.String(), "hook codex") {
		t.Errorf("codex dry run: %d\n%s", code, out.String())
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("CODEX_HOME"), "hooks.json")); err == nil {
		t.Error("dry run wrote $CODEX_HOME/hooks.json")
	}
	out.Reset()
	if code := runInit([]string{"--agent", "agents-md"}, &out, &errb, onPath); code != 0 || !strings.Contains(out.String(), "AGENTS.md") {
		t.Errorf("agents-md snippet: %d\n%s", code, out.String())
	}
}
