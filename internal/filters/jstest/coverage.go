package jstest

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"sort"
	"strconv"
	"strings"
)

// istanbul "text" coverage table (jest --coverage, vitest --coverage, nyc):
//
//	---------------------|---------|----------|---------|---------|-------------------
//	File                 | % Stmts | % Branch | % Funcs | % Lines | Uncovered Line #s
//	---------------------|---------|----------|---------|---------|-------------------
//	All files            |   96.67 |    94.58 |   95.31 |   96.68 |
//	 src                 |   96.91 |     97.4 |   92.92 |   96.98 |
//	  datetime.js        |   95.32 |    97.68 |   87.58 |    95.5 | 1460,1485,2094-2097
//	---------------------|---------|----------|---------|---------|-------------------

var (
	covSepRe  = lazyre.New(`^-+(?:\|-+)+\|?$`)
	covHeadRe = lazyre.New(`^File\s+\|\s*% Stmts\s*\|\s*% Branch\s*\|\s*% Funcs\s*\|\s*% Lines\s*\|`)
	covRowRe  = lazyre.New(`^( *)(\S.*?)\s*\|\s*([\d.]+|-)\s*\|\s*([\d.]+|-)\s*\|\s*([\d.]+|-)\s*\|\s*([\d.]+|-)\s*\|`)
	// jest: `Jest: "global" coverage threshold for lines (95%) not met: 40%`
	// vitest: `ERROR: Coverage for lines (40%) does not meet global threshold (95%)`
	jestThresholdRe   = lazyre.New(`coverage threshold for \w+ \((\d+(?:\.\d+)?)%\) not met`)
	vitestThresholdRe = lazyre.New(`does not meet (?:global )?threshold \((\d+(?:\.\d+)?)%\)`)
)

// maxCoverageRows caps the file rows kept from a coverage table.
const maxCoverageRows = 30

// coverageTableEnd returns the end of the coverage table starting at line i
// (a separator line followed by the header), or i when there is none.
func coverageTableEnd(lines []string, i int) int {
	if i+1 >= len(lines) || !covSepRe.MatchString(lines[i]) || !covHeadRe.MatchString(lines[i+1]) {
		return i
	}
	j := i + 2
	for j < len(lines) && covSepRe.MatchString(lines[j]) {
		j++
	}
	for j < len(lines) && covRowRe.MatchString(lines[j]) {
		j++
	}
	if j < len(lines) && covSepRe.MatchString(lines[j]) {
		j++
	}
	return j
}

// coverageThreshold returns the lowest threshold the run reported as not
// met, or 100 when it reported none.
func coverageThreshold(lines []string) float64 {
	th := 101.0
	for _, ln := range lines {
		if !strings.Contains(ln, "hreshold") {
			continue
		}
		for _, re := range []*lazyre.Regexp{jestThresholdRe, vitestThresholdRe} {
			if m := re.FindStringSubmatch(ln); m != nil {
				if v, err := strconv.ParseFloat(m[1], 64); err == nil && v < th {
					th = v
				}
			}
		}
	}
	if th > 100 {
		return 100
	}
	return th
}

type covRow struct {
	i      int
	indent int
	name   string
	pct    [4]float64 // stmts, branch, funcs, lines; -1 when "-"
	dir    bool
	keep   bool
}

func (r covRow) below(th float64) bool {
	for _, p := range r.pct {
		if p >= 0 && p < th {
			return true
		}
	}
	return false
}

func (r covRow) min() float64 {
	m := 101.0
	for _, p := range r.pct {
		if p >= 0 && p < m {
			m = p
		}
	}
	return m
}

// renderCoverage keeps the table header, the "All files" row, and the file
// rows below the threshold (any metric under the lowest threshold the run
// reported as not met, or under 100% when none was), each with its
// directory rows; other rows are counted. Small tables are kept whole.
func renderCoverage(d *doc, from, to int, th float64) {
	var rows []covRow
	for i := from; i < to; i++ {
		m := covRowRe.FindStringSubmatch(d.in[i])
		if m == nil || covHeadRe.MatchString(d.in[i]) {
			continue
		}
		r := covRow{i: i, indent: len(m[1]), name: m[2]}
		for k := 0; k < 4; k++ {
			r.pct[k] = -1
			if v, err := strconv.ParseFloat(m[3+k], 64); err == nil {
				r.pct[k] = v
			}
		}
		rows = append(rows, r)
	}
	if len(rows) <= 12 {
		for i := from; i < to; i++ {
			d.keep(i)
		}
		return
	}
	for k := range rows {
		rows[k].dir = k+1 < len(rows) && rows[k+1].indent > rows[k].indent
	}
	var cand []int
	files := 0
	for k, r := range rows {
		if r.name == "All files" || r.dir {
			continue
		}
		files++
		if r.below(th) {
			cand = append(cand, k)
		}
	}
	more := 0
	if len(cand) > maxCoverageRows {
		sort.SliceStable(cand, func(a, b int) bool { return rows[cand[a]].min() < rows[cand[b]].min() })
		more = len(cand) - maxCoverageRows
		cand = cand[:maxCoverageRows]
	}
	for _, k := range cand {
		rows[k].keep = true
		// Directory rows above it (nearest shallower rows).
		ind := rows[k].indent
		for p := k - 1; p >= 0 && ind > 0; p-- {
			if rows[p].indent < ind {
				rows[p].keep = true
				ind = rows[p].indent
			}
		}
	}
	kept := map[int]bool{}
	for _, r := range rows {
		if r.keep || r.name == "All files" {
			kept[r.i] = true
		}
	}
	for i := from; i < to; i++ {
		if kept[i] || !covRowRe.MatchString(d.in[i]) || covHeadRe.MatchString(d.in[i]) {
			d.keep(i)
			continue
		}
		d.drop(i) // a coverage data row (file names are data, not status)
	}
	hidden := files - len(cand) - more
	pct := strconv.FormatFloat(th, 'f', -1, 64)
	at := "at ≥" + pct + "%"
	if th >= 100 {
		at = "at 100%"
	}
	switch {
	case hidden > 0 && more > 0:
		d.emit(fmt.Sprintf("[coverage: %s %s hidden; %d more below %s%% not shown]", plural(hidden, "file", "files"), at, more, pct))
	case hidden > 0:
		d.emit(fmt.Sprintf("[coverage: %s %s hidden]", plural(hidden, "file", "files"), at))
	case more > 0:
		d.emit(fmt.Sprintf("[coverage: %d more files below %s%% not shown]", more, pct))
	}
}
