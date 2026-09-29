//go:build unix

package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const shimSnapshot = `# Snapshot file
if ! (unalias rg 2>/dev/null; command -v rg) >/dev/null 2>&1; then
  function rg {
    ARGV0=rg "$_cc_bin" ${1+"$@"}
  }
fi
function find {
    ARGV0=bfs "$_cc_bin" -S dfs -regextype findutils-default ${1+"$@"}
}
function grep {
    ARGV0=ugrep "$_cc_bin" -G --ignore-files --hidden -I ${1+"$@"}
}
export PATH=/usr/bin
`

const rgAliasSnapshot = `# Snapshot file
if ! (unalias rg 2>/dev/null; command -v rg) >/dev/null 2>&1; then
  alias rg='/usr/lib/node_modules/@anthropic-ai/claude-code/vendor/ripgrep/x64-linux/rg'
fi
export PATH=/usr/bin
`

const rgOnlySnapshot = `# Snapshot file
if ! (unalias rg 2>/dev/null; command -v rg) >/dev/null 2>&1; then
  function rg {
    (exec -a rg "$_cc_bin" ${1+"$@"})
  }
fi
export PATH=/usr/bin
`

func searchChecks(e Env, env map[string]string) []Check {
	e.Getenv = func(k string) string { return env[k] }
	if e.Exec == nil {
		e.Exec = func(context.Context, string, []string, string) (string, error) { return "", errNoExec }
	}
	s := newState(&e)
	s.checkSearch()
	return s.checks
}

func writeExec(t *testing.T, path, body string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSearchCheck(t *testing.T) {
	e := emptyEnv(t)
	bin := filepath.Join(e.Cwd, "bin")
	writeExec(t, filepath.Join(bin, "grep"), "#!/bin/sh\n", 0o755)
	writeExec(t, filepath.Join(bin, "find"), "#!/bin/sh\n", 0o755)
	t.Setenv("PATH", bin)
	claude := writeExec(t, filepath.Join(e.Cwd, "native", "claude"), "#!/bin/sh\n", 0o755)
	snaps := filepath.Join(e.ConfigDir, "shell-snapshots")
	cc := map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_EXECPATH": claude, "HOME": e.Home}

	if got := searchChecks(e, map[string]string{}); len(got) != 0 {
		t.Fatalf("outside Claude Code: %+v", got)
	}

	one := func(env map[string]string) Check {
		t.Helper()
		got := searchChecks(e, env)
		if len(got) != 1 {
			t.Fatalf("want one search line, got %+v", got)
		}
		return got[0]
	}
	if c := one(cc); c.Status != Skip || !strings.Contains(c.Message, "grep → Claude Code's ugrep, find → Claude Code's bfs, rg → Claude Code's rg") {
		t.Errorf("no snapshot: %+v", c)
	}

	writeExec(t, filepath.Join(snaps, "snapshot-zsh-1-aaaaaa.sh"), shimSnapshot, 0o600)
	if c := one(cc); c.Status != OK || c.Message != "same as the agent's shell: grep → Claude Code's ugrep, find → Claude Code's bfs, rg → Claude Code's rg" {
		t.Errorf("shims: %+v", c)
	}

	writeExec(t, filepath.Join(bin, "rg"), "#!/bin/sh\n", 0o755)
	if c := one(cc); c.Status != OK || !strings.HasSuffix(c.Message, "rg → "+filepath.Join(bin, "rg")) {
		t.Errorf("rg installed: %+v", c)
	}
	os.Remove(filepath.Join(bin, "rg"))

	installed := writeExec(t, filepath.Join(e.Home, ".local", "bin", "claude"), "#!/bin/sh\n", 0o755)
	if c := one(map[string]string{"CLAUDECODE": "1", "HOME": e.Home}); c.Status != Warn ||
		!strings.Contains(c.Message, "grep: the agent's shell runs Claude Code's ugrep, lx runs "+filepath.Join(bin, "grep")) ||
		strings.Contains(c.Message, "--allowedTools") {
		t.Errorf("no CLAUDE_CODE_EXECPATH: %+v", c)
	}
	os.Remove(installed)

	later := writeExec(t, filepath.Join(snaps, "snapshot-zsh-2-bbbbbb.sh"), rgOnlySnapshot, 0o600)
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(later, future, future); err != nil {
		t.Fatal(err)
	}
	c := one(cc)
	if c.Status != Warn || !strings.Contains(c.Message, "grep: the agent's shell runs "+filepath.Join(bin, "grep")+", lx runs Claude Code's ugrep") ||
		!strings.Contains(c.Message, "find: the agent's shell runs") || strings.Contains(c.Message, "rg:") ||
		!strings.Contains(c.Message, "--allowedTools") {
		t.Errorf("newest snapshot has no grep/find functions: %+v", c)
	}

	sourced := writeExec(t, filepath.Join(e.Cwd, "Jane Doe", ".claude", "shell-snapshots", "snapshot-zsh-3-cccccc.sh"), shimSnapshot, 0o600)
	var ps []string
	e.Exec = func(_ context.Context, name string, args []string, _ string) (string, error) {
		if name != "ps" {
			return "", errNoExec
		}
		ps = append(ps, strings.Join(args, " "))
		if len(ps) == 1 {
			return "  4242 go test ./...\n", nil
		}
		return "  1 /bin/zsh -c source '" + sourced + "' 2>/dev/null || true && eval 'lx doctor' < /dev/null\n", nil
	}
	if c := one(cc); c.Status != OK {
		t.Errorf("the snapshot the agent's shell sourced wins over the newest one: %+v", c)
	}
	if len(ps) != 2 || !strings.HasSuffix(ps[1], "-p 4242") || !strings.HasPrefix(ps[0], "-ww ") {
		t.Errorf("ps calls: %q", ps)
	}

	dir := filepath.Join(e.Cwd, "d", "shell-snapshots", "snapshot-zsh-4-eeeeee.sh")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(e.Cwd, "gone", "shell-snapshots", "snapshot-zsh-4-eeeeee.sh"), dir} {
		e.Exec = func(context.Context, string, []string, string) (string, error) {
			return "  1 /bin/zsh -c source '" + p + "' && eval 'lx doctor'\n", nil
		}
		if c := one(cc); c.Status != Warn || !strings.Contains(c.Message, "--allowedTools") {
			t.Errorf("an unreadable sourced snapshot falls back to the newest one: %+v", c)
		}
	}
	e.Exec = nil

	writeExec(t, filepath.Join(snaps, "snapshot-bash-5-dddddd.sh"), rgAliasSnapshot, 0o600)
	future = future.Add(time.Hour)
	if err := os.Chtimes(filepath.Join(snaps, "snapshot-bash-5-dddddd.sh"), future, future); err != nil {
		t.Fatal(err)
	}
	c = one(map[string]string{"CLAUDECODE": "1", "CLAUDE_CODE_EXECPATH": "/usr/bin/node", "HOME": e.Home})
	if c.Status != Warn || c.Message != "rg: the agent's shell runs the rg bundled with Claude Code (an alias), lx runs nothing (not found), so results can differ" {
		t.Errorf("npm Claude Code's rg alias: %+v", c)
	}
}
