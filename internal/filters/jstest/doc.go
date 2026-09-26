package jstest

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// result is what a renderer produced.
type result struct {
	out string
	// benignLines() lists the error-class input lines the view dropped on
	// purpose because their structure shows they are not a status: test
	// and suite titles, source lines around a failing line, library stack
	// frames counted in a fold marker, a repeated section header, failures
	// counted as not shown. Tests check that every error line missing from
	// out is one of these.
	d           *doc
	have        map[string]bool // squashed lines of out
	readdedText map[string]bool // trimmed lines the safety net re-added
	// readded counts error lines the safety net had to append (a renderer
	// bug when non-zero on real output).
	readded int
	// quiet are input lines of console output hidden because the test
	// passed or the entry was capped (an "at file:line" there is where a
	// test logged, not where it failed).
	quiet []string
	// failures is how many test failures the view rendered (levels uses it
	// to skip levels that would change nothing).
	failures int
}

// shared is state reused by the renders of one output at several levels.
type shared struct {
	errs   map[string]bool // engine.IsError results by line text
	inErrs []int8          // by input line: 0 unknown, 1 not an error, 2 error
}

func newShared() *shared { return &shared{errs: map[string]bool{}} }

// isErr is engine.IsError, memoized: classifying is the dominant cost on
// huge outputs, and levels renders the same lines several times.
func (sh *shared) isErr(ln string) bool {
	v, ok := sh.errs[ln]
	if !ok {
		v = engine.IsError(ln)
		sh.errs[ln] = v
	}
	return v
}

// doc accumulates a view of the normalized input lines.
type doc struct {
	c      *engine.Context
	sh     *shared
	in     []string
	benign []bool
	quiet  []bool
	out    []string
	// level of detail (see levels.go): 0 renders every failure in full;
	// higher levels shorten failures after the first fullFailures.
	level    int
	failures int // test failures rendered so far
	omitted  int // failures left out (level 3) since the last flushOmitted
	// errSeen: squashed error lines already shown inside failure blocks
	// (names-only failures keep only an error message not shown before).
	errSeen map[string]bool
	// chatter marks indices of out holding console output of passing
	// tests: an error there is not why the run failed (see noFailure).
	chatter map[int]bool
	// compactEnd is len(out) after the last failure reduced to its name
	// (or left out), -1 otherwise: consecutive ones are not separated by
	// blank lines.
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

// isErr reports an error-class line (engine.IsError, memoized).
func (d *doc) isErr(ln string) bool { return d.sh.isErr(ln) }

// inErr reports whether input line i is error-class (memoized by index).
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

// markChatter marks d.out[from:] as console output of passing tests.
func (d *doc) markChatter(from int) {
	for k := from; k < len(d.out); k++ {
		d.chatter[k] = true
	}
}

// emit appends output lines.
func (d *doc) emit(s ...string) { d.out = append(d.out, s...) }

// keep appends input line i verbatim.
func (d *doc) keep(i int) { d.out = append(d.out, d.in[i]) }

// drop marks input line i as intentionally dropped benign text.
func (d *doc) drop(i int) { d.benign[i] = true }

// hush marks input lines [from, to) as console output that may be hidden.
func (d *doc) hush(from, to int) {
	for k := from; k < to; k++ {
		d.quiet[k] = true
	}
}

// sep ends the current group with one blank line (never two, never first).
func (d *doc) sep() {
	if n := len(d.out); n > 0 && d.out[n-1] != "" {
		d.out = append(d.out, "")
	}
}

// maxSafetyLines bounds how many missing error lines the safety net re-adds.
const maxSafetyLines = 40

// finish joins the view, relativizes paths in non-error lines, and runs the
// safety net: every error-class input line that is neither in the view
// (whitespace runs collapsed, as the engine guard compares) nor marked
// benign is appended under the engine guard's heading. Lines already in the
// view or marked benign are not classified at all (classifying is the
// dominant cost on huge outputs).
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
	seen := map[string]bool{} // texts already judged
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

// benignLines returns the error-class input lines (trimmed, distinct) that
// the view dropped on purpose (see result.benign). Tests use it; Apply does
// not pay for it.
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

// relativize is engine.RelativizeNonErrors, except for file:// URLs, which
// engine.Relativize would break ("file:///cwd/a.mjs:3:9" → "file://a.mjs:3:9"):
// in stack frames they become relative paths ("at load (a.mjs:3:9)"), and
// other lines holding one are left as they are (an error's url property is
// data). Error-class lines are never changed.
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

// mayBeError is a cheap prefilter: blank lines are never error-class.
func mayBeError(ln string) bool { return strings.TrimSpace(ln) != "" }

// squash collapses whitespace runs to one space and trims (the engine
// guard's comparison), without allocating for lines that need nothing.
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

// indentOf returns the number of leading spaces/tabs.
func indentOf(s string) int { return len(s) - len(strings.TrimLeft(s, " \t")) }

// plural formats "1 suite" / "3 suites".
func plural(n int, one, many string) string { return engine.Plural(n, one, many) }
