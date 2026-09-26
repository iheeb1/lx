package jstest

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// jest's default and --verbose reporters (jest 25-30).
//
//	PASS test/a.test.js
//	FAIL test/b.test.js (5.2 s)
//	  ● Suite › test name                        failure block (indented ≥4)
//	  ● Test suite failed to run                 file could not load
//	  ● Console                                   console entries of the suite
//	  console.log                                --verbose: console entries
//	                                             printed before the suite line
//	 › 2 snapshots failed.
//	Summary of all failing tests                 all failure blocks, again
//	Snapshot Summary
//	Test Suites: 3 failed, 55 passed, 58 total   summary, kept verbatim
//	Tests:       10 failed, 1212 passed, 1222 total
//	Snapshots:   1 passed, 1 total
//	Time:        2.137 s
//	Ran all test suites.

var (
	jestSuiteRe    = regexp.MustCompile(`^ ?(PASS|FAIL) +(\S.*)$`) // " PASS " badge with FORCE_COLOR
	jestBulletRe   = regexp.MustCompile(`^  ● (.*)$`)
	jestConsoleRe  = regexp.MustCompile(`^( +)console\.(?:log|info|warn|error|debug|trace|dir|dirxml|table|group|groupCollapsed|time|timeEnd|timeLog|count|assert)$`)
	jestSnapNoteRe = regexp.MustCompile(`^ › \d+ snapshots? `)
	jestTreeRe     = regexp.MustCompile(`^\s+([✓✕○✎√×]) (.*)$`)
	jestDiffHeadRe = regexp.MustCompile(`^(\s*)- (?:Expected|Snapshot)\b`)
	// file of a frame: "at Object.log (test/log.test.js:2:11)", "at test/a.js:1:2"
	frameFileRe = regexp.MustCompile(`(?:\(|at )([^\s():]+):\d+:\d+\)?$`)
)

// jestSuiteLine parses a "PASS file" / "FAIL file (5.2 s)" line (jestSuiteRe,
// without a regexp: it runs on every line of every render).
func jestSuiteLine(ln string) (fail bool, file string, ok bool) {
	t := strings.TrimPrefix(ln, " ")
	switch {
	case strings.HasPrefix(t, "PASS "):
	case strings.HasPrefix(t, "FAIL "):
		fail = true
	default:
		return false, "", false
	}
	file = strings.TrimLeft(t[5:], " ")
	if file == "" || file[0] == '\t' {
		return false, "", false
	}
	return fail, file, true
}

func isSuiteLine(ln string) bool { _, _, ok := jestSuiteLine(ln); return ok }

type jestSuite struct {
	line   int // input line of "PASS/FAIL file"
	fail   bool
	file   string
	passed int // ✓ lines (--verbose)
	// attached segments, in input order
	console []span // console entry groups
	tree    span   // --verbose test list
	blocks  []span // ● blocks except Console
	notes   []int  // " › N snapshots failed." lines
	// inSummary: the suite line is in the run's "Summary of all failing
	// tests" section, which repeats failures shown above.
	inSummary bool
}

type span struct{ from, to int }

// renderJest condenses jest's text reporter output. It bails unless the
// run's "Test Suites:" and "Tests:" summary lines are present.
func renderJest(c *engine.Context, clean string) (result, bool) {
	if !hasJestSummary(clean) {
		return result{}, false
	}
	return levels(func(level int, sh *shared) (result, bool) { return renderJestView(c, clean, level, sh) })
}

