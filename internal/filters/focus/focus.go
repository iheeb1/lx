// Package focus ranks a filter's items by the engine's focus.
package focus

import (
	"math"
	"sort"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

const maxItems = 64

type term struct {
	text string
	w    float64
	fold bool
}

type Set struct {
	terms []term
	files []string
}

func New(f *engine.Focus) *Set {
	if f.Empty() {
		return nil
	}
	s := &Set{}
	for _, t := range f.Terms {
		text := strings.TrimSpace(t.Text)
		if len(text) < 3 || strings.ContainsAny(text, "\r\n") || len(s.terms) == maxItems {
			continue
		}
		w := t.Weight
		if !(w > 0) || math.IsInf(w, 1) {
			w = 1
		}
		ft := term{text: text, w: w, fold: foldable(text)}
		if ft.fold {
			ft.text = strings.ToLower(text)
		}
		s.terms = append(s.terms, ft)
	}
	for _, p := range f.Files {
		p = normPath(p)
		if base := p[strings.LastIndexByte(p, '/')+1:]; len(base) >= 2 && base != ".." && len(s.files) < maxItems {
			s.files = append(s.files, p)
		}
	}
	if len(s.terms) == 0 && len(s.files) == 0 {
		return nil
	}
	return s
}

func (s *Set) HasFiles() bool { return s != nil && len(s.files) > 0 }

func (s *Set) Score(name, body string) float64 {
	if s == nil {
		return 0
	}
	var lowName, lowBody string
	w := 0.0
	for _, t := range s.terms {
		switch {
		case t.in(name, &lowName):
			w += 2 * t.w
		case t.in(body, &lowBody):
			w += t.w
		}
	}
	for _, f := range s.files {
		if mentions(name, f) || mentions(body, f) {
			w++
		}
	}
	return w
}

func (s *Set) File(path string) bool {
	if s == nil {
		return false
	}
	if path = normPath(path); path == "" {
		return false
	}
	for _, f := range s.files {
		if sameFile(f, path) {
			return true
		}
	}
	return false
}

func (t term) in(s string, low *string) bool {
	if s == "" {
		return false
	}
	if t.fold {
		if *low == "" {
			*low = strings.ToLower(s)
		}
		s = *low
	}
	first, last := wordChar(t.text[0]), wordChar(t.text[len(t.text)-1])
	for off := 0; ; {
		i := strings.Index(s[off:], t.text)
		if i < 0 {
			return false
		}
		i += off
		j := i + len(t.text)
		if (!first || i == 0 || !wordChar(s[i-1])) && (!last || j == len(s) || !wordChar(s[j])) {
			return true
		}
		off = i + 1
	}
}

func mentions(s, file string) bool {
	base := file[strings.LastIndexByte(file, '/')+1:]
	for off := 0; ; {
		i := strings.Index(s[off:], base)
		if i < 0 {
			return false
		}
		i += off
		j := i + len(base)
		off = i + 1
		if j < len(s) && wordChar(s[j]) || i > 0 && (wordChar(s[i-1]) || s[i-1] == '.' || s[i-1] == '-') {
			continue
		}
		k := i
		for k > 0 && pathChar(s[k-1]) {
			k--
		}
		if sameFile(file, normPath(s[k:j])) {
			return true
		}
	}
}

func normPath(p string) string {
	p = strings.TrimPrefix(strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(p), `\`, "/"), "/"), "./")
	for strings.HasPrefix(p, "../") {
		p = p[3:]
	}
	return p
}

func sameFile(file, p string) bool {
	return p == file || strings.HasSuffix(file, "/"+p) || strings.HasSuffix(p, "/"+file)
}

func foldable(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c == ' ' || c == '-' || i == 0 && c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

func wordChar(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
}

func pathChar(b byte) bool {
	return b > ' ' && strings.IndexByte("\"'`()[]{}<>:=,;|", b) < 0
}

// TestParseConfig also yields ParseConfig and parseConfig.
func NameForms(name string) string {
	var b strings.Builder
	b.WriteString(name)
	for i := 0; i < len(name); i++ {
		if i > 0 && wordChar(name[i-1]) {
			continue
		}
		rest, n := name[i:], 0
		switch {
		case strings.HasPrefix(rest, "test_"), strings.HasPrefix(rest, "Test_"):
			n = 5
		case len(rest) > 4 && (strings.HasPrefix(rest, "Test") || strings.HasPrefix(rest, "test")) && rest[4] >= 'A' && rest[4] <= 'Z':
			n = 4
		}
		if n == 0 || n == len(rest) {
			continue
		}
		b.WriteByte('\n')
		b.WriteString(rest[n:])
		if c := rest[n]; c >= 'A' && c <= 'Z' {
			b.WriteByte('\n')
			b.WriteByte(c + 'a' - 'A')
			b.WriteString(rest[n+1:])
		}
	}
	return b.String()
}

// nil when the order would not change
func Rank(n int, score func(int) float64) []int {
	s := make([]float64, n)
	order := make([]int, n)
	for i := range s {
		s[i], order[i] = score(i), i
	}
	sort.SliceStable(order, func(a, b int) bool { return s[order[a]] > s[order[b]] })
	for i, k := range order {
		if i != k {
			return order
		}
	}
	return nil
}
