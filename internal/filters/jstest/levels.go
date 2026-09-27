package jstest

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/tokens"
)

// Views of runs with many failures degrade in levels, each rendered only
// when the previous one is longer than briefAbove tokens (the engine budget
// is 8000, and its head/tail cut would keep little more than the FAIL lines
// of a run with hundreds of failures):
//
//	level 0  every failure in full
//	level 1  failures after the first fullFailures are brief: title,
//	         error lines, matcher, expected/received values (including up to
//	         maxBriefDiff changed diff lines) and application frames
//	level 2  as 1, but failures after briefFailures show only their title
//	         and their first error line not shown before
//	level 3  as 2, but failures after the first
//	         briefFailures+namedFailures are left out and counted
//	         ("[+N more failing tests not shown …]"); their error lines not
//	         shown elsewhere are re-added by the safety net (doc.finish)
//
// Each change of level inside a view is announced by a marker line. Suite
// load errors and unhandled errors are never shortened this way.
const (
	briefAbove    = 6000
	jumpAbove     = 4 * briefAbove
	fullFailures  = 10
	briefFailures = 40
	namedFailures = 150
	maxLevel      = 3
	maxBriefDiff  = 8
)

const (
	briefNote = "[lx: many failures; from here on each shows only its messages, expected/received values and locations]"
	namesNote = "[lx: very many failures; from here on each shows only its name and first new error message]"
)

// levelMin[l] is the number of failures above which level l changes a view.
var levelMin = [maxLevel + 1]int{0, fullFailures, briefFailures, briefFailures + namedFailures}

// levels renders a view at increasing levels until it fits briefAbove,
// skipping levels that cannot change a view with that many failures. A
// full view over jumpAbove goes straight to the highest useful level: the
// intermediate ones cannot bring it under briefAbove, and every render
// costs a pass over the whole output.
func levels(render func(level int, sh *shared) (result, bool)) (result, bool) {
	sh := newShared()
	r, ok := render(0, sh)
	if !ok {
		return r, false
	}
	t := tokens.Count(r.out)
	level := 0
	for t > briefAbove && level < maxLevel && r.failures > levelMin[level+1] {
		next := level + 1
		if level == 0 && t > jumpAbove {
			for next < maxLevel && r.failures > levelMin[next+1] {
				next++
			}
		}
		nr, ok2 := render(next, sh)
		if !ok2 {
			break
		}
		r, level, t = nr, next, tokens.Count(nr.out)
	}
	return r, true
}

var (
	// diffHeadRe: the header lines of jest / vitest / mocha diffs, which
	// carry no values.
	diffHeadRe = lazyre.New(`^[-+] (?:Expected|Received|Snapshot)\b|^\+ expected - actual$|^\+ actual - expected$|^- expected \+ actual$`)
	// briefKeepRe: value lines of jest/vitest matchers ("Expected: 2",
	// "Received string: …", "Snapshot: …", "thrown: …") and the matcher
	// itself ("expect(received).toBe(expected) // Object.is equality").
	briefKeepRe = lazyre.New(`^(?:Expected|Received|Snapshot|thrown|Resolved to value|Rejected to value|Number of calls)\b|^expect\(`)
)

// diffChange reports a changed line of a diff ("-   \"a\": 1,", "+hello").
func diffChange(t string) bool {
	return (strings.HasPrefix(t, "-") || strings.HasPrefix(t, "+")) && t != "-" && t != "+" &&
		!strings.HasPrefix(t, "---") && !strings.HasPrefix(t, "+++") && !diffHeadRe.MatchString(t)
}