func renderJestView(c *engine.Context, clean string, level int, sh *shared) (result, bool) {
	d := newDoc(c, clean, sh)
	d.level = level
	in := d.in
	n := len(in)

	// Pass 1: attach console entries, trees, blocks and notes to suites;
	// everything else is a top-level unit rendered in place.
	type unit struct {
		suite int // index into suites, or -1 for a top-level line range
		span
		kind string // top-level kind
	}
	var (
		suites []*jestSuite
		units  []unit
		loose  []span // console entries printed as they happened (--verbose, single file)
		cur    = -1
		inSum  bool // inside the current run's "Summary of all failing tests"
	)
	blockEnd := func(i int) int {
		j := i + 1
		for j < n && (strings.TrimSpace(in[j]) == "" || indentOf(in[j]) >= 4) {
			j++
		}
		for j > i+1 && strings.TrimSpace(in[j-1]) == "" {
			j--
		}
		return j
	}
	for i := 0; i < n; {
		ln := in[i]
		switch {
		case strings.TrimSpace(ln) == "":
			i++
		case isSuiteLine(ln):
			fail, file, _ := jestSuiteLine(ln)
			if k := strings.Index(file, " ("); k > 0 {
				file = file[:k] // " (5.21 s)"
			}
			s := &jestSuite{line: i, fail: fail, file: file, inSummary: inSum}
			suites = append(suites, s)
			cur = len(suites) - 1
			units = append(units, unit{suite: cur})
			i++
			// --verbose test list: indented lines up to a blank line.
			j := i
			for j < n && strings.TrimSpace(in[j]) != "" && indentOf(in[j]) >= 2 &&
				!jestBulletRe.MatchString(in[j]) && !jestConsoleRe.MatchString(in[j]) {
				if m := jestTreeRe.FindStringSubmatch(in[j]); m != nil && (m[1] == "✓" || m[1] == "√") {
					s.passed++
				}
				j++
			}
			s.tree = span{i, j}
			i = j
		case strings.HasPrefix(ln, "  ● ") && jestBulletRe.MatchString(ln):
			j := blockEnd(i)
			title := jestBulletRe.FindStringSubmatch(ln)[1]
			if cur < 0 {
				// A bullet before any suite line (open handles report,
				// validation errors): keep it as it is.
				units = append(units, unit{suite: -1, span: span{i, j}, kind: "block"})
			} else if title == "Console" {
				suites[cur].console = append(suites[cur].console, span{i, j})
			} else {
				suites[cur].blocks = append(suites[cur].blocks, span{i, j})
			}
			i = j
		case indentOf(ln) == 2 && strings.HasPrefix(ln, "  console.") && jestConsoleRe.MatchString(ln):
			j := i + 1
			for j < n && (strings.TrimSpace(in[j]) == "" || indentOf(in[j]) >= 4) {
				j++
			}
			for j > i+1 && strings.TrimSpace(in[j-1]) == "" {
				j--
			}
			loose = append(loose, span{i, j})
			units = append(units, unit{suite: -1, span: span{i, j}, kind: "loose-console"})
			i = j
		case cur >= 0 && strings.HasPrefix(ln, " › ") && jestSnapNoteRe.MatchString(ln):
			suites[cur].notes = append(suites[cur].notes, i)
			i++
		case ln == "Summary of all failing tests":
			inSum = true
			cur = -1
			units = append(units, unit{suite: -1, span: span{i, i + 1}, kind: "summary-header"})
			i++
		case ln == "Snapshot Summary":
			j := i + 1
			for j < n && (strings.HasPrefix(in[j], " › ") || strings.HasPrefix(in[j], "   ↳ ") || strings.HasPrefix(in[j], "       • ")) {
				j++
			}
			units = append(units, unit{suite: -1, span: span{i, j}, kind: "keep"})
			cur = -1
			i = j
		case strings.HasPrefix(ln, "--") && coverageTableEnd(in, i) > i:
			j := coverageTableEnd(in, i)
			units = append(units, unit{suite: -1, span: span{i, j}, kind: "coverage"})
			cur = -1
			i = j
		default:
			// Summary lines, thresholds, wrapper lines, anything unknown:
			// top level, kept. "Test Suites:" ends a run (npm workspaces
			// run jest once per package).
			if strings.HasPrefix(ln, "Test Suites: ") {
				inSum = false
			}
			units = append(units, unit{suite: -1, span: span{i, i + 1}, kind: "line"})
			cur = -1
			i++
		}
	}
	// Console entries printed as they happened are not next to their
	// suite's line: attribute each to the suite named by its first frame.
	// Unattributed entries stay where they are.
	attributed := map[int]bool{}
	byFile := map[string][]*jestSuite{}
	for _, s := range suites {
		byFile[s.file] = append(byFile[s.file], s)
	}
	for _, sp := range loose {
		// The call site is the entry's last frame; frames above it may
		// belong to a logged Error's own stack.
		for k := sp.to - 1; k >= sp.from; k-- {
			if m := frameFileRe.FindStringSubmatch(in[k]); m != nil && isFrame(in[k]) {
				if s := suiteOf(byFile, m[1], sp.from); s != nil {
					s.console = append(s.console, sp)
					attributed[sp.from] = true
				}
				break
			}
		}
	}

	// Pass 2: render.
	var (
		w          wrapper
		emitted    = map[string]bool{} // block signatures shown (the failing-tests summary repeats them)
		shownSuite = map[string]bool{}
		passSuites int
		passTests  int
		hiddenLogs int
		markerDone bool
		th         = coverageThreshold(in)
	)
	marker := func() {
		if markerDone {
			return
		}
		markerDone = true
		if passSuites == 0 && hiddenLogs == 0 {
			return
		}
		d.sep()
		parts := []string{plural(passSuites, "passing suite", "passing suites")}
		if passTests > 0 {
			parts = append(parts, plural(passTests, "passing test", "passing tests"))
		}
		s := "[" + strings.Join(parts, ", ") + " hidden"
		if hiddenLogs > 0 {
			s += fmt.Sprintf("; %s of passing suites hidden", plural(hiddenLogs, "console message", "console messages"))
		}
		d.emit(s + "]")
	}
	for _, u := range units {
		if u.suite >= 0 {
			s := suites[u.suite]
			if !s.fail {
				passSuites++
				passTests += s.passed
				dropTree(d, s.tree)
				shown := false
				start := len(d.out)
				for _, sp := range s.console {
					hiddenLogs += renderConsole(d, sp, false, func() {
						if !shown {
							shown = true
							d.sep()
							d.keep(s.line)
						}
					})
				}
				d.markChatter(start)
				continue
			}
			// Blocks not shown yet (all of them, except in the repeated
			// summary section).
			var fresh []span
			for _, b := range s.blocks {
				sig := s.file + "\x00" + squash(strings.Join(in[b.from:b.to], "\n"))
				if emitted[sig] {
					for k := b.from; k < b.to; k++ {
						d.drop(k) // repeated verbatim from above
					}
					continue
				}
				emitted[sig] = true
				fresh = append(fresh, b)
			}
			if s.inSummary && len(fresh) == 0 && shownSuite[s.file] && len(s.console) == 0 {
				d.drop(s.line) // "FAIL file" repeated by the summary section
				continue
			}
			shownSuite[s.file] = true
			d.sep()
			d.keep(s.line)
			renderJestTree(d, s, len(fresh))
			for _, sp := range s.console {
				renderConsole(d, sp, true, nil)
			}
			for _, b := range fresh {
				renderJestBlock(d, b)
			}
			for _, k := range s.notes {
				d.keep(k)
			}
			continue
		}
		switch u.kind {
		case "loose-console":
			if !attributed[u.from] {
				d.sep()
				renderConsole(d, u.span, true, nil)
			}
		case "summary-header":
			d.drop(u.from) // its blocks are shown once, above
		case "coverage":
			d.sep()
			renderCoverage(d, u.from, u.to, th)
		case "block":
			d.sep()
			renderJestBlock(d, u.span)
		case "keep":
			d.sep()
			for k := u.from; k < u.to; k++ {
				d.keep(k)
			}
		default:
			ln := in[u.from]
			if k := w.handle(d, u.from); k > 0 {
				continue
			}
			if !strings.HasPrefix(ln, "Test Suites: ") {
				d.keep(u.from)
				continue
			}
			flushOmitted(d, "")
			d.sep()
			marker()
			d.keep(u.from)
			// The run is over: a later run (npm workspaces) gets its own
			// marker, and its failures are not repeats of this run's.
			markerDone, passSuites, passTests, hiddenLogs = false, 0, 0, 0
			emitted, shownSuite = map[string]bool{}, map[string]bool{}
		}
	}
	flushOmitted(d, "")
	marker()
	if c.Exit != 0 {
		noFailure(d, "jest", c.Exit)
	}
	return d.finish(), true
}

