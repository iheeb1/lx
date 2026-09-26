package data

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// window describes how an over-budget text is cut.
type window struct {
	// path is the one file the text came from, when output line N is that
	// file's line N; the omission marker then says how to read the range
	// with sed. Empty: the marker points at lx show.
	path string
	// lang picks the outline patterns (a file extension such as ".go", or ""
	// to detect declarations of any common language).
	lang string
	// keep reports lines that must survive even inside the omitted range
	// (the tool's own diagnostics); they are listed with their line numbers.
	keep func(string) bool
	// keepIdx lists more lines (by index) to list inside the omitted range:
	// the whole of small merge-conflict hunks.
	keepIdx map[int]bool
	// nums, when set, holds the 1-based output line number of each line
	// (a response body's lines are not the output's first lines, and need
	// not be contiguous in it); nil numbers lines 1, 2, ….
	nums                []int
	head, tail, outline int // token allowances
}

const (
	windowHead    = 2500
	windowTail    = 700
	windowOutline = 2500
	// maxShownLine: longer lines (minified code, data blobs) are shortened
	// with an explicit "…[+N chars]…" marker so one line cannot eat the view.
	maxShownLine = 2000
	// outlineCut: outline entries are cut here (with "…"); they point at the
	// real line, which the marker says how to read.
	outlineCut = 120
)

func defaultWindow(path, lang string, keep func(string) bool) window {
	return window{path: path, lang: lang, keep: keep, head: windowHead, tail: windowTail, outline: windowOutline}
}

// apply cuts lines to exact head and tail windows plus, for the omitted
// middle, an outline of its declarations (and every keep line), each with
// its line number. Kept lines are byte-for-byte the input lines, except that
// lines over 2000 bytes are shortened with a counted marker. When the
// windows would cover everything it returns all lines (long ones shortened).
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
	// Head window [0, h).
	h, used := 0, 0
	for h < n && used+cost[h] <= w.head {
		used += cost[h]
		h++
	}
	if h == 0 && n > 0 {
		h = 1 // always show the first line, even a huge one
	}
	// Tail window [t, n).
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

// snapHead moves the head cut back to a natural boundary within the last
// quarter of the window: preferably just before an unindented line that
// follows a blank line or a closing brace (a new top-level block), else just
// after any blank line.
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

// snapTail starts the tail window at a natural boundary: it extends the
// window back (by at most half its allowance) to the nearest unindented
// declaration and the comment block above it, or else moves the cut forward
// to just after a blank line within the first quarter of the window.
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

// isCommentLine reports a doc-comment or decorator line above a declaration.
func isCommentLine(ln string) bool {
	t := strings.TrimSpace(ln)
	return strings.HasPrefix(t, "//") || strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "*") ||
		strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "#include") || strings.HasPrefix(t, "@") || strings.HasPrefix(t, "///")
}

// num returns the 1-based output line number of lines[i].
func (w window) num(i int) int {
	if i < len(w.nums) {
		return w.nums[i]
	}
	return i + 1
}

// marker names the omitted range [a, b] (1-based, inclusive) and how to
// read it.
func (w window) marker(a, b int) string {
	how := fmt.Sprintf("lx show <id> --lines %d-%d, <id> is on the last line", a, b)
	if w.path != "" {
		how = fmt.Sprintf("sed -n '%d,%dp' %s", a, b, shellQuote(w.path))
	}
	return fmt.Sprintf("… lines %d-%d omitted (%s; read them: %s)", a, b, engine.Plural(b-a+1, "line", "lines"), how)
}

// outlineOf lists, for lines [a, b), the declarations and keep lines with
// their 1-based line numbers, within the outline token allowance. keep
// lines are always listed; declarations past the allowance are counted,
// with the range they fall in.
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
			// A diagnostic glued to a long data line: ShortenLine keeps
			// the line's end, where the diagnostic is.
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

// shellQuote quotes a path for a copy-pasteable command.
func shellQuote(p string) string {
	for _, ch := range p {
		if !(ch == '/' || ch == '.' || ch == '_' || ch == '-' || ch == '+' || ch == ',' || ch == '@' || ch == ':' ||
			ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			return "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
		}
	}
	return p
}

