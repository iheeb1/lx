package hook

import "strings"

// This file is a deliberately small POSIX-ish shell lexer. It is not a shell:
// it only has to answer "where are the simple commands in this string, what
// are their words, and is there anything here we cannot reason about?".
// Anything it cannot reason about (substitutions, heredocs, subshells,
// compound commands, multi-line scripts) is reported in unsafe, and callers
// refuse to rewrite. Every token keeps its byte offsets so a rewrite can
// splice "lx " into the original string without re-quoting anything.

type tokKind uint8

const (
	tWord  tokKind = iota
	tOp            // && || ; | |& & ;; ;& newline ( )
	tRedir         // redirection operator, including a leading fd number
)

type token struct {
	kind   tokKind
	start  int    // byte offset of the first byte
	end    int    // byte offset one past the last byte
	text   string // raw source bytes
	val    string // words: value after quote removal (best effort)
	quoted bool   // words: contained quotes or backslash escapes
	expand bool   // words: contains a $ expansion (unquoted or in "…")
}

type lexed struct {
	toks   []token
	unsafe []string // constructs that make the string non-rewritable
	broken bool     // unterminated quote: tokens are unreliable
}

type lexer struct {
	s   string
	out lexed
}

func (l *lexer) flag(reason string) {
	for _, r := range l.out.unsafe {
		if r == reason {
			return
		}
	}
	l.out.unsafe = append(l.out.unsafe, reason)
}

func (l *lexer) emit(k tokKind, start, end int) {
	l.out.toks = append(l.out.toks, token{kind: k, start: start, end: end, text: l.s[start:end]})
}

func lex(s string) lexed {
	l := &lexer{s: s}
	i, n := 0, len(s)
	for i < n {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '\n':
			l.emit(tOp, i, i+1)
			i++
		case c == '#':
			// A comment runs to the end of the line; bash never executes it.
			for i < n && s[i] != '\n' {
				i++
			}
		case c == '&':
			switch {
			case strings.HasPrefix(s[i:], "&&"):
				l.emit(tOp, i, i+2)
				i += 2
			case strings.HasPrefix(s[i:], "&>>"):
				l.emit(tRedir, i, i+3)
				i += 3
			case strings.HasPrefix(s[i:], "&>"):
				l.emit(tRedir, i, i+2)
				i += 2
			default:
				l.emit(tOp, i, i+1)
				i++
			}
		case c == '|':
			if strings.HasPrefix(s[i:], "||") || strings.HasPrefix(s[i:], "|&") {
				l.emit(tOp, i, i+2)
				i += 2
			} else {
				l.emit(tOp, i, i+1)
				i++
			}
		case c == ';':
			switch {
			case strings.HasPrefix(s[i:], ";;&"):
				l.flag("case statement")
				l.emit(tOp, i, i+3)
				i += 3
			case strings.HasPrefix(s[i:], ";;"), strings.HasPrefix(s[i:], ";&"):
				l.flag("case statement")
				l.emit(tOp, i, i+2)
				i += 2
			default:
				l.emit(tOp, i, i+1)
				i++
			}
		case c == '(' || c == ')':
			l.flag("subshell or grouping parenthesis")
			l.emit(tOp, i, i+1)
			i++
		case c == '<' || c == '>':
			i = l.redir(i, i)
		default:
			i = l.word(i)
		}
	}
	return l.out
}

// redir lexes a redirection operator at s[i] ('<' or '>'); start is where a
// leading fd number began (start == i when there is none).
func (l *lexer) redir(start, i int) int {
	rest := l.s[i:]
	size := 1
	switch {
	case strings.HasPrefix(rest, "<<<"):
		l.flag("here-string")
		size = 3
	case strings.HasPrefix(rest, "<<-"):
		l.flag("heredoc")
		size = 3
	case strings.HasPrefix(rest, "<<"):
		l.flag("heredoc")
		size = 2
	case strings.HasPrefix(rest, "<("), strings.HasPrefix(rest, ">("):
		l.flag("process substitution")
		size = 2
	case strings.HasPrefix(rest, "<&"), strings.HasPrefix(rest, ">&"),
		strings.HasPrefix(rest, ">>"), strings.HasPrefix(rest, ">|"), strings.HasPrefix(rest, "<>"):
		size = 2
	}
	l.emit(tRedir, start, i+size)
	return i + size
}

func isWordBreak(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '&', '|', ';', '<', '>', '(', ')':
		return true
	}
	return false
}

