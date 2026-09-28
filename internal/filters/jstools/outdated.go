package jstools

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

type npmOutdated struct{}

func (npmOutdated) Name() string { return "npm-outdated" }

func (npmOutdated) Match(c *engine.Context) bool {
	pm, sub, rest, ok := manager(c)
	return ok && pm != "bun" && sub == "outdated" &&
		!hasArg(rest, "--json", "--parseable", "-p", "--long", "-l", "--help", "-h", "--format")
}

func (npmOutdated) Faithful(*engine.Context) bool { return true }

func (npmOutdated) IsContent() bool { return true }

var outdatedHeadRe = lazyre.New(`^Package\s+Current\s+Wanted\s+Latest\s+Location\s+Depended by$`)

var yarnLegendRe = lazyre.New(`^info Color legend : ?$|^ "<(?:red|yellow|green)>"\s+: `)

func (npmOutdated) Apply(c *engine.Context, s string) (string, bool) {
	lines := strings.Split(s, "\n")
	if strings.Contains(s, "┌") {
		return unboxAll(lines)
	}
	if strings.Contains(s, "info Color legend") {
		var o out
		for _, ln := range lines {
			if yarnLegendRe.MatchString(ln) {
				o.drop(ln)
			} else {
				o.add(ln)
			}
		}
		return o.String(), true
	}
	head := -1
	for i, ln := range lines {
		if outdatedHeadRe.MatchString(ln) {
			head = i
			break
		}
	}
	if head < 0 {
		return "", false
	}
	var rows [][]string
	end := head + 1
	for ; end < len(lines); end++ {
		f := strings.Fields(lines[end])
		if len(f) != 6 {
			break
		}
		rows = append(rows, f)
	}
	if len(rows) == 0 {
		return "", false
	}
	if end < len(lines) && strings.TrimSpace(lines[end]) != "" && !strings.HasPrefix(lines[end], "npm ") {
		return "", false
	}
	sameLoc, sameDep := true, true
	for _, r := range rows {
		if r[4] != "node_modules/"+r[0] {
			sameLoc = false
		}
		if r[5] != rows[0][5] {
			sameDep = false
		}
	}
	cols := []int{0, 1, 2, 3}
	var notes []string
	if !sameLoc {
		cols = append(cols, 4)
	} else {
		notes = append(notes, "Location node_modules/<Package>")
	}
	if !sameDep {
		cols = append(cols, 5)
	} else {
		notes = append(notes, fmt.Sprintf("Depended by %s", rows[0][5]))
	}
	if len(notes) == 0 {
		return "", false
	}
	header := []string{"Package", "Current", "Wanted", "Latest", "Location", "Depended by"}
	table := append([][]string{header}, rows...)
	width := make([]int, 6)
	for _, r := range table {
		for _, k := range cols {
			width[k] = max(width[k], len(r[k]))
		}
	}
	var o out
	o.add(lines[:head]...)
	for _, r := range table {
		var b strings.Builder
		for n, k := range cols {
			cell := r[k]
			switch {
			case n == len(cols)-1:
				b.WriteString(cell)
			case k == 0:
				b.WriteString(cell + strings.Repeat(" ", width[k]-len(cell)+2))
			default:
				b.WriteString(strings.Repeat(" ", width[k]-len(cell)) + cell + "  ")
			}
		}
		o.add(strings.TrimRight(b.String(), " "))
	}
	o.add("[every row: " + strings.Join(notes, ", ") + "]")
	o.add(lines[end:]...)
	return o.String(), true
}

func unboxAll(lines []string) (string, bool) {
	var o out
	found := false
	for i := 0; i < len(lines); {
		rows, end, ok := parseBoxTable(lines, i)
		if !ok {
			o.add(lines[i])
			i++
			continue
		}
		found = true
		var cells [][]string
		for _, r := range rows {
			var row []string
			for _, cell := range r {
				row = append(row, cell.text())
			}
			cells = append(cells, row)
		}
		o.add(alignTable(cells)...)
		i = end
	}
	return o.String(), found
}
