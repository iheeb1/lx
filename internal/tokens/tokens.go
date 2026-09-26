// Package tokens estimates how many LLM tokens a piece of text costs,
// offline and without a vocabulary file.
//
// The estimator reproduces the cl100k/o200k pre-tokenizer split (the regex
// that chops text into words, number groups, punctuation runs and whitespace
// before BPE runs) and then prices each piece with a cost curve fitted
// against real tiktoken counts over ~5.7 MB of real developer command output.
// On that corpus it lands within 5.8% of cl100k on average (median 5.1%),
// versus ~20% for the usual bytes/4 rule of thumb. See docs/tokens.md.
package tokens

import (
	"unicode"
	"unicode/utf8"
)

// Count returns the estimated token count of s.
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

// piece returns the length (in runes) of the pre-token starting at i and its
// estimated BPE cost. Alternatives are tried in the same order as the cl100k
// pattern:
//
//	'(?i:[sdmt]|ll|ve|re) | [^\r\n\pL\pN]?\pL+ | \pN{1,3} |
//	 ?[^\s\pL\pN]+[\r\n]* | \s*[\r\n]+ | \s+(?!\S) | \s+
func piece(r []rune, i int) (int, float64) {
	c := r[i]
	at := func(k int) rune {
		if k < len(r) {
			return r[k]
		}
		return 0
	}

	// Contractions.
	if c == '\'' {
		a, b := unicode.ToLower(at(i+1)), unicode.ToLower(at(i+2))
		switch {
		case (a == 'r' && b == 'e') || (a == 'v' && b == 'e') || (a == 'l' && b == 'l'):
			return 3, 1
		case a == 's' || a == 't' || a == 'm' || a == 'd':
			return 2, 1
		}
	}

	// Words, with an optional single leading non-letter/non-digit.
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

	// Numbers, three digits per piece.
	if isNumber(c) {
		j := i
		for j < len(r) && j-i < 3 && isNumber(r[j]) {
			j++
		}
		return j - i, 1
	}

	// Punctuation runs, optionally led by one space, swallowing newlines.
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

	// Whitespace.
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
		return j - i - 1, 1 // leave one space to lead the next word/punct
	}
	if j == i {
		// A rune no class claims (NUL, lone surrogates): one byte token.
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

// punctCost prices a punctuation run. Runs of one repeated ASCII character
// ("=====", "-----", "......") are single BPE merges, so they count once;
// non-ASCII symbols (✓ ❯ ⎯ │) usually fall apart into byte tokens.
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
