package git

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
	"github.com/iheeb1/lx/internal/tokens"
)

func isGit(c *engine.Context) bool {
	return strings.TrimSuffix(c.Name(), ".exe") == "git"
}

func subArgs(c *engine.Context) []string {
	args := c.Args()
	sub := c.Sub()
	for i, a := range args {
		if a == sub {
			rest := args[i+1:]
			for j, r := range rest {
				if r == "--" {
					return rest[:j]
				}
			}
			return rest
		}
	}
	return nil
}

func hasArg(c *engine.Context, names ...string) bool {
	for _, a := range subArgs(c) {
		for _, n := range names {
			if a == n || strings.HasSuffix(n, "=") && strings.HasPrefix(a, n) {
				return true
			}
		}
	}
	return false
}

func positionals(c *engine.Context) []string {
	var out []string
	for _, a := range subArgs(c) {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

func join(lines []string) string {
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func wrapItems(indent string, items []string, sep string, width int) []string {
	var out []string
	var b strings.Builder
	open := false
	for _, it := range items {
		if open && b.Len() > len(indent) && b.Len()+len(sep)+len(it) > width {
			out = append(out, strings.TrimRight(b.String(), " "))
			b.Reset()
			open = false
		}
		if !open {
			b.WriteString(indent)
			open = true
		} else {
			b.WriteString(sep)
		}
		b.WriteString(it)
	}
	if open {
		out = append(out, b.String())
	}
	return out
}

func capItems(items []string, max int) []string {
	if len(items) <= max {
		return items
	}
	out := append([]string(nil), items[:max]...)
	return append(out, fmt.Sprintf("… +%d more", len(items)-max))
}

func countTokens(lines []string) int {
	if len(lines) == 0 {
		return 0
	}
	return tokens.Count(strings.Join(lines, "\n")) + 1
}

func fitsBudget(lines []string, budget int) bool {
	const chunk = 256
	total, chunks := 0, 0
	for i := 0; i < len(lines); i += chunk {
		total += tokens.Count(strings.Join(lines[i:min(i+chunk, len(lines))], "\n")) + 1
		chunks++
		if total-2*chunks > budget {
			return false
		}
	}
	return countTokens(lines) <= budget
}

func wrapList(indent string, items []string, width int) []string {
	out := wrapItems(indent, items, ", ", width)
	for i := 0; i < len(out)-1; i++ {
		out[i] += ","
	}
	return out
}

var (
	statRowRe = lazyre.New(`^ (\S.*?) +\| +(\d+) ?([+-]*)$`)
	statBinRe = lazyre.New(`^ (\S.*?) +\| +(Bin(?: \d+ -> \d+ bytes)?)$`)

	statSumRe = lazyre.New(`^ (\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?$`)
)

type statRow struct {
	path     string
	n        int
	ins, del int
	exact    bool
	bin      string
	label    string
}

func (r statRow) desc() string {
	switch {
	case r.label != "":
		return r.label
	case r.bin != "":
		return r.bin
	case !r.exact:
		return fmt.Sprintf("±%d", r.n)
	case r.n == 0:
		return "0"
	case r.del == 0:
		return fmt.Sprintf("+%d", r.ins)
	case r.ins == 0:
		return fmt.Sprintf("-%d", r.del)
	}
	return fmt.Sprintf("+%d -%d", r.ins, r.del)
}

func parseStatRow(ln string) (statRow, bool) {
	if len(ln) < 5 || ln[0] != ' ' || !strings.Contains(ln, "|") {
		return statRow{}, false
	}
	if m := statBinRe.FindStringSubmatch(ln); m != nil {
		return statRow{path: m[1], bin: m[2]}, true
	}
	m := statRowRe.FindStringSubmatch(ln)
	if m == nil {
		return statRow{}, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return statRow{}, false
	}
	plus := strings.Count(m[3], "+")
	minus := len(m[3]) - plus
	r := statRow{path: m[1], n: n}
	switch {
	case n == 0 && plus+minus == 0:
		r.exact = true
	case minus == 0 && plus > 0:
		r.ins, r.exact = n, true
	case plus == 0 && minus > 0:
		r.del, r.exact = n, true
	case plus+minus == n:
		r.ins, r.del, r.exact = plus, minus, true
	case plus > 0 && minus > 0:
	default:
		return statRow{}, false
	}
	return r, true
}

func resolveStat(rows []statRow, summary string) {
	m := statSumRe.FindStringSubmatch(summary)
	if m == nil {
		return
	}
	files, _ := strconv.Atoi(m[1])
	ins, _ := strconv.Atoi(m[2])
	del, _ := strconv.Atoi(m[3])
	if files != len(rows) {
		return
	}
	open := -1
	for i, r := range rows {
		switch {
		case r.bin != "":
		case r.exact:
			ins -= r.ins
			del -= r.del
		case open >= 0:
			return
		default:
			open = i
		}
	}
	if open >= 0 && ins > 0 && del > 0 && ins+del == rows[open].n {
		rows[open].ins, rows[open].del, rows[open].exact = ins, del, true
	}
}

const maxStatRows = 300

func renderGitStat(rows []statRow, indent string) []string {
	if len(rows) <= maxStatRows {
		return renderStat(rows, indent, 0)
	}
	return renderStat(rows[:maxStatRows], indent, len(rows)-maxStatRows)
}

func renderStat(rows []statRow, indent string, more int) []string {
	paths := make([]string, len(rows))
	labels := make([]string, len(rows))
	for i, r := range rows {
		paths[i], labels[i] = r.path, r.desc()
	}
	items := groupByDir(paths, labels)
	if more > 0 {
		items = append(items, fmt.Sprintf("… +%d more files", more))
	}
	return wrapList(indent, items, statWidth)
}

func groupByDir(paths, labels []string) []string {
	type group struct {
		dir   string
		items []string
	}
	var groups []*group
	byDir := map[string]*group{}
	for i, p := range paths {
		dir, base := "", p
		if !strings.ContainsAny(p, "{}") && !strings.Contains(p, " => ") && !strings.HasPrefix(p, "...") {
			if j := strings.LastIndexByte(p, '/'); j >= 0 {
				dir, base = p[:j+1], p[j+1:]
			}
		}
		if dir == "" {
			dir = "\x00" + p
		}
		if labels != nil && labels[i] != "" {
			base += " " + labels[i]
		}
		g := byDir[dir]
		if g == nil {
			g = &group{dir: dir}
			byDir[dir] = g
			groups = append(groups, g)
		}
		g.items = append(g.items, base)
	}
	var items []string
	for _, g := range groups {
		switch {
		case strings.HasPrefix(g.dir, "\x00"):
			items = append(items, g.items...)
		case len(g.items) == 1:
			items = append(items, g.dir+g.items[0])
		default:
			for _, chunk := range wrapItems("", g.items, ", ", statWidth-len(g.dir)-2) {
				items = append(items, g.dir+"{"+chunk+"}")
			}
		}
	}
	return items
}

const statWidth = 160
