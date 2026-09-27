package infra

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// dockerTable condenses docker ps / images / compose ps tables.
//
//   - Columns with one value in every row (3+ rows) are dropped and noted
//     once ("[lx: same in all 12 rows: STATUS=Up 3 days (healthy)]").
//   - COMMAND cells over 60 characters are cut to 40 (docker already cuts
//     them to 20 unless --no-trunc; with --no-trunc nothing is cut).
//   - Over 60 rows: the first 40, then every unhealthy row (exited with a
//     non-zero code, restarting, unhealthy, dead) with exact counts of the
//     rows not shown and a count of all rows by status.
//
// Everything else is left as docker printed it.
type dockerTable struct{}

func (dockerTable) Name() string { return "docker-table" }

func (dockerTable) Match(c *engine.Context) bool {
	return !engine.MachineReadable(c) && !hasQuiet(c) &&
		dockerSub(c, "ps", "images", "container ls", "container list", "container ps", "image ls", "image list", "compose ps")
}

// hasQuiet: -q prints bare IDs, one per line.
func hasQuiet(c *engine.Context) bool {
	for _, a := range c.Args() {
		if a == "-q" || a == "--quiet" || len(a) > 2 && a[0] == '-' && a[1] != '-' && strings.ContainsRune(a[1:], 'q') {
			return true
		}
	}
	return false
}

const (
	maxTableRows   = 60 // tables up to this many rows are kept whole
	keepTableRows  = 40 // … bigger ones keep this many first rows
	maxProblemRows = 40 // … and at most this many unhealthy rows past them
	maxCommand     = 60
	cutCommand     = 40
)

var (
	// problemStatusRe: container states worth seeing even in a long list.
	problemStatusRe = lazyre.New(`^(?:Exited \((?:[1-9]\d*|-\d+)\)|Restarting|Dead|Removal In Progress|OOMKilled)|\(unhealthy\)|\(health: starting\)`)
	statusAgeRe     = lazyre.New(`^(Up|Exited \(-?\d+\)|Restarting \(-?\d+\)|Created|Dead|Paused|Removal In Progress)(?:\s.*?)?(\((?:healthy|unhealthy|health: starting|Paused)\))?$`)
)

// statusKey buckets a docker STATUS cell: "Up (healthy)", "Exited (137)".
func statusKey(s string) string {
	m := statusAgeRe.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	if m[2] != "" {
		return m[1] + " " + m[2]
	}
	return m[1]
}

