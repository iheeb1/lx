package jstest

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

var (
	vRunRe = lazyre.New(`^ RUN {2}v\d+\.\d+\S* |^ +Coverage enabled with \w+$`)

	vFileRe    = lazyre.New(`^ ([✓❯×↓]) (\S.*?) \((\d+)(?: tests?)?((?: \| [^)]*)?)\)(?: .*)?$`)
	vSkipCntRe = lazyre.New(`(\d+) (?:skipped|todo)`)
	vGroupRe   = lazyre.New(`^ {3,}([❯✓×↓]) (.*)$`)

	vGroupCountRe = lazyre.New(` \(\d+\)$`)
	vVerboseRe    = lazyre.New(`^ ([✓×↓□]) (\S+ > .*)$`)
	vArrowRe      = lazyre.New(`^ {3}→ `)

	vConsoleRe  = lazyre.New(`^[·x\-*]*(stdout|stderr) \| (.+)$`)
	vSepRe      = lazyre.New(`^⎯{3,}`)
	vSectionRe  = lazyre.New(`^⎯+ (.+?) ⎯+$`)
	vFailRe     = lazyre.New(`^ FAIL {2}(.+)$`)
	vSummaryRe  = lazyre.New(`^ *(Test Files|Tests|Errors|Snapshots|Type Errors|Start at|Duration) {2}\S`)
	vDiffHeadRe = lazyre.New(`^(\s*)- Expected\s*$`)
	vDotsRe     = lazyre.New(`^[·x\-*]+$`)
)

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
	fx := focusFor(d)

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

	section := ""
	inPassFile := false

	xShown, xHidden := 0, 0
	xName := func(i int) int {
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
			inPassFile = false
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
					i++
					continue
				}
				d.keep(i)
				i++
			}
			section = "summary"
		case (strings.HasPrefix(ln, " RUN  v") || strings.Contains(ln, "Coverage enabled with ")) && vRunRe.MatchString(ln):
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
					j++
				}
				i = j
			}
		case vSepRe.MatchString(ln):
			d.sep()
			i++
		case vFailRe.MatchString(ln):
			failure := func(i int) int {
				j := vitestFailEnd(in, i, summaryAt)
				d.sep()
				start := len(d.out)
				d.keep(i)

				test := !strings.HasPrefix(section, "Failed Suites")
				renderVitestBody(d, start, i+1, j, test, test)
				return j
			}
			units, end := vitestFailures(fx, in, i, summaryAt)
			if units == nil {
				i = failure(i)
				break
			}
			for _, u := range units {
				for k := u.from; k < u.to; {
					k = failure(k)
				}
			}
			i = end
		case section == "" && vConsoleRe.MatchString(ln):
			j := i + 1
			for j < n && strings.TrimSpace(in[j]) != "" {
				j++
			}
			target := vConsoleRe.FindStringSubmatch(ln)[2]
			if failed[target] {
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
				d.drop(i)
				t, _ := strconv.Atoi(m[3])
				for _, sk := range vSkipCntRe.FindAllStringSubmatch(m[4], -1) {
					k, _ := strconv.Atoi(sk[1])
					t -= k
				}
				passTests += max(t, 0)
			case "↓":
				d.drop(i)
			default:
				d.keep(i)
			}
			i++
		case section == "" && vVerboseRe.MatchString(ln):
			m := vVerboseRe.FindStringSubmatch(ln)
			switch m[1] {
			case "✓":
				passTests++
				d.drop(i)
			case "×":
				i = xName(i)
			default:
				d.drop(i)
			}
			i++
		case section == "" && vTestLine(ln) != "":
			switch vTestLine(ln) {
			case "✓":
				if !inPassFile && !vGroupCountRe.MatchString(ln) {
					passTests++
				}
				d.drop(i)
			case "×":
				i = xName(i)
			default:
				d.drop(i)
			}
			i++
		case section == "" && vGroupRe.MatchString(ln) && !isFrame(ln):
			d.drop(i)
			i++
		case section == "" && vDotsRe.MatchString(ln):
			i++
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

func renderVitestBody(d *doc, start, from, to int, trim, failure bool) {
	loc := 0
	for i := from; i < to; {
		ln := d.in[i]
		switch {
		case strings.TrimSpace(ln) == "":
			i++
		case vFrameRe.MatchString(ln):
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
