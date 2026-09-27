package jstest

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strconv"
	"strings"
)

var (
	// V8 stack frame: "    at fn (file:1:2)", "    at file:1:2", "at async fn (…)",
	// "at fn (<anonymous>)", "at new X (native)".
	atFrameRe = lazyre.New(`^\s+at \S`)
	// vitest frame: " ❯ fn file:1:2" or " ❯ file:1:2".
	// The file may contain spaces; only the line number is used.
	vFrameRe = lazyre.New(`^\s*❯ (?:(\S+) )?(\S.*?):(\d+):(\d+)$`)
	// mocha separates the synchronous part of a trace from its async part.
	asyncSepRe = lazyre.New(`^\s+-{4,}$`)
	// library locations: node_modules, node core (node:x, internal/x.js),
	// frames without a location.
	// Windows paths use backslashes.
	nodeModRe  = lazyre.New(`node_modules[/\\]((?:@[^/\\\s]+[/\\])?[^/\\\s:)]+)`)
	nodeCoreRe = lazyre.New(`\(?\b(node:[a-z_]+)|[\s(](internal)/[\w/.-]+\.js:\d`)
	noLocRe    = lazyre.New(`\((?:<anonymous>|native|index \d+)\)$|^\s*at (?:<anonymous>|native)$`)
)

func isFrame(ln string) bool {
	return atFrameRe.MatchString(ln) || strings.Contains(ln, "❯ ") && vFrameRe.MatchString(ln)
}

// libRoot reports whether a frame is library code, and names its root
// (package, node core module) for the fold marker.
func libRoot(ln string) (string, bool) {
	if m := nodeModRe.FindStringSubmatch(ln); m != nil {
		return m[1], true
	}
	if m := nodeCoreRe.FindStringSubmatch(ln); m != nil {
		if m[1] != "" {
			return m[1], true
		}
		return "node:internal", true
	}
	if noLocRe.MatchString(ln) {
		return "native", true
	}
	return "", false
}

// foldFrames copies lines[from:to] (all frames or mocha "----" async
// separators) to d, replacing each run of 2+ library frames by one
//
//	<indent>… N library frames (supertest, node:events, node:net)
//
// line. Application frames, lone library frames and frames that open an
// error-properties block ("… {") are kept. Folded frames are marked benign:
// the marker counts them and names where they came from.
func foldFrames(d *doc, from, to int) {
	for i := from; i < to; {
		ln := d.in[i]
		_, lib := libRoot(ln)
		sep := asyncSepRe.MatchString(ln)
		if !(lib || sep) || strings.HasSuffix(ln, "{") {
			d.keep(i)
			i++
			continue
		}
		// Collect the run of library frames (async separators inside).
		j := i
		frames := 0
		var roots []string
		seen := map[string]bool{}
		for j < to {
			l := d.in[j]
			if asyncSepRe.MatchString(l) {
				j++
				continue
			}
			root, isLib := libRoot(l)
			if !isLib || strings.HasSuffix(l, "{") {
				break
			}
			frames++
			if !seen[root] && root != "native" {
				seen[root] = true
				roots = append(roots, root)
			}
			j++
		}
		// Trailing separators belong to what follows.
		for j > i && asyncSepRe.MatchString(d.in[j-1]) {
			j--
		}
		if frames < 2 {
			for k := i; k < max(j, i+1); k++ {
				d.keep(k)
			}
			i = max(j, i+1)
			continue
		}
		if len(roots) == 0 {
			roots = []string{"native"}
		}
		if len(roots) > 3 {
			roots = append(roots[:3], "…")
		}
		ind := d.in[i]
		if sep {
			// Indent like the frames, not like the separator.
			for k := i; k < j; k++ {
				if !asyncSepRe.MatchString(d.in[k]) {
					ind = d.in[k]
					break
				}
			}
		}
		d.emit(fmt.Sprintf("%s… %d library frames (%s)", ind[:indentOf(ind)], frames, strings.Join(roots, ", ")))
		for k := i; k < j; k++ {
			d.drop(k)
		}
		i = j
	}
}

var (
	// jest / babel code frame: "    > 28 |   code", "      27 |", caret line "         |    ^".
	jestCodeRe  = lazyre.New(`^\s*(>)?\s*\d+ \|`)
	jestCaretRe = lazyre.New(`^\s+\|[\s^~]*\^[\s^~]*$`)
	// vitest code frame: "     15|       code", "      3|", caret "       |     ^".
	vCodeRe  = lazyre.New(`^\s*(\d+)\|`)
	vCaretRe = lazyre.New(`^\s+\|\s*\^+\s*$`)
)

