package hook

import (
	"os"
	"path/filepath"
	"strings"
)

func (a *analysis) inClaudeWorktree(cwd string) bool {
	if underWorktree(cwd) || underWorktree(os.Getenv("CLAUDE_PROJECT_DIR")) {
		return true
	}
	for _, s := range a.segs {
		if len(s.argv) < 2 || s.argv[0] != "cd" && s.argv[0] != "pushd" {
			continue
		}
		d := s.argv[len(s.argv)-1]
		if !filepath.IsAbs(d) && cwd != "" {
			d = filepath.Join(cwd, d)
		}
		if underWorktree(d) {
			return true
		}
	}
	return false
}

func underWorktree(p string) bool {
	if p == "" {
		return false
	}
	if worktreePath(p) {
		return true
	}
	r, err := filepath.EvalSymlinks(p)
	return err == nil && worktreePath(r)
}

func worktreePath(p string) bool {
	return strings.Contains("/"+filepath.ToSlash(filepath.Clean(p))+"/", "/.claude/worktrees/")
}
