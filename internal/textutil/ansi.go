// Package textutil cleans terminal output.
package textutil

import "strings"

func Clean(s string) string {
	s = StripANSI(s)
	s = CollapseCR(s)
	s = StripOverstrike(s)
	return TrimTrailingSpace(s)
}

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
		case '[':
			j := i + 2
			for j < len(s) && s[j] >= 0x20 && s[j] <= 0x3F {
				j++
			}
			if j < len(s) && s[j] >= 0x40 && s[j] <= 0x7E {
				j++
			}
			i = j
		case ']', 'P', '^', '_', 'X':
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
		case '(', ')', '*', '+', '#', '%':
			i += 3
		default:
			i += 2
		}
	}
	return b.String()
}

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

func TrimTrailingSpace(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}