// codeFrameEnd returns the end of the jest code frame starting at i.
func jestCodeFrameEnd(lines []string, i, to int) int {
	for i < to && (jestCodeRe.MatchString(lines[i]) || jestCaretRe.MatchString(lines[i])) {
		i++
	}
	return i
}

// keepJestCodeFrame keeps the ">" line of a jest code frame and the caret
// line under it; the surrounding source lines are dropped as benign. A frame
// without a ">" line is kept whole (nothing to anchor on).
func keepJestCodeFrame(d *doc, from, to int) {
	mark := -1
	for i := from; i < to; i++ {
		if m := jestCodeRe.FindStringSubmatch(d.in[i]); m != nil && m[1] == ">" {
			mark = i
			break
		}
	}
	first := mark
	if mark >= 0 && continuation(d.in[mark], "|") {
		first = from // the statement starts above: keep what jest shows of it
	}
	for i := from; i < to; i++ {
		switch {
		case mark < 0, i >= first && i <= mark, i == mark+1 && jestCaretRe.MatchString(d.in[i]):
			d.keep(i)
		default:
			d.drop(i)
		}
	}
}

// continuation reports whether the source text after the first sep of a
// code frame line continues a statement begun on an earlier line
// ("  ).toEqual({ hours: 2999 });").
func continuation(ln, sep string) bool {
	_, code, ok := strings.Cut(ln, sep)
	code = strings.TrimSpace(code)
	return ok && code != "" && strings.ContainsRune(")]}.", rune(code[0]))
}

func vCodeFrameEnd(lines []string, i, to int) int {
	for i < to && (vCodeRe.MatchString(lines[i]) || vCaretRe.MatchString(lines[i])) {
		i++
	}
	return i
}

// keepVitestCodeFrame keeps the source line numbered line (the failing line
// named by the preceding "❯ file:line:col") and its caret line. A frame
// that does not contain that line is kept whole.
func keepVitestCodeFrame(d *doc, from, to, line int) {
	mark := -1
	for i := from; i < to; i++ {
		if m := vCodeRe.FindStringSubmatch(d.in[i]); m != nil {
			if n, _ := strconv.Atoi(m[1]); n == line && line > 0 {
				mark = i
				break
			}
		}
	}
	first := mark
	if mark >= 0 && continuation(d.in[mark], "|") {
		first = from
	}
	for i := from; i < to; i++ {
		switch {
		case mark < 0, i >= first && i <= mark, i == mark+1 && vCaretRe.MatchString(d.in[i]):
			d.keep(i)
		default:
			d.drop(i)
		}
	}
}

// diffLimit: diffs longer than this many lines keep their changed lines and
// diffContext lines around each run of changes; unchanged runs become a
// counted marker.
const (
	diffLimit   = 40
	diffContext = 3
)

// keepDiff copies the diff body lines[from:to] (after its "- Expected /
// + Received" header), trimming unchanged context of long diffs. ind is the
// column of the +/- markers.
func keepDiff(d *doc, from, to, ind int) {
	if to-from <= diffLimit {
		for i := from; i < to; i++ {
			d.keep(i)
		}
		return
	}
	changed := func(i int) bool {
		ln := d.in[i]
		if len(ln) <= ind {
			return false
		}
		c := ln[ind]
		return (c == '-' || c == '+') && strings.TrimSpace(ln[:ind]) == "" || strings.HasPrefix(strings.TrimSpace(ln), "@@ ")
	}
	near := make([]bool, to-from)
	for i := from; i < to; i++ {
		if changed(i) {
			for k := max(from, i-diffContext); k <= min(to-1, i+diffContext); k++ {
				near[k-from] = true
			}
		}
	}
	for i := from; i < to; {
		if near[i-from] {
			d.keep(i)
			i++
			continue
		}
		j := i
		for j < to && !near[j-from] {
			j++
		}
		pad := strings.Repeat(" ", ind+2)
		d.emit(fmt.Sprintf("%s… %s …", pad, plural(j-i, "unchanged line", "unchanged lines")))
		for k := i; k < j; k++ {
			d.drop(k)
		}
		i = j
	}
}
