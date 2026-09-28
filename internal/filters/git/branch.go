package git

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

func init() { engine.Register(branchFilter{}) }

type branchFilter struct{}

func (branchFilter) Name() string    { return "git-branch" }
func (branchFilter) IsContent() bool { return true }

func (branchFilter) Match(c *engine.Context) bool {
	if !isGit(c) || c.Sub() != "branch" || engine.MachineReadable(c) {
		return false
	}

	for _, a := range subArgs(c) {
		switch a {
		case "-a", "--all", "-r", "--remotes", "--list", "-l", "--no-color", "--color", "--color=always",
			"--color=never", "--color=auto", "--no-column", "--merged", "--no-merged", "--contains", "--no-contains",
			"--sort", "--points-at":
		default:
			if strings.HasPrefix(a, "--sort=") || strings.HasPrefix(a, "--merged=") || strings.HasPrefix(a, "--no-merged=") ||
				strings.HasPrefix(a, "--contains=") || strings.HasPrefix(a, "--points-at=") || !strings.HasPrefix(a, "-") {
				continue
			}
			return false
		}
	}
	return true
}

var branchLineRe = lazyre.New(`^([*+ ]) (\S+)(?: -> (\S+))?$`)

const minWrapLocals = 20

func (branchFilter) Apply(c *engine.Context, out string) (string, bool) {
	remotePrefix := "remotes/"
	if hasArg(c, "-r", "--remotes") && !hasArg(c, "-a", "--all") {
		remotePrefix = ""
	}
	lines := strings.Split(out, "\n")
	var locals, current, symbolic []string
	type remote struct {
		prefix string
		names  []string
	}
	var remotes []*remote
	byPrefix := map[string]*remote{}
	for _, ln := range lines {
		if ln == "" {
			continue
		}
		if strings.HasPrefix(ln, "* (") || strings.HasPrefix(ln, "  (") {
			current = append(current, ln)
			continue
		}
		m := branchLineRe.FindStringSubmatch(ln)
		if m == nil {
			return "", false
		}
		name := m[2]
		switch {
		case m[3] != "":
			symbolic = append(symbolic, ln)
		case m[1] != " ":
			current = append(current, ln)
		case remotePrefix == "" || strings.HasPrefix(name, "remotes/"):
			rest := strings.TrimPrefix(name, remotePrefix)
			i := strings.IndexByte(rest, '/')
			if i < 0 {
				return "", false
			}
			prefix := remotePrefix + rest[:i+1]
			r := byPrefix[prefix]
			if r == nil {
				r = &remote{prefix: prefix}
				byPrefix[prefix] = r
				remotes = append(remotes, r)
			}
			r.names = append(r.names, rest[i+1:])
		default:
			locals = append(locals, name)
		}
	}
	var res []string
	res = append(res, current...)
	if len(locals) <= minWrapLocals {
		for _, l := range locals {
			res = append(res, "  "+l)
		}
	} else {
		res = append(res, fmt.Sprintf("%d other local branches:", len(locals)))
		res = append(res, wrapItems("  ", capItems(locals, maxNames), " ", statWidth)...)
	}
	res = append(res, symbolic...)
	for _, r := range remotes {
		if len(r.names) < minRefGroup {
			for _, n := range r.names {
				res = append(res, "  "+r.prefix+n)
			}
			continue
		}
		res = append(res, fmt.Sprintf("  %s (%d):", r.prefix, len(r.names)))
		res = append(res, wrapItems("    ", capItems(r.names, maxNames), " ", statWidth)...)
	}
	if len(res) == 0 {
		return "", false
	}
	return join(res), true
}
