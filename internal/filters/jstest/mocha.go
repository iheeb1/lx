package jstest

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// mocha's Base reporters (spec, dot, list, …) end with the same epilogue:
//
//	  Route                                  spec: suite titles
//	    ✔ should work (55ms)                 passing test
//	    1) should return an array            failing test (details below)
//	    - pending test
//
//	  1252 passing (2s)                      summary, kept verbatim
//	  2 pending
//	  9 failing
//
//	  1) req                                 numbered failure: title path,
//	       .subdomains                       message, diff, stack
//	         should return an array:
//
//	      Error: expected …
//	      at Context.<anonymous> (test/req.subdomains.js:19:10)
//
// A spec file that cannot load aborts the run with "Exception during run:"
// and a stack, and no summary.

var (
	mochaPassRe = lazyre.New(`^\s+[✔✓√] `)
	mochaFailRe = lazyre.New(`^\s+(\d+)\) \S`)
	mochaPendRe = lazyre.New(`^\s+- \S`)
	mochaDotsRe = lazyre.New(`^\s*[.!,․]+$`)
	// progress reporter bar: "  [▬▬▬▬▬▬.......]"
	mochaBarRe     = lazyre.New(`^\s*\[[▬.⋅]*\]$`)
	mochaCountRe   = lazyre.New(`^ {2}\d+ (?:passing \(.*\)|pending|failing)$`)
	mochaPendingRe = lazyre.New(`^ {2}(\d+) pending$`)
	mochaDetailRe  = lazyre.New(`^ {2}(\d+)\) (.*)$`)
	mochaDiffRe    = lazyre.New(`^(\s*)\+ expected - actual$`)
	mochaExcRe     = lazyre.New(`^\s*Exception during run: `)
	nodeTraceHint  = lazyre.New("^\\(Use `node --trace-(?:deprecation|warnings|uncaught) \\.\\.\\.` to show where the (?:warning|exception) was (?:created|thrown)\\)$")
	maxOtherOutput = 30
)

// renderMocha condenses mocha's Base reporters. It bails unless the
// "N passing (T)" summary (or an "Exception during run:" abort) is present.
func renderMocha(c *engine.Context, clean string) (result, bool) {
	return levels(func(level int, sh *shared) (result, bool) { return renderMochaView(c, clean, level, sh) })
}

