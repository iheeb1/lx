package ci

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

var (
	runHeadRe = lazyre.New(`^[✓X*!-] \S.* · \d+$`)
	jobRowRe  = lazyre.New(`^([✓X*!-]) (.+?)(?: in \S+)? \(ID \d+\)$`)
	stepRowRe = lazyre.New(`^  ([✓X*!-]) \S`)
	annMsgRe  = lazyre.New(`^([X!-]) (.*)$`)
	annLocRe  = lazyre.New(`^.+: \S+#\d+$`)
)

type runView struct{}

func (runView) Name() string { return "gh-run-view" }

func (runView) GuardsErrors() bool { return true }

func (runView) Match(c *engine.Context) bool {
	return ghIs(c, "run", "view") && !c.HasFlag("--log", "--log-failed")
}

func (runView) Apply(c *engine.Context, out string) (string, bool) {
	lines := splitLines(out)
	s, ok := summarize(lines)
	if !ok {
		return "", false
	}
	return selfGuard(lines, nil, strings.Join(s, "\n")), true
}

type runWatch struct{}

func (runWatch) Name() string { return "gh-run-watch" }

func (runWatch) GuardsErrors() bool { return true }

func (runWatch) Match(c *engine.Context) bool { return ghIs(c, "run", "watch") }

func (runWatch) Apply(c *engine.Context, out string) (string, bool) {
	lines := splitLines(out)
	last, refreshes := -1, 0
	for i, ln := range lines {
		switch {
		case strings.HasPrefix(ln, "Refreshing run status every "):
			refreshes++
		case runHeadRe.MatchString(ln):
			last = i
		}
	}
	if last < 0 {
		return "", false
	}
	s, ok := summarize(lines[last:])
	if !ok {
		return "", false
	}
	var res []string
	if refreshes > 1 {
		res = append(res, fmt.Sprintf("[lx: %d status refreshes; the latest is shown]", refreshes))
	}
	res = append(res, s...)
	return selfGuard(lines, nil, strings.Join(res, "\n")), true
}

type annotationGroup struct {
	head string
	locs []string
}

func summarize(lines []string) ([]string, bool) {
	var out []string
	jobs, hidden := 0, 0
	section := ""
	var icon string
	var groups []*annotationGroup
	byHead := map[string]*annotationGroup{}
	var pending *annotationGroup
	flushAnn := func() {
		for _, g := range groups {
			if len(g.locs) <= 1 {
				out = append(out, g.head)
				out = append(out, g.locs...)
				continue
			}
			out = append(out, fmt.Sprintf("%s [×%d]", g.head, len(g.locs)))
			if strings.HasPrefix(g.head, "X ") {
				out = append(out, "  "+strings.Join(g.locs, " · "))
			} else {
				out = append(out, "  "+nameList(g.locs, 3, " · "))
			}
		}
		groups, byHead, pending = nil, map[string]*annotationGroup{}, nil
	}
	for _, ln := range lines {
		switch section {
		case "jobs":
			if m := jobRowRe.FindStringSubmatch(ln); m != nil {
				jobs++
				icon = m[1]
				out = append(out, ln)
				continue
			}
			if m := stepRowRe.FindStringSubmatch(ln); m != nil {
				if icon == "✓" || icon == "-" || m[1] == "✓" || m[1] == "-" {
					hidden++
				} else {
					out = append(out, ln)
				}
				continue
			}
			if ln == "ANNOTATIONS" {
				section = "annotations"
			}
			out = append(out, ln)
			continue
		case "annotations":
			if ln == "" {
				continue
			}
			if pending != nil && annLocRe.MatchString(ln) {
				pending.locs = append(pending.locs, ln)
				pending = nil
				continue
			}
			if annMsgRe.MatchString(ln) && !jobRowRe.MatchString(ln) {
				g := byHead[ln]
				if g == nil {
					g = &annotationGroup{head: ln}
					byHead[ln] = g
					groups = append(groups, g)
				}
				pending = g
				continue
			}
			flushAnn()
			out = append(out, "")
			section = "tail"
		}
		if ln == "JOBS" && section == "" {
			section = "jobs"
		} else if section == "" && jobRowRe.MatchString(ln) {
			section = "jobs"
			jobs++
			icon = jobRowRe.FindStringSubmatch(ln)[1]
		}
		out = append(out, ln)
	}
	flushAnn()
	if jobs == 0 {
		return nil, false
	}
	if hidden > 0 {
		out = append([]string{fmt.Sprintf("[lx: %s not shown (passed or skipped)]", plural(hidden, "step line", "step lines"))}, out...)
	}
	return out, true
}

type prChecks struct{}

func (prChecks) Name() string { return "gh-pr-checks" }

func (prChecks) GuardsErrors() bool { return true }

func (prChecks) Match(c *engine.Context) bool {
	return ghIs(c, "pr", "checks") && !c.HasFlag("--watch")
}

func (prChecks) Apply(c *engine.Context, out string) (string, bool) {
	lines := splitLines(out)
	var res, pass, skip []string
	rows := 0
	for _, ln := range lines {
		f := strings.Split(ln, "\t")
		if len(f) < 4 {
			res = append(res, ln)
			continue
		}
		rows++
		switch {
		case engine.IsError(ln):
			res = append(res, ln)
		case f[1] == "pass":
			pass = append(pass, f[0])
		case f[1] == "skipping":
			skip = append(skip, f[0])
		default:
			res = append(res, ln)
		}
	}
	if rows < 2 {
		return "", false
	}
	if len(pass) > 0 {
		res = append(res, fmt.Sprintf("[lx: %s: %s]", plural(len(pass), "passing check", "passing checks"), nameList(pass, 30, "; ")))
	}
	if len(skip) > 0 {
		res = append(res, fmt.Sprintf("[lx: %s: %s]", plural(len(skip), "skipped check", "skipped checks"), nameList(skip, 30, "; ")))
	}
	return selfGuard(lines, nil, strings.Join(res, "\n")), true
}
