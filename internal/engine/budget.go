package engine

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/tokens"
)

// Budget trims out to about maxTokens. What survives, in priority order:
//
//  1. the last lines (runners and compilers print verdicts last);
//  2. when errorsFirst: every error-class line plus the line after it;
//  3. the head, up to a share of the budget that depends on the output —
//     most of it for data (newest-first logs, diffs, listings), less for
//     failures, whose story is at the end;
//  4. the tail, filling what is left; then warnings; then more head.
//
// Every gap becomes a counted "… N lines omitted …" marker, so nothing
// disappears without a trace, and the budget is actually used rather than
// cut to a fixed number of lines.
func Budget(out string, maxTokens int, failed, errorsFirst bool) string {
	if tokens.Count(out) <= maxTokens {
		return out
	}
	// Gap markers cost tokens too, and scattered errors make many gaps:
	// re-fit with a tighter allowance until the result is within budget.
	reserve := 80
	var res string
	for attempt := 0; attempt < 4; attempt++ {
		res = budgetOnce(out, maxTokens, maxTokens-reserve, failed, errorsFirst)
		over := tokens.Count(res) - maxTokens
		if over <= 0 {
			break
		}
		reserve += over + over/4 + 20
	}
	return res
}

func budgetOnce(out string, maxTokens, avail int, failed, errorsFirst bool) string {
	lines := strings.Split(out, "\n")
	n := len(lines)
	for i, ln := range lines {
		// A single line can be enormous (minified code); cap it first so
		// one line can't eat the budget.
		lines[i] = ShortenLine(ln, 400)
	}
	cost := make([]int, n)
	for i, ln := range lines {
		cost[i] = tokens.Count(ln) + 1
	}
	keep := make([]bool, n)
	used := 0
	take := func(i int) bool {
		if keep[i] {
			return true
		}
		if used+cost[i] > avail {
			return false
		}
		keep[i] = true
		used += cost[i]
		return true
	}
	upTo := func(frac float64) int { return int(float64(avail) * frac) }

	// 1. Verdict tail.
	for i := n - 1; i >= max(0, n-15) && used < upTo(0.25); i-- {
		take(i)
	}
	// 2. Errors with one line of context.
	var level []Level
	if errorsFirst {
		level = make([]Level, n)
		for i, ln := range lines {
			level[i] = Classify(ln)
		}
		for i := 0; i < n && used < upTo(0.6); i++ {
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
	for ; head < n && used < upTo(headShare); head++ {
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
