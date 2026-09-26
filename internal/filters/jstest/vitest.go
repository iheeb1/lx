package jstest

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// vitest's default, verbose and agent reporters (vitest 1-4):
//
//	 RUN  v4.1.11 /abs/project
//	stdout | test/ok.test.ts > works             console output of a test
//	 ✓ test/url.test.ts (6 tests) 5ms            passing file
//	 ❯ test/query.test.ts (34 tests | 1 failed) 8ms
//	     ✓ / with {} 0ms                          tests of a failing file
//	     × / with {"str":"&"} 3ms
//	 × test/a.test.ts > suite > name 4ms         --reporter=verbose
//	   → expected 1 to be 2
//	⎯⎯⎯⎯⎯⎯ Failed Suites 2 ⎯⎯⎯⎯⎯⎯⎯               files that failed to load
//	 FAIL  test/x.test.ts [ test/x.test.ts ]
//	⎯⎯⎯⎯⎯⎯⎯ Failed Tests 7 ⎯⎯⎯⎯⎯⎯⎯
//	 FAIL  test/a.test.ts > suite > name
//	AssertionError: …
//	 ❯ test/a.test.ts:15:41                       location, then a code frame
//	⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯⎯[1/7]⎯
//	⎯⎯⎯⎯⎯⎯ Unhandled Errors ⎯⎯⎯⎯⎯⎯
//	 Test Files  3 failed | 10 passed (13)        summary, kept verbatim
//	      Tests  7 failed | 482 passed (489)
//	     Errors  1 error
//	   Start at  00:35:29
//	   Duration  368ms (transform 289ms, …)
//
// With CLAUDECODE / AI_AGENT set, vitest's agent reporter prints only the
// failing files; the layout is otherwise the same.

var (
	vRunRe = regexp.MustCompile(`^ RUN {2}v\d+\.\d+\S* |^ +Coverage enabled with \w+$`)
	// vitest ≥2: "(34 tests | 1 failed) 8ms"; vitest 1: "(34)".
	vFileRe    = regexp.MustCompile(`^ ([✓❯×↓]) (\S.*?) \((\d+)(?: tests?)?((?: \| [^)]*)?)\)(?: .*)?$`)
	vSkipCntRe = regexp.MustCompile(`(\d+) (?:skipped|todo)`)
	vGroupRe   = regexp.MustCompile(`^ {3,}([❯✓×↓]) (.*)$`)
	// a describe group of the tree reporter: "   ✓ error handling (2)"
	// (tests end with a duration: "     ✓ works 1ms")
	vGroupCountRe = regexp.MustCompile(` \(\d+\)$`)
	vVerboseRe    = regexp.MustCompile(`^ ([✓×↓□]) (\S+ > .*)$`)
	vArrowRe      = regexp.MustCompile(`^ {3}→ `)
	// The dot reporter prints no newline after its dots: "··xstdout | …".
	vConsoleRe  = regexp.MustCompile(`^[·x\-*]*(stdout|stderr) \| (.+)$`)
	vSepRe      = regexp.MustCompile(`^⎯{3,}`)
	vSectionRe  = regexp.MustCompile(`^⎯+ (.+?) ⎯+$`)
	vFailRe     = regexp.MustCompile(`^ FAIL {2}(.+)$`)
	vSummaryRe  = regexp.MustCompile(`^ *(Test Files|Tests|Errors|Snapshots|Type Errors|Start at|Duration) {2}\S`)
	vDiffHeadRe = regexp.MustCompile(`^(\s*)- Expected\s*$`)
	vDotsRe     = regexp.MustCompile(`^[·x\-*]+$`)
)

// vTestLine returns the status mark of a test line of the default
// reporter's list ("     ✓ name 3ms": ✓ × ↓ skipped □ todo · not run, after
// --bail), or "". It runs on every line of every render, so it does without
// a regexp.
func vTestLine(ln string) string {
	n := 0
	for n < len(ln) && ln[n] == ' ' {
		n++
	}
	if n < 3 {
		return ""
	}
	for _, mark := range [...]string{"✓", "×", "↓", "□", "·"} {
		if strings.HasPrefix(ln[n:], mark+" ") {
			return mark
		}
	}
	return ""
}

// renderVitest condenses vitest's text reporters. It bails unless the
// "Test Files" / "Tests" summary is present.
func renderVitest(c *engine.Context, clean string) (result, bool) {
	if !hasVitestSummary(clean) {
		return result{}, false
	}
	return levels(func(level int, sh *shared) (result, bool) { return renderVitestView(c, clean, level, sh) })
}

