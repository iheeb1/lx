package jstools

import (
	"strings"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/lazyre"
)

var (
	boxTopRe = lazyre.New(`^\s*┌[─┬]+┐$`)
	boxSepRe = lazyre.New(`^\s*├[─┼]+┤$`)
	boxEndRe = lazyre.New(`^\s*└[─┴]+┘$`)
)

type boxCell []string

func (c boxCell) text() string { return strings.Join(c, " ") }

func parseBoxTable(lines []string, i int) (rows [][]boxCell, end int, ok bool) {
	if i >= len(lines) || !boxTopRe.MatchString(lines[i]) {
		return nil, i, false
	}
	top := strings.TrimSpace(lines[i])
	ncols := strings.Count(top, "┬") + 1
	var widths []int
	for _, seg := range strings.Split(strings.Trim(top, "┌┐"), "┬") {
		widths = append(widths, utf8.RuneCountInString(seg))
	}
	var cur [][]string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		row := make([]boxCell, ncols)
		for c := 0; c < ncols; c++ {
			var vals []string
			var b strings.Builder
			prevFull := false
			for _, pl := range cur {
				seg := strings.TrimSpace(pl[c])
				if seg == "" {
					if b.Len() > 0 {
						vals = append(vals, b.String())
						b.Reset()
					}
					continue
				}
				if b.Len() > 0 && !prevFull {
					b.WriteByte(' ')
				}
				b.WriteString(seg)

				prevFull = utf8.RuneCountInString(seg) >= widths[c]-2 && !strings.Contains(seg, " ")
			}
			if b.Len() > 0 {
				vals = append(vals, b.String())
			}
			row[c] = vals
		}
		empty := true
		for _, cell := range row {
			if len(cell) > 0 {
				empty = false
			}
		}
		if !empty {
			rows = append(rows, row)
		}
		cur = nil
	}
	for j := i + 1; j < len(lines); j++ {
		ln := strings.TrimSpace(lines[j])
		switch {
		case boxSepRe.MatchString(ln):
			flush()
		case boxEndRe.MatchString(ln):
			flush()
			return rows, j + 1, len(rows) > 0
		case strings.HasPrefix(ln, "│") && strings.HasSuffix(ln, "│"):
			cells := strings.Split(ln, "│")
			if len(cells) != ncols+2 {
				return nil, i, false
			}
			cur = append(cur, cells[1:ncols+1])
		default:
			return nil, i, false
		}
	}
	return nil, i, false
}

func alignTable(rows [][]string) []string {
	var width []int
	for _, r := range rows {
		for k, cell := range r {
			if k >= len(width) {
				width = append(width, 0)
			}
			width[k] = max(width[k], utf8.RuneCountInString(cell))
		}
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		var b strings.Builder
		for k, cell := range r {
			b.WriteString(cell)
			if k < len(r)-1 {
				b.WriteString(strings.Repeat(" ", width[k]-utf8.RuneCountInString(cell)+2))
			}
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}
