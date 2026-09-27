package engine

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/tokens"
)

// MinMaxChars is the smallest character cap BudgetFit honours: below it
// not even one "… N lines omitted …" marker would fit, so smaller caps are
// raised to it.
const MinMaxChars = 64

// Budget trims out to about maxTokens. It is BudgetFit with no character
// cap.
func Budget(out string, maxTokens int, failed, errorsFirst bool) string {
	return BudgetFit(out, maxTokens, 0, failed, errorsFirst)
}

// BudgetFit trims out to about maxTokens and, when maxChars > 0, to at most
// maxChars bytes (a hard bound, gap markers included). What survives, in
// priority order:
//
//  1. the last lines (runners and compilers print verdicts last);
//  2. when errorsFirst: every error-class line plus the line after it;
//  3. the head, up to a share of the budget that depends on the output —
//     most of it for data (newest-first logs, diffs, listings), less for
//     failures, whose story is at the end;
//  4. the tail, filling what is left; then warnings; then more head.
//
// Each share applies to both allowances, so a character cap squeezes every
// stage in proportion instead of letting one stage take it all.
//
// Every gap becomes a counted "… N lines omitted …" marker, so nothing
// disappears without a trace, and the budget is actually used rather than
// cut to a fixed number of lines.
//
// The character cap counts bytes (len), which is never less than the
// characters (or UTF-16 units) a host counts, so it errs on the safe side.
func BudgetFit(out string, maxTokens, maxChars int, failed, errorsFirst bool) string {
	if maxChars > 0 && maxChars < MinMaxChars {
		maxChars = MinMaxChars
	}
	overChars := func(s string) int {
		if maxChars <= 0 {
			return 0
		}
		return len(s) - maxChars
	}
	if overChars(out) <= 0 && tokens.Count(out) <= maxTokens {
		return out
	}
	// Gap markers cost tokens too, and scattered errors make many gaps:
	// re-fit with a tighter allowance until the result is within budget.
	// Characters are accounted exactly in budgetOnce (markers included), so
	// the character reserve only grows if that accounting is ever wrong.
	reserve, charReserve := 80, 0
	var res string
	for attempt := 0; attempt < 4; attempt++ {
		availChars := 0
		if maxChars > 0 {
			availChars = max(maxChars-charReserve, 1)
		}
		res = budgetOnce(out, maxTokens-reserve, availChars, failed, errorsFirst)
		over := tokens.Count(res) - maxTokens
		overC := overChars(res)
		if over <= 0 && overC <= 0 {
			break
		}
		if over > 0 {
			reserve += over + over/4 + 20
		}
		if overC > 0 {
			charReserve += overC + overC/4 + 64
		}
	}
	return res
}

// markerLine is the gap marker budgetOnce writes, newline included.
func markerLine(n int) string { return fmt.Sprintf("… %d lines omitted …\n", n) }

// minLineChars is the smallest per-line byte limit shortenBytes is used
// with: room for the " …[+N chars]… " marker and some text around it.
const minLineChars = 64

// shortenBytes cuts line to at most maxBytes bytes (maxBytes >=
// minLineChars), keeping its start and end at rune boundaries around a
// " …[+N chars]… " marker, the shape ShortenLine gives by characters.
func shortenBytes(line string, maxBytes int) string {
	if len(line) <= maxBytes {
		return line
	}
	// The marker is 17 bytes plus the digits of N (under 11).
	b := maxBytes - 28
	head := b * 3 / 4
	for head > 0 && !utf8.RuneStart(line[head]) {
		head--
	}
	tail := len(line) - b/5
	for tail < len(line) && !utf8.RuneStart(line[tail]) {
		tail++
	}
	return fmt.Sprintf("%s …[+%d chars]… %s", line[:head], utf8.RuneCountInString(line[head:tail]), line[tail:])
}

// budgetOnce keeps lines within avail tokens and, when availChars > 0,
// within availChars bytes of output. The byte bound is exact: every kept
// line costs len+1, and markers are paid for by run: the result has at most
// one more gap than it has runs of kept lines, so one marker is reserved up
// front and each take that starts a new run pays for one more (a take that
// joins two runs gets one back).
func budgetOnce(out string, avail, availChars int, failed, errorsFirst bool) string {
	lines := strings.Split(out, "\n")
	n := len(lines)
	for i, ln := range lines {
		// A single line can be enormous (minified code); cap it first so
		// one line can't eat the budget.
		lines[i] = ShortenLine(ln, 400)
	}
	if lim := availChars / 5; lim >= minLineChars {
		// Under a character cap no line may take more than a fifth of it
		// either: ShortenLine keeps error lines up to 1,200 characters
		// (4,800 bytes), which a small cap could only drop whole. Claude
		// Code's default cap (26,800) never shortens here.
		for i, ln := range lines {
			lines[i] = shortenBytes(ln, lim)
		}
	}
	cost := make([]int, n)
	for i, ln := range lines {
		cost[i] = tokens.Count(ln) + 1
	}
	keep := make([]bool, n)
	used := 0
	capChars := availChars > 0
	usedChars, marker := 0, 0
	if capChars {
		// No gap is longer than n lines, so this marker is the longest.
		marker = len(markerLine(n))
		usedChars = marker
	}
	take := func(i int) bool {
		if keep[i] {
			return true
		}
		if used+cost[i] > avail {
			return false
		}
		if capChars {
			prev := i > 0 && keep[i-1]
			next := i+1 < n && keep[i+1]
			c := len(lines[i]) + 1
			switch {
			case !prev && !next:
				c += marker // a new run, so possibly a new gap
			case prev && next:
				c -= marker // two runs become one
			}
			if usedChars+c > availChars {
				return false
			}
			usedChars += c
		}
		keep[i] = true
		used += cost[i]
		return true
	}
	// under reports whether both allowances are below frac of their size.
	under := func(frac float64) bool {
		if used >= int(float64(avail)*frac) {
			return false
		}
		return !capChars || usedChars < int(float64(availChars)*frac)
	}

	// 1. Verdict tail.
	for i := n - 1; i >= max(0, n-15) && under(0.25); i-- {
		take(i)
	}
	// 2. Errors with one line of context.
	var level []Level
	if errorsFirst {
		level = make([]Level, n)
		for i, ln := range lines {
			level[i] = Classify(ln)
		}
		for i := 0; i < n && under(0.6); i++ {
			if level[i] == Err {
				take(i)
				if i+1 < n && strings.TrimSpace(lines[i+1]) != "" {
					take(i + 1)
				}
			}
		}
	}
	// 3. Head up to its share.
	headShare := 0.5
	switch {
	case !errorsFirst:
		headShare = 0.8
	case failed:
		headShare = 0.35
	}
	head := 0
	for ; head < n && under(headShare); head++ {
		if !take(head) {
			break
		}
	}
	// 4. Tail, then warnings, then the rest of the head.
	for i := n - 1; i >= head; i-- {
		if !take(i) {
			break
		}
	}
	if errorsFirst {
		for i := 0; i < n; i++ {
			if level[i] == Warn && !take(i) {
				break
			}
		}
	}
	for ; head < n; head++ {
		if !take(head) {
			break
		}
	}

	var b strings.Builder
	gap := 0
	flush := func() {
		if gap > 0 {
			fmt.Fprintf(&b, "… %d lines omitted …\n", gap)
			gap = 0
		}
	}
	for i, ln := range lines {
		if !keep[i] {
			gap++
			continue
		}
		flush()
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	flush()
	return strings.TrimRight(b.String(), "\n")
}
