package jstest

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type result struct {
	out string

	d           *doc
	have        map[string]bool
	readdedText map[string]bool

	readded int

	quiet []string

	failures int
}

type shared struct {
	errs   map[string]bool
	inErrs []int8
}

func newShared() *shared { return &shared{errs: map[string]bool{}} }

func (sh *shared) isErr(ln string) bool {
	v, ok := sh.errs[ln]
	if !ok {
		v = engine.IsError(ln)
		sh.errs[ln] = v
	}
	return v
}

type doc struct {
	c      *engine.Context
	sh     *shared
	in     []string
	benign []bool
	quiet  []bool
	out    []string

	level    int
	failures int
	omitted  int

	errSeen map[string]bool

	chatter map[int]bool

	compactEnd int
}

func newDoc(c *engine.Context, clean string, sh *shared) *doc {
	if sh == nil {
		sh = newShared()
	}
	in := strings.Split(clean, "\n")
	if len(sh.inErrs) != len(in) {
		sh.inErrs = make([]int8, len(in))
	}
	return &doc{c: c, sh: sh, in: in, benign: make([]bool, len(in)), quiet: make([]bool, len(in)),
		errSeen: map[string]bool{}, chatter: map[int]bool{}, compactEnd: -1}
}

func (d *doc) isErr(ln string) bool { return d.sh.isErr(ln) }

func (d *doc) inErr(i int) bool {
	switch d.sh.inErrs[i] {
	case 1:
		return false
	case 2:
		return true
	}
	v := engine.IsError(d.in[i])
	d.sh.inErrs[i] = 1
	if v {
		d.sh.inErrs[i] = 2
	}
	return v
}

func (d *doc) markChatter(from int) {
	for k := from; k < len(d.out); k++ {
		d.chatter[k] = true
	}
}

func (d *doc) emit(s ...string) { d.out = append(d.out, s...) }

func (d *doc) keep(i int) { d.out = append(d.out, d.in[i]) }

func (d *doc) drop(i int) { d.benign[i] = true }

func (d *doc) hush(from, to int) {
	for k := from; k < to; k++ {
		d.quiet[k] = true
	}
}

func (d *doc) sep() {
	if n := len(d.out); n > 0 && d.out[n-1] != "" {
		d.out = append(d.out, "")
	}
}

const maxSafetyLines = 40

func (d *doc) finish() result {
	lines := d.out
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	view := relativize(d.c, strings.Join(lines, "\n"))

	have := make(map[string]bool, len(lines))
	var squashed []string
	for _, ln := range strings.Split(view, "\n") {
		s := squash(ln)
		have[s] = true
		squashed = append(squashed, s)
	}
	all := ""
	res := result{d: d, have: have}
	var missing []string
	seen := map[string]bool{}
	for i, ln := range d.in {
		if d.benign[i] || !mayBeError(ln) {
			continue
		}
		s := squash(ln)
		if s == "" || seen[s] || have[s] || !d.inErr(i) {
			continue
		}
		seen[s] = true
		if all == "" {
			all = strings.Join(squashed, "\n")
		}
		if strings.Contains(all, s) {
			continue
		}
		missing = append(missing, ln)
	}
	if len(missing) > 0 {
		res.readdedText = map[string]bool{}
		for _, ln := range missing {
			res.readdedText[strings.TrimSpace(ln)] = true
		}
		var b strings.Builder
		b.WriteString(view)
		b.WriteString("\n[lx: error lines from the full output]\n")
		n := min(len(missing), maxSafetyLines)
		for _, ln := range missing[:n] {
			b.WriteString(ln)
			b.WriteByte('\n')
		}
		if len(missing) > n {
			fmt.Fprintf(&b, "… +%d more error lines\n", len(missing)-n)
		}
		view = strings.TrimRight(b.String(), "\n")
		res.readded = n
	}
	for i, q := range d.quiet {
		if q {
			res.quiet = append(res.quiet, d.in[i])
		}
	}
	res.out = view
	res.failures = d.failures
	return res
}

func (r result) benignLines() []string {
	d := r.d
	if d == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for i, ln := range d.in {
		if !d.benign[i] || !mayBeError(ln) {
			continue
		}
		s := squash(ln)
		if s == "" || seen[s] || r.have[s] || !d.inErr(i) {
			continue
		}
		seen[s] = true
		if t := strings.TrimSpace(ln); !r.readdedText[t] {
			out = append(out, t)
		}
	}
	return out
}

func relativize(c *engine.Context, s string) string {
	if c == nil || !strings.Contains(s, "file://") {
		return engine.RelativizeNonErrors(c, s)
	}
	cwd := strings.TrimRight(c.Cwd, "/") + "/"
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		switch {
		case len(ln) > 16<<10 || engine.IsError(ln):
		case strings.Contains(ln, "file://"):
			if isFrame(ln) && cwd != "/" {
				ln = strings.ReplaceAll(ln, "file://"+cwd, cwd)
				ln = strings.ReplaceAll(ln, "file:///private"+cwd, "/private"+cwd)
				lines[i] = engine.Relativize(c, ln)
			}
		default:
			lines[i] = engine.Relativize(c, ln)
		}
	}
	return strings.Join(lines, "\n")
}

func mayBeError(ln string) bool { return strings.TrimSpace(ln) != "" }

func squash(s string) string {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' || c >= 0x80 ||
			c == ' ' && (i == 0 || i == len(s)-1 || s[i+1] == ' ') {
			return strings.Join(strings.Fields(s), " ")
		}
	}
	return s
}

func indentOf(s string) int { return len(s) - len(strings.TrimLeft(s, " \t")) }

func plural(n int, one, many string) string { return engine.Plural(n, one, many) }