// Declaration patterns for outlines. They look at unindented (or, for
// class members, lightly indented) lines only.
var (
	goDeclRe   = regexp.MustCompile(`^(?:func|type|var|const)\b`)
	jsDeclRe   = regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:declare\s+)?(?:async\s+)?(?:function\*?|class|interface|type|enum|const|let|var|abstract\s+class|namespace)\s|^export\s+(?:default\b|\{|\*)|^module\.exports\b`)
	pyDeclRe   = regexp.MustCompile(`^(?:    )?(?:async\s+)?(?:def|class)\s|^@\w`)
	rsDeclRe   = regexp.MustCompile(`^\s{0,4}(?:pub(?:\([\w:]+\))?\s+)?(?:async\s+)?(?:unsafe\s+)?(?:const\s+)?(?:fn|struct|enum|trait|impl|mod|type|static|union|macro_rules!)\b|^impl\b`)
	jvmDeclRe  = regexp.MustCompile(`^\s{0,4}(?:@\w+\s+)*(?:(?:public|private|protected|internal|static|final|abstract|sealed|open|data|override|suspend|inline|virtual|partial|readonly|async|export)\s+)*(?:class|interface|enum|record|object|fun|struct|namespace|trait)\s|^\s{0,4}(?:public|private|protected|internal)\s+(?:static\s+)?(?:final\s+)?[\w<>\[\],.? ]+\s+\w+\s*\(`)
	cDeclRe    = regexp.MustCompile(`^(?:static\s+|extern\s+|inline\s+|const\s+|unsigned\s+|struct\s+|enum\s+)*[A-Za-z_][\w*() ]*?[\s*)]\**[A-Za-z_]\w*\s*\([^;]*$|^(?:typedef|struct|union|enum|class|namespace|template)\b|^#define\s+\w+\(`)
	rbDeclRe   = regexp.MustCompile(`^\s{0,4}(?:def|class|module)\s`)
	phpDeclRe  = regexp.MustCompile(`^\s{0,4}(?:(?:abstract|final|public|private|protected|static|readonly)\s+)*(?:function|class|interface|trait|enum)\s`)
	shDeclRe   = regexp.MustCompile(`^(?:function\s+)?[\w.:-]+\s*\(\)\s*\{?\s*$|^function\s+[\w.:-]+`)
	mdDeclRe   = regexp.MustCompile(`^#{1,6}\s+\S`)
	yamlDeclRe = regexp.MustCompile(`^[\w.-]+:(?:\s|$)`)
	anyDeclRe  = regexp.MustCompile(`^(?:export\s+|pub\s+|public\s+|private\s+|static\s+|async\s+|abstract\s+)*(?:func|function|def|class|interface|type|struct|enum|trait|impl|module|fn|namespace)\s+\w|^#{1,6}\s+\S`)
	cKeywordRe = regexp.MustCompile(`^(?:if|for|while|switch|return|else|do|goto|case|sizeof)\b`)
)

// lineMatcher is a *regexp.Regexp or a pattern with an exclusion.
type lineMatcher interface{ MatchString(string) bool }

// outlineRe returns the declaration pattern for a file extension.
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

// cDeclReNoKeywords is cDeclRe minus control-flow statements that happen to
// start at column 0 (rare, but "if (x)" at column 0 is not a declaration).
var cDeclReNoKeywords = &matcher{re: cDeclRe, not: cKeywordRe}

// matcher lets outlineRe return a pattern with an exclusion.
type matcher struct{ re, not *regexp.Regexp }

func (m *matcher) MatchString(s string) bool { return m.re.MatchString(s) && !m.not.MatchString(s) }

// langOf returns the extension used to pick outline patterns.
func langOf(path string) string {
	switch base := filepath.Base(path); base {
	case "Makefile", "makefile", "GNUmakefile", "Dockerfile":
		return ""
	default:
		return filepath.Ext(base)
	}
}
