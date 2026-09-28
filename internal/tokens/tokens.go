// Package tokens estimates LLM token counts.
package tokens

import (
	"unicode"
	"unicode/utf8"
)

func Count(s string) int {
	if s == "" {
		return 0
	}
	r := []rune(s)
	var total float64
	for i := 0; i < len(r); {
		n, cost := piece(r, i)
		total += cost
		i += n
	}
	return int(total + 0.5)
}

func piece(r []rune, i int) (int, float64) {
	c := r[i]
	at := func(k int) rune {
		if k < len(r) {
			return r[k]
		}
		return 0
	}

	if c == '\'' {
		a, b := unicode.ToLower(at(i+1)), unicode.ToLower(at(i+2))
		switch {
		case (a == 'r' && b == 'e') || (a == 'v' && b == 'e') || (a == 'l' && b == 'l'):
			return 3, 1
		case a == 's' || a == 't' || a == 'm' || a == 'd':
			return 2, 1
		}
	}

	if isLetter(c) || (c != '\r' && c != '\n' && !isLetter(c) && !isNumber(c) && isLetter(at(i+1))) {
		start, lead := i, rune(0)
		if !isLetter(c) {
			lead = c
			start++
		}
		j := start
		for j < len(r) && isLetter(r[j]) {
			j++
		}
		return j - i, wordCost(r[start:j], lead)
	}

	if isNumber(c) {
		j := i
		for j < len(r) && j-i < 3 && isNumber(r[j]) {
			j++
		}
		return j - i, 1
	}

	if isPunct(c) || (c == ' ' && isPunct(at(i+1))) {
		j := i
		if c == ' ' {
			j++
		}
		pstart := j
		for j < len(r) && isPunct(r[j]) {
			j++
		}
		pend := j
		for j < len(r) && (r[j] == '\r' || r[j] == '\n') {
			j++
		}
		return j - i, punctCost(r[pstart:pend])
	}

	j := i
	lastNL := -1
	for j < len(r) && unicode.IsSpace(r[j]) {
		if r[j] == '\n' || r[j] == '\r' {
			lastNL = j
		}
		j++
	}
	if lastNL >= 0 {
		return lastNL + 1 - i, 1
	}
	if j < len(r) && j-i > 1 {
		return j - i - 1, 1
	}
	if j == i {

		return 1, 1
	}
	return j - i, 1
}

func wordCost(w []rune, lead rune) float64 {
	L := float64(len(w))
	ascii, upper, capital := true, true, unicode.IsUpper(w[0])
	for _, ch := range w {
		if ch >= utf8.RuneSelf {
			ascii = false
		}
		if !unicode.IsUpper(ch) {
			upper = false
		}
	}
	if !ascii {
		return max(1, 0.9*L)
	}
	var base float64
	switch {
	case upper && L > 1:
		base = 1
		if L > 2 {
			base = 0.55 * L
		}
	case L <= 4:
		base = 1
	case L <= 8:
		base = 1 + 0.12*(L-4)
	default:
		base = 1.5 + 0.2*(L-8)
	}
	if capital && !upper {
		base *= 1.12
	}
	if lead != 0 && lead != ' ' {
		base += 0.3
	}
	return base
}

func punctCost(p []rune) float64 {
	units, extra := 0, 0.0
	for i := 0; i < len(p); {
		j := i
		for j < len(p) && p[j] == p[i] {
			j++
		}
		n := j - i
		if p[i] < utf8.RuneSelf {
			if n >= 3 {
				units++
			} else {
				units += n
			}
		} else {
			switch utf8.RuneLen(p[i]) {
			case 2:
				extra += float64(n)
			case 3:
				extra += 1.6 * float64(n)
			default:
				extra += 2.5 * float64(n)
			}
		}
		i = j
	}
	t := 0.0
	if units > 0 {
		t = 1
		if units > 3 {
			t += 0.5 * float64(units-3)
		}
	}
	return max(1, t+extra)
}

func isLetter(c rune) bool { return c != 0 && unicode.IsLetter(c) }
func isNumber(c rune) bool { return unicode.IsNumber(c) }
func isPunct(c rune) bool {
	return c != 0 && !unicode.IsSpace(c) && !unicode.IsLetter(c) && !unicode.IsNumber(c)
}
