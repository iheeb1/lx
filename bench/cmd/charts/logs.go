package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

type logbench struct {
	Tokens   string `json:"tokens"`
	Variants []struct {
		Key   string `json:"key"`
		Label string `json:"label"`
	} `json:"variants"`
	Cases []struct {
		ID        string `json:"id"`
		Source    string `json:"source"`
		Templates int    `json:"templates"`
		Variants  []struct {
			Key       string `json:"key"`
			Tokens    int    `json:"tokens"`
			Templates int    `json:"templates_covered"`
		} `json:"variants"`
	} `json:"cases"`
}

type logPoint struct {
	label, class string
	hollow       bool
	cover, left  float64
	tokens       float64
	raw, tmpl    float64
	cases        [][2]float64
	names        []string
}

func logFamily(key string) (class string, hollow bool) {
	switch {
	case key == "raw":
		return "s3", false
	case strings.HasPrefix(key, "rtk"):
		return "s2", key != "rtk"
	}
	return "s1", strings.Contains(key, "laya")
}

func logsH2H(lb logbench) string {
	byKey := map[string]*logPoint{}
	var order []string
	for _, v := range lb.Variants {
		class, hollow := logFamily(v.Key)
		byKey[v.Key] = &logPoint{label: v.Label, class: class, hollow: hollow}
		order = append(order, v.Key)
	}
	var tmpl float64
	n := 0
	for _, c := range lb.Cases {
		if c.Source != "loghub" || c.Templates == 0 {
			continue
		}
		var r float64
		for _, v := range c.Variants {
			if v.Key == "raw" {
				r = float64(v.Tokens)
			}
		}
		if r == 0 {
			continue
		}
		n++
		tmpl += float64(c.Templates)
		for _, v := range c.Variants {
			if p := byKey[v.Key]; p != nil {
				p.cover += float64(v.Templates)
				p.tokens += float64(v.Tokens)
				p.raw += r
				p.tmpl += float64(c.Templates)
				p.cases = append(p.cases, [2]float64{100 * float64(v.Templates) / float64(c.Templates), 100 * float64(v.Tokens) / r})
				p.names = append(p.names, c.ID)
			}
		}
	}
	var pts []*logPoint
	for _, k := range order {
		if p := byKey[k]; len(p.cases) > 0 {
			p.cover, p.left = 100*p.cover/p.tmpl, 100*p.tokens/p.raw
			pts = append(pts, p)
		}
	}
	counted := "o200k"
	if !strings.HasPrefix(lb.Tokens, "o200k") {
		counted = "estimated"
	}
	return logScatter("Logs: tokens saved against templates kept",
		fmt.Sprintf("%d loghub samples of 2,000 lines, read with docker logs app · %s tokens · big dots: all %d pooled (%s templates), small: each log",
			n, counted, n, compact(tmpl)), pts)
}

const (
	hollow = "fill:var(--surface);stroke-width:2"
	halo   = "paint-order:stroke;stroke:var(--surface);stroke-width:4px;stroke-linejoin:round"
)

