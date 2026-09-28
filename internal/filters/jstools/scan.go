package jstools

import "strings"

func isRESpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\f' || b == '\r'
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func digitsBack(s string, end int) int {
	i := end
	for i > 0 && isDigit(s[i-1]) {
		i--
	}
	return i
}

func digitsFwd(s string, i int) int {
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	return i
}

func tscTail(s string) (sev, code, msg string, ok bool) {
	for _, sv := range [...]string{"error", "warning", "message"} {
		if !strings.HasPrefix(s, sv) || !strings.HasPrefix(s[len(sv):], " TS") {
			continue
		}
		d := len(sv) + 3
		e := digitsFwd(s, d)
		if e == d || !strings.HasPrefix(s[e:], ": ") {
			return "", "", "", false
		}
		if strings.IndexByte(s[e+2:], '\n') >= 0 {
			return "", "", "", false
		}
		return sv, s[len(sv)+1 : e], s[e+2:], true
	}
	return "", "", "", false
}

func scanTSCPlain(ln string) (m [6]string, ok bool) {
	for off := 0; ; {
		k := strings.Index(ln[off:], "): ")
		if k < 0 {
			return m, false
		}
		k += off
		off = k + 1
		sev, code, msg, tok := tscTail(ln[k+3:])
		if !tok {
			continue
		}
		cs := digitsBack(ln, k)
		if cs == k || cs == 0 || ln[cs-1] != ',' {
			continue
		}
		ls := digitsBack(ln, cs-1)
		if ls == cs-1 || ls == 0 || ln[ls-1] != '(' {
			continue
		}
		p := ls - 1
		if p < 1 || strings.IndexByte(ln[:p], '\n') >= 0 {
			continue
		}
		return [6]string{ln[:p], ln[ls : cs-1], ln[cs:k], sev, code, msg}, true
	}
}

func scanTSCPretty(ln string) (m [6]string, ok bool) {
	for off := 0; ; {
		k := strings.Index(ln[off:], " - ")
		if k < 0 {
			return m, false
		}
		k += off
		off = k + 1
		sev, code, msg, tok := tscTail(ln[k+3:])
		if !tok {
			continue
		}
		cs := digitsBack(ln, k)
		if cs == k || cs == 0 || ln[cs-1] != ':' {
			continue
		}
		ls := digitsBack(ln, cs-1)
		if ls == cs-1 || ls == 0 || ln[ls-1] != ':' {
			continue
		}
		p := ls - 1
		if p < 1 || strings.IndexByte(ln[:p], '\n') >= 0 {
			continue
		}
		return [6]string{ln[:p], ln[ls : cs-1], ln[cs:k], sev, code, msg}, true
	}
}

func scanTSCGlobal(ln string) bool {
	_, _, _, ok := tscTail(ln)
	return ok
}

func scanESMsg(ln string) (line, col, sev, rest string, ok bool) {
	i := 0
	for i < len(ln) && isRESpace(ln[i]) {
		i++
	}
	if i == 0 {
		return
	}
	ls := i
	i = digitsFwd(ln, i)
	if i == ls || i >= len(ln) || ln[i] != ':' {
		return
	}
	line = ln[ls:i]
	cs := i + 1
	i = digitsFwd(ln, cs)
	if i == cs {
		return
	}
	col = ln[cs:i]
	ws := i
	for i < len(ln) && isRESpace(ln[i]) {
		i++
	}
	if i == ws {
		return
	}
	switch {
	case strings.HasPrefix(ln[i:], "error"):
		sev = "error"
	case strings.HasPrefix(ln[i:], "warning"):
		sev = "warning"
	default:
		return
	}
	i += len(sev)
	ws = i
	for i < len(ln) && isRESpace(ln[i]) {
		i++
	}
	if i == ws || strings.IndexByte(ln[i:], '\n') >= 0 {
		return
	}
	return line, col, sev, ln[i:], true
}

func scanESRule(s string) (text, rule string, ok bool) {
	e := len(s)
	b := e
	for b > 0 && !isRESpace(s[b-1]) {
		b--
	}
	if b == e {
		return "", "", false
	}
	w := b
	for w > 0 && isRESpace(s[w-1]) {
		w--
	}

	if b-w < 2 || strings.IndexByte(s[:w], '\n') >= 0 {
		return "", "", false
	}
	return s[:w], s[b:], true
}
