package search

import (
	"strings"

	"github.com/iheeb1/lx/internal/filters/focus"
)

func focusGroups(fx *focus.Set, groups []*fileGroup) ([]*fileGroup, bool) {
	if fx == nil || len(groups) < 2 {
		return groups, false
	}
	order := focus.Rank(len(groups), func(k int) float64 {
		g := groups[k]
		var b strings.Builder
		for _, en := range g.entries {
			if en.match {
				b.WriteString(en.text)
				b.WriteByte('\n')
			}
		}
		return fx.Score(g.path, b.String())
	})
	if order == nil {
		return groups, false
	}
	out := make([]*fileGroup, len(groups))
	for k, o := range order {
		out[k] = groups[o]
	}
	return out, true
}

// focused runs first, then the earliest others; printed in file order
func focusPick(fx *focus.Set, entries []entry, limit, matches int) ([]entry, int) {
	if fx == nil || limit <= 0 || matches <= limit {
		return nil, 0
	}
	context := false
	for _, en := range entries {
		context = context || en.sep || !en.match
	}
	var runs [][2]int
	for i := 0; i < len(entries); {
		if entries[i].sep {
			i++
			continue
		}
		j := i + 1
		for context && j < len(entries) && !entries[j].sep {
			j++
		}
		runs = append(runs, [2]int{i, j})
		i = j
	}
	order := focus.Rank(len(runs), func(k int) float64 {
		best := 0.0
		for _, en := range entries[runs[k][0]:runs[k][1]] {
			if en.match {
				best = max(best, fx.Score(en.text, ""))
			}
		}
		return best
	})
	if order == nil {
		return nil, 0
	}
	take := make([]int, len(runs))
	left := limit
	for _, k := range order {
		if left == 0 {
			break
		}
		r := runs[k]
		end := r[0]
		for end < r[1] && left > 0 {
			if entries[end].match {
				left--
			}
			end++
		}
		for end < r[1] && !entries[end].match {
			end++
		}
		take[k] = end
	}
	prefix, ended := true, false
	for k, r := range runs {
		if take[k] > 0 && ended {
			prefix = false
		}
		ended = ended || take[k] < r[1]
	}
	if prefix {
		return nil, 0
	}
	var out []entry
	for k, r := range runs {
		if take[k] == 0 {
			continue
		}
		if context && len(out) > 0 {
			out = append(out, entry{sep: true})
		}
		out = append(out, entries[r[0]:take[k]]...)
	}
	return out, limit
}
