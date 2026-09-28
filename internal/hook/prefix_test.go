package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	resolvePrefix = pinnedPrefix

	os.Unsetenv("LX_MODE")
	os.Exit(m.Run())
}

func pinnedPrefix(explicit string) string {
	if explicit != "" {
		return shellQuote(explicit)
	}
	return "lx"
}

func fakeLx(t *testing.T, dir string) string {
	t.Helper()
	exe := filepath.Join(dir, "lx")
	writeFile(t, exe, "#!/bin/sh\n")
	if err := os.Chmod(exe, 0o755); err != nil {
		t.Fatal(err)
	}
	old := executable
	executable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executable = old })
	return exe
}

func TestDefaultPrefixNotOnPath(t *testing.T) {
	exe := fakeLx(t, filepath.Join(t.TempDir(), "Jane Doe", "bin"))
	t.Setenv("PATH", t.TempDir())
	if got, want := defaultPrefix(""), shellQuote(exe); got != want || !strings.HasPrefix(got, "'/") {
		t.Errorf("defaultPrefix = %q, want %q", got, want)
	}
}

func TestDefaultPrefixOnPath(t *testing.T) {
	exe := fakeLx(t, t.TempDir())
	bin := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(bin, "lx")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if got := defaultPrefix(""); got != "lx" {
		t.Errorf("lx on PATH is this binary: defaultPrefix = %q, want lx", got)
	}
}

func TestDefaultPrefixOtherLxOnPath(t *testing.T) {
	exe := fakeLx(t, t.TempDir())
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "lx"), "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(bin, "lx"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if got := defaultPrefix(""); got != shellQuote(exe) {
		t.Errorf("another lx on PATH: defaultPrefix = %q, want %q", got, exe)
	}
}

func TestDefaultPrefixNeedsLxName(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if got := defaultPrefix(""); got != "lx" {
		t.Errorf("binary not named lx: defaultPrefix = %q, want lx", got)
	}
	for _, bad := range []string{"/opt/bin/lx-dev", "relative/lx", "/opt/bin/sh"} {
		if got := defaultPrefix(bad); got != "lx" {
			t.Errorf("defaultPrefix(%q) = %q, want lx", bad, got)
		}
	}
}

func TestDefaultPrefixExplicit(t *testing.T) {
	spaced := filepath.Join(t.TempDir(), "John Doe", "bin")
	lx := filepath.Join(spaced, "lx")
	writeFile(t, lx, "#!/bin/sh\n")
	if got, want := defaultPrefix(lx), shellQuote(lx); got != want || !strings.HasPrefix(got, "'") {
		t.Errorf("defaultPrefix = %q, want %q", got, want)
	}

	exe := fakeLx(t, t.TempDir())
	t.Setenv("PATH", t.TempDir())
	if got := defaultPrefix("/nonexistent/John Doe/bin/lx"); got != shellQuote(exe) {
		t.Errorf("missing explicit prefix: defaultPrefix = %q, want %q", got, shellQuote(exe))
	}
}

func TestHookExplicitPrefix(t *testing.T) {
	const lx = "/Users/John Doe/bin/lx"

	withRules(t, "")
	out := runHookWith(t, "claude", bashInput("git status"), HookOptions{Prefix: lx})
	if !strings.Contains(out, `"command":"'/Users/John Doe/bin/lx' git status"`) {
		t.Errorf("prefix not used: %s", out)
	}

	withRules(t, `{"permissions":{"deny":["Bash(git status:*)"]}}`)
	if out := runHookWith(t, "claude", bashInput("git status"), HookOptions{Prefix: lx}); out != "" {
		t.Errorf("deny on the original: %s", out)
	}
	out = runHookWith(t, "claude", bashInput(`'/Users/John Doe/bin/lx' git status`), HookOptions{Prefix: lx})
	if !strings.Contains(out, `"permissionDecision":"deny"`) {
		t.Errorf("model-written prefixed form escaped the deny rule: %q", out)
	}

	if got, _ := Rewrite("git status"); got != "lx git status" {
		t.Errorf("Rewrite = %q", got)
	}
}

func TestShellJoin(t *testing.T) {
	if got := ShellJoin([]string{"git", "log", "--grep=a b", ""}); got != `git log '--grep=a b' ''` {
		t.Errorf("ShellJoin = %q", got)
	}
}
