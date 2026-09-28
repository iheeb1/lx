package git

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

func init() {
	engine.Register(commitFilter{})
	engine.Register(tagFilter{})
	engine.Register(remoteFilter{})
	engine.Register(stashListFilter{})
}

type stashListFilter struct{}

func (stashListFilter) Name() string    { return "git-stash" }
func (stashListFilter) IsContent() bool { return true }

func (stashListFilter) Match(c *engine.Context) bool {
	p := positionals(c)
	return isGit(c) && c.Sub() == "stash" && len(p) > 0 && p[0] == "list" && !engine.MachineReadable(c)
}

func (stashListFilter) Apply(c *engine.Context, out string) (string, bool) {
	if strings.TrimSpace(out) == "" {
		return "", false
	}
	return headLines(out, logBudget), true
}

type commitFilter struct{}

func (commitFilter) Name() string { return "git-commit" }

func (commitFilter) Match(c *engine.Context) bool {
	return isGit(c) && c.Sub() == "commit" && !engine.MachineReadable(c)
}

var (
	commitHeadRe = lazyre.New(`^\[\S+(?: \(root-commit\))? [0-9a-f]{7,}\] `)
	hookOKRe     = lazyre.New(`^(.+?)\.{3,}(?:\(no files to check\))?(Passed|Skipped)$`)
)

func (commitFilter) Apply(c *engine.Context, out string) (string, bool) {
	var res, modes []string
	passed, skipped := 0, 0
	hookAt := -1
	recognized := false
	flushModes := func() {
		res = append(res, groupModes(modes)...)
		modes = nil
	}
	for _, ln := range strings.Split(out, "\n") {
		if isModeLine(ln) && !engine.IsError(ln) {
			modes = append(modes, ln)
			continue
		}
		flushModes()
		switch m := hookOKRe.FindStringSubmatch(ln); {
		case m != nil && !engine.IsError(ln):
			if hookAt < 0 {
				hookAt = len(res)
				res = append(res, "")
			}
			if m[2] == "Passed" {
				passed++
			} else {
				skipped++
			}
			recognized = true
		case ln == "":
		default:
			if commitHeadRe.MatchString(ln) || statSumRe.MatchString(ln) {
				recognized = true
			}
			res = append(res, ln)
		}
	}
	flushModes()
	if hookAt >= 0 {
		res[hookAt] = fmt.Sprintf("[pre-commit: %d passed, %d skipped hooks not shown]", passed, skipped)
	}
	if !recognized {
		return "", false
	}
	return join(res), true
}

type tagFilter struct{}

func (tagFilter) Name() string    { return "git-tag" }
func (tagFilter) IsContent() bool { return true }

func (tagFilter) Match(c *engine.Context) bool {
	if !isGit(c) || c.Sub() != "tag" || engine.MachineReadable(c) {
		return false
	}
	for _, a := range subArgs(c) {
		if strings.HasPrefix(a, "-n") || a == "--format" || a == "-d" || a == "--delete" || a == "-v" || a == "--verify" {
			return false
		}
	}
	return true
}

func (tagFilter) Apply(c *engine.Context, out string) (string, bool) {
	var names []string
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" {
			continue
		}
		if strings.ContainsAny(ln, " \t") {
			return "", false
		}
		names = append(names, ln)
	}
	if len(names) < minRefGroup {
		return "", false
	}
	res := []string{fmt.Sprintf("[%s]", plural(len(names), "tag"))}
	return join(append(res, wrapItems("", braceRuns(capItems(names, maxNames)), " ", statWidth)...)), true
}

func braceRuns(names []string) []string {
	prefix := func(s string) string {
		if i := strings.LastIndexByte(s, '.'); i > 0 && !strings.ContainsAny(s, "{},") {
			return s[:i+1]
		}
		return ""
	}
	var out []string
	for i := 0; i < len(names); {
		p := prefix(names[i])
		j := i + 1
		for p != "" && j < len(names) && prefix(names[j]) == p {
			j++
		}
		if j-i < 3 {
			out = append(out, names[i:j]...)
			i = j
			continue
		}
		rests := make([]string, 0, j-i)
		for _, n := range names[i:j] {
			rests = append(rests, n[len(p):])
		}
		out = append(out, p+"{"+strings.Join(rests, ",")+"}")
		i = j
	}
	return out
}

type remoteFilter struct{}

func (remoteFilter) Name() string    { return "git-remote" }
func (remoteFilter) IsContent() bool { return true }

func (remoteFilter) Match(c *engine.Context) bool {
	return isGit(c) && c.Sub() == "remote" && hasArg(c, "-v", "--verbose") && len(positionals(c)) == 0
}

var remoteLineRe = lazyre.New(`^(\S+)\t(\S+) \((fetch|push)\)$`)

func (remoteFilter) Apply(c *engine.Context, out string) (string, bool) {
	lines := strings.Split(out, "\n")
	var res []string
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		if ln == "" {
			continue
		}
		m := remoteLineRe.FindStringSubmatch(ln)
		if m == nil {
			return "", false
		}
		if m[3] == "fetch" && i+1 < len(lines) {
			if n := remoteLineRe.FindStringSubmatch(lines[i+1]); n != nil && n[1] == m[1] && n[2] == m[2] && n[3] == "push" {
				res = append(res, m[1]+"\t"+m[2]+" (fetch, push)")
				i++
				continue
			}
		}
		res = append(res, ln)
	}
	if len(res) == 0 {
		return "", false
	}
	return join(res), true
}
