package python

import (
	"strings"

	"github.com/iheeb1/lx/internal/filters/focus"
)

func (r *ptReducer) focusBlocks(idx []int, blocks [][2]int) [][2]int {
	fx := focus.New(r.c.Focus)
	if fx == nil || len(blocks) < 2 {
		return blocks
	}
	order := focus.Rank(len(blocks), func(k int) float64 {
		b := blocks[k]
		name := r.lines[idx[b[0]]]
		if m := ptBlockRe.FindStringSubmatch(name); m != nil {
			name = m[1]
		}
		body := make([]string, 0, b[1]-b[0]-1)
		for _, i := range idx[b[0]+1 : b[1]] {
			body = append(body, r.lines[i])
		}
		return fx.Score(focus.NameForms(name), strings.Join(body, "\n"))
	})
	if order == nil {
		return blocks
	}
	out := make([][2]int, len(blocks))
	for k, o := range order {
		out[k] = blocks[o]
	}
	return out
}

func (r *ptReducer) focusSummary(idx []int) []int {
	fx := focus.New(r.c.Focus)
	if fx == nil {
		return idx
	}
	var slots []int
	for k, i := range idx {
		if ln := r.lines[i]; strings.HasPrefix(ln, "FAILED ") || strings.HasPrefix(ln, "ERROR ") {
			slots = append(slots, k)
		}
	}
	order := focus.Rank(len(slots), func(k int) float64 {
		id, msg, _ := strings.Cut(r.lines[idx[slots[k]]], " - ")
		return fx.Score(focus.NameForms(id), msg)
	})
	if order == nil {
		return idx
	}
	out := append([]int(nil), idx...)
	for k, o := range order {
		out[slots[k]] = idx[slots[o]]
	}
	return out
}