func renderVitestView(c *engine.Context, clean string, level int, sh *shared) (result, bool) {
	d := newDoc(c, clean, sh)
	d.level = level
	in := d.in
	n := len(in)

	// Tests that failed, by "file > suite > test" (to tell failing tests'
	// console output from passing tests').
	failed := map[string]bool{}
	for _, ln := range in {
		if m := vFailRe.FindStringSubmatch(ln); m != nil {
			failed[m[1]] = true
		}
	}

	var (
		w          wrapper
		passFiles  int
		passTests  int
		hiddenLogs int
		th         = coverageThreshold(in)
		summaryAt  = -1
	)
	// The summary is the block of summary lines around the last "Test
	// Files" line ("Snapshots  1 failed" may come first).
	for i := n - 1; i >= 0; i-- {
		if vSummaryRe.MatchString(in[i]) && strings.HasPrefix(strings.TrimSpace(in[i]), "Test Files") {
			summaryAt = i
			for summaryAt > 0 && vSummaryRe.MatchString(in[summaryAt-1]) {
				summaryAt--
			}
			break
		}
	}
	marker := func() {
		if passFiles == 0 && passTests == 0 && hiddenLogs == 0 {
			return
		}
		var parts []string
		if passFiles > 0 {
			parts = append(parts, plural(passFiles, "passing file", "passing files"))
		}
		if passTests > 0 {
			parts = append(parts, plural(passTests, "passing test", "passing tests"))
		}
		s := "["
		if len(parts) > 0 {
			s += strings.Join(parts, ", ") + " hidden"
		}
		if hiddenLogs > 0 {
			if len(parts) > 0 {
				s += "; "
			}
			s += plural(hiddenLogs, "console block", "console blocks") + " of passing tests hidden"
		}
		d.emit(s + "]")
	}

	section := "" // "" (test list), or the section title
	inPassFile := false
	// Level 3 lists the names of the first briefFailures failing tests in
	// the test list; the file lines count the rest ("(10 tests | 10
	// failed)"), and one marker says how many names were left out.
	xShown, xHidden := 0, 0
	xName := func(i int) int { // returns the last line used (arrows follow)
		keep := d.level < 3 || xShown < briefFailures
		xShown++
		for {
			if keep {
				d.keep(i)
			} else {
				d.drop(i)
			}
			if i+1 >= n || !vArrowRe.MatchString(in[i+1]) {
				break
			}
			i++
		}
		if !keep {
			xHidden++
		}
		return i
	}
	flushNames := func() {
		if xHidden > 0 {
			d.emit(fmt.Sprintf("[+%s not listed; the file lines above count them]", plural(xHidden, "more failing test name", "more failing test names")))
			xHidden = 0
		}
	}
	for i := 0; i < n; {
		ln := in[i]
		if !strings.HasPrefix(ln, "   ") {
			inPassFile = false // left the file's test list (a file line sets it again)
		}
		if k := w.handle(d, i); k > 0 {
			i += k
			continue
		}
		switch {
		case strings.TrimSpace(ln) == "":
			i++
		case i == summaryAt:
			flushNames()
			flushOmitted(d, "")
			d.sep()
			marker()
			for i < n && (vSummaryRe.MatchString(in[i]) || strings.TrimSpace(in[i]) == "") {
				if strings.HasPrefix(strings.TrimSpace(in[i]), "Start at") || strings.TrimSpace(in[i]) == "" {
					i++ // wall-clock time of the run
					continue
				}
				d.keep(i)
				i++
			}
			section = "summary"
		case (strings.HasPrefix(ln, " RUN  v") || strings.Contains(ln, "Coverage enabled with ")) && vRunRe.MatchString(ln):
			// " RUN  v4.1.11 /abs/project", "Coverage enabled with v8". A
			// RUN line starts a new run (npm workspaces): back to its list.
			if strings.HasPrefix(ln, " RUN  v") {
				flushOmitted(d, "")
				section = ""
			}
			i++
		case vSectionRe.MatchString(ln):
			flushNames()
			flushOmitted(d, "")
			section = vSectionRe.FindStringSubmatch(ln)[1]
			d.sep()
			d.keep(i)
			i++
			if strings.HasPrefix(section, "Unhandled") {
				j := i
				for j < n && !(vSepRe.MatchString(in[j]) && !vSectionRe.MatchString(in[j]) && !strings.Contains(in[j], "[")) {
					j++
				}
				renderVitestBody(d, len(d.out), i, j, false, false)
				if j < n {
					j++ // closing separator
				}
				i = j
			}
		case vSepRe.MatchString(ln):
			d.sep() // "⎯⎯⎯[1/7]⎯" between failures
			i++
		case vFailRe.MatchString(ln):
			j := i + 1
			for j < n && !vSepRe.MatchString(in[j]) && !vFailRe.MatchString(in[j]) && j != summaryAt {
				j++
			}
			d.sep()
			start := len(d.out)
			d.keep(i)
			// Test failures keep only the failing source line; files that
			// failed to load keep their whole diagnostic.
			test := !strings.HasPrefix(section, "Failed Suites")
			renderVitestBody(d, start, i+1, j, test, test)
			i = j
		case section == "" && vConsoleRe.MatchString(ln):
			j := i + 1
			for j < n && strings.TrimSpace(in[j]) != "" {
				j++
			}
			target := vConsoleRe.FindStringSubmatch(ln)[2]
			if failed[target] {
				// A failing test's output: up to maxConsoleLines lines,
				// then only its error lines. Console output is never how
				// vitest reports a failure (see noFailure).
				d.sep()
				start := len(d.out)
				d.keep(i)
				more := 0
				for k := i + 1; k < j; k++ {
					if k-i <= maxConsoleLines || d.isErr(in[k]) {
						d.keep(k)
						continue
					}
					more++
					d.hush(k, k+1)
				}
				if more > 0 {
					d.emit(fmt.Sprintf("… +%s", plural(more, "more line", "more lines")))
				}
				d.markChatter(start)
				d.sep()
			} else {
				shown := false
				start := len(d.out)
				for k := i + 1; k < j; k++ {
					if d.isErr(in[k]) {
						if !shown {
							shown = true
							d.sep()
							d.keep(i)
						}
						d.keep(k)
					}
				}
				d.markChatter(start)
				if !shown {
					hiddenLogs++
					d.hush(i, j)
				} else {
					d.sep()
				}
			}
			i = j
		case section == "" && vFileRe.MatchString(ln):
			m := vFileRe.FindStringSubmatch(ln)
			inPassFile = m[1] == "✓"
			switch m[1] {
			case "✓":
				passFiles++
				d.drop(i) // counted in the marker
				t, _ := strconv.Atoi(m[3])
				for _, sk := range vSkipCntRe.FindAllStringSubmatch(m[4], -1) {
					k, _ := strconv.Atoi(sk[1])
					t -= k // "(6 tests | 2 skipped)"
				}
				passTests += max(t, 0)
			case "↓":
				d.drop(i) // skipped file: its title only
			default:
				d.keep(i)
			}
			i++
		case section == "" && vVerboseRe.MatchString(ln):
			m := vVerboseRe.FindStringSubmatch(ln)
			switch m[1] {
			case "✓":
				passTests++
				d.drop(i) // counted in the marker
			case "×":
				i = xName(i)
			default:
				d.drop(i) // skipped / todo test title
			}
			i++
		case section == "" && vTestLine(ln) != "":
			switch vTestLine(ln) {
			case "✓":
				// The tree reporter lists the tests of passing files,
				// already counted from the file line, and marks passing
				// describe groups "✓ name (N)" too.
				if !inPassFile && !vGroupCountRe.MatchString(ln) {
					passTests++
				}
				d.drop(i) // counted in the marker
			case "×":
				i = xName(i)
			default:
				d.drop(i) // skipped / todo test title
			}
			i++
		case section == "" && vGroupRe.MatchString(ln) && !isFrame(ln):
			d.drop(i) // describe group title in the test list
			i++
		case section == "" && vDotsRe.MatchString(ln):
			i++ // dot reporter progress
		case coverageTableEnd(in, i) > i:
			j := coverageTableEnd(in, i)
			d.sep()
			renderCoverage(d, i, j, th)
			i = j
		default:
			d.keep(i)
			i++
		}
	}
	flushNames()
	flushOmitted(d, "")
	if summaryAt < 0 {
		d.sep()
		marker()
	}
	if c.Exit != 0 {
		noFailure(d, "vitest", c.Exit)
	}
	return d.finish(), true
}