// y is what a view leaves of the raw tokens, on a log scale, labelled as saved.
func logScatter(title, subtitle string, pts []*logPoint) string {
	const (
		w      = 760.0
		h      = 500.0
		left   = 72.0
		right  = 28.0
		top    = 104.0
		bottom = 52.0
	)
	c := newCanvas(w, h, title, subtitle)
	c.text(24, 30, "t", "start", title)
	c.text(24, 50, "st", "start", subtitle)
	items := [][2]string{{"s3", "raw output"}, {"s2", "rtk 0.50"}, {"s1", "lx"}}
	c.legend(24, 78, items)
	rx := 24.0
	for _, it := range items {
		rx += 18 + textWidth(it[1]) + 22
	}
	fmt.Fprintf(&c.b, `<circle cx="%.1f" cy="72" r="5" class="k1" style="%s"/>`, rx+6, hollow)
	c.text(rx+18, 78, "l", "start", "ring: with laya, or rtk log <file>")

	lo := 0.0
	for _, p := range pts {
		for _, cs := range p.cases {
			if cs[1] > 0 {
				lo = math.Min(lo, math.Floor(math.Log10(cs[1])))
			}
		}
	}
	lo = math.Max(lo, -3)
	pw, ph := w-left-right, h-top-bottom
	x := func(v float64) float64 { return left + pw*v/100 }
	y := func(v float64) float64 {
		return top + ph*(math.Log10(math.Max(v, math.Pow(10, lo)))-lo)/(2-lo)
	}
	for v := 0.0; v <= 100; v += 20 {
		c.line(x(v), top, x(v), top+ph, "g")
		c.text(x(v), top+ph+16, "m", "middle", fmtVal(v, "%"))
	}
	for d := lo; d <= 2; d++ {
		v := math.Pow(10, d)
		c.line(left, y(v), left+pw, y(v), "g")
		c.text(left-8, y(v)+4, "m", "end", strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.3f", 100-v), "0"), ".")+"%")
	}
	c.line(left, top, left, top+ph, "a")
	c.line(left, top+ph, left+pw, top+ph, "a")
	c.text(left+pw/2, h-12, "l", "middle", "ground-truth templates with a line or a count in the view →")
	fmt.Fprintf(&c.b, `<text x="18" y="%.1f" class="l" text-anchor="middle" transform="rotate(-90 18 %.1f)">tokens saved (log scale) →</text>`, top+ph/2, top+ph/2)

	for _, p := range pts {
		for i, cs := range p.cases {
			fmt.Fprintf(&c.b, `<circle cx="%.1f" cy="%.1f" r="2.5" class="%s" opacity="0.35"><title>%s — %s: %.0f%% of templates, %.2f%% of the tokens left</title></circle>`,
				x(cs[0]), y(cs[1]), p.class, esc(p.names[i]), esc(p.label), cs[0], cs[1])
		}
	}

	type group struct {
		x, y   float64
		labels []string
		pts    []*logPoint
	}
	var groups []*group
	for _, p := range pts {
		px, py := x(p.cover), y(p.left)
		var g *group
		for _, o := range groups {
			if math.Abs(o.x-px) < 3 && math.Abs(o.y-py) < 3 {
				g = o
			}
		}
		if g == nil {
			g = &group{x: px, y: py}
			groups = append(groups, g)
		}
		g.pts = append(g.pts, p)
		g.labels = append(g.labels, p.label)
	}
	for _, g := range groups {
		for i, p := range g.pts {
			tip := fmt.Sprintf("%s: %.1f%% of templates, %.1f%% of tokens saved (%s tokens)", p.label, p.cover, 100-p.left, compact(p.tokens))
			r := max(6-2*float64(i), 3)
			if p.hollow {
				fmt.Fprintf(&c.b, `<circle cx="%.1f" cy="%.1f" r="%.1f" class="k%s" style="%s"><title>%s</title></circle>`, g.x, g.y, r, p.class[1:], hollow, esc(tip))
			} else {
				fmt.Fprintf(&c.b, `<circle cx="%.1f" cy="%.1f" r="%.1f" class="%s ring"><title>%s</title></circle>`, g.x, g.y, r, p.class, esc(tip))
			}
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].y < groups[j].y })
	var placed [][4]float64
	for _, g := range groups {
		placed = append(placed, [4]float64{g.x - 9, g.y - 9, g.x + 9, g.y + 9})
	}
	hits := func(b [4]float64) bool {
		for _, o := range placed {
			if b[0] < o[2] && b[2] > o[0] && b[1] < o[3] && b[3] > o[1] {
				return true
			}
		}
		return false
	}
	// points too close to label apart share one block
	var clusters [][]*group
	for _, g := range groups {
		if n := len(clusters); n > 0 {
			last := clusters[n-1][len(clusters[n-1])-1]
			if math.Abs(last.x-g.x) < 14 && math.Abs(last.y-g.y) < 14 {
				clusters[n-1] = append(clusters[n-1], g)
				continue
			}
		}
		clusters = append(clusters, []*group{g})
	}
	type line struct{ class, text string }
	for _, cl := range clusters {
		var lines []line
		tw := 0.0
		for _, g := range cl {
			p := g.pts[0]
			t := strings.Join(g.labels, " · ")
			n := fmt.Sprintf("%s tokens, %.1f%% saved, %.0f%% of templates", compact(p.tokens), 100-p.left, p.cover)
			lines = append(lines, line{"v", t}, line{"m", n})
			tw = math.Max(tw, math.Max(textWidth(t), textWidth(n)))
		}
		x0, x1, gy := cl[0].x, cl[0].x, 0.0
		for _, g := range cl {
			x0, x1, gy = math.Min(x0, g.x), math.Max(x1, g.x), gy+g.y/float64(len(cl))
		}
		bh := float64(len(lines))*14 + 2
		box := func(lx, ly float64, anchor string) [4]float64 {
			bx := lx
			if anchor == "end" {
				bx = lx - tw
			}
			return [4]float64{bx, ly - 12, bx + tw, ly - 12 + bh}
		}
		var lx, ly float64
		anchor, found := "start", false
		for _, dy := range []float64{4, 4 - bh + 8, 26} {
			for _, side := range []float64{1, -1} {
				lx, ly, anchor = x1+14, gy+dy, "start"
				if side < 0 {
					lx, anchor = x0-14, "end"
				}
				b := box(lx, ly, anchor)
				if found = b[0] >= 8 && b[2] <= w-8 && b[3] <= top+ph+2 && !hits(b); found {
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			lx, ly, anchor = x1+14, gy+4, "start"
			for hits(box(lx, ly, anchor)) {
				ly += 6
			}
			c.line(x1+7, gy, lx-2, ly-4, "a")
		}
		placed = append(placed, box(lx, ly, anchor))
		for k, t := range lines {
			fmt.Fprintf(&c.b, `<text x="%.1f" y="%.1f" class="%s" text-anchor="%s" style="%s">%s</text>`, lx, ly+float64(k)*14, t.class, anchor, halo, esc(t.text))
		}
	}
	return c.String()
}
