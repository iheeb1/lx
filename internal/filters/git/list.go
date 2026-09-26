package git

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

func init() { engine.Register(listFilter{}) }

// listFilter keeps git's listing commands as git prints them: reflog,
// verbose branch listings (-v, -vv), annotated tag listings (tag -n),
// worktree list, shortlog, cherry, ls-remote, remote show, submodule
// status, show-branch and notes list, and the per-file reports of clean,
// rm, mv and add ("Would remove x", "rm 'x'"). Every line is an item an
// agent may act on (a reflog entry to reset to, a file a dry run would
// delete), and the generic reducer would fold runs of look-alike items
// (consecutive amend entries, numbered files) into "… N similar lines …"
// or "test<N>.out ×24". Output over the budget is cut from the end with an exact
// count, keeping the head (newest reflog entries, first branches); git's
// own diagnostics are never cut.
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
		// Read-only listings the branch filter (registered first) does
		// not take: -v, -vv, -av, --format-free verbose forms.
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

// branchWriteFlags create, delete, rename, copy or configure branches.
var branchWriteFlags = []string{"-d", "-D", "--delete", "-m", "-M", "--move", "-c", "-C", "--copy",
	"-u", "--set-upstream-to", "--set-upstream-to=", "--unset-upstream", "--edit-description", "-f", "--force"}

// shortLetters returns the letters of combined single-dash flags ("-av"
// → "av"); single-letter flags are left to hasArg.
func shortLetters(c *engine.Context) string {
	var b strings.Builder
	for _, a := range subArgs(c) {
		if len(a) > 2 && a[0] == '-' && a[1] != '-' && strings.Trim(a[1:], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") == "" {
			b.WriteString(a[1:])
		}
	}
	return b.String()
}

// isReflogVerb reports whether a reflog positional is a subcommand other
// than show/list (expire, delete, exists); anything else is a ref to show.
func isReflogVerb(s string) bool {
	switch s {
	case "expire", "delete", "exists", "drop":
		return true
	}
	return false
}

// hasNFlag reports a -n / -n<num> tag flag.
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
