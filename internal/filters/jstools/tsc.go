package jstools

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// tsc condenses TypeScript compiler diagnostics (tsc, vue-tsc, tsgo).
//
// The plain format (`file(l,c): error TSxxxx: msg`, what tsc prints into a
// pipe) is already dense and is kept verbatim with its elaboration lines.
// The --pretty format is rewritten to one `file:l:c - error TSxxxx: msg`
// line (tsc's own header, verbatim) plus its elaboration lines: the code
// frame and ~~~ underline are dropped, and related information ("An argument
// for 'x' was not provided." at the declaration) becomes one
// `  related file:l:c - message` line. "Found N errors in M files." is kept
// verbatim; the "Errors  Files" table after it is dropped only when every
// file it lists is already named by a diagnostic.
//
// Above 80 diagnostics, a single-line message repeated more than 5 times is
// printed once followed by every one of its locations.
//
// tsc is Guarded: code frames quote source code, which may contain words
// like "error", and are dropped on purpose. Every other line is kept.
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

// Stream: watch mode never ends, so it runs in passthrough.
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
		if end, rendered := tscRegion(lines, i); end > i {
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
	// Related information in --pretty output: "  file:line:col".
	tscRelLocRe = lazyre.New(`^  (\S.*):(\d+):(\d+)$`)
	// Code frame: "151   source" gutter lines paired with "    ~~~~" lines,
	// and a "..." gutter for spans of more than 5 lines.
	tscGutterRe     = lazyre.New(`^\s*\d+(?: .*)?$`)
	tscSquiggleRe   = lazyre.New(`^\s+~+$`)
	tscEllipsisRe   = lazyre.New(`^\s*\.\.\.$`)
	tscBareGutterRe = lazyre.New(`^\s*\d+$`)
)

// tscDiag is one diagnostic.
type tscDiag struct {
	header  string   // tsc's own first line, verbatim
	file    string   // "" for global diagnostics
	loc     string   // "file(l,c)" or "file:l:c", as tsc wrote it
	key     string   // severity, code and message
	chain   []string // elaboration lines, verbatim
	related []string // rendered related-information lines
}

// tscHeader parses a diagnostic's first line.
func tscHeader(ln string) (d tscDiag, pretty, ok bool) {
	if !strings.Contains(ln, "TS") {
		return d, false, false
	}
	// The shapes (see scan.go):
	//   pretty  file:line:col - error TS2322: message
	//   plain   file(line,col): error TS2322: message
	//   global  error TS5023: message
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

// tscFrame returns the end of the code frame starting at lines[i] (i when
// there is none).
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
		// An empty source line (a bare gutter number) has an empty
		// underline, which trimming turned into "".
		if tscSquiggleRe.MatchString(next) || next == "" && tscBareGutterRe.MatchString(ln) {
			j += 2
			continue
		}
		break
	}
	return j
}

// tscParse parses the diagnostic whose header is lines[i] and returns it
// with the index after its last line.
func tscParse(lines []string, i int) (tscDiag, int) {
	d, pretty, _ := tscHeader(lines[i])
	n := len(lines)
	j := i + 1
	// Elaboration: indented lines right under the header.
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
	// Blank line, code frame, then related information blocks.
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
		// The related message: lines indented by 4, after the frame.
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

// tscFactorMin: above this many diagnostics, repeated messages are factored.
const (
	tscFactorMin    = 80
	tscFactorRepeat = 5
	locsPerLine     = 8
)

// tscRegion consumes the run of diagnostics starting at lines[i] (with the
// blank lines between them, the "Found N errors" summary and its table) and
// renders it. It returns i when lines[i] does not start a diagnostic.
func tscRegion(lines []string, i int) (int, []string) {
	if _, _, ok := tscHeader(lines[i]); !ok {
		return i, nil
	}
	var (
		diags []tscDiag
		tail  []string // summary and table
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
			// "Errors  Files" table: dropped when it names no new file.
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

// tscFactor groups single-line diagnostics sharing a message when there are
// more than tscFactorMin diagnostics. The result maps a diagnostic index to
// its replacement: the group rendering at the first member, nil for the
// other members.
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