func (l *lexer) word(i int) int {
	s, n := l.s, len(l.s)
	start := i
	var b strings.Builder
	quoted, expand := false, false
	for i < n && !isWordBreak(s[i]) {
		c := s[i]
		switch c {
		case '\\':
			if i+1 >= n {
				b.WriteByte('\\')
				i++
				continue
			}
			quoted = true
			if s[i+1] == '\n' { // line continuation: both bytes vanish
				i += 2
				continue
			}
			b.WriteByte(s[i+1])
			i += 2
		case '\'':
			quoted = true
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				l.out.broken = true
				l.flag("unterminated quote")
				b.WriteString(s[i+1:])
				i = n
				continue
			}
			b.WriteString(s[i+1 : i+1+j])
			i += j + 2
		case '"':
			quoted = true
			var e bool
			i, e = l.double(i+1, &b)
			expand = expand || e
		case '`':
			l.flag("command substitution")
			b.WriteByte(c)
			i++
		case '$':
			if i+1 < n {
				switch s[i+1] {
				case '(':
					l.flag("command substitution")
				case '\'': // ANSI-C quoting: $'…' with backslash escapes
					quoted = true
					j := i + 2
					for j < n && s[j] != '\'' {
						if s[j] == '\\' {
							j++
						}
						j++
					}
					if j >= n {
						l.out.broken = true
						l.flag("unterminated quote")
						b.WriteString(s[i+2:])
						i = n
						continue
					}
					b.WriteString(s[i+2 : j])
					i = j + 1
					continue
				case '"': // $"…" locale string: same as "…"
					i++
					continue
				case '{':
					expand = true
					j, bad := braceEnd(s, i+1)
					if bad {
						l.flag("command substitution")
					}
					b.WriteString(s[i:j])
					i = j
					continue
				}
			}
			expand = true
			b.WriteByte('$')
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	// "2>file", "10<&0": an all-digit unquoted word glued to < or > is an fd.
	if i < n && (s[i] == '<' || s[i] == '>') && !quoted && i > start && isDigits(s[start:i]) {
		return l.redir(start, i)
	}
	l.out.toks = append(l.out.toks, token{
		kind: tWord, start: start, end: i, text: s[start:i],
		val: b.String(), quoted: quoted, expand: expand,
	})
	return i
}

// double consumes a "…" string starting just after the opening quote and
// returns the index after the closing quote.
func (l *lexer) double(i int, b *strings.Builder) (int, bool) {
	s, n := l.s, len(l.s)
	expand := false
	for i < n {
		c := s[i]
		switch c {
		case '"':
			return i + 1, expand
		case '\\':
			if i+1 < n {
				switch s[i+1] {
				case '$', '`', '"', '\\':
					b.WriteByte(s[i+1])
					i += 2
					continue
				case '\n':
					i += 2
					continue
				}
			}
			b.WriteByte('\\')
			i++
		case '`':
			l.flag("command substitution")
			b.WriteByte(c)
			i++
		case '$':
			if i+1 < n && s[i+1] == '(' {
				l.flag("command substitution")
			}
			expand = true
			b.WriteByte(c)
			i++
		default:
			b.WriteByte(c)
			i++
		}
	}
	l.out.broken = true
	l.flag("unterminated quote")
	return n, expand
}

// braceEnd returns the index just past the '}' matching the '{' at s[i] of a
// ${…} expansion, and whether the expansion hides a command substitution.
func braceEnd(s string, i int) (int, bool) {
	depth := 0
	bad := false
	for j := i; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '`':
			bad = true
		case '$':
			if j+1 < len(s) && s[j+1] == '(' {
				bad = true
			}
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return j + 1, bad
			}
		}
	}
	return len(s), true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ---- structure ----

type redirect struct{ op, target token }

// simple is one simple command: words and redirections between operators.
type simple struct {
	words  []token
	redirs []redirect
	start  int
	end    int
}

func (c *simple) empty() bool { return len(c.words) == 0 && len(c.redirs) == 0 }

func (c *simple) span(t token) {
	if c.empty() {
		c.start = t.start
	}
	c.end = t.end
}

type pipeline struct{ cmds []*simple }

// andOr is a list of pipelines joined by && / ||, ended by ; & or newline.
// A trailing & backgrounds the whole list. ops[k] ("&&" or "||") joins
// pipes[k] and pipes[k+1].
type andOr struct {
	pipes []pipeline
	ops   []string
	bg    bool
}

type script struct {
	lists     []andOr
	syntaxErr bool
}

func parse(toks []token) script {
	var sc script
	cur := &simple{}
	var pipe pipeline
	var list andOr
	needCmd := false // after | && || a command is mandatory

	closeList := func(bg bool) {
		pipe.cmds = append(pipe.cmds, cur)
		list.pipes = append(list.pipes, pipe)
		list.bg = bg
		sc.lists = append(sc.lists, list)
		cur, pipe, list = &simple{}, pipeline{}, andOr{}
	}

	for k := 0; k < len(toks); k++ {
		t := toks[k]
		switch t.kind {
		case tWord:
			cur.span(t)
			cur.words = append(cur.words, t)
			needCmd = false
		case tRedir:
			if k+1 >= len(toks) || toks[k+1].kind != tWord {
				sc.syntaxErr = true
				continue
			}
			cur.span(t)
			cur.span(toks[k+1])
			cur.redirs = append(cur.redirs, redirect{op: t, target: toks[k+1]})
			k++
			needCmd = false
		case tOp:
			switch t.text {
			case "|", "|&":
				if cur.empty() {
					sc.syntaxErr = true
					continue
				}
				pipe.cmds = append(pipe.cmds, cur)
				cur = &simple{}
				needCmd = true
			case "&&", "||":
				if cur.empty() {
					sc.syntaxErr = true
					continue
				}
				pipe.cmds = append(pipe.cmds, cur)
				list.pipes = append(list.pipes, pipe)
				list.ops = append(list.ops, t.text)
				cur, pipe = &simple{}, pipeline{}
				needCmd = true
			default: // ; & newline ;; ;& ( )
				if cur.empty() {
					if needCmd && t.text != "\n" {
						sc.syntaxErr = true
					} else if !needCmd && (t.text == ";" || t.text == "&") {
						sc.syntaxErr = true // "; x", "a; ;"
					}
					continue
				}
				closeList(t.text == "&")
				needCmd = false
			}
		}
	}
	if needCmd {
		sc.syntaxErr = true
	}
	if !cur.empty() {
		closeList(false)
	}
	return sc
}