// compactBlock removes blank lines from d.out[start:] and caps the block.
// failure marks a test failure (not a load error or unhandled error), which
// d.level may shorten (see levels); its title is the head input lines from
// line from on (mocha titles span several lines), rendered first.
func compactBlock(d *doc, start int, failure bool, from, head int) {
	blk := d.out[start:]
	kept := blk[:0]
	for _, ln := range blk {
		if strings.TrimSpace(ln) != "" {
			kept = append(kept, ln)
		}
	}
	if len(kept) > maxBlockLines {
		capped := append([]string(nil), kept[:blockHeadLines]...)
		gap := 0
		flush := func() {
			if gap > 0 {
				capped = append(capped, fmt.Sprintf("%s… %s omitted", strings.Repeat(" ", indentOf(kept[1])), plural(gap, "line", "lines")))
				gap = 0
			}
		}
		for _, ln := range kept[blockHeadLines:] {
			_, lib := libRoot(ln)
			if d.isErr(ln) || isFrame(ln) && !lib || strings.Contains(ln, "library frames (") {
				flush()
				capped = append(capped, ln)
				continue
			}
			gap++
		}
		flush()
		kept = capped
	}
	head = min(max(head, 1), len(kept))
	compact := false
	if failure {
		d.failures++
		k := d.failures
		switch {
		case d.level == 0 || k <= fullFailures:
		case d.level >= 3 && k > briefFailures+namedFailures:
			// Counted by flushOmitted's marker. The title is a name, not a
			// status; error lines of the body not shown elsewhere are
			// re-added by the safety net.
			d.omitted++
			kept = kept[:0]
			compact = true
			for t := from; t < min(from+head, len(d.in)); t++ {
				d.drop(t)
			}
		case len(kept) <= head:
		case d.level >= 2 && k > briefFailures:
			kept = nameOnly(d, kept, head)
			compact = true
		default:
			kept = brief(d, kept, head)
		}
		switch {
		case d.level >= 1 && k == fullFailures+1:
			kept = append([]string{briefNote}, kept...)
		case d.level >= 2 && k == briefFailures+1:
			kept = append([]string{namesNote}, kept...)
		}
		if d.level >= 2 {
			for _, ln := range kept {
				if d.isErr(ln) {
					d.errSeen[squash(ln)] = true
				}
			}
		}
	}
	if compact && within(d.compactEnd, start) && allBlank(d.out[d.compactEnd:start]) {
		start = d.compactEnd // no blank line between names-only failures
	}
	d.out = append(d.out[:start], kept...)
	d.compactEnd = -1
	if compact {
		d.compactEnd = len(d.out)
	}
}

// within reports 0 <= a <= b.
func within(a, b int) bool { return a >= 0 && a <= b }

func allBlank(lines []string) bool {
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			return false
		}
	}
	return true
}

// brief keeps a failure's title, error lines, matcher and value lines, up to
// maxBriefDiff changed diff lines, and its application frames.
func brief(d *doc, kept []string, head int) []string {
	short := append([]string(nil), kept[:head]...)
	diff := 0
	for _, ln := range kept[head:] {
		t := strings.TrimSpace(ln)
		_, lib := libRoot(ln)
		switch {
		case d.isErr(ln), isFrame(ln) && !lib, briefKeepRe.MatchString(t):
			short = append(short, ln)
		case diffChange(t) && !isFrame(ln):
			diff++
			if diff <= maxBriefDiff {
				short = append(short, ln)
			}
		}
	}
	if diff > maxBriefDiff {
		short = append(short, fmt.Sprintf("%s… +%s", strings.Repeat(" ", indentOf(kept[min(1, len(kept)-1)])), plural(diff-maxBriefDiff, "more changed line", "more changed lines")))
	}
	return short
}

// nameOnly keeps a failure's title and its first error line, when that
// text was not shown by an earlier failure.
func nameOnly(d *doc, kept []string, head int) []string {
	short := append([]string(nil), kept[:head]...)
	for _, ln := range kept[head:] {
		if d.isErr(ln) && !isFrame(ln) {
			if !d.errSeen[squash(ln)] {
				short = append(short, ln)
			}
			break
		}
	}
	return short
}

// flushOmitted reports the failures left out (level 3) since the last call.
func flushOmitted(d *doc, indent string) {
	if d.omitted == 0 {
		return
	}
	d.emit(fmt.Sprintf("%s[+%s not shown; the summary below counts them, the full output has them]", indent, plural(d.omitted, "more failing test", "more failing tests")))
	d.omitted = 0
}

// noFailure makes a non-zero exit visible when the view shows no failure
// (coverage thresholds, open handles, a crashed worker): the verdict must
// never read as a pass. Console output of passing tests does not count as a
// failure even when it holds error lines ("console.error(…)" in a passing
// test is not why the run failed).
func noFailure(d *doc, runner string, exit int) {
	for i, ln := range d.out {
		if !d.chatter[i] && d.isErr(ln) {
			return
		}
	}
	d.sep()
	d.emit(fmt.Sprintf("[lx: %s exited %d but reported no failing test; see the lines above or the full output]", runner, exit))
}
