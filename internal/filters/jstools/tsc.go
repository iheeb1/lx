package jstools

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/filters/focus"
	"github.com/iheeb1/lx/internal/lazyre"
)

type tsc struct{}

func (tsc) Name() string { return "tsc" }

func (tsc) Match(c *engine.Context) bool {
	t, args := tool(c)
	if t != "tsc" && t != "vue-tsc" && t != "tsgo" {
		return false
	}
	return !hasArg(args, "--version", "-v", "--help", "-h", "--all", "--init", "--showConfig",
		"--listFilesOnly", "--listFiles", "--listEmittedFiles", "--explainFiles", "--traceResolution",
		"--generateTrace", "--extendedDiagnostics", "--diagnostics")
}

func (tsc) Stream(c *engine.Context) bool {
	_, args := tool(c)
	return hasArg(args, "--watch", "-w")
}

func (tsc) GuardsErrors() bool { return true }

func (tsc) Apply(c *engine.Context, s string) (string, bool) {
	lines := strings.Split(s, "\n")
	var o out
	found := false
	for i := 0; i < len(lines); {
		if end, rendered := tscRegion(c, lines, i); end > i {
			o.add(rendered...)
			found = true
			i = end
			continue
		}
		o.add(lines[i])
		i++
	}
	if !found {
		return "", false
	}
	if note := failNote(c, o.lines, nil); note != "" {
		o.add(note)
	}
	return o.String(), true
}

var (
	tscFoundRe = lazyre.New(`^Found \d+ errors?(?:\.| in .+)$`)
	tscTableRe = lazyre.New(`^Errors\s+Files$`)
	tscRowRe   = lazyre.New(`^\s+\d+\s+(\S.*?)(?::\d+)?$`)

	tscRelLocRe = lazyre.New(`^  (\S.*):(\d+):(\d+)$`)

	tscGutterRe     = lazyre.New(`^\s*\d+(?: .*)?$`)
	tscSquiggleRe   = lazyre.New(`^\s+~+$`)
	tscEllipsisRe   = lazyre.New(`^\s*\.\.\.$`)
	tscBareGutterRe = lazyre.New(`^\s*\d+$`)
)

type tscDiag struct {
	header  string
	file    string
	loc     string
	key     string
	chain   []string
	related []string
}

func tscHeader(ln string) (d tscDiag, pretty, ok bool) {
	if !strings.Contains(ln, "TS") {
		return d, false, false
	}

	if m, ok := scanTSCPretty(ln); ok {
		return tscDiag{header: ln, file: m[0], loc: m[0] + ":" + m[1] + ":" + m[2], key: m[3] + " " + m[4] + ": " + m[5]}, true, true
	}
	if m, ok := scanTSCPlain(ln); ok {
		return tscDiag{header: ln, file: m[0], loc: m[0] + "(" + m[1] + "," + m[2] + ")", key: m[3] + " " + m[4] + ": " + m[5]}, false, true
	}
	if scanTSCGlobal(ln) {
		return tscDiag{header: ln, key: ln}, false, true
	}
	return d, false, false
}

func tscFrame(lines []string, i int) int {
	j := i
	for j < len(lines) {
		ln := lines[j]
		if tscEllipsisRe.MatchString(ln) && j > i {
			j++
			continue
		}
		if !tscGutterRe.MatchString(ln) || j+1 >= len(lines) {
			break
		}
		next := lines[j+1]

		if tscSquiggleRe.MatchString(next) || next == "" && tscBareGutterRe.MatchString(ln) {
			j += 2
			continue
		}
		break
	}
	return j
}