func (dockerTable) Apply(c *engine.Context, out string) (string, bool) {
	lines := strings.Split(out, "\n")
	// Leading lines that are not the table (warnings) are kept as is.
	start := 0
	for start < len(lines) {
		if _, ok := parseHeader(lines[start]); ok {
			break
		}
		start++
	}
	if start == len(lines) {
		return "", false
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	t, ok := parseTable(lines[start:end])
	if !ok {
		return "", false
	}
	res := append([]string(nil), lines[:start]...)
	noTrunc := false
	for _, a := range c.Args() {
		if a == "--no-trunc" {
			noTrunc = true
		}
	}
	changed := false
	if k := t.col("COMMAND"); k >= 0 && !noTrunc {
		for _, row := range t.cells {
			if v := row[k]; len([]rune(v)) > maxCommand {
				row[k] = string([]rune(v)[:cutCommand]) + "…"
				changed = true
			}
		}
	}
	rows := make([]int, len(t.cells))
	for i := range rows {
		rows[i] = i
	}
	// Error-class rows, classified once.
	rowErr := make([]bool, len(t.lines))
	anyErr := engine.IsError(t.header)
	for r, ln := range t.lines {
		rowErr[r] = engine.IsError(ln)
		anyErr = anyErr || rowErr[r]
	}
	// Big tables: the first rows, then the unhealthy ones among the rest.
	var extra, tail []string
	var extraRows []int
	if len(rows) > maxTableRows {
		st := t.col("STATUS")
		problems := 0
		for r := keepTableRows; r < len(rows); r++ {
			if st >= 0 && problemStatusRe.MatchString(t.cells[r][st]) || rowErr[r] {
				problems++
				if len(extraRows) < maxProblemRows {
					extraRows = append(extraRows, r)
				}
			}
		}
		hidden := len(rows) - keepTableRows - len(extraRows)
		switch {
		case st >= 0:
			// Rows are picked by STATUS, and also when an error word is
			// anywhere in the row (a container named "…-oom"), so the
			// note names both reasons.
			const why = "a non-zero exit, restart loop, failing health check, dead status, or an error word in the row"
			what := fmt.Sprintf("the %d rows with %s follow", problems, why)
			switch {
			case problems > len(extraRows):
				what = fmt.Sprintf("the first %d of the %d rows with %s follow", len(extraRows), problems, why)
			case problems == 1:
				what = "the row with " + why + " follows"
			case problems == 0:
				what = "none has " + why
			}
			extra = append(extra, fmt.Sprintf("[lx: rows %d-%d: %d rows not shown; %s]", keepTableRows+1, len(rows), hidden, what))
			tail = append(tail, fmt.Sprintf("[lx: %d rows by STATUS: %s]", len(rows), countBy(rows, func(r int) string { return statusKey(t.cells[r][st]) })))
		default:
			note := fmt.Sprintf("[lx: rows %d-%d: %d rows not shown", keepTableRows+1, len(rows), hidden)
			if k := t.col("TAG"); k >= 0 {
				dangling := 0
				for _, r := range rows[keepTableRows:] {
					if t.cells[r][k] == "<none>" {
						dangling++
					}
				}
				if dangling > 0 {
					note += fmt.Sprintf(" (%d of them <none>)", dangling)
				}
			}
			if len(extraRows) > 0 {
				if len(extraRows) == 1 {
					note += "; the row with an error follows"
				} else {
					note += fmt.Sprintf("; the %d rows with an error follow", len(extraRows))
				}
			}
			extra = append(extra, note+"]")
		}
		rows = rows[:keepTableRows]
		changed = true
	}
	drop, notes := t.constantCols(map[string]bool{"NAMES": true, "NAME": true, "CONTAINER ID": true, "IMAGE ID": true, "ID": true, "IMAGE": true, "REPOSITORY": true})
	if len(notes) > 0 && anyErr {
		// Dropping a column rewrites every row; an error-class row (three
		// replicas running "python -m error_reporter") must stay whole, or
		// the engine guard re-appends it after the table.
		drop, notes = map[int]bool{}, nil
	}
	if len(notes) > 0 {
		changed = true
	}
	if !changed {
		return out, true
	}
	if len(notes) > 0 {
		res = append(res, fmt.Sprintf("[lx: same in all %d rows: %s]", len(t.cells), strings.Join(notes, ", ")))
	}
	all := append(append([]int(nil), rows...), extraRows...)
	rendered := t.render(all, drop)
	res = append(res, rendered[:1+len(rows)]...)
	res = append(res, extra...)
	res = append(res, rendered[1+len(rows):]...)
	res = append(res, tail...)
	res = append(res, lines[end:]...)
	return strings.TrimRight(strings.Join(res, "\n"), "\n"), true
}

// dockerPull condenses docker pull / compose pull: per-layer progress
// lines become one "[lx: layers: …]" line; everything else (tag, digest,
// status, errors) is kept verbatim.
type dockerPull struct{}

func (dockerPull) Name() string { return "docker-pull" }

func (dockerPull) Match(c *engine.Context) bool {
	return dockerSub(c, "pull", "image pull", "compose pull")
}

var (
	layerRe        = lazyre.New(`^ ?([0-9a-f]{12}):? (Pulling fs layer|Waiting|Downloading|Verifying Checksum|Download complete|Extracting|Pull complete|Already exists|Pulled|Downloaded|Exists)\b`)
	composePullRe  = lazyre.New(`^ ?\S+ (?:Pulling|Pulled|Skipped|Waiting)\b`)
	pullProgressRe = lazyre.New(`^\s*\[[=> ]*\]\s+[\d.]+[kMG]?B/[\d.]+[kMG]?B`)
)

func (dockerPull) Apply(c *engine.Context, out string) (string, bool) {
	lines := strings.Split(out, "\n")
	var res []string
	seen := map[string]bool{}
	var order []string
	existed, pos := 0, -1
	for _, ln := range lines {
		if m := layerRe.FindStringSubmatch(ln); m != nil && !engine.IsError(ln) {
			if !seen[m[1]] {
				seen[m[1]] = true
				order = append(order, m[1])
			}
			if m[2] == "Already exists" || m[2] == "Exists" {
				existed++
			}
			if pos < 0 {
				pos = len(res)
				res = append(res, "")
			}
			continue
		}
		if pullProgressRe.MatchString(ln) {
			continue
		}
		res = append(res, ln)
	}
	if pos < 0 {
		return out, true // no layer lines: nothing to condense
	}
	note := fmt.Sprintf("[lx: %s", engine.Plural(len(order), "layer", "layers"))
	if existed > 0 {
		note += fmt.Sprintf(", %d already existed", existed)
	}
	res[pos] = note + "; per-layer progress not shown]"
	return strings.TrimRight(strings.Join(res, "\n"), "\n"), true
}
