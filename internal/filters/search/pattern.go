package search

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type matcher struct {
	re, fold *regexp.Regexp
}

func newMatcher(o opts) *matcher {
	if len(o.patterns) == 0 {
		return nil
	}
	var parts []string
	upper := false
	for _, p := range o.patterns {
		for _, q := range strings.Split(p, "\n") {
			if q == "" {
				continue
			}
			for _, r := range q {
				if unicode.IsUpper(r) {
					upper = true
					break
				}
			}
			switch {
			case o.fixed:
				q = regexp.QuoteMeta(q)
			case o.flavor == 'b':
				q = breToRE2(q)
			default:
				q = wordBounds(q)
			}
			if o.word {
				q = `\b(?:` + q + `)\b`
			}
			parts = append(parts, "(?:"+q+")")
		}
	}
	if len(parts) == 0 {
		return nil
	}
	expr := strings.Join(parts, "|")
	if o.icase || o.smart && !upper {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil
	}
	m := &matcher{re: re}
	if f, err := regexp.Compile("(?i)" + expr); err == nil {
		m.fold = f
	}
	return m
}

func (m *matcher) find(s string) (int, int, bool) {
	if m == nil {
		return 0, 0, false
	}
	if loc := m.re.FindStringIndex(s); loc != nil {
		return loc[0], loc[1], true
	}

	if m.fold != nil {
		if loc := m.fold.FindStringIndex(s); loc != nil {
			return loc[0], loc[1], true
		}
	}
	return 0, 0, false
}

func breToRE2(p string) string {
	var b strings.Builder
	inBracket := false
	for i := 0; i < len(p); i++ {
		c := p[i]
		if inBracket {
			b.WriteByte(c)
			if c == ']' {
				inBracket = false
			}
			continue
		}
		switch {
		case c == '[':
			inBracket = true
			b.WriteByte(c)

			if i+1 < len(p) && p[i+1] == '^' {
				b.WriteByte('^')
				i++
			}
			if i+1 < len(p) && p[i+1] == ']' {
				b.WriteString(`\]`)
				i++
			}
		case c == '\\' && i+1 < len(p):
			n := p[i+1]
			i++
			switch n {
			case '(', ')', '{', '}', '|', '+', '?':
				b.WriteByte(n)
			case '<', '>':
				b.WriteString(`\b`)
			default:
				b.WriteByte('\\')
				b.WriteByte(n)
			}
		case strings.IndexByte("(){}|+?", c) >= 0:
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '*' && (i == 0 || p[i-1] == '^' && i == 1):
			b.WriteString(`\*`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func wordBounds(p string) string {
	if !strings.Contains(p, `\<`) && !strings.Contains(p, `\>`) {
		return p
	}
	return strings.NewReplacer(`\\`, `\\`, `\<`, `\b`, `\>`, `\b`).Replace(p)
}

const (
	longLine = 200

	windowSize = 160
)

func window(s string, m *matcher) string {
	if len(s) <= longLine {
		return s
	}
	n := utf8.RuneCountInString(s)
	if n <= longLine {
		return s
	}
	startB, endB, ok := m.find(s)
	start := 0
	if ok {
		ms := utf8.RuneCountInString(s[:startB])
		ml := utf8.RuneCountInString(s[startB:endB])
		start = ms - (windowSize-ml)/2
		if ml >= windowSize {
			start = ms
		}

		start = max(0, min(start, n-windowSize))
		if start < 20 {
			start = 0
		}
	}
	end := min(n, start+windowSize)

	bs, be := runeOffset(s, start), runeOffset(s, end)
	body := s[bs:be]
	var b strings.Builder
	if start > 0 {
		b.WriteString("…[+")
		b.WriteString(itoa(start))
		b.WriteString(" chars]… ")
		body = strings.TrimLeft(body, " \t")
	}
	if end < n {
		body = strings.TrimRight(body, " \t")
	}
	b.WriteString(body)
	if end < n {
		b.WriteString(" …[+")
		b.WriteString(itoa(n - end))
		b.WriteString(" chars]…")
	}
	return b.String()
}

func runeOffset(s string, i int) int {
	if i <= 0 {
		return 0
	}
	n := 0
	for off := range s {
		if n == i {
			return off
		}
		n++
	}
	return len(s)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
