package main

import (
	"fmt"
	"html"
	"math"
	"strings"
)

const style = `<style>
svg{--surface:#fcfcfb;--ink1:#0b0b0b;--ink2:#52514e;--muted:#898781;--grid:#e1e0d9;--axis:#c3c2b7;--s1:#2a78d6;--s2:#eb6834;--s3:#1baf7a}
@media (prefers-color-scheme:dark){svg{--surface:#1a1a19;--ink1:#ffffff;--ink2:#c3c2b7;--muted:#898781;--grid:#2c2c2a;--axis:#383835;--s1:#3987e5;--s2:#d95926;--s3:#199e70}}
text{font-family:system-ui,-apple-system,"Segoe UI",Helvetica,Arial,sans-serif;font-size:12px;fill:var(--ink2)}
.bg{fill:var(--surface)}
.t{font-size:15px;font-weight:600;fill:var(--ink1)}
.st{fill:var(--ink2)}
.m{fill:var(--muted);font-size:11px;font-variant-numeric:tabular-nums}
.v{fill:var(--ink1);font-variant-numeric:tabular-nums}
.l{fill:var(--ink2)}
.g{stroke:var(--grid);stroke-width:1}
.a{stroke:var(--axis);stroke-width:1}
.s1{fill:var(--s1)}.s2{fill:var(--s2)}.s3{fill:var(--s3)}
.k1{stroke:var(--s1)}.k2{stroke:var(--s2)}.k3{stroke:var(--s3)}
.ring{stroke:var(--surface);stroke-width:2}
.conn{stroke:var(--axis);stroke-width:2;stroke-linecap:round}
</style>`

type canvas struct {
	w, h float64
	b    strings.Builder
}

func newCanvas(w, h float64, title, desc string) *canvas {
	c := &canvas{w: w, h: h}
	fmt.Fprintf(&c.b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %.0f %.0f" width="%.0f" height="%.0f" role="img" aria-labelledby="title desc">`, w, h, w, h)
	fmt.Fprintf(&c.b, "<title id=\"title\">%s</title><desc id=\"desc\">%s</desc>%s", esc(title), esc(desc), style)
	fmt.Fprintf(&c.b, `<rect class="bg" width="%.0f" height="%.0f" rx="10"/>`, w, h)
	return c
}

func (c *canvas) text(x, y float64, class, anchor, s string) {
	fmt.Fprintf(&c.b, `<text x="%.1f" y="%.1f" class="%s" text-anchor="%s">%s</text>`, x, y, class, anchor, esc(s))
}

func (c *canvas) line(x1, y1, x2, y2 float64, class string) {
	fmt.Fprintf(&c.b, `<line x1="%.1f" y1="%.1f" x2="%.1f" y2="%.1f" class="%s"/>`, x1, y1, x2, y2, class)
}

func (c *canvas) hbar(x0, y, w, h float64, class, tip string) {
	if w <= 0 {
		return
	}
	r := math.Min(4, math.Min(w, h/2))
	fmt.Fprintf(&c.b, `<path class="%s" d="M%.1f %.1fh%.1fa%.1f %.1f 0 0 1 %.1f %.1fv%.1fa%.1f %.1f 0 0 1 %.1f %.1fh%.1fz"><title>%s</title></path>`,
		class, x0, y, w-r, r, r, r, r, h-2*r, r, r, -r, r, -(w - r), esc(tip))
}

func (c *canvas) dot(x, y float64, class, tip string) {
	fmt.Fprintf(&c.b, `<circle cx="%.1f" cy="%.1f" r="5" class="%s ring"><title>%s</title></circle>`, x, y, class, esc(tip))
}

func (c *canvas) legend(x, y float64, items [][2]string) {
	for _, it := range items {
		fmt.Fprintf(&c.b, `<rect x="%.1f" y="%.1f" width="12" height="12" rx="3" class="%s"/>`, x, y-10, it[0])
		c.text(x+18, y, "l", "start", it[1])
		x += 18 + textWidth(it[1]) + 22
	}
}

func (c *canvas) String() string { return c.b.String() + "</svg>\n" }

func esc(s string) string { return html.EscapeString(s) }

func textWidth(s string) float64 {
	w := 0.0
	for _, r := range s {
		switch {
		case r == ' ' || r == '.' || r == ',' || r == ':' || r == 'i' || r == 'l' || r == '|' || r == '/':
			w += 3.6
		case r >= 'A' && r <= 'Z' || r == 'm' || r == 'w' || r == '%':
			w += 8.4
		default:
			w += 6.6
		}
	}
	return w
}

func niceTicks(maxV float64, n int) []float64 {
	if maxV <= 0 {
		return []float64{0}
	}
	raw := maxV / float64(n)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	step := mag
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if m*mag >= raw {
			step = m * mag
			break
		}
	}
	var t []float64
	for v := 0.0; v <= maxV+step*0.001; v += step {
		t = append(t, v)
	}
	if t[len(t)-1] < maxV {
		t = append(t, t[len(t)-1]+step)
	}
	return t
}

func compact(v float64) string {
	switch {
	case v >= 1e6:
		return trimZero(fmt.Sprintf("%.1fM", v/1e6))
	case v >= 1e3:
		return trimZero(fmt.Sprintf("%.1fk", v/1e3))
	default:
		return fmt.Sprintf("%.0f", v)
	}
}

func trimZero(s string) string { return strings.Replace(s, ".0", "", 1) }