func renderMochaView(c *engine.Context, clean string, level int, sh *shared) (result, bool) {
	d := newDoc(c, clean, sh)
	d.level = level
	in := d.in
	n := len(in)
	sum := -1
	for i, ln := range in {
		if mochaPassingRe.MatchString(ln) {
			sum = i
			break
		}
	}
	if sum < 0 {
		for _, ln := range in {
			if mochaExcRe.MatchString(ln) {
				return renderNodeLines(d, 0, n), true
			}
		}
		return result{}, false
	}
	// Failure details present, by number.
	details := map[string]bool{}
	for _, ln := range in[sum:] {
		if m := mochaDetailRe.FindStringSubmatch(ln); m != nil {
			details[m[1]] = true
		}
	}

	var (
		w      wrapper
		passes int
		other  int
		hidden int
		title  = mochaTitles(in, sum)
	)
	// "- title" lines are pending tests only when mocha's own "N pending"
	// accounts for all of them; otherwise some are console output (a
	// logged list, "  - Error: …"), and none is dropped.
	pendingOK := false
	{
		cand, want := 0, 0
		for _, ln := range in[:sum] {
			if mochaPendRe.MatchString(ln) {
				cand++
			}
		}
		for _, ln := range in[sum:min(sum+4, n)] {
			if m := mochaPendingRe.FindStringSubmatch(ln); m != nil {
				want, _ = strconv.Atoi(m[1])
			}
		}
		pendingOK = cand <= want
	}
	// Test list / console output before the summary.
	for i := 0; i < sum; i++ {
		ln := in[i]
		if k := w.handle(d, i); k > 0 {
			i += k - 1
			continue
		}
		switch {
		case strings.TrimSpace(ln) == "":
		case mochaPassRe.MatchString(ln):
			passes++
			d.drop(i) // counted in the marker
		case mochaFailRe.MatchString(ln) && details[mochaFailRe.FindStringSubmatch(ln)[1]]:
			d.drop(i) // failing test title; its numbered details follow
		case pendingOK && mochaPendRe.MatchString(ln):
			d.drop(i) // pending test title (counted by "N pending")
		case mochaDotsRe.MatchString(ln), mochaBarRe.MatchString(ln):
			// dot / progress reporter progress
		case nodeTraceHint.MatchString(ln):
			// node's hint under a warning
		case title[i]:
			d.drop(i) // describe() title
		case isFrame(ln):
			// The stack of an Error a test logged: library frames folded.
			j := i
			for j < sum && isFrame(in[j]) {
				j++
			}
			start := len(d.out)
			foldFrames(d, i, j)
			d.markChatter(start)
			i = j - 1
		default:
			// Console output of tests, runtime warnings. Errors here are
			// not why the run failed: failures are the numbered list.
			if d.isErr(ln) || engine.IsWarning(ln) || other < maxOtherOutput {
				if !d.isErr(ln) && !engine.IsWarning(ln) {
					other++
				}
				d.keep(i)
				d.chatter[len(d.out)-1] = true
			} else {
				hidden++
				d.hush(i, i+1)
			}
		}
	}
	if hidden > 0 {
		d.emit(fmt.Sprintf("[+%s of test output hidden]", plural(hidden, "line", "lines")))
	}
	d.sep()
	if passes > 0 {
		d.emit(fmt.Sprintf("[%s hidden]", plural(passes, "passing test", "passing tests")))
	}
	i := sum
	for i < n && (mochaCountRe.MatchString(in[i]) || strings.TrimSpace(in[i]) == "") {
		if strings.TrimSpace(in[i]) != "" {
			d.keep(i)
		}
		i++
	}
	// Numbered failures, then whatever the script printed after mocha
	// (coverage tables, npm errors).
	th := coverageThreshold(in)
	afterFailure := false
	for i < n {
		switch {
		case mochaDetailRe.MatchString(in[i]):
			j := mochaFailureEnd(in, i)
			d.sep()
			renderMochaFailure(d, i, j)
			afterFailure = true
			i = j
		case coverageTableEnd(in, i) > i:
			flushOmitted(d, "  ")
			j := coverageTableEnd(in, i)
			d.sep()
			renderCoverage(d, i, j, th)
			i = j
		case isFrame(in[i]):
			// A crash after mocha's report (its own "throw err").
			flushOmitted(d, "  ")
			j := i
			for j < n && isFrame(in[j]) {
				j++
			}
			foldFrames(d, i, j)
			afterFailure = false
			i = j
		default:
			flushOmitted(d, "  ")
			if strings.TrimSpace(in[i]) != "" {
				if afterFailure {
					d.sep() // what the script printed after mocha's report
				}
				afterFailure = false
				d.keep(i)
			}
			i++
		}
	}
	flushOmitted(d, "  ")
	if c.Exit != 0 {
		noFailure(d, "mocha", c.Exit)
	}
	return d.finish(), true
}

