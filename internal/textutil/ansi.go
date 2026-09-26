// Package textutil holds byte/line level cleanup shared by every filter:
// ANSI escape removal, carriage-return (progress bar) collapse, and
// backspace overstrike removal.
package textutil

import "strings"

// Clean applies the terminal-noise removal every filter wants before it looks
// at output: ANSI/OSC escapes, \r-overwritten progress frames, overstrike,
// and trailing whitespace. Line structure is preserved.
func Clean(s string) string {
	s = StripANSI(s)
	s = CollapseCR(s)
	s = StripOverstrike(s)
	return TrimTrailingSpace(s)
}

// StripANSI removes CSI (ESC [ ... final), OSC (ESC ] ... BEL | ESC \),
// DCS/PM/APC strings, and two-byte ESC sequences. Unterminated sequences at
// end of input are dropped.
func StripANSI(s string) string {
	if strings.IndexByte(s, 0x1b) < 0 && strings.IndexByte(s, 0x9b) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c != 0x1b {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(s) {
			break
		}
		switch s[i+1] {
		case '[': // CSI: params 0x30-0x3F, intermediates 0x20-0x2F, final 0x40-0x7E
			j := i + 2
			for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3F {
				j++
			}
			if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7E {
				j++
			}
			i = j
		case ']', 'P', '^', '_', 'X': // OSC / DCS / PM / APC / SOS: until BEL or ST
			j := i + 2
			for j < len(s) {
				if s[j] == 0x07 {
					j++
					break
				}
				if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
					j += 2
					break
				}
				j++
			}
			i = j
		case '(', ')', '*', '+', '#', '%': // charset designation: ESC ( B
			i += 3
		default: // two-byte sequence: ESC 7, ESC 8, ESC =, ESC >, ESC M ...
			i += 2
		}
	}
	return b.String()
}

// CollapseCR keeps, for every line, only the text written after the last bare
// carriage return, which is what a terminal would finally display. This turns
// thousands of progress-bar frames into one line. CRLF endings are normalized.
func CollapseCR(s string) string {
	if strings.IndexByte(s, '\r') < 0 {
		return s
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if strings.IndexByte(s, '\r') < 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if k := strings.LastIndexByte(ln, '\r'); k >= 0 {
			last := ln[k+1:]
			// "50%\r" followed by nothing: keep the last non-empty frame.
			if strings.TrimSpace(last) == "" {
				parts := strings.Split(ln, "\r")
				last = ""
				for p := len(parts) - 1; p >= 0; p-- {
					if strings.TrimSpace(parts[p]) != "" {
						last = parts[p]
						break
					}
				}
			}
			lines[i] = last
		}
	}
	return strings.Join(lines, "\n")
}

// StripOverstrike removes "x\bx" bold and "_\bx" underline sequences (man,
// some legacy tools) and any other stray backspaces.
func StripOverstrike(s string) string {
	if strings.IndexByte(s, '\b') < 0 {
		return s
	}
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\b' {
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// TrimTrailingSpace removes trailing spaces/tabs from every line and trailing
// blank lines from the whole text.
func TrimTrailingSpace(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}
