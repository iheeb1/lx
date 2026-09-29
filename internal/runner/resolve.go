package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type Resolved struct {
	Path string
	Args []string

	Builtin string
}

var claudeShims = map[string][]string{
	"grep": {"ugrep", "-G", "--ignore-files", "--hidden", "-I", "--exclude-dir=.git", "--exclude-dir=.svn",
		"--exclude-dir=.hg", "--exclude-dir=.bzr", "--exclude-dir=.jj", "--exclude-dir=.sl"},
	"find": {"bfs", "-S", "dfs", "-regextype", "findutils-default"},
	"rg":   {"rg"},
}

func ClaudeShimmed(name string) bool { return claudeShims[name] != nil }

func Resolve(argv []string, getenv func(string) string) (Resolved, error) {
	path, err := exec.LookPath(argv[0])
	if bin := claudeTool(argv, err == nil, getenv); bin != "" {
		shim := claudeShims[argv[0]]
		args := append(append([]string(nil), shim...), argv[1:]...)
		return Resolved{Path: bin, Args: args, Builtin: shim[0]}, nil
	}
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Path: path, Args: argv}, nil
}

func (r Resolved) Command() *exec.Cmd {
	cmd := exec.Command(r.Path, r.Args[1:]...)
	cmd.Args[0] = r.Args[0]
	// Git Bash passes it as ARGV0=, which bash exports
	if r.Builtin != "" && runtime.GOOS == "windows" {
		cmd.Env = append(os.Environ(), "ARGV0="+r.Builtin)
	}
	return cmd
}

func claudeTool(argv []string, onPath bool, getenv func(string) string) string {
	tool := argv[0]
	if !ClaudeShimmed(tool) || getenv("CLAUDECODE") != "1" {
		return ""
	}
	switch {
	case tool == "rg" && onPath:
		return ""
	case tool == "grep" && grepOptOut(argv[1:]):
		return ""
	case tool != "rg" && getenv("CLAUDE_CODE_ENTRYPOINT") == "local-agent":
		return ""
	}
	exe := getenv("CLAUDE_CODE_EXECPATH")
	if exe == "" && onPath || jsRuntime(exe) {
		return ""
	}
	return ClaudeBinary(getenv)
}

func ClaudeBinary(getenv func(string) string) string {
	if exe := getenv("CLAUDE_CODE_EXECPATH"); isExec(exe) {
		return exe
	}
	home, name := getenv("HOME"), "claude"
	if runtime.GOOS == "windows" {
		home, name = getenv("USERPROFILE"), "claude.exe"
	}
	if p := filepath.Join(home, ".local", "bin", name); home != "" && isExec(p) {
		return p
	}
	return ""
}

func isExec(p string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && (runtime.GOOS == "windows" || fi.Mode()&0o111 != 0)
}

// Claude Code run as a script has no embedded tools
func jsRuntime(exe string) bool {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(exe)), ".exe")
	for _, rt := range []string{"node", "bun", "deno"} {
		if strings.HasPrefix(base, rt) {
			return true
		}
	}
	return false
}

func grepOptOut(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--null", a == "--null-data", strings.HasPrefix(a, "---"), strings.HasPrefix(a, "-@"):
			return true
		case len(a) < 2 || a[0] != '-':
			continue
		case a[1] != '-' && strings.ContainsAny(a[1:], "Zz"):
			return true
		}
		for _, s := range []string{"-filter", "-pager", "-view", "-format-open", "-config"} {
			if strings.Contains(a[1:], s) {
				return true
			}
		}
	}
	return false
}
