package git

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tokens"
)

// Shared helpers for the git filters.
//
// Every filter here follows the same rule: an input line is either printed
// verbatim, dropped because it is provably redundant or pure noise (index
// lines, progress meters, headers that repeat the path on the line above),
// or folded into a marker that says exactly what was folded and how much.
// Lines a filter does not recognize are printed verbatim.

// isGit reports whether c runs git (argv[0] may be a path or git.exe).
func isGit(c *engine.Context) bool {
	return strings.TrimSuffix(c.Name(), ".exe") == "git"
}

// subArgs returns the arguments after the git subcommand, stopping at "--"
// (pathspecs), with git's global options before the subcommand skipped.
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

// hasArg reports whether any subcommand argument equals one of names or, for
// names ending in "=", starts with it.
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

// positionals returns the non-flag subcommand arguments (a rough cut: flag
// values given as separate words are included).
func positionals(c *engine.Context) []string {
	var out []string
	for _, a := range subArgs(c) {
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

// join renders lines with no trailing newline.
func join(lines []string) string {
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// wrapItems joins items with sep into lines of at most width bytes, each
// starting with indent. Items are never split.
func wrapItems(indent string, items []string, sep string, width int) []string {
	var out []string
	var b strings.Builder
	open := false // b holds a started line (even an empty item makes one)
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

// capItems keeps the first max items and appends "… +N more" when needed.
func capItems(items []string, max int) []string {
	if len(items) <= max {
		return items
	}
	out := append([]string(nil), items[:max]...)
	return append(out, fmt.Sprintf("… +%d more", len(items)-max))
}

// countTokens is tokens.Count of lines joined by newlines, plus one for the
// newline that joins them to what comes before.
func countTokens(lines []string) int {
	if len(lines) == 0 {
		return 0
	}
	return tokens.Count(strings.Join(lines, "\n")) + 1
}

// fitsBudget reports whether countTokens(lines) <= budget. Outputs far over
// budget are rejected after counting only a prefix: the tokenizer never
// joins text across a line break, so the counts of chunks of whole lines
// add up to the joint count within about 2 tokens per chunk (rounding and
// the joining newline). A "fits" answer is always the exact count's.
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

// wrapList joins items with ", " into lines of about width bytes; a line
// that continues on the next one ends with ",".
func wrapList(indent string, items []string, width int) []string {
	out := wrapItems(indent, items, ", ", width)
	for i := 0; i < len(out)-1; i++ {
		out[i] += ","
	}
	return out
}

// ---- diffstat rows ("path | 12 +++---") ----------------------------------

var (
	statRowRe = regexp.MustCompile(`^ (\S.*?) +\| +(\d+) ?([+-]*)$`)
	statBinRe = regexp.MustCompile(`^ (\S.*?) +\| +(Bin(?: \d+ -> \d+ bytes)?)$`)
	// statSumRe is git's own summary line, printed verbatim.
	statSumRe = regexp.MustCompile(`^ (\d+) files? changed(?:, (\d+) insertions?\(\+\))?(?:, (\d+) deletions?\(-\))?$`)
)

// statRow is one parsed diffstat line.
type statRow struct {
	path     string
	n        int
	ins, del int
	exact    bool   // ins and del are known
	bin      string // "Bin 0 -> 12 bytes" for binary files
	label    string // overrides the computed description
}

// desc renders the change counts: "+3 -1", "+24", "-7", "0", "Bin …", or
// "±97" when git scaled the graph and the split is unknown.
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

// parseStatRow parses one " path | N +-" diffstat line.
//
// The graph is scaled when a file has more changes than fit in the terminal
// width, but git gives every non-zero side at least one character, so a
// graph with only '+' (only '-') still proves there were no deletions
// (insertions). When the graph is unscaled (its length equals N) the exact
// split is known; resolveStat can recover one more from the summary line.
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

// resolveStat fills in the split of the one row whose graph was scaled
// when git's summary line accounts for every row: its insertions and
// deletions are the totals minus those of the exactly known rows.
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
			return // two unknown rows: the split is not determined
		default:
			open = i
		}
	}
	if open >= 0 && ins > 0 && del > 0 && ins+del == rows[open].n {
		rows[open].ins, rows[open].del, rows[open].exact = ins, del, true
	}
}

// maxStatRows caps the files listed from one diffstat; git's own
// "N files changed" line (always printed) keeps the totals.
const maxStatRows = 300

// renderGitStat renders a diffstat git printed: renderStat of its first
// maxStatRows rows, the rest counted.
func renderGitStat(rows []statRow, indent string) []string {
	if len(rows) <= maxStatRows {
		return renderStat(rows, indent, 0)
	}
	return renderStat(rows[:maxStatRows], indent, len(rows)-maxStatRows)
}

// renderStat renders diffstat rows as a compact list: files sharing a
// directory are written dir/{a.go +1 -2, b.go +4}. Paths git abbreviated or
// wrote as renames (".../x", "a => b", "{a => b}") are printed as they are.
// more > 0 adds a "… +more more files" item.
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

// groupByDir turns paths (each with an optional label) into list items,
// writing files that share a directory as dir/{a.go, b.go}. Files at the
// top level, and paths git abbreviated or wrote as renames (".../x",
// "a => b", "{a => b}"), are items of their own.
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
			dir = "\x00" + p // never grouped
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
			// Long groups are split into several dir/{…} items so that no
			// line grows past the wrap width by much.
			for _, chunk := range wrapItems("", g.items, ", ", statWidth-len(g.dir)-2) {
				items = append(items, g.dir+"{"+chunk+"}")
			}
		}
	}
	return items
}

// statWidth is the wrap width of compact file lists.
const statWidth = 160
