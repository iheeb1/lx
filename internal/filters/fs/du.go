package fs

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// du keeps du's own lines and order. Up to duKeepAll entries are shown as
// printed; beyond that the duTop largest entries are kept (in their
// original order, plus the -c "total" line) and one line counts the rest
// and bounds their size. Diagnostics ("du: x: Permission denied") come
// first, verbatim.
type du struct{}

const (
	duKeepAll = 40
	duTop     = 30
)

func (du) Name() string    { return "du" }
func (du) IsContent() bool { return true }

func (du) Match(c *engine.Context) bool {
	e := Effective(c)
	if engine.MachineReadable(e) {
		return false
	}
	if e.Name() != "du" && e.Name() != "gdu" {
		return false
	}
	for _, a := range e.Args() {
		if a == "--time" || strings.HasPrefix(a, "--time=") || a == "--null" || a == "-0" ||
			a == "--inodes" || a == "--help" || a == "--version" {
			return false
		}
	}
	return true
}

// duLineRe: size (blocks, or -h/--si human form, possibly right-aligned),
// a tab, the path.
var duLineRe = lazyre.New(`^ *(\d+(?:[.,]\d+)?)([BbkKMGTPEZY]?)(?:i?B)?\t(.+)$`)

type duEntry struct {
	line  string
	size  float64
	human bool
	path  string
}

func (d du) Apply(c *engine.Context, out string) (string, bool) {
	e := Effective(c)
	if !d.Match(e) {
		return "", false
	}
	var notes []string
	var entries []duEntry
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" {
			continue
		}
		if NoteHasPrefix(e, ln, "du") {
			notes = append(notes, ln)
			continue
		}
		m := duLineRe.FindStringSubmatch(ln)
		if m == nil {
			return "", false
		}
		v, err := strconv.ParseFloat(strings.Replace(m[1], ",", ".", 1), 64)
		if err != nil {
			return "", false
		}
		entries = append(entries, duEntry{line: ln, size: v * unitScale(m[2]), human: m[2] != "", path: m[3]})
	}
	// A failing run must be explained by a diagnostic the listing keeps
	// (see Unexplained); otherwise it was cut short (timeout, signal) and
	// counts would claim a complete listing.
	if len(entries) == 0 || Unexplained(e, len(notes)) {
		return "", false
	}
	var b strings.Builder
	for _, n := range CapNotes(notes) {
		b.WriteString(n + "\n")
	}
	if len(entries) <= duKeepAll {
		for _, en := range entries {
			b.WriteString(en.line + "\n")
		}
		return strings.TrimRight(b.String(), "\n"), true
	}

	// Rank by size (stable: earlier lines win ties); the grand total from
	// -c is always kept and never ranked.
	idx := make([]int, 0, len(entries))
	keep := make([]bool, len(entries))
	for i, en := range entries {
		if en.path == "total" && i == len(entries)-1 {
			keep[i] = true
			continue
		}
		idx = append(idx, i)
	}
	sort.SliceStable(idx, func(a, b int) bool { return entries[idx[a]].size > entries[idx[b]].size })
	for _, i := range idx[:min(duTop, len(idx))] {
		keep[i] = true
	}
	omitted, maxOmitted, sumOmitted := 0, -1.0, 0.0
	maxLine := ""
	for i, en := range entries {
		if keep[i] {
			continue
		}
		omitted++
		sumOmitted += en.size
		if en.size > maxOmitted {
			maxOmitted, maxLine = en.size, en.line
		}
	}
	for i, en := range entries {
		if keep[i] {
			b.WriteString(en.line + "\n")
		}
	}
	human := entries[0].human
	marker := fmt.Sprintf("… %d smaller %s omitted (each ≤ %s", omitted, plural(omitted, "entry", "entries"), sizeField(maxLine))
	if summarized(e.Args()) {
		// Siblings don't nest, so their sizes add up (≈: du rounds each one).
		if human {
			marker += ", ≈" + humanSize(sumOmitted) + " together"
		} else {
			marker += fmt.Sprintf(", %s together", strconv.FormatFloat(sumOmitted, 'f', -1, 64))
		}
	}
	b.WriteString(marker + ")")
	return b.String(), true
}

// summarized reports -s / --summarize / -d 0: entries are siblings.
func summarized(args []string) bool {
	for i, a := range args {
		switch {
		case a == "--summarize", a == "--max-depth=0", a == "-d0":
			return true
		case a == "-d" || a == "--max-depth":
			if i+1 < len(args) && args[i+1] == "0" {
				return true
			}
		case len(a) > 1 && a[0] == '-' && a[1] != '-':
			for _, ch := range a[1:] {
				if ch == 's' {
					return true
				}
				if ch == 'd' || ch == 'B' || ch == 't' || ch == 'X' {
					break // the rest of the cluster is a value
				}
			}
		}
	}
	return false
}

func sizeField(line string) string {
	if i := strings.IndexByte(line, '\t'); i >= 0 {
		return strings.TrimSpace(line[:i])
	}
	return strings.TrimSpace(line)
}

func unitScale(u string) float64 {
	switch u {
	case "k", "K":
		return 1 << 10
	case "M":
		return 1 << 20
	case "G":
		return 1 << 30
	case "T":
		return 1 << 40
	case "P":
		return 1 << 50
	case "E":
		return 1 << 60
	case "Z", "Y":
		return math.Pow(2, 70)
	}
	return 1
}

// humanSize formats bytes the way du -h does: one decimal below 10.
func humanSize(v float64) string {
	units := []string{"B", "K", "M", "G", "T", "P", "E"}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v < 10 && i > 0 {
		return strconv.FormatFloat(math.Ceil(v*10)/10, 'f', 1, 64) + units[i]
	}
	return strconv.FormatFloat(math.Ceil(v), 'f', 0, 64) + units[i]
}
