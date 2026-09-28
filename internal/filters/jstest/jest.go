package jstest

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

var (
	jestSuiteRe    = lazyre.New(`^ ?(PASS|FAIL) +(\S.*)$`)
	jestBulletRe   = lazyre.New(`^  ● (.*)$`)
	jestConsoleRe  = lazyre.New(`^( +)console\.(?:log|info|warn|error|debug|trace|dir|dirxml|table|group|groupCollapsed|time|timeEnd|timeLog|count|assert)$`)
	jestSnapNoteRe = lazyre.New(`^ › \d+ snapshots? `)
	jestTreeRe     = lazyre.New(`^\s+([✓✕○✎√×]) (.*)$`)
	jestDiffHeadRe = lazyre.New(`^(\s*)- (?:Expected|Snapshot)\b`)

	frameFileRe = lazyre.New(`(?:\(|at )([^\s():]+):\d+:\d+\)?$`)
)

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
	line   int
	fail   bool
	file   string
	passed int

	console []span
	tree    span
	blocks  []span
	notes   []int

	inSummary bool
}

type span struct{ from, to int }

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

	type unit struct {
		suite int
		span
		kind string
	}
	var (
		suites []*jestSuite
		units  []unit
		loose  []span
		cur    = -1
		inSum  bool
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
				file = file[:k]
			}
			s := &jestSuite{line: i, fail: fail, file: file, inSummary: inSum}
			suites = append(suites, s)
			cur = len(suites) - 1
			units = append(units, unit{suite: cur})
			i++

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
			if strings.HasPrefix(ln, "Test Suites: ") {
				inSum = false
			}
			units = append(units, unit{suite: -1, span: span{i, i + 1}, kind: "line"})
			cur = -1
			i++
		}
	}

	attributed := map[int]bool{}
	byFile := map[string][]*jestSuite{}
	for _, s := range suites {
		byFile[s.file] = append(byFile[s.file], s)
	}
	for _, sp := range loose {
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
	fx := focusFor(d)
	if fx != nil {
		perm := focusSlots(len(units), func(k int) int {
			switch u := units[k]; {
			case u.suite >= 0 && suites[u.suite].fail:
				return slotMove
			case u.kind == "summary-header", u.kind == "line" && strings.HasPrefix(in[u.from], "Test Suites: "):
				return slotCut
			}
			return slotStay
		}, func(k int) float64 { return suiteScore(fx, in, suites[units[k].suite]) })
		if perm != nil {
			units = reorder(units, perm)
		}
	}

	var (
		w          wrapper
		emitted    = map[string]bool{}
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

			var fresh []span
			for _, b := range s.blocks {
				sig := s.file + "\x00" + squash(strings.Join(in[b.from:b.to], "\n"))
				if emitted[sig] {
					for k := b.from; k < b.to; k++ {
						d.drop(k)
					}
					continue
				}
				emitted[sig] = true
				fresh = append(fresh, b)
			}
			fresh = focusSpans(fx, in, fresh)
			if s.inSummary && len(fresh) == 0 && shownSuite[s.file] && len(s.console) == 0 {
				d.drop(s.line)
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
			d.drop(u.from)
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

func dropTree(d *doc, t span) {
	for k := t.from; k < t.to; k++ {
		d.drop(k)
	}
}

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

const (
	maxBlockLines  = 80
	blockHeadLines = 60
)

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
					d.keep(k)
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

const maxConsoleLines = 20

func renderConsole(d *doc, sp span, failing bool, anchor func()) int {
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

		j := i + 1
		for j < sp.to && !jestConsoleRe.MatchString(d.in[j]) {
			j++
		}
		head := indentOf(d.in[i])
		if !failing {
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
						d.drop(k)
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

		start := len(d.out)
		d.keep(i)
		for k := i + 1; k < j; {
			ln := d.in[k]
			switch {
			case strings.TrimSpace(ln) == "":
				k++
			case jestCodeRe.MatchString(ln) || jestCaretRe.MatchString(ln):
				d.drop(k)
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
