package engine

import (
	"fmt"
	"regexp"
	"strings"
)

// maskRe finds the variable parts of a line. Alternatives are tried left to
// right (leftmost-first), so the longer shapes (full timestamps) win over
// their pieces (dates, times, numbers). Each capture group is one class.
var maskRe = regexp.MustCompile(
	// 1: full timestamps: ISO 8601, 2026/09/26 10:00:01, CLF, syslog.
	`(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2}(?:[.,]\d+)?)?(?:Z|[+-]\d{2}:?\d{2}\b)?` +
		`|\d{4}/\d{2}/\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?` +
		`|\d{1,2}/(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)/\d{4}:\d{2}:\d{2}:\d{2}(?: [+-]\d{4})?` +
		`|\b(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) [ \d]\d \d{2}:\d{2}:\d{2})` +
		// 2: dates.
		`|(\d{4}[-/]\d{2}[-/]\d{2})` +
		// 3: times of day.
		`|(\b\d{1,2}:\d{2}:\d{2}(?:[.,]\d+)?)` +
		// 4: UUIDs.
		`|(\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b)` +
		// 5: IPv4 with optional port.
		`|(\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(?::\d{1,5})?\b)` +
		// 6: hex: 0x-prefixed, or 8+ hex digits (checked for a digit below).
		`|(\b0x[0-9a-fA-F]+\b|\b[0-9a-fA-F]{8,}\b)` +
		// 7: durations: 12ms, 2.3µs, 1m30s.
		`|(\b\d+(?:\.\d+)?(?:ns|µs|us|ms|s|m|h)(?:\d+(?:\.\d+)?(?:ns|µs|us|ms|s|m|h))*\b)` +
		// 8: sizes: 3.2MB, 512 KiB, 4.0K, 12 bytes.
		`|(\b\d+(?:\.\d+)?\s?(?:[KMGTP]i?B|kB|B|bytes)\b|\b\d+(?:\.\d+)?[KMGT]\b)` +
		// 9: numbers (masked only with 2+ digits).
		`|(\d+(?:[.,]\d+)*)`)

var maskNames = [...]string{"", "<TS>", "<DATE>", "<TIME>", "<UUID>", "<IP>", "<HEX>", "<DUR>", "<SIZE>", "<N>"}

// Mask returns line with its variable tokens replaced by placeholders, so
// that lines differing only in values compare equal:
//
//	<TS>    timestamps (ISO 8601, "2026/09/26 10:00:01", CLF, syslog)
//	<DATE>  dates (2026-09-26, 2026/09/26)
//	<TIME>  times of day (10:00:01, 10:00:01.123)
//	<UUID>  UUIDs
//	<IP>    IPv4 addresses, with optional :port
//	<HEX>   0x… values and 8+ digit hex strings containing a digit
//	<DUR>   durations (12ms, 2.3µs, 1m30s)
//	<SIZE>  sizes (3.2MB, 512 KiB, 4.0K)
//	<N>     numbers with at least two digits (single digits stay)
//
// Lines without any digit are returned unchanged (fast path). Mask is pure
// and deterministic; it never changes whitespace outside masked spans.
func Mask(line string) string {
	if !hasDigit(line) {
		return line
	}
	idx := maskRe.FindAllStringSubmatchIndex(line, -1)
	if idx == nil {
		return line
	}
	var b strings.Builder
	b.Grow(len(line))
	last := 0
	for _, m := range idx {
		start, end := m[0], m[1]
		group := 0
		for g := 1; g < len(maskNames); g++ {
			if m[2*g] >= 0 {
				group = g
				break
			}
		}
		tok := line[start:end]
		repl := maskNames[group]
		switch group {
		case 6: // hex: needs a digit; all-digit runs are plain numbers
			if !hasDigit(tok) {
				repl = tok
			} else if isAllDigits(tok) {
				repl = "<N>"
			}
		case 9:
			if countDigits(tok) < 2 {
				repl = tok
			}
		}
		b.WriteString(line[last:start])
		b.WriteString(repl)
		last = end
	}
	b.WriteString(line[last:])
	return b.String()
}

func hasDigit(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return true
		}
	}
	return false
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

func countDigits(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			n++
		}
	}
	return n
}

// locationRe finds source locations (file.go:12, x.ts(3), x.ts-45- in rg
// context, grep's path:12:, eslint's "  12:5  error"). Lines carrying one
// are never folded: the location is what the reader acts on.
var locationRe = regexp.MustCompile(`[\w@-]\.[A-Za-z][A-Za-z0-9]{0,5}[:(-]\d+|^[^\s:]*[^\s:\d][^\s:]*:\d+[:-]|^\s*\d+:\d+\s`)

const (
	// minSimilarRun is the shortest run CollapseSimilar folds. With three
	// lines the marker would cost as much as the one line it replaces.
	minSimilarRun = 4
	// maxMaskLen: longer lines (minified code, data blobs) are never treated
	// as similar; masking them costs time and a false match would hide data.
	maxMaskLen = 1000
)

// CollapseSimilar folds runs of at least 4 consecutive lines that are equal
// after Mask (same text, different numbers/timestamps/ids) into
//
//	first line
//	<indent>… N similar lines …
//	last line
//
// where N is the number of lines folded away. Error-class lines (IsError)
// and lines carrying a source location (file.go:12, path:12: as printed by
// grep, eslint's "12:5") never join a run and are always kept verbatim,
// because the numbers in them are what the reader acts on; blank lines and
// lines longer than 1000 bytes never join a run either. The input slice is
// not modified.
func CollapseSimilar(lines []string) []string {
	n := len(lines)
	if n < minSimilarRun {
		return append([]string(nil), lines...)
	}
	masks := make([]string, n)
	ok := make([]bool, n)
	for i, ln := range lines {
		if len(ln) > maxMaskLen || strings.TrimSpace(ln) == "" || locationRe.MatchString(ln) || IsError(ln) {
			continue
		}
		masks[i], ok[i] = Mask(ln), true
	}
	out := make([]string, 0, n)
	for i := 0; i < n; {
		if !ok[i] {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i + 1
		for j < n && ok[j] && masks[j] == masks[i] {
			j++
		}
		if j-i < minSimilarRun {
			out = append(out, lines[i:j]...)
			i = j
			continue
		}
		indent := leadingSpace(lines[i])
		if indent == "" {
			indent = "  "
		}
		out = append(out, lines[i], fmt.Sprintf("%s… %d similar lines …", indent, j-i-2), lines[j-1])
		i = j
	}
	return out
}

// leadingSpace returns the leading spaces/tabs of s.
func leadingSpace(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}
