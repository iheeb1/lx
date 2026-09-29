package hook

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Rewrite tests shouldn't depend on which tools this machine has.
func init() { lxRunsSame = func(*segment) bool { return true } }

func realRunsSame(t *testing.T, path string, env ...string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake tools are shell scripts")
	}
	old := lxRunsSame
	lxRunsSame = runsSame
	t.Cleanup(func() { lxRunsSame = old })
	t.Setenv("PATH", path)
	for _, k := range []string{"CLAUDECODE", "CLAUDE_CODE_EXECPATH", "CLAUDE_CODE_ENTRYPOINT"} {
		t.Setenv(k, "")
	}
	t.Setenv("HOME", t.TempDir())
	for i := 0; i+1 < len(env); i += 2 {
		t.Setenv(env[i], env[i+1])
	}
}

func execFile(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func hookTargetsOf(cmd string) string {
	var out []string
	for _, s := range analyze(cmd).hookTargets() {
		out = append(out, strings.Join(s.argv, " "))
	}
	return strings.Join(out, " | ")
}

func TestHookLeavesUnrunnableCommands(t *testing.T) {
	bin := t.TempDir()
	execFile(t, filepath.Join(bin, "git"))
	realRunsSame(t, bin)
	for _, c := range []struct{ cmd, want string }{
		{"git status && rg -n foo src", "git status"},
		{"rg -n foo src", ""},
		{"grep -rn TODO .", ""},
		{"find . -name '*.go' && git diff", "git diff"},
		{"cargo test", ""},
		{"FOO=1 git status", "git status"},
		{"/opt/tools/rg -n foo", "/opt/tools/rg -n foo"},
		{"./gradlew build", "./gradlew build"},
	} {
		if got := hookTargetsOf(c.cmd); got != c.want {
			t.Errorf("%q: hook rewrites %q, want %q", c.cmd, got, c.want)
		}
	}
	if !Inspect("rg -n foo src").Changed {
		t.Error("Inspect (lx rewrite, discover) should still report rg as supported")
	}
}

func TestHookClaudeSearchFunctions(t *testing.T) {
	bin := t.TempDir()
	execFile(t, filepath.Join(bin, "git"))
	claude := execFile(t, filepath.Join(t.TempDir(), "claude"))
	cc := []string{"CLAUDECODE", "1", "CLAUDE_CODE_EXECPATH", claude}

	realRunsSame(t, bin, cc...)
	for _, c := range []struct{ cmd, want string }{
		{"rg -n foo src", "rg -n foo src"},
		{"grep -rn TODO .", "grep -rn TODO ."},
		{"find . -name x", "find . -name x"},
		{"time grep -rn x .", "grep -rn x ."},
		{"FOO=1 rg x", "rg x"},
		{"noglob find . -name *.go", "find . -name *.go"},
		{"grep --config=x foo .", ""},
		{"command grep -rn x .", ""},
		{"timeout 5 grep -rn x .", ""},
		{"nice -n 5 rg x", ""},
		{"nohup find . -type f", ""},
		{"env FOO=1 grep -n x f", ""},
		{"timeout 5 git status", "git status"},
		{"egrep -rn x .", ""},
	} {
		if got := hookTargetsOf(c.cmd); got != c.want {
			t.Errorf("%q: hook rewrites %q, want %q", c.cmd, got, c.want)
		}
	}

	realRunsSame(t, bin, "CLAUDECODE", "1", "CLAUDE_CODE_EXECPATH", "/usr/local/bin/node")
	if got := hookTargetsOf("rg -n foo && grep -rn x ."); got != "" {
		t.Errorf("npm Claude Code has no search functions: hook rewrites %q", got)
	}

	realRunsSame(t, bin, "CLAUDECODE", "1", "CLAUDE_CODE_ENTRYPOINT", "local-agent", "CLAUDE_CODE_EXECPATH", claude)
	if got := hookTargetsOf("rg x && grep -rn x ."); got != "rg x" {
		t.Errorf("local-agent: hook rewrites %q, want only rg", got)
	}

	realRunsSame(t, bin, "CLAUDECODE", "1")
	if got := hookTargetsOf("rg x"); got != "" {
		t.Errorf("no Claude Code binary: hook rewrites %q", got)
	}
	execFile(t, filepath.Join(os.Getenv("HOME"), ".local", "bin", "claude"))
	if got := hookTargetsOf("rg x"); got != "rg x" {
		t.Errorf("hooks don't get CLAUDE_CODE_EXECPATH; ~/.local/bin/claude should do: got %q", got)
	}
}
