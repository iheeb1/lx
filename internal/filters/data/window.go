package data

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type window struct {
	path string

	lang string

	keep func(string) bool

	keepIdx map[int]bool

	nums                []int
	head, tail, outline int
}

const (
	windowHead    = 2500
	windowTail    = 700
	windowOutline = 2500

	maxShownLine = 2000

	outlineCut = 120
)

func defaultWindow(path, lang string, keep func(string) bool) window {
	return window{path: path, lang: lang, keep: keep, head: windowHead, tail: windowTail, outline: windowOutline}
}

func (w window) apply(lines []string) []string {
	n := len(lines)
	shown := make([]string, n)
	cost := make([]int, n)
	for i, ln := range lines {
		if len(ln) > maxShownLine {
			ln = engine.ShortenLine(ln, maxShownLine/2)
		}
		shown[i] = ln
		cost[i] = countTokens(ln) + 1
	}

	h, used := 0, 0
	for h < n && used+cost[h] <= w.head {
		used += cost[h]
		h++
	}
	if h == 0 && n > 0 {
		h = 1
	}

	t, used := n, 0
	for t > h && used+cost[t-1] <= w.tail {
		used += cost[t-1]
		t--
	}
	if t >= n && n > h {
		t = n - 1
	}
	if h >= t {
		return shown
	}
	re := outlineRe(w.lang)
	h = snapHead(lines, h)
	t = w.snapTail(lines, cost, re, h, t)
	if h >= t {
		return shown
	}

	out := make([]string, 0, h+(n-t)+64)
	out = append(out, shown[:h]...)
	out = append(out, w.marker(w.num(h), w.num(t-1)))
	out = append(out, w.outlineOf(lines, h, t)...)
	out = append(out, shown[t:]...)
	return out
}

func snapHead(lines []string, h int) int {
	lo := max(h-h/4, 1)
	for i := h - 1; i >= lo; i-- {
		prev, ln := lines[i-1], lines[i]
		if ln != "" && ln[0] != ' ' && ln[0] != '\t' && ln[0] != '}' && ln[0] != ')' &&
			(strings.TrimSpace(prev) == "" || prev == "}" || prev == "};") {
			return i
		}
	}
	for i := h - 1; i >= lo; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			return i + 1
		}
	}
	return h
}

func (w window) snapTail(lines []string, cost []int, re lineMatcher, h, t int) int {
	extra := 0
	for i := t - 1; i > h && extra+cost[i] <= w.tail/2; i-- {
		extra += cost[i]
		ln := lines[i]
		if ln == "" || ln[0] == ' ' || ln[0] == '\t' || !re.MatchString(ln) {
			continue
		}
		for i > h+1 && isCommentLine(lines[i-1]) && extra+cost[i-1] <= w.tail/2 {
			i--
			extra += cost[i]
		}
		return i
	}
	hi := t + (len(lines)-t)/4
	for i := t; i < hi && i < len(lines)-1; i++ {
		if strings.TrimSpace(lines[i]) == "" && i+1 > h {
			return i + 1
		}
	}
	return t
}

func isCommentLine(ln string) bool {
	t := strings.TrimSpace(ln)
	return strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") ||
		strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "#include") || strings.HasPrefix(t, "@") || strings.HasPrefix(t, "///")
}

func (w window) num(i int) int {
	if i < len(w.nums) {
		return w.nums[i]
	}
	return i + 1
}

func (w window) marker(a, b int) string {
	how := fmt.Sprintf("lx show <id> --lines %d-%d, <id> is on the last line", a, b)
	if w.path != "" {
		how = fmt.Sprintf("sed -n '%d,%dp' %s", a, b, shellQuote(w.path))
	}
	return fmt.Sprintf("… lines %d-%d omitted (%s; read them: %s)", a, b, engine.Plural(b-a+1, "line", "lines"), how)
}

func (w window) outlineOf(lines []string, a, b int) []string {
	re := outlineRe(w.lang)
	var out []string
	used, more, firstMore, lastMore := 0, 0, 0, 0
	for i := a; i < b; i++ {
		ln := lines[i]
		keep := w.keep != nil && w.keep(ln) || w.keepIdx[i] && ln != ""
		if !keep && (len(ln) > maxShownLine || !re.MatchString(ln)) {
			continue
		}
		entry := ln
		switch {
		case !keep:
			entry = cutRunes(strings.TrimRight(ln, " {"), outlineCut)
		case len(ln) > maxShownLine:

			entry = engine.ShortenLine(ln, maxShownLine/2)
		}
		entry = fmt.Sprintf("  L%d: %s", w.num(i), entry)
		c := countTokens(entry) + 1
		if !keep && (more > 0 || used+c > w.outline) {
			if more == 0 {
				firstMore = w.num(i)
			}
			more++
			lastMore = w.num(i)
			continue
		}
		used += c
		out = append(out, entry)
	}
	if len(out) > 0 {
		out = append([]string{"  outline of the omitted lines:"}, out...)
	}
	if more > 0 {
		out = append(out, fmt.Sprintf("  … +%s in lines %d-%d", engine.Plural(more, "more declaration", "more declarations"), firstMore, lastMore))
	}
	return out
}

func cutRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}

func shellQuote(p string) string {
	for _, ch := range p {
		if !(ch == '/' || ch == '.' || ch == '_' || ch == '-' || ch == '+' || ch == ',' || ch == '@' || ch == ':' ||
			ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
		}
	}
	return p
}

var (
	goDeclRe   = lazyre.New(`^(?:func|type|var|const)\b`)
	jsDeclRe   = lazyre.New(`^(?:export\s+)?(?:default\s+)?(?:declare\s+)?(?:async\s+)?(?:function\*?|class|interface|type|enum|const|let|var|abstract\s+class|namespace)\s|^export\s+(?:default\b|\{|\*)|^module\.exports\b`)
	pyDeclRe   = lazyre.New(`^(?:    )?(?:async\s+)?(?:def|class)\s|^@\w`)
	rsDeclRe   = lazyre.New(`^\s{0,4}(?:pub(?:\([\w:]+\))?\s+)?(?:async\s+)?(?:unsafe\s+)?(?:const\s+)?(?:fn|struct|enum|trait|impl|mod|type|static|union|macro_rules!)\b|^impl\b`)
	jvmDeclRe  = lazyre.New(`^\s{0,4}(?:@\w+\s+)*(?:(?:public|private|protected|internal|static|final|abstract|sealed|open|data|override|suspend|inline|virtual|partial|readonly|async|export)\s+)*(?:class|interface|enum|record|object|fun|struct|namespace|trait)\s|^\s{0,4}(?:public|private|protected|internal)\s+(?:static\s+)?(?:final\s+)?[\w<>\[\],.? ]+\s+\w+\s*\(`)
	cDeclRe    = lazyre.New(`^(?:static\s+|extern\s+|inline\s+|const\s+|unsigned\s+|struct\s+|enum\s+)*[A-Za-z_][\w*() ]*?[\s*)]\**[A-Za-z_]\w*\s*\([^;]*$|^(?:typedef|struct|union|enum|class|namespace|template)\b|^#define\s+\w+\(`)
	rbDeclRe   = lazyre.New(`^\s{0,4}(?:def|class|module)\s`)
	phpDeclRe  = lazyre.New(`^\s{0,4}(?:(?:abstract|final|public|private|protected|static|readonly)\s+)*(?:function|class|interface|trait|enum)\s`)
	shDeclRe   = lazyre.New(`^(?:function\s+)?[\w.:-]+\s*\(\)\s*\{?\s*$|^function\s+[\w.:-]+`)
	mdDeclRe   = lazyre.New(`^#{1,6}\s+\S`)
	yamlDeclRe = lazyre.New(`^[\w.-]+:(?:\s|$)`)
	anyDeclRe  = lazyre.New(`^(?:export\s+|pub\s+|public\s+|private\s+|static\s+|async\s+|abstract\s+)*(?:func|function|def|class|interface|type|struct|enum|trait|impl|module|fn|namespace)\s+\w|^#{1,6}\s+\S`)
	cKeywordRe = lazyre.New(`^(?:if|for|while|switch|return|else|do|goto|case|sizeof)\b`)
)

type lineMatcher interface{ MatchString(string) bool }

func outlineRe(ext string) lineMatcher {
	switch strings.ToLower(ext) {
	case ".go":
		return goDeclRe
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".mts", ".cts", ".vue", ".svelte":
		return jsDeclRe
	case ".py", ".pyi":
		return pyDeclRe
	case ".rs":
		return rsDeclRe
	case ".java", ".kt", ".kts", ".scala", ".cs", ".swift", ".dart", ".groovy":
		return jvmDeclRe
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".m", ".mm":
		return cDeclReNoKeywords
	case ".rb":
		return rbDeclRe
	case ".php":
		return phpDeclRe
	case ".sh", ".bash", ".zsh":
		return shDeclRe
	case ".md", ".markdown", ".mdx", ".rst":
		return mdDeclRe
	case ".yaml", ".yml", ".toml", ".ini", ".cfg":
		return yamlDeclRe
	}
	return anyDeclRe
}

var cDeclReNoKeywords = &matcher{re: cDeclRe, not: cKeywordRe}

type matcher struct{ re, not *lazyre.Regexp }

func (m *matcher) MatchString(s string) bool { return m.re.MatchString(s) && !m.not.MatchString(s) }

func langOf(path string) string {
	switch base := filepath.Base(path); base {
	case "Makefile", "makefile", "GNUmakefile", "Dockerfile":
		return ""
	default:
		return filepath.Ext(base)
	}
}
