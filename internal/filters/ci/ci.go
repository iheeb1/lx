// Package ci handles gh run and pr checks output, and monorepo task output.
package ci

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

func init() {
	engine.Register(runLog{})
	engine.Register(runView{})
	engine.Register(runWatch{})
	engine.Register(prChecks{})
	engine.RegisterDetector(engine.Detector{
		Name:   "monorepo",
		Detect: detectTasks,
		Filter: tasksFilter{},
		Argv:   []string{"monorepo"},
	})
}

var ghValue = map[string]bool{
	"-R": true, "--repo": true, "-a": true, "--attempt": true, "-j": true, "--job": true,
	"-i": true, "--interval": true, "--hostname": true, "-b": true, "--branch": true,
}

var jobLogsRe = lazyre.New(`^/?repos/[^/\s]+/[^/\s]+/actions/jobs/\d+/logs$`)

func ghWords(c *engine.Context) []string {
	if n := strings.TrimSuffix(c.Name(), ".exe"); n != "gh" {
		return nil
	}
	args := c.Args()
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return append(pos, args[i+1:]...)
		}
		if len(a) > 1 && a[0] == '-' {
			if ghValue[a] {
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return pos
}

func ghIs(c *engine.Context, cmd, sub string) bool {
	w := ghWords(c)
	return len(w) >= 2 && w[0] == cmd && w[1] == sub && plainGH(c)
}

func plainGH(c *engine.Context) bool {
	return !engine.MachineReadable(c) && !c.HasFlag("-w", "--web", "-q", "--jq", "-t", "--template", "--json")
}

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func safeApply(f engine.Filter, c *engine.Context, in string) (out string, ok bool) {
	defer func() {
		if recover() != nil {
			out, ok = "", false
		}
	}()
	return f.Apply(c, in)
}

func safeDetect(d engine.Detector, s string) (hit bool) {
	defer func() {
		if recover() != nil {
			hit = false
		}
	}()
	return d.Detect(s)
}

func isContent(f engine.Filter) bool {
	ct, ok := f.(engine.Content)
	return ok && ct.IsContent()
}

func own(f engine.Filter) bool {
	switch f.(type) {
	case runLog, runView, runWatch, prChecks, tasksFilter:
		return true
	}
	return false
}

func selfGuard(in []string, exempt map[int]bool, out string) string {
	var b strings.Builder
	for i, ln := range in {
		if exempt[i] || !engine.IsError(ln) {
			continue
		}
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return out
	}
	res, _ := engine.Guard(b.String(), out)
	return res
}

func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func nameList(names []string, max int, sep string) string {
	if len(names) <= max {
		return strings.Join(names, sep)
	}
	return strings.Join(names[:max], sep) + fmt.Sprintf("%s… +%d more", sep, len(names)-max)
}
