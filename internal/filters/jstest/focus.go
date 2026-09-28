package jstest

import (
	"strings"

	"github.com/iheeb1/lx/internal/filters/focus"
)

// from level 2 on, how much of a failure is shown depends on its position
func focusFor(d *doc) *focus.Set {
	if d.level >= 2 {
		return nil
	}
	return focus.New(d.c.Focus)
}

const (
	slotStay = iota
	slotMove
	slotCut
)

func focusSlots(n int, class func(int) int, score func(int) float64) []int {
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	changed := false
	var slots []int
	flush := func() {
		if order := focus.Rank(len(slots), func(k int) float64 { return score(slots[k]) }); order != nil {
			for k, o := range order {
				perm[slots[k]] = slots[o]
			}
			changed = true
		}
		slots = slots[:0]
	}
	for i := 0; i < n; i++ {
		switch class(i) {
		case slotMove:
			slots = append(slots, i)
		case slotCut:
			flush()
		}
	}
	flush()
	if !changed {
		return nil
	}
	return perm
}

func reorder[T any](s []T, perm []int) []T {
	out := make([]T, len(s))
	for i, p := range perm {
		out[i] = s[p]
	}
	return out
}

func focusSpans(fx *focus.Set, in []string, spans []span) []span {
	if fx == nil || len(spans) < 2 {
		return spans
	}
	order := focus.Rank(len(spans), func(k int) float64 {
		b := spans[k]
		return fx.Score(in[b.from], strings.Join(in[b.from+1:b.to], "\n"))
	})
	if order == nil {
		return spans
	}
	return reorder(spans, order)
}

func suiteScore(fx *focus.Set, in []string, s *jestSuite) float64 {
	name := []string{s.file}
	var body []string
	for _, b := range s.blocks {
		name = append(name, in[b.from])
		body = append(body, in[b.from+1:b.to]...)
	}
	return fx.Score(strings.Join(name, "\n"), strings.Join(body, "\n"))
}

func vitestFailEnd(in []string, i, summaryAt int) int {
	j := i + 1
	for j < len(in) && !vSepRe.MatchString(in[j]) && !vFailRe.MatchString(in[j]) && j != summaryAt {
		j++
	}
	return j
}

// stacked FAIL lines share one error and move together
func vitestFailures(fx *focus.Set, in []string, i, summaryAt int) ([]span, int) {
	if fx == nil {
		return nil, i
	}
	var units []span
	for {
		j := vitestFailEnd(in, i, summaryAt)
		for j < len(in) && vFailRe.MatchString(in[j]) {
			j = vitestFailEnd(in, j, summaryAt)
		}
		units = append(units, span{i, j})
		if j >= len(in) || j == summaryAt || !vSepRe.MatchString(in[j]) || vSectionRe.MatchString(in[j]) {
			break
		}
		k := j + 1
		for k < len(in) && strings.TrimSpace(in[k]) == "" && k != summaryAt {
			k++
		}
		if k >= len(in) || k == summaryAt || !vFailRe.MatchString(in[k]) {
			break
		}
		i = k
	}
	end := units[len(units)-1].to
	order := focus.Rank(len(units), func(k int) float64 {
		u := units[k]
		var name, body []string
		for p := u.from; p < u.to; p++ {
			if vFailRe.MatchString(in[p]) {
				name = append(name, in[p])
			} else {
				body = append(body, in[p])
			}
		}
		return fx.Score(strings.Join(name, "\n"), strings.Join(body, "\n"))
	})
	if order == nil {
		return units, end
	}
	return reorder(units, order), end
}

func mochaFailures(fx *focus.Set, in []string, i int) ([]span, int) {
	spans := []span{{i, mochaFailureEnd(in, i)}}
	if fx == nil {
		return spans, spans[0].to
	}
	for j := spans[0].to; j < len(in) && mochaDetailRe.MatchString(in[j]); j = spans[len(spans)-1].to {
		spans = append(spans, span{j, mochaFailureEnd(in, j)})
	}
	end := spans[len(spans)-1].to
	if len(spans) < 2 {
		return spans, end
	}
	order := focus.Rank(len(spans), func(k int) float64 {
		s := spans[k]
		h := s.from + 1
		for h < s.to && h < s.from+12 && !strings.HasSuffix(strings.TrimSpace(in[h-1]), ":") {
			h++
		}
		return fx.Score(strings.Join(in[s.from:h], "\n"), strings.Join(in[h:s.to], "\n"))
	})
	if order == nil {
		return spans, end
	}
	return reorder(spans, order), end
}
