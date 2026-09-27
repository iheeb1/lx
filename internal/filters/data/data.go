// Package data condenses commands whose output is data the user asked to
// see: HTTP clients (curl, wget, httpie), jq, and file viewers (cat, bat,
// head, tail).
//
// Every filter here is a Content filter: words like "error" in a response
// body, a JSON document or a source file are content, not status, so the
// engine's error guard is off and each filter keeps the tool's own
// diagnostics ("curl: (6) …", "cat: x: No such file or directory",
// "jq: error …") itself, verbatim.
//
// Rules shared by all of them:
//   - small outputs are returned unchanged (the engine then passes them
//     through), because exact data beats a condensed view;
//   - source text is never rewritten: over the budget a file is shown as
//     exact head and tail windows with an outline of the omitted lines and a
//     marker naming the omitted range and how to read it;
//   - JSON over its threshold goes through engine.CompactJSON (error fields
//     first), HTML through a title + visible text reduction, lockfiles become
//     a one-line summary with a package count — each marked "[lx: …]".
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
	// Order matters only for shell-wrapped pipelines, where effective()
	// already picks the stage that shapes the output.
	engine.Register(jqFilter{})
	engine.Register(curlFilter{})
	engine.Register(wgetFilter{})
	engine.Register(httpieFilter{})
	engine.Register(fileFilter{})
}

const (
	// fileBudget: text at or under this many tokens is shown unchanged. It is
	// the engine's default budget, so a file that would fit is never cut.
	fileBudget = engine.DefaultBudget
	// jsonBodyBudget: HTTP JSON bodies above this are condensed (brief 3.10).
	jsonBodyBudget = 1500
	// errorBodyBudget: bodies of 4xx/5xx responses are kept verbatim up to
	// this many tokens — the error text is what the agent needs.
	errorBodyBudget = 2000
	// htmlBudget: HTML above this becomes title + visible text …
	htmlBudget = 2000
	// … capped at this many tokens.
	htmlTextCap = 1500
)

// conflictRe matches git's merge-conflict markers (<<<<<<< ours, the
// ||||||| base of diff3/zdiff3, =======, >>>>>>> theirs).
var conflictRe = lazyre.New(`^(?:<{7}|>{7}|\|{7})(?: |$)|^={7}$`)

// isConflictMarker is conflictRe with a cheap first-byte check.
func isConflictMarker(ln string) bool {
	return len(ln) >= 7 && strings.IndexByte("<=>|", ln[0]) >= 0 && conflictRe.MatchString(ln)
}

// hasConflict reports whether lines hold a complete set of conflict markers
// (a start, a separator and an end marker).
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

// splitGlued moves a diagnostic that starts after column 0 of a line onto
// a line of its own: the text before it stays in the line, the diagnostic
// (from its start to the end of the line) becomes a new line right after.
// idx[k] is the index in the input of output line k (both parts of a split
// line map to its index), so line numbers shown to the agent keep
// referring to the output as it was captured.
//
// stdout and stderr share one capture buffer, and a tool's stdout is
// flushed in blocks that need not end at a newline, so a message written to
// stderr can land inside a data line:
//
//	{"id": 199, "name": "item-19curl: (18) transfer closed with 48213 bytes remaining to read
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

// binarySample: looksBinary judges the first 64 KB.
const binarySample = 64 << 10

// looksBinary reports data that is not text: more than a stray few NUL
// bytes, or more than 30% of characters that are neither valid UTF-8 nor
// printable (random bytes score about 60%, 8-bit text a few percent). Text in a legacy 8-bit encoding (a Latin-1 "©" in a source
// file, real capture cat-lua-latin1) is text and is shown as it is; the
// old check (any invalid UTF-8 byte) hid such whole files as "binary".
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

// joinLines joins lines and trims trailing newlines.
func joinLines(lines []string) string {
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// humanTokens renders a token count as "940" or "11.5k".
func humanTokens(n int) string {
	if n < 1000 {
		return fmt.Sprint(n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// commaInt renders 12345 as "12,345".
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

// humanBytes renders a byte count as "734 B", "12.3 KB", "1.2 MB".
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

// countTokens is tokens.Count, named for readability at call sites.
func countTokens(s string) int { return tokens.Count(s) }
