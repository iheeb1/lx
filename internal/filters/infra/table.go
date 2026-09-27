package infra

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"sort"
	"strings"
)

// table is a fixed-width table as printed by docker and kubectl (Go's
// tabwriter): a header of upper-case column names separated by 2+ spaces,
// then rows whose cells start at (or, for right-aligned columns, end at)
// the header's column positions.
type table struct {
	header string
	cols   []column
	lines  []string   // row lines, verbatim
	cells  [][]string // cells per row, one per column ("" when empty)
}

type column struct {
	name  string
	start int // byte offset in the header
}

var (
	headerNameRe = lazyre.New(`^[A-Z][A-Z0-9()/%_.-]*(?: [A-Z0-9()/%_.-]+)*$`)
	cellSplitRe  = lazyre.New(`\S+(?: \S+)*`)
)

// isPerlSpace is \s in RE2 syntax: tab, newline, form feed, carriage
// return, space.
func isPerlSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\n' || b == '\f' || b == '\r' }

// cellSpans returns the [start, end) byte spans of the cells of a table
// line: runs of non-space text joined by single spaces, exactly what
// cellSplitRe (`\S+(?: \S+)*`) matches, without the regexp's cost on
// 50k-row tables.
func cellSpans(s string) [][2]int {
	var out [][2]int
	for i := 0; i < len(s); {
		for i < len(s) && isPerlSpace(s[i]) {
			i++
		}
		if i >= len(s) {
			break
		}
		start := i
		for {
			for i < len(s) && !isPerlSpace(s[i]) {
				i++
			}
			if i+1 < len(s) && s[i] == ' ' && !isPerlSpace(s[i+1]) {
				i++
				continue
			}
			break
		}
		out = append(out, [2]int{start, i})
	}
	return out
}

// parseHeader splits a header line into columns. It fails unless there are
// at least two upper-case column names.
func parseHeader(h string) ([]column, bool) {
	if strings.HasPrefix(h, " ") || strings.Contains(h, "\t") {
		return nil, false
	}
	var cols []column
	for _, loc := range cellSpans(h) {
		name := h[loc[0]:loc[1]]
		// Names are separated by 2+ spaces; a single space joins words of
		// one name ("CONTAINER ID"), handled by the regexp.
		if !headerNameRe.MatchString(name) {
			return nil, false
		}
		cols = append(cols, column{name: name, start: loc[0]})
	}
	return cols, len(cols) >= 2
}

// parseTable parses a header and rows. It fails when a row's cells cannot
// be placed in distinct columns in order.
func parseTable(lines []string) (*table, bool) {
	if len(lines) == 0 {
		return nil, false
	}
	cols, ok := parseHeader(lines[0])
	if !ok {
		return nil, false
	}
	t := &table{header: lines[0], cols: cols}
	for _, ln := range lines[1:] {
		cells, ok := splitRow(ln, cols)
		if !ok {
			return nil, false
		}
		t.lines = append(t.lines, ln)
		t.cells = append(t.cells, cells)
	}
	return t, true
}

// splitRow places each run of text (separated by 2+ spaces) in the column
// whose span contains its start; a cell starting before its column's
// header (right-aligned and wider than the header) goes to the column
// whose header ends where the cell ends. The last column takes the rest of
// the line (free text such as MESSAGE may hold double spaces).
func splitRow(ln string, cols []column) ([]string, bool) {
	cells := make([]string, len(cols))
	last := -1
	for _, loc := range cellSpans(ln) {
		s, e := loc[0], loc[1]
		k := len(cols) - 1
		for i := 1; i < len(cols); i++ {
			if s < cols[i].start {
				k = i - 1
				break
			}
		}
		if k+1 < len(cols) && e == cols[k+1].start+len(cols[k+1].name) {
			k++
		}
		if k <= last {
			return nil, false
		}
		if k == len(cols)-1 {
			cells[k] = strings.TrimSpace(ln[s:])
			return cells, true
		}
		cells[k] = ln[s:e]
		last = k
	}
	return cells, true
}

func (t *table) col(name string) int {
	for i, c := range t.cols {
		if c.name == name {
			return i
		}
	}
	return -1
}

// render prints the header and the given rows re-aligned (two spaces
// between columns), without the columns in drop.
func (t *table) render(rows []int, drop map[int]bool) []string {
	width := make([]int, len(t.cols))
	for i, c := range t.cols {
		width[i] = len(c.name)
	}
	for _, r := range rows {
		for i, cell := range t.cells[r] {
			width[i] = max(width[i], len([]rune(cell)))
		}
	}
	line := func(cells []string) string {
		var b strings.Builder
		first := true
		for i, cell := range cells {
			if drop[i] {
				continue
			}
			if !first {
				b.WriteString("  ")
			}
			first = false
			b.WriteString(cell)
			if pad := width[i] - len([]rune(cell)); pad > 0 {
				b.WriteString(strings.Repeat(" ", pad))
			}
		}
		return strings.TrimRight(b.String(), " ")
	}
	names := make([]string, len(t.cols))
	for i, c := range t.cols {
		names[i] = c.name
	}
	out := []string{line(names)}
	for _, r := range rows {
		out = append(out, line(t.cells[r]))
	}
	return out
}

// constantCols returns the columns whose value is the same non-empty text
// in every row (3+ rows), as "NAME=value" notes.
func (t *table) constantCols(skip map[string]bool) (map[int]bool, []string) {
	drop := map[int]bool{}
	var notes []string
	if len(t.cells) < 3 {
		return drop, nil
	}
	for i, c := range t.cols {
		if skip[c.name] {
			continue
		}
		v := t.cells[0][i]
		same := v != ""
		for _, row := range t.cells[1:] {
			if row[i] != v {
				same = false
				break
			}
		}
		if same {
			drop[i] = true
			notes = append(notes, c.name+"="+v)
		}
	}
	return drop, notes
}

// countBy renders "Running 398, CrashLoopBackOff 6" for the values of rows
// under key, most frequent first (ties by name).
func countBy(rows []int, key func(int) string) string {
	counts := map[string]int{}
	for _, r := range rows {
		counts[key(r)]++
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s %d", k, counts[k])
	}
	return strings.Join(parts, ", ")
}

// splitBlocks splits output into blank-line separated blocks, keeping the
// blank lines as separators.
func splitBlocks(lines []string) [][]string {
	var out [][]string
	var cur []string
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			if cur != nil {
				out = append(out, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, ln)
	}
	if cur != nil {
		out = append(out, cur)
	}
	return out
}
