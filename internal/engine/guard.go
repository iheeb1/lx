package engine

import (
	"fmt"
	"strings"
)

type Guarded interface {
	GuardsErrors() bool
}

type Content interface {
	IsContent() bool
}

const maxGuardLines = 40

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

func MissingErrorLines(in, out string) []string {
	var m []string
	for _, ln := range missingErrorLines(in, out, -1) {
		m = append(m, strings.TrimSpace(ln))
	}
	return m
}

const guardScanLimit = 2000

func missingErrorLines(in, out string, scanLimit int) []string {
	outLines := strings.Split(out, "\n")
	have := make(map[string]bool, len(outLines))
	for _, ln := range outLines {
		have[strings.Join(strings.Fields(ln), " ")] = true
	}
	var norm string
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

func squash(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.Join(strings.Fields(ln), " ")
	}
	return strings.Join(lines, "\n")
}