// suiteOf finds the suite of a frame's file, which may be absolute: the
// longest trailing part of the path that names a suite. Console entries are
// printed before their suite's line, so of several suites with that name
// (one per npm workspace run) the first one after line at wins.
func suiteOf(byFile map[string][]*jestSuite, file string, at int) *jestSuite {
	for p := file; p != ""; {
		if ss := byFile[p]; len(ss) > 0 {
			for _, s := range ss {
				if s.line > at {
					return s
				}
			}
			return ss[len(ss)-1]
		}
		k := strings.IndexByte(p, '/')
		if k < 0 {
			break
		}
		p = p[k+1:]
	}
	return nil
}

// dropTree drops a --verbose test list: passing/skipped/todo test titles
// and describe titles are names, not results.
func dropTree(d *doc, t span) {
	for k := t.from; k < t.to; k++ {
		d.drop(k)
	}
}

// renderJestTree shows the --verbose test list of a failing suite only
// when it names failures that have no "●" block (normally every ✕ test has
// one, and the list is dropped).
func renderJestTree(d *doc, s *jestSuite, blocks int) {
	fails := 0
	for k := s.tree.from; k < s.tree.to; k++ {
		if m := jestTreeRe.FindStringSubmatch(d.in[k]); m != nil && (m[1] == "✕" || m[1] == "×") {
			fails++
		}
	}
	if fails <= blocks {
		dropTree(d, s.tree)
		return
	}
	for k := s.tree.from; k < s.tree.to; k++ {
		if m := jestTreeRe.FindStringSubmatch(d.in[k]); m != nil && (m[1] == "✕" || m[1] == "×") {
			d.keep(k)
		} else {
			d.drop(k)
		}
	}
}

