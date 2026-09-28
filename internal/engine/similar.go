package engine

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"
)

var maskRe = lazyre.New(
	`(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(?::\d{2}(?:[.,]\d+)?)?(?:Z|[+-]\d{2}:?\d{2}\b)?` +
		`|\d{4}/\d{2}/\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?` +
		`|\d{1,2}/(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)/\d{4}:\d{2}:\d{2}:\d{2}(?: [+-]\d{4})?` +
		`|\b(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec) [ \d]\d \d{2}:\d{2}:\d{2})` +
		`|(\d{4}[-/]\d{2}[-/]\d{2})` +
		`|(\b\d{1,2}:\d{2}:\d{2}(?:[.,]\d+)?)` +
		`|(\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b)` +
		`|(\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(?::\d{1,5})?\b)` +
		`|(\b0x[0-9a-fA-F]+\b|\b[0-9a-fA-F]{8,}\b)` +
		`|(\b\d+(?:\.\d+)?(?:ns|µs|us|ms|s|m|h)(?:\d+(?:\.\d+)?(?:ns|µs|us|ms|s|m|h))*\b)` +
		`|(\b\d+(?:\.\d+)?\s?(?:[KMGTP]i?B|kB|B|bytes)\b|\b\d+(?:\.\d+)?[KMGT]\b)` +
		`|(\d+(?:[.,]\d+)*)`)

var maskNames = [...]string{"", "<TS>", "<DATE>", "<TIME>", "<UUID>", "<IP>", "<HEX>", "<DUR>", "<SIZE>", "<N>"}

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
		case 6:
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

var locationRe = lazyre.New(`[\w@-]\.[A-Za-z][A-Za-z0-9]{0,5}[:(-]\d+|^[^\s:]*[^\s:\d][^\s:]*:\d+[:-]|^\s*\d+:\d+\s`)

const (
	minSimilarRun = 4

	maxMaskLen = 1000
)

func CollapseSimilar(lines []string) []string {
	return collapseSimilar(lines, minSimilarRun, nil)
}

func collapseSimilar(lines []string, minRun int, fm *focusMatcher) []string {
	n := len(lines)
	if n < minRun {
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
	var focus []uint64
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
		if j-i < minRun {
			out = append(out, lines[i:j]...)
			i = j
			continue
		}
		indent := leadingSpace(lines[i])
		if indent == "" {
			indent = "  "
		}
		if fm != nil && focus == nil {
			focus = make([]uint64, n)
			for _, f := range fm.rank(strings.Join(lines, "\n")) {
				if f.i < n {
					focus[f.i] = f.mask
				}
			}
		}
		out = append(out, lines[i])
		from := i + 1
		for k := i + 1; focus != nil && k < j-1; k++ {
			if mask := focus[k]; mask != 0 {
				out = appendSimilar(out, lines[from:k], indent)
				out = append(out, lines[k])
				fm.hits = append(fm.hits, newFocusHit(lines[k], mask))
				from = k + 1
			}
		}
		out = appendSimilar(out, lines[from:j-1], indent)
		out = append(out, lines[j-1])
		i = j
	}
	return out
}

func appendSimilar(out, run []string, indent string) []string {
	if len(run) < 2 {
		return append(out, run...)
	}
	return append(out, fmt.Sprintf("%s… %d similar lines …", indent, len(run)))
}

func leadingSpace(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}
