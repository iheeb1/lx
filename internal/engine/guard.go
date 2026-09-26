package engine

import (
	"fmt"
	"strings"
)

// Guarded is implemented by filters that preserve errors themselves in a
// reformatted shape (e.g. tsc diagnostics grouped by file) and prove it in
// their fidelity tests. The guard is skipped for them.
type Guarded interface {
	GuardsErrors() bool
}

// Content is implemented by filters whose output is data (file listings,
// search matches, diffs, commit logs) where words like "error" are content,
// not status. The guard is skipped for them.
type Content interface {
	IsContent() bool
}

// maxGuardLines bounds how many missing error lines the guard re-adds.
const maxGuardLines = 40

// Guard makes invariant I2 hold at runtime: every error-class line of the
// normalized output that no longer appears in the filtered output is appended
// in a clearly labeled section. It returns the new output and how many lines
// it re-added.
func Guard(clean, out string) (string, int) {
	missing := missingErrorLines(clean, out, guardScanLimit)
	if len(missing) == 0 {
		return out, 0
	}
	var b strings.Builder
	b.WriteString(out)
	if !strings.HasSuffix(out, "\n") && out != "" {
		b.WriteByte('\n')
	}
	b.WriteString("[lx: error lines from the full output]\n")
	n := min(len(missing), maxGuardLines)
	for _, ln := range missing[:n] {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	if len(missing) > n {
		fmt.Fprintf(&b, "… +%d more error lines\n", len(missing)-n)
	}
	return strings.TrimRight(b.String(), "\n"), n
}

// MissingErrorLines returns the distinct error-class lines of in whose text
// (with whitespace runs collapsed) does not occur in out.
func MissingErrorLines(in, out string) []string {
	var m []string
	for _, ln := range missingErrorLines(in, out, -1) {
		m = append(m, strings.TrimSpace(ln))
	}
	return m
}

// guardScanLimit bounds the substring scans the runtime guard performs, so
// outputs with tens of thousands of distinct error lines stay linear. Past
// it, a line absent from the exact-line set is reported missing — the guard
// may then re-add a line that was present in a different shape, never the
// other way round.
const guardScanLimit = 2000

func missingErrorLines(in, out string, scanLimit int) []string {
	outLines := strings.Split(out, "\n")
	have := make(map[string]bool, len(outLines))
	for _, ln := range outLines {
		have[strings.Join(strings.Fields(ln), " ")] = true
	}
	var norm string // built lazily: most checks hit the set
	var missing []string
	seen := make(map[string]bool)
	scans := 0
	for _, ln := range strings.Split(in, "\n") {
		t := strings.Join(strings.Fields(ln), " ")
		if t == "" || seen[t] || have[t] || !IsError(ln) {
			continue
		}
		seen[t] = true
		if scanLimit < 0 || scans < scanLimit {
			if norm == "" {
				norm = squash(out)
			}
			scans++
			if strings.Contains(norm, t) {
				continue
			}
		}
		missing = append(missing, ln)
	}
	return missing
}

// squash collapses every whitespace run to one space, per line.
func squash(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.Join(strings.Fields(ln), " ")
	}
	return strings.Join(lines, "\n")
}
