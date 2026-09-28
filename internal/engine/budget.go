package engine

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/tokens"
)

const MinMaxChars = 64

func Budget(out string, maxTokens int, failed, errorsFirst bool) string {
	return BudgetFit(out, maxTokens, 0, failed, errorsFirst)
}

func BudgetFit(out string, maxTokens, maxChars int, failed, errorsFirst bool) string {
	return BudgetFitLines(out, maxTokens, maxChars, 0, CutEither, failed, errorsFirst)
}

func BudgetFitLines(out string, maxTokens, maxChars, maxLines int, cut Cut, failed, errorsFirst bool) string {
	return budgetFitLines(out, maxTokens, maxChars, maxLines, cut, failed, errorsFirst, errKeep{context: autoErrContext})
}

type errKeep struct {
	context int
	all     bool
}

func budgetFitLines(out string, maxTokens, maxChars, maxLines int, cut Cut, failed, errorsFirst bool, ek errKeep) string {
	s, _ := budgetFocus(out, maxTokens, maxChars, maxLines, cut, failed, errorsFirst, ek, nil)
	return s
}

func budgetFocus(out string, maxTokens, maxChars, maxLines int, cut Cut, failed, errorsFirst bool, ek errKeep, fm *focusMatcher) (string, []focusHit) {
	if maxChars > 0 && maxChars < MinMaxChars {
		maxChars = MinMaxChars
	}
	overChars := func(s string) int {
		if maxChars <= 0 {
			return 0
		}
		return len(s) - maxChars
	}
	if overChars(out) <= 0 && tokens.Count(out) <= maxTokens && (maxLines <= 0 || countLines(out) <= maxLines) {
		return out, nil
	}
	focus := fm.rank(out)

	reserve, charReserve := 80, 0
	var res string
	for attempt := 0; attempt < 4; attempt++ {
		availChars := 0
		if maxChars > 0 {
			availChars = max(maxChars-charReserve, 1)
		}
		var focused string
		var hits []focusHit
		res, focused, hits = budgetOnce(out, maxTokens-reserve, availChars, max(maxLines, 0), cut, failed, errorsFirst, ek, focus)
		if focused != "" && tokens.Count(focused) <= maxTokens && overChars(focused) <= 0 {
			return focused, hits
		}
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
	return res, nil
}

func markerLine(n int) string { return fmt.Sprintf("… %d lines omitted …\n", n) }

const minLineChars = 64

func shortenBytes(line string, maxBytes int) string {
	if len(line) <= maxBytes {
		return line
	}

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

func budgetOnce(out string, avail, availChars, availLines int, cut Cut, failed, errorsFirst bool, ek errKeep, focus []focusLine) (string, string, []focusHit) {
	lines := strings.Split(out, "\n")
	n := len(lines)
	for i, ln := range lines {
		lines[i] = ShortenLine(ln, 400)
	}
	if lim := availChars / 5; lim >= minLineChars {
		for i, ln := range lines {
			lines[i] = shortenBytes(ln, lim)
		}
	}
	cost := make([]int, n)
	for i, ln := range lines {
		cost[i] = tokens.Count(ln) + 1
	}
	var level []Level
	if errorsFirst {
		level = make([]Level, n)
		for i, ln := range lines {
			level[i] = Classify(ln)
		}
	}
	capChars := availChars > 0
	capLines := availLines > 0
	markTokens := tokens.Count(markerLine(n))

	pick := func(focus []focusLine, pin []int) []bool {
		keep := make([]bool, n)
		used := 0
		usedChars, marker := 0, 0
		if capChars {
			marker = len(markerLine(n))
			usedChars = marker
		}
		usedLines := 0
		if capLines {
			usedLines = 1
		}
		take := func(i int) bool {
			if keep[i] {
				return true
			}
			if used+cost[i] > avail {
				return false
			}
			dl := 0
			if capLines {
				prevGap := i > 0 && !keep[i-1]
				nextGap := i+1 < n && !keep[i+1]
				dl = 1
				switch {
				case prevGap && nextGap:
					dl++
				case !prevGap && !nextGap:
					dl--
				}
				if usedLines+dl > availLines {
					return false
				}
			}
			if capChars {
				prev := i > 0 && keep[i-1]
				next := i+1 < n && keep[i+1]
				c := len(lines[i]) + 1
				switch {
				case !prev && !next:
					c += marker
				case prev && next:
					c -= marker
				}
				if usedChars+c > availChars {
					return false
				}
				usedChars += c
			}
			keep[i] = true
			used += cost[i]
			usedLines += dl
			return true
		}

		underTC := func(frac float64) bool {
			if used >= int(float64(avail)*frac) {
				return false
			}
			return !capChars || usedChars < int(float64(availChars)*frac)
		}

		lineBase, lineRoom := 0, availLines
		under := func(frac float64) bool {
			if !underTC(frac) {
				return false
			}
			return !capLines || usedLines-1-lineBase < int(math.Ceil(float64(lineRoom)*frac))
		}

		errShare := 0.6
		if ek.all {
			errShare = 1
		}

		errsFit := capLines && errorsFirst && errorLinesFit(level, availLines)
		if errsFit {
			for i := 0; i < n && underTC(errShare); i++ {
				if level[i] == Err {
					take(i)
				}
			}
			lineBase = usedLines - 1
			lineRoom = availLines - lineBase
		}
		if !capLines {
			cut = CutEither
		}

		for i := n - 1; i >= max(0, n-15) && cut != CutHead && under(0.25); i-- {
			take(i)
		}

		if errorsFirst {
			if capLines || ek.all {
				for i := 0; i < n && !errsFit && underTC(errShare); i++ {
					if level[i] == Err {
						take(i)
					}
				}
				for i := 0; i+1 < n && under(0.6); i++ {
					if level[i] == Err && keep[i] && strings.TrimSpace(lines[i+1]) != "" {
						take(i + 1)
					}
				}
			} else {
				for i := 0; i < n && under(0.6); i++ {
					if level[i] == Err {
						take(i)
						if i+1 < n && strings.TrimSpace(lines[i+1]) != "" {
							take(i + 1)
						}
					}
				}
			}
			for d := 2; d <= ek.context; d++ {
				for i := 0; i+d < n && under(0.6); i++ {
					if level[i] == Err && keep[i] && contextRun(lines, keep, i, d) {
						take(i + d)
					}
				}
			}
		}

		if len(focus) > 0 && allKept(keep, level) {
			takeRun := func(i int) bool {
				split := !keep[i] && i > 0 && !keep[i-1] && i+1 < n && !keep[i+1]
				if split && used+cost[i]+markTokens > avail || !take(i) {
					return false
				}
				if split {
					used += markTokens
				}
				return true
			}
			for _, i := range pin {
				takeRun(i)
			}
			for _, f := range focus {
				if !under(focusShare) {
					break
				}
				if i := f.i; takeRun(i) && i+1 < n && strings.TrimSpace(lines[i+1]) != "" {
					take(i + 1)
				}
			}
		}

		headShare := 0.5
		switch {
		case !errorsFirst:
			headShare = 0.8
		case failed:
			headShare = 0.35
		}
		switch cut {
		case CutHead:
			headShare = 1
		case CutTail:
			headShare = 0
		}
		head := 0
		for ; head < n && under(headShare); head++ {
			if !take(head) {
				break
			}
		}

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
		return keep
	}

	keep := pick(nil, nil)
	base := renderKept(lines, keep)
	if len(focus) == 0 {
		return base, "", nil
	}
	var pin []int
	for i, k := range keep {
		if k && (level != nil && level[i] == Err || level == nil && Classify(lines[i]) == Err) {
			pin = append(pin, i)
		}
	}
	fk := pick(focus, pin)
	if slices.Equal(fk, keep) {
		return base, "", nil
	}
	for _, i := range pin {
		if !fk[i] {
			return base, "", nil
		}
	}
	var hits []focusHit
	for _, f := range focus {
		if fk[f.i] && !keep[f.i] {
			hits = append(hits, newFocusHit(lines[f.i], f.mask))
		}
	}
	return base, renderKept(lines, fk), hits
}

func renderKept(lines []string, keep []bool) string {
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

func contextRun(lines []string, keep []bool, i, d int) bool {
	for j := i + 1; j <= i+d; j++ {
		if strings.TrimSpace(lines[j]) == "" || j < i+d && !keep[j] {
			return false
		}
	}
	return true
}

func allKept(keep []bool, level []Level) bool {
	for i, l := range level {
		if l == Err && !keep[i] {
			return false
		}
	}
	return true
}

func errorNeed(out string, ek errKeep) (int, []string) {
	lines := strings.Split(out, "\n")
	n, need := len(lines), 0
	var errs []string
	for i, ln := range lines {
		ln = ShortenLine(ln, 400)
		isErr := Classify(ln) == Err
		if isErr {
			errs = append(errs, strings.Join(strings.Fields(ln), " "))
		}
		switch {
		case i >= n-15:
			need += tokens.Count(ln) + 1
		case isErr:
			need += tokens.Count(ln) + 1
			if i+1 < n-15 {
				need += tokens.Count(ShortenLine(lines[i+1], 400)) + 1
			}
		}
	}
	share := 0.6
	if ek.all {
		share = 1
	}
	markers := (len(errs) + 2) * (tokens.Count(markerLine(n)) + 1)
	return int(float64(need)/share) + markers*5/4 + 100, errs
}

func lostErrors(errs []string, view, ref string) bool {
	has := func(s string) func(string) bool {
		sq := squash(s)
		set := map[string]bool{}
		for _, ln := range strings.Split(sq, "\n") {
			set[ln] = true
		}
		return func(e string) bool { return set[e] || strings.Contains(sq, e) }
	}
	inView := has(view)
	var inRef func(string) bool
	if ref != "" {
		inRef = has(ref)
	}
	for _, e := range errs {
		if !inView(e) && (inRef == nil || inRef(e)) {
			return true
		}
	}
	return false
}

func errorLinesFit(level []Level, maxLines int) bool {
	need, inGap := 0, false
	for _, l := range level {
		switch {
		case l == Err:
			need++
			inGap = false
		case !inGap:
			need++
			inGap = true
		}
		if need > maxLines {
			return false
		}
	}
	return true
}
