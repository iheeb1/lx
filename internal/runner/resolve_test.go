//go:build unix

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// the test binary doubles as a fake Claude Code
func init() {
	if os.Getenv("LX_FAKE_CLAUDE") == "1" {
		fmt.Print(strings.Join(os.Args, "\n"))
		os.Exit(3)
	}
}

func fakeClaude(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func fakeBin(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("#!/bin/sh\necho system-"+n+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func envOf(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

var ugrepArgs = []string{"ugrep", "-G", "--ignore-files", "--hidden", "-I", "--exclude-dir=.git", "--exclude-dir=.svn",
	"--exclude-dir=.hg", "--exclude-dir=.bzr", "--exclude-dir=.jj", "--exclude-dir=.sl"}

func TestResolveClaudeShims(t *testing.T) {
	claude := fakeClaude(t)
	bin := fakeBin(t, "grep", "find")
	t.Setenv("PATH", bin)
	home := t.TempDir()
	installed := filepath.Join(home, ".local", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(installed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	node22 := filepath.Join(t.TempDir(), "node22")
	if err := os.WriteFile(node22, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	notExec := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(notExec, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cc := func(kv ...string) func(string) string {
		return envOf(append([]string{"CLAUDECODE", "1", "CLAUDE_CODE_EXECPATH", claude, "HOME", t.TempDir()}, kv...)...)
	}
	system := func(name string) string { return filepath.Join(bin, name) }

	for _, c := range []struct {
		name string
		argv []string
		env  func(string) string
		path string
		args []string
	}{
		{"grep", []string{"grep", "-rn", "TODO", "."}, cc(), claude, append(slices.Clone(ugrepArgs), "-rn", "TODO", ".")},
		{"find", []string{"find", ".", "-name", "*.go"}, cc(), claude,
			[]string{"bfs", "-S", "dfs", "-regextype", "findutils-default", ".", "-name", "*.go"}},
		{"rg not installed", []string{"rg", "-n", "x"}, cc(), claude, []string{"rg", "-n", "x"}},
		{"outside Claude Code", []string{"grep", "-n", "x"}, envOf(), system("grep"), []string{"grep", "-n", "x"}},
		{"CLAUDECODE=0", []string{"grep", "x"}, cc("CLAUDECODE", "0"), system("grep"), []string{"grep", "x"}},
		{"path to grep", []string{"/bin/sh", "-c", "x"}, cc(), "/bin/sh", []string{"/bin/sh", "-c", "x"}},
		{"egrep", []string{"egrep", "x"}, cc(), "", nil},
		{"npm install", []string{"grep", "x"}, cc("CLAUDE_CODE_EXECPATH", "/usr/local/bin/node"), system("grep"), []string{"grep", "x"}},
		{"npm install, no rg", []string{"rg", "x"}, cc("CLAUDE_CODE_EXECPATH", "/opt/node/bin/node"), "", nil},
		{"bun", []string{"find", "."}, cc("CLAUDE_CODE_EXECPATH", "/x/bun"), system("find"), []string{"find", "."}},
		{"local-agent", []string{"grep", "x"}, cc("CLAUDE_CODE_ENTRYPOINT", "local-agent"), system("grep"), []string{"grep", "x"}},
		{"local-agent rg", []string{"rg", "x"}, cc("CLAUDE_CODE_ENTRYPOINT", "local-agent"), claude, []string{"rg", "x"}},
		{"execpath gone, installed claude", []string{"grep", "x"}, cc("CLAUDE_CODE_EXECPATH", notExec, "HOME", home),
			installed, append(slices.Clone(ugrepArgs), "x")},
		{"execpath gone, nothing installed", []string{"grep", "x"}, cc("CLAUDE_CODE_EXECPATH", notExec), system("grep"), []string{"grep", "x"}},
		{"execpath is a directory", []string{"find", "."}, cc("CLAUDE_CODE_EXECPATH", home), system("find"), []string{"find", "."}},
		{"no execpath, on PATH", []string{"grep", "x"}, cc("CLAUDE_CODE_EXECPATH", "", "HOME", home), system("grep"), []string{"grep", "x"}},
		{"no execpath, rg via installed claude", []string{"rg", "x"}, cc("CLAUDE_CODE_EXECPATH", "", "HOME", home), installed, []string{"rg", "x"}},
		{"no execpath, nothing", []string{"rg", "x"}, cc("CLAUDE_CODE_EXECPATH", ""), "", nil},
		{"versioned node", []string{"find", "."}, cc("CLAUDE_CODE_EXECPATH", node22), system("find"), []string{"find", "."}},
	} {
		r, err := Resolve(c.argv, c.env)
		if c.path == "" {
			if err == nil {
				t.Errorf("%s: resolved to %s %q, want not found", c.name, r.Path, r.Args)
			}
			continue
		}
		if err != nil || r.Path != c.path || !slices.Equal(r.Args, c.args) {
			t.Errorf("%s: got %s %q (%v)\nwant %s %q", c.name, r.Path, r.Args, err, c.path, c.args)
		}
		if want := c.path == claude || c.path == installed; (r.Builtin != "") != want {
			t.Errorf("%s: Builtin=%q", c.name, r.Builtin)
		}
	}
}

func TestResolveRelativeExecpath(t *testing.T) {
	t.Setenv("PATH", fakeBin(t, "grep"))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	r, err := Resolve([]string{"grep", "x"}, envOf("CLAUDECODE", "1", "CLAUDE_CODE_EXECPATH", "claude", "HOME", t.TempDir()))
	if err != nil || r.Builtin != "" || r.Path != filepath.Join(os.Getenv("PATH"), "grep") {
		t.Errorf("exec.Command would look a bare claude up on PATH: got %+v, %v", r, err)
	}
}

func TestResolveRgOnPath(t *testing.T) {
	bin := fakeBin(t, "rg")
	t.Setenv("PATH", bin)
	r, err := Resolve([]string{"rg", "x"}, envOf("CLAUDECODE", "1", "CLAUDE_CODE_EXECPATH", fakeClaude(t)))
	if err != nil || r.Path != filepath.Join(bin, "rg") || r.Builtin != "" {
		t.Errorf("the snapshot defines rg only when rg isn't installed: got %+v, %v", r, err)
	}
}

func TestGrepOptOut(t *testing.T) {
	for _, a := range []string{"--null", "--null-data", "-Z", "-z", "-rZ", "-lz", "-Enz", "--filter=pdf:x", "--no-filter",
		"--pager", "--pager=less", "--view", "--view=vim", "--format-open=%f", "--config", "--config=x",
		"--save-config", "---", "---x", "-@", "-@x", "-x-config"} {
		if !grepOptOut([]string{"-rn", "x", a}) {
			t.Errorf("%q should run the system grep", a)
		}
	}
	for _, a := range []string{"-rn", "--nul", "--include=*.zip", "--exclude-dir=zz", "zz", "-", "--", "-e", "config", "-config",
		"--ignore-case", "--color=never", "-filter", "size"} {
		if grepOptOut([]string{a}) {
			t.Errorf("%q should run Claude Code's ugrep", a)
		}
	}
}

func TestRunExecsClaudeShim(t *testing.T) {
	t.Setenv("PATH", fakeBin(t, "grep"))
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_EXECPATH", fakeClaude(t))
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("LX_FAKE_CLAUDE", "1")

	res := Run([]string{"grep", "-rn", "TODO", "."}, nil)
	want := strings.Join(append(slices.Clone(ugrepArgs), "-rn", "TODO", "."), "\n")
	if res.Output != want || res.ExitCode != 3 {
		t.Errorf("got exit %d:\n%s\nwant exit 3:\n%s", res.ExitCode, res.Output, want)
	}
	if res := Run([]string{"find", "."}, nil); !strings.HasPrefix(res.Output, "bfs\n-S\ndfs\n") {
		t.Errorf("find: %q", res.Output)
	}
	if res := Run([]string{"grep", "--null", "x"}, nil); res.Output != "system-grep\n" || res.ExitCode != 0 {
		t.Errorf("grep --null: exit %d %q", res.ExitCode, res.Output)
	}
	r, err := Resolve([]string{"grep", "x"}, os.Getenv)
	if cmd := r.Command(); err != nil || cmd.Args[0] != "ugrep" || cmd.Env != nil {
		t.Errorf("zsh doesn't export ARGV0 and bash uses exec -a: args %q env %q", cmd.Args, cmd.Env)
	}
}

func TestSearchMatchesAgentShell(t *testing.T) {
	if os.Getenv("CLAUDECODE") != "1" || !isExec(os.Getenv("CLAUDE_CODE_EXECPATH")) {
		t.Skip("not under Claude Code")
	}
	snap := agentSnapshot(t)
	shell, err := exec.LookPath(strings.SplitN(strings.TrimPrefix(filepath.Base(snap), "snapshot-"), "-", 2)[0])
	if err != nil {
		t.Skip("no shell for", snap)
	}
	dir := t.TempDir()
	for name, body := range map[string]string{
		"a.txt":             "TODO one\n",
		"sub/b.go":          "// TODO two\nx := 1\n",
		".hidden/c.txt":     "TODO hidden\n",
		".gitignore":        "ignored/\n*.log\n",
		"ignored/d.txt":     "TODO ignored\n",
		"e.log":             "TODO log\n",
		".git/f.txt":        "TODO in .git\n",
		"bin.dat":           "TODO\x00binary\n",
		"node_modules/g.js": "// TODO dep\n",
		"Upper.TXT":         "todo upper\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	for _, argv := range [][]string{
		{"grep", "-rn", "TODO", "."},
		{"grep", "-rni", "todo", "."},
		{"grep", "-rn", "--null", "TODO", "."},
		{"grep", "-n", "TODO", "a.txt", "sub/b.go"},
		{"rg", "-n", "TODO"},
		{"rg", "--hidden", "-n", "TODO"},
		{"find", ".", "-name", "*.txt"},
		{"find", ".", "-type", "f"},
		{"find", ".", "-regex", `.*\.\(go\|js\)`},
	} {
		script := `source "$0" >/dev/null 2>&1; "$@"`
		out, err := exec.Command(shell, append([]string{"-c", script, snap}, argv...)...).CombinedOutput()
		want := 0
		if ee, ok := err.(*exec.ExitError); ok {
			want = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		got := Run(argv, nil)
		if got.ExitCode != want || !slices.Equal(sortedLines(got.Output), sortedLines(string(out))) {
			t.Errorf("%q: lx exit %d, agent shell exit %d\nlx:\n%s\nagent shell:\n%s", argv, got.ExitCode, want, got.Output, out)
		}
	}
}

func sortedLines(s string) []string {
	l := strings.Split(strings.TrimRight(s, "\n"), "\n")
	slices.Sort(l)
	return l
}

var reSourced = regexp.MustCompile(`(?:^|\s)source\s+(?:'([^']*shell-snapshots/[^']*)'|(\S*shell-snapshots/\S*))`)

func agentSnapshot(t *testing.T) string {
	t.Helper()
	pid := os.Getppid()
	for range 8 {
		out, err := exec.Command("ps", "-ww", "-o", "ppid=", "-o", "args=", "-p", strconv.Itoa(pid)).Output()
		if err != nil {
			break
		}
		ppid, args, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
		if m := reSourced.FindStringSubmatch(args); m != nil {
			return m[1] + m[2]
		}
		if pid, err = strconv.Atoi(ppid); err != nil || pid <= 1 {
			break
		}
	}
	t.Skip("can't find the shell snapshot the agent's shell sourced")
	return ""
}
