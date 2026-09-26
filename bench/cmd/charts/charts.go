package main

import (
	"fmt"
	"math"
)

// Series is one colored identity in a chart.
type Series struct {
	Name  string
	Class string // s1 | s2 | s3
}

// BarRow is one category row: a value per series.
type BarRow struct {
	Label  string
	Values []float64
	Notes  []string // optional per-value tip text
}

// HBars renders grouped horizontal bars (one bar per series per row) on a
// linear axis from 0. unit formats values ("%" or "ms" or "").
func HBars(title, subtitle string, series []Series, rows []BarRow, maxV float64, unit string) string {
	const (
		w       = 760.0
		left    = 190.0
		right   = 64.0
		barH    = 16.0
		gap     = 2.0
		rowPad  = 14.0
		topBase = 64.0
	)
	n := float64(len(series))
	groupH := n*barH + (n-1)*gap
	top := topBase
	if len(series) > 1 {
		top += 22
	}
	h := top + float64(len(rows))*(groupH+rowPad) + 40
	c := newCanvas(w, h, title, subtitle)
	c.text(24, 30, "t", "start", title)
	c.text(24, 50, "st", "start", subtitle)
	if len(series) > 1 {
		var items [][2]string
		for _, s := range series {
			items = append(items, [2]string{s.Class, s.Name})
		}
		c.legend(24, 76, items)
	}
	if maxV <= 0 {
		for _, r := range rows {
			for _, v := range r.Values {
				maxV = math.Max(maxV, v)
			}
		}
	}
	ticks := niceTicks(maxV, 5)
	axisMax := ticks[len(ticks)-1]
	plotW := w - left - right
	x := func(v float64) float64 { return left + plotW*v/axisMax }
	bottom := h - 34
	for _, t := range ticks {
		c.line(x(t), top-6, x(t), bottom, "g")
		c.text(x(t), bottom+16, "m", "middle", fmtVal(t, unit))
	}
	c.line(left, top-6, left, bottom, "a")
	y := top
	for _, r := range rows {
		c.text(left-10, y+groupH/2+4, "l", "end", r.Label)
		for i, v := range r.Values {
			by := y + float64(i)*(barH+gap)
			tip := fmt.Sprintf("%s — %s: %s", r.Label, series[i].Name, fmtVal(v, unit))
			if i < len(r.Notes) && r.Notes[i] != "" {
				tip += " (" + r.Notes[i] + ")"
			}
			c.hbar(left, by, x(v)-left, barH, series[i].Class, tip)
			c.text(x(v)+6, by+barH/2+4, "v", "start", fmtVal(v, unit))
		}
		y += groupH + rowPad
	}
	return c.String()
}

// DumbRow is one row of a dumbbell chart: values per series on a log axis.
type DumbRow struct {
	Label  string
	Values []float64 // same order as series; <=0 means missing
}

// Dumbbell renders, per row, a connector from the largest to the smallest
// value with a dot per series, on a log10 x axis — the right form when
// values span orders of magnitude and the story is "from here to there".
func Dumbbell(title, subtitle string, series []Series, rows []DumbRow, unit string) string {
	const (
		w     = 760.0
		left  = 230.0
		right = 40.0
		rowH  = 26.0
	)
	top := 96.0
	h := top + float64(len(rows))*rowH + 44
	c := newCanvas(w, h, title, subtitle)
	c.text(24, 30, "t", "start", title)
	c.text(24, 50, "st", "start", subtitle)
	var items [][2]string
	for _, s := range series {
		items = append(items, [2]string{s.Class, s.Name})
	}
	c.legend(24, 76, items)
	lo, hi := math.Inf(1), 0.0
	for _, r := range rows {
		for _, v := range r.Values {
			if v > 0 {
				lo, hi = math.Min(lo, v), math.Max(hi, v)
			}
		}
	}
	d0 := math.Floor(math.Log10(math.Max(lo, 1)))
	d1 := math.Ceil(math.Log10(math.Max(hi, 10)))
	plotW := w - left - right
	x := func(v float64) float64 { return left + plotW*(math.Log10(v)-d0)/(d1-d0) }
	bottom := h - 34
	for d := d0; d <= d1; d++ {
		v := math.Pow(10, d)
		c.line(x(v), top-8, x(v), bottom, "g")
		c.text(x(v), bottom+16, "m", "middle", compact(v)+unit)
	}
	y := top + rowH/2
	for _, r := range rows {
		c.text(left-12, y+4, "l", "end", r.Label)
		mn, mx := math.Inf(1), 0.0
		for _, v := range r.Values {
			if v > 0 {
				mn, mx = math.Min(mn, v), math.Max(mx, v)
			}
		}
		if mx > mn {
			c.line(x(mn), y, x(mx), y, "conn")
		}
		for i, v := range r.Values {
			if v > 0 {
				c.dot(x(v), y, series[i].Class, fmt.Sprintf("%s — %s: %s%s", r.Label, series[i].Name, compact(v), unit))
			}
		}
		// Label the ratio at the right end: the story of the row.
		if len(r.Values) >= 2 && r.Values[0] > 0 && r.Values[len(r.Values)-1] > 0 {
			ratio := r.Values[0] / r.Values[len(r.Values)-1]
			c.text(x(mx)+10, y+4, "m", "start", fmt.Sprintf("%.0f×", ratio))
		}
		y += rowH
	}
	return c.String()
}

func fmtVal(v float64, unit string) string {
	switch unit {
	case "%":
		return fmt.Sprintf("%.0f%%", v)
	case "ms":
		if v < 10 {
			return fmt.Sprintf("%.1f ms", v)
		}
		return fmt.Sprintf("%.0f ms", v)
	default:
		return compact(v)
	}
}