// renderVitestBody renders the lines of one failure (after its FAIL line)
// or of the unhandled-errors section: blank lines dropped, library frames
// folded, long diffs trimmed; with trim, code frames keep only the line the
// preceding "❯ file:line:col" points at (and its caret).
func renderVitestBody(d *doc, start, from, to int, trim, failure bool) {
	loc := 0
	for i := from; i < to; {
		ln := d.in[i]
		switch {
		case strings.TrimSpace(ln) == "":
			i++
		case vFrameRe.MatchString(ln):
			// A run of frames; remember the last app frame's line for
			// the code frame that may follow (none: keep that frame whole).
			j := i
			loc = 0
			for j < to && vFrameRe.MatchString(d.in[j]) {
				if _, lib := libRoot(d.in[j]); !lib {
					loc, _ = strconv.Atoi(vFrameRe.FindStringSubmatch(d.in[j])[3])
				}
				j++
			}
			foldFrames(d, i, j)
			i = j
		case isFrame(ln):
			j := i
			for j < to && (isFrame(d.in[j]) || asyncSepRe.MatchString(d.in[j])) {
				j++
			}
			foldFrames(d, i, j)
			i = j
		case vCodeRe.MatchString(ln) || vCaretRe.MatchString(ln):
			j := vCodeFrameEnd(d.in, i, to)
			if trim {
				keepVitestCodeFrame(d, i, j, loc)
			} else {
				for k := i; k < j; k++ {
					d.keep(k)
				}
			}
			i = j
		case vDiffHeadRe.MatchString(ln):
			ind := len(vDiffHeadRe.FindStringSubmatch(ln)[1])
			d.keep(i)
			j := i + 1
			if j < to && strings.HasPrefix(strings.TrimSpace(d.in[j]), "+ Received") {
				d.keep(j)
				j++
			}
			e := j
			for e < to && !vFrameRe.MatchString(d.in[e]) && !isFrame(d.in[e]) && !vCodeRe.MatchString(d.in[e]) {
				e++
			}
			for e > j && strings.TrimSpace(d.in[e-1]) == "" {
				e--
			}
			keepDiff(d, j, e, ind)
			i = e
		default:
			d.keep(i)
			i++
		}
	}
	compactBlock(d, start, failure, from-1, 1)
}