// mochaTitles marks the describe() titles of the spec reporter's tree in
// lines[:end]. A title at indent k is followed by its first child at indent
// k+2, which is a test line (✔, "N)", "-") or another title; so a title is
// a line from which such a chain leads down to a test line. Console output
// interleaved with the tree (column 0, or indented text that leads to no
// test) is not a title, so it is never dropped as one.
func mochaTitles(lines []string, end int) []bool {
	title := make([]bool, end)
	next := -1 // the next non-blank indented line
	for i := end - 1; i >= 0; i-- {
		ln := lines[i]
		ind := indentOf(ln)
		if ind == len(ln) || ind == 0 {
			continue // blank, or column-0 console output
		}
		test := mochaPassRe.MatchString(ln) || mochaFailRe.MatchString(ln) || mochaPendRe.MatchString(ln)
		if !test && ind >= 2 && next >= 0 && indentOf(lines[next]) == ind+2 && !isFrame(ln) {
			nl := lines[next]
			title[i] = title[next] || mochaPassRe.MatchString(nl) || mochaFailRe.MatchString(nl) || mochaPendRe.MatchString(nl)
		}
		next = i
	}
	return title
}

// mochaFailureEnd returns the end of the numbered failure starting at i: the
// next numbered failure, or the first line after its stack trace that is
// neither a frame nor indented (what the test script printed after mocha).
func mochaFailureEnd(in []string, i int) int {
	stack := false
	for j := i + 1; j < len(in); j++ {
		ln := in[j]
		switch {
		case mochaDetailRe.MatchString(ln):
			return j
		case strings.TrimSpace(ln) == "":
		case isFrame(ln) || asyncSepRe.MatchString(ln):
			stack = true
		case stack && indentOf(ln) < 4:
			return j
		}
	}
	return len(in)
}

// renderMochaFailure renders one numbered failure: title path, message,
// diff and stack, with blank lines dropped and library frames folded.
func renderMochaFailure(d *doc, from, to int) {
	start := len(d.out)
	for i := from; i < to; {
		ln := d.in[i]
		switch {
		case strings.TrimSpace(ln) == "":
			i++
		case isFrame(ln) || asyncSepRe.MatchString(ln):
			j := i
			for j < to && (isFrame(d.in[j]) || asyncSepRe.MatchString(d.in[j]) || strings.TrimSpace(d.in[j]) == "" && j+1 < to && isFrame(d.in[j+1])) {
				j++
			}
			foldFramesSkipBlank(d, i, j)
			i = j
		case mochaDiffRe.MatchString(ln):
			ind := len(mochaDiffRe.FindStringSubmatch(ln)[1])
			d.keep(i)
			e := i + 1
			for e < to && !isFrame(d.in[e]) {
				e++
			}
			for e > i+1 && strings.TrimSpace(d.in[e-1]) == "" {
				e--
			}
			keepDiff(d, i+1, e, ind)
			i = e
		default:
			d.keep(i)
			i++
		}
	}
	// The title path ends with the line ending in ":" ("should work:",
	// "\"before all\" hook for \"x\":").
	head := 1
	for k := from; k < to && k < from+12; k++ {
		if strings.HasSuffix(strings.TrimSpace(d.in[k]), ":") {
			head = k - from + 1
			break
		}
	}
	compactBlock(d, start, true, from, head)
}

// foldFramesSkipBlank is foldFrames over a range that may contain blank
// lines between frames.
func foldFramesSkipBlank(d *doc, from, to int) {
	for i := from; i < to; {
		if strings.TrimSpace(d.in[i]) == "" {
			i++
			continue
		}
		j := i
		for j < to && strings.TrimSpace(d.in[j]) != "" {
			j++
		}
		foldFrames(d, i, j)
		i = j
	}
}

// renderNodeLines keeps lines[from:to] with blank runs collapsed and
// library frames folded (mocha's "Exception during run:" abort).
func renderNodeLines(d *doc, from, to int) result {
	var w wrapper
	for i := from; i < to; {
		ln := d.in[i]
		if k := w.handle(d, i); k > 0 {
			i += k
			continue
		}
		switch {
		case strings.TrimSpace(ln) == "":
			d.sep()
			i++
		case isFrame(ln):
			j := i
			for j < to && isFrame(d.in[j]) {
				j++
			}
			foldFrames(d, i, j)
			i = j
		default:
			d.keep(i)
			i++
		}
	}
	return d.finish()
}