func tscParse(lines []string, i int) (tscDiag, int) {
	d, pretty, _ := tscHeader(lines[i])
	n := len(lines)
	j := i + 1

	for j < n && strings.HasPrefix(lines[j], "  ") && strings.TrimSpace(lines[j]) != "" {
		if pretty && tscFrame(lines, j) > j {
			break
		}
		d.chain = append(d.chain, lines[j])
		j++
	}
	if !pretty {
		return d, j
	}

	skipBlank := func(k int) int {
		for k < n && lines[k] == "" {
			k++
		}
		return k
	}
	if k := skipBlank(j); k < n {
		if e := tscFrame(lines, k); e > k {
			j = e
		}
	}
	for {
		k := skipBlank(j)
		if k >= n {
			return d, j
		}
		ln := lines[k]
		loc := ""
		if m := tscRelLocRe.FindStringSubmatch(ln); m != nil {
			loc = m[1] + ":" + m[2] + ":" + m[3]
			k = tscFrame(lines, k+1)
		} else if !strings.HasPrefix(ln, "    ") {
			return d, j
		}

		var msg []string
		for k < n && strings.HasPrefix(lines[k], "    ") && strings.TrimSpace(lines[k]) != "" {
			msg = append(msg, lines[k])
			k++
		}
		if loc == "" && len(msg) == 0 {
			return d, j
		}
		first := ""
		if len(msg) > 0 {
			first = strings.TrimSpace(msg[0])
			msg = msg[1:]
		}
		switch {
		case loc != "" && first != "":
			d.related = append(d.related, "  related "+loc+" - "+first)
		case loc != "":
			d.related = append(d.related, "  related "+loc)
		default:
			d.related = append(d.related, "  related: "+first)
		}
		d.related = append(d.related, msg...)
		j = k
	}
}

const (
	tscFactorMin    = 80
	tscFactorRepeat = 5
	locsPerLine     = 8
)

func tscRegion(c *engine.Context, lines []string, i int) (int, []string) {
	if _, _, ok := tscHeader(lines[i]); !ok {
		return i, nil
	}
	var (
		diags []tscDiag
		tail  []string
		files = map[string]bool{}
		n     = len(lines)
		j     = i
	)
	for j < n {
		k := j
		for k < n && lines[k] == "" {
			k++
		}
		if k >= n {
			j = k
			break
		}
		if _, _, ok := tscHeader(lines[k]); ok {
			d, e := tscParse(lines, k)
			diags = append(diags, d)
			if d.file != "" {
				files[d.file] = true
			}
			j = e
			continue
		}
		if tscFoundRe.MatchString(lines[k]) {
			tail = append(tail, lines[k])
			j = k + 1

			t := j
			for t < n && lines[t] == "" {
				t++
			}
			if t < n && tscTableRe.MatchString(lines[t]) {
				e := t + 1
				redundant := true
				for e < n {
					m := tscRowRe.FindStringSubmatch(lines[e])
					if m == nil {
						break
					}
					if !files[m[1]] {
						redundant = false
					}
					e++
				}
				if !redundant {
					tail = append(tail, "")
					tail = append(tail, lines[t:e]...)
				}
				j = e
			}
			break
		}
		break
	}

	diags = focusFirst(focus.New(c.Focus), diags, func(d tscDiag) string { return d.file })
	var r []string
	factored := tscFactor(diags)
	for idx, d := range diags {
		if g, ok := factored[idx]; ok {
			if g != nil {
				r = append(r, g...)
			}
			continue
		}
		r = append(r, d.header)
		r = append(r, d.chain...)
		r = append(r, d.related...)
	}
	if len(tail) > 0 {
		r = append(r, "")
		r = append(r, tail...)
	}
	return j, r
}

func tscFactor(diags []tscDiag) map[int][]string {
	res := map[int][]string{}
	if len(diags) <= tscFactorMin {
		return res
	}
	groups := map[string][]int{}
	var order []string
	for idx, d := range diags {
		if d.file == "" || len(d.chain) > 0 || len(d.related) > 0 {
			continue
		}
		if _, ok := groups[d.key]; !ok {
			order = append(order, d.key)
		}
		groups[d.key] = append(groups[d.key], idx)
	}
	for _, k := range order {
		g := groups[k]
		if len(g) <= tscFactorRepeat {
			continue
		}
		lines := []string{fmt.Sprintf("%s [×%d, every location:]", k, len(g))}
		for s := 0; s < len(g); s += locsPerLine {
			var locs []string
			for _, idx := range g[s:min(s+locsPerLine, len(g))] {
				locs = append(locs, diags[idx].loc)
			}
			lines = append(lines, "  "+strings.Join(locs, " "))
		}
		res[g[0]] = lines
		for _, idx := range g[1:] {
			res[idx] = nil
		}
	}
	return res
}