// maxBlockLines caps one failure block; longer blocks keep their first
// lines plus every later error line and application frame.
const (
	maxBlockLines  = 80
	blockHeadLines = 60
)

// renderJestBlock renders one "● title" block: blank lines removed, source
// context around the failing line dropped, library frames folded, long
// diffs trimmed to their changes.
func renderJestBlock(d *doc, b span) {
	start := len(d.out)
	d.keep(b.from)
	suiteFail := strings.Contains(d.in[b.from], "Test suite failed to run")
	for i := b.from + 1; i < b.to; {
		ln := d.in[i]
		switch {
		case strings.TrimSpace(ln) == "":
			i++
		case jestCodeRe.MatchString(ln) || jestCaretRe.MatchString(ln):
			j := jestCodeFrameEnd(d.in, i, b.to)
			if suiteFail {
				for k := i; k < j; k++ {
					d.keep(k) // syntax errors: the context is the diagnosis
				}
			} else {
				keepJestCodeFrame(d, i, j)
			}
			i = j
		case isFrame(ln) || asyncSepRe.MatchString(ln):
			j := i
			for j < b.to && (isFrame(d.in[j]) || asyncSepRe.MatchString(d.in[j])) {
				j++
			}
			foldFrames(d, i, j)
			i = j
		case jestDiffHeadRe.MatchString(ln):
			ind := len(jestDiffHeadRe.FindStringSubmatch(ln)[1])
			d.keep(i)
			j := i + 1
			if j < b.to && strings.HasPrefix(strings.TrimSpace(d.in[j]), "+ Received") {
				d.keep(j)
				j++
			}
			e := j
			for e < b.to && !jestCodeRe.MatchString(d.in[e]) && !isFrame(d.in[e]) {
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
	compactBlock(d, start, !suiteFail, b.from, 1)
	d.sep()
}

// maxConsoleLines caps one console entry of a failing suite.
const maxConsoleLines = 20

// renderConsole renders the console entries in sp. For failing suites each
// entry keeps its "console.x" line and up to 20 lines of message and
// frames (source context dropped, library frames folded). For passing
// suites only entries with error-class message lines are shown (those
// lines and the call site, preceded by anchor()); it returns how many
// entries it hid.
func renderConsole(d *doc, sp span, failing bool, anchor func()) int {
	// jest never reports a failure through console output (failures are
	// "●" blocks under a FAIL line), so none of it explains a non-zero exit.
	defer d.markChatter(len(d.out))
	hidden := 0
	for i := sp.from; i < sp.to; {
		if !jestConsoleRe.MatchString(d.in[i]) {
			if strings.TrimSpace(d.in[i]) != "" && (failing || d.isErr(d.in[i])) {
				if anchor != nil {
					anchor()
				}
				d.keep(i)
			}
			i++
			continue
		}
		// One entry: header, message, blank, code frame, frames.
		j := i + 1
		for j < sp.to && !jestConsoleRe.MatchString(d.in[j]) {
			j++
		}
		head := indentOf(d.in[i])
		if !failing {
			// Passing suite: only error-class message lines survive, with
			// the call site: the first frame of the entry's last frame
			// group (frames above it belong to a logged Error's stack).
			e := j - 1
			for e > i && strings.TrimSpace(d.in[e]) == "" {
				e--
			}
			site := e
			for site > i && isFrame(d.in[site]) {
				site--
			}
			site++
			var errs []int
			for k := i + 1; k < j; k++ {
				switch ln := d.in[k]; {
				case isFrame(ln) || jestCodeRe.MatchString(ln) || jestCaretRe.MatchString(ln):
					if k != site {
						d.drop(k) // where a passing test logged, and its source
					}
				case d.isErr(ln):
					errs = append(errs, k)
				}
			}
			last := -1
			if site <= e && isFrame(d.in[site]) {
				last = site
			}
			d.hush(i, j)
			if len(errs) == 0 {
				hidden++
				i = j
				continue
			}
			if anchor != nil {
				anchor()
			}
			d.keep(i)
			for _, k := range errs {
				d.keep(k)
			}
			if last >= 0 {
				d.keep(last)
			}
			i = j
			continue
		}
		// Failing suite: the entry without blank lines and source context,
		// library frames folded, at most maxConsoleLines lines (later
		// error-class lines and the call site are kept).
		start := len(d.out)
		d.keep(i)
		for k := i + 1; k < j; {
			ln := d.in[k]
			switch {
			case strings.TrimSpace(ln) == "":
				k++
			case jestCodeRe.MatchString(ln) || jestCaretRe.MatchString(ln):
				d.drop(k) // source context of the console call
				k++
			case isFrame(ln):
				e := k
				for e < j && isFrame(d.in[e]) {
					e++
				}
				foldFrames(d, k, e)
				k = e
			default:
				d.keep(k)
				k++
			}
		}
		if body := d.out[start+1:]; len(body) > maxConsoleLines+1 {
			kept := append([]string(nil), body[:maxConsoleLines]...)
			more := 0
			for _, ln := range body[maxConsoleLines : len(body)-1] {
				if d.isErr(ln) {
					kept = append(kept, ln)
				} else {
					more++
				}
			}
			if more > 0 {
				kept = append(kept, fmt.Sprintf("%s… +%s", strings.Repeat(" ", head+2), plural(more, "more line", "more lines")))
			}
			kept = append(kept, body[len(body)-1])
			d.out = append(d.out[:start+1], kept...)
			d.hush(i, j)
		}
		i = j
	}
	return hidden
}
