package golang

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/filters/focus"
)

func focusSegments(c *engine.Context, segs []*segment) []*segment {
	fx := focus.New(c.Focus)
	if fx == nil {
		return segs
	}
	var slots []int
	var scores []float64
	for i, s := range segs {
		var best float64
		s.view, best = orderTests(fx, s.viewItems())
		if len(s.crash) > 0 {
			best = max(best, fx.Score("", strings.Join(s.crash, "\n")))
		}
		if s.failed() {
			slots = append(slots, i)
			scores = append(scores, best)
		}
	}
	order := focus.Rank(len(slots), func(k int) float64 { return scores[k] })
	if order == nil {
		return segs
	}
	out := append([]*segment(nil), segs...)
	for k, o := range order {
		out[slots[k]] = segs[slots[o]]
	}
	return out
}

func orderTests(fx *focus.Set, items []item) ([]item, float64) {
	var blocks [][2]int
	movable := true
	for i := 0; i < len(items); i++ {
		if it := items[i]; it.kind != itResult || it.t.status != 'F' || it.t.depth() != 0 {
			continue
		}
		j := i + 1
		for j < len(items) && subResult(items[j]) {
			j++
		}
		// a subtest result after a stray line would be left under the wrong parent
		for k := j; k < len(items) && (items[k].kind != itResult || items[k].t.depth() > 0); k++ {
			if subResult(items[k]) {
				movable = false
			}
		}
		blocks = append(blocks, [2]int{i, j})
		i = j - 1
	}
	best := 0.0
	order := focus.Rank(len(blocks), func(k int) float64 {
		s := blockScore(fx, items[blocks[k][0]:blocks[k][1]])
		best = max(best, s)
		return s
	})
	if order == nil || !movable {
		return items, best
	}
	out := make([]item, 0, len(items))
	prev := 0
	for k, b := range blocks {
		nb := blocks[order[k]]
		out = append(out, items[prev:b[0]]...)
		out = append(out, items[nb[0]:nb[1]]...)
		prev = b[1]
	}
	return append(out, items[prev:]...), best
}

func subResult(it item) bool { return it.kind == itResult && it.t.depth() > 0 }

func blockScore(fx *focus.Set, block []item) float64 {
	var b strings.Builder
	for _, it := range block {
		b.WriteString(focus.NameForms(it.t.name))
		b.WriteByte('\n')
		for _, o := range it.t.out {
			b.WriteString(o.text)
			b.WriteByte('\n')
		}
	}
	return fx.Score(focus.NameForms(block[0].t.name), b.String())
}
