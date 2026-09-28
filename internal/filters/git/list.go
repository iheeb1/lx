package git

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

func init() { engine.Register(listFilter{}) }

type listFilter struct{}

func (listFilter) Name() string    { return "git-list" }
func (listFilter) IsContent() bool { return true }

func (listFilter) Match(c *engine.Context) bool {
	if !isGit(c) || engine.MachineReadable(c) {
		return false
	}
	p := positionals(c)
	first := ""
	if len(p) > 0 {
		first = p[0]
	}
	switch c.Sub() {
	case "reflog":
		return first == "" || first == "show" || !isReflogVerb(first)
	case "branch":
		return !hasArg(c, branchWriteFlags...) && !strings.ContainsAny(shortLetters(c), "dDmMcCuf")
	case "tag":
		return hasNFlag(c) && !hasArg(c, "-d", "--delete", "-v", "--verify", "-a", "-s", "-m", "-F", "-f", "--force")
	case "worktree":
		return first == "list"
	case "shortlog", "cherry", "ls-remote", "show-branch", "clean", "rm", "mv", "add":
		return true
	case "remote":
		return first == "show"
	case "submodule":
		return first == "" || first == "status" || first == "summary"
	case "notes":
		return first == "" || first == "list"
	}
	return false
}

var branchWriteFlags = []string{"-d", "-D", "--delete", "-m", "-M", "--move", "-c", "-C", "--copy",
	"-u", "--set-upstream-to", "--set-upstream-to=", "--unset-upstream", "--edit-description", "-f", "--force"}

func shortLetters(c *engine.Context) string {
	var b strings.Builder
	for _, a := range subArgs(c) {
		if len(a) > 2 && a[0] == '-' && a[1] != '-' && strings.Trim(a[1:], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") == "" {
			b.WriteString(a[1:])
		}
	}
	return b.String()
}

func isReflogVerb(s string) bool {
	switch s {
	case "expire", "delete", "exists", "drop":
		return true
	}
	return false
}

func hasNFlag(c *engine.Context) bool {
	for _, a := range subArgs(c) {
		if a == "-n" || strings.HasPrefix(a, "-n") && len(a) > 2 && a[2] >= '0' && a[2] <= '9' {
			return true
		}
	}
	return false
}

func (listFilter) Apply(c *engine.Context, out string) (string, bool) {
	if strings.TrimSpace(out) == "" {
		return "", false
	}
	return headLines(out, logBudget), true
}
