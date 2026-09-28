// Package data handles curl, wget, httpie, jq and file viewers.
package data

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tokens"
)

func init() {

	engine.Register(jqFilter{})
	engine.Register(curlFilter{})
	engine.Register(wgetFilter{})
	engine.Register(httpieFilter{})
	engine.Register(fileFilter{})
}

const (
	fileBudget = engine.DefaultBudget

	jsonBodyBudget = 1500

	errorBodyBudget = 2000

	htmlBudget = 2000

	htmlTextCap = 1500
)

var conflictRe = lazyre.New(`^(?:<{7}|>{7}|\|{7})(?: |$)|^={7}$`)

func isConflictMarker(ln string) bool {
	return len(ln) >= 7 && strings.IndexByte("<=>|", ln[0]) >= 0 && conflictRe.MatchString(ln)
}

func hasConflict(lines []string) bool {
	var start, sep bool
	for _, ln := range lines {
		if !isConflictMarker(ln) {
			continue
		}
		switch ln[0] {
		case '<':
			start = true
		case '=':
			sep = sep || start
		case '>':
			if sep {
				return true
			}
		}
	}
	return false
}

func splitGlued(lines []string, find func(string) int) (out []string, idx []int) {
	out, idx = lines, nil
	for i, ln := range lines {
		k := find(ln)
		if k <= 0 {
			if idx != nil {
				out, idx = append(out, ln), append(idx, i)
			}
			continue
		}
		if idx == nil {
			out = append([]string(nil), lines[:i]...)
			idx = make([]int, i, len(lines)+4)
			for j := range idx {
				idx[j] = j
			}
		}
		out, idx = append(out, ln[:k], ln[k:]), append(idx, i, i)
	}
	if idx == nil {
		idx = make([]int, len(lines))
		for j := range idx {
			idx[j] = j
		}
	}
	return out, idx
}

const binarySample = 64 << 10

func looksBinary(s string) bool {
	sample := s[:min(len(s), binarySample)]
	if strings.Count(sample, "\x00") > 8 {
		return true
	}
	bad, n := 0, 0
	for i := 0; i < len(sample); {
		r, size := utf8.DecodeRuneInString(sample[i:])
		n++
		if r == utf8.RuneError && size == 1 || r > 0 && r < 0x20 && r != '\t' && r != '\n' && r != '\r' && r != '\f' && r != '\v' && r != '\b' && r != 0x1b || r == 0x7f {
			bad++
		}
		i += size
	}
	return bad*10 > n*3
}

func joinLines(lines []string) string {
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

func humanTokens(n int) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

func commaInt(n int) string {
	s := fmt.Sprint(n)
	if n < 0 {
		return s
	}
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	return b.String()
}

func humanBytes(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d B", n)
	case n < 1000*1000:
		return fmt.Sprintf("%.1f KB", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
}

func countTokens(s string) int { return tokens.Count(s) }
