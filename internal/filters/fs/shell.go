package fs

import (
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

func Effective(c *engine.Context) *engine.Context {
	if c == nil || len(c.Argv) < 3 {
		return c
	}
	switch c.Name() {
	case "sh", "bash", "zsh", "dash", "ksh":
	default:
		return c
	}
	flag := c.Argv[1]
	if len(flag) < 2 || flag[0] != '-' || flag[1] == '-' || !strings.Contains(flag, "c") {
		return c
	}
	for _, ch := range flag[1:] {

		if !strings.ContainsRune("celu", ch) {
			return c
		}
	}
	stages, ok := splitShell(c.Argv[2])
	if !ok || len(stages) == 0 {
		return c
	}
	first := stages[0]
	for len(first) > 0 && isAssignment(first[0]) {
		first = first[1:]
	}
	for len(first) > 0 && (first[0] == "exec" || first[0] == "command") {
		first = first[1:]
	}
	if len(first) == 0 {
		return c
	}
	for _, st := range stages[1:] {
		if !sliceConsumer(st) {
			return c
		}
	}
	return &engine.Context{Argv: first, Exit: c.Exit, Cwd: c.Cwd, Home: c.Home}
}

func sliceConsumer(st []string) bool {
	if len(st) == 0 {
		return false
	}
	args := st[1:]
	switch filepath.Base(st[0]) {
	case "cat":
		for _, a := range args {
			if a != "-" && a != "-u" {
				return false
			}
		}
		return true
	case "head", "tail":
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case a == "-n" || a == "--lines":
				if i+1 >= len(args) || !isCount(args[i+1]) {
					return false
				}
				i++
			case strings.HasPrefix(a, "--lines="):
				if !isCount(a[len("--lines="):]) {
					return false
				}
			case strings.HasPrefix(a, "-n") && isCount(a[2:]),
				strings.HasPrefix(a, "-") && isCount(a[1:]),
				strings.HasPrefix(a, "+") && isCount(a[1:]),
				a == "-q", a == "--quiet", a == "--silent":
			default:
				return false
			}
		}
		return true
	case "sort":
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case a == "-k" || a == "-t" || a == "--key" || a == "--field-separator":
				i++
			case strings.HasPrefix(a, "--key="), strings.HasPrefix(a, "--field-separator="),
				a == "--reverse", a == "--numeric-sort", a == "--human-numeric-sort",
				a == "--general-numeric-sort", a == "--version-sort", a == "--month-sort",
				a == "--unique", a == "--ignore-case", a == "--stable", a == "--ignore-leading-blanks",
				a == "--dictionary-order":
			case len(a) > 1 && a[0] == '-' && a[1] != '-':
				for j := 1; j < len(a); j++ {
					ch := a[j]
					if ch == 'k' || ch == 't' {
						if j == len(a)-1 {
							i++
						}
						break
					}
					if !strings.ContainsRune("bdfghinMrRsuV", rune(ch)) {
						return false
					}
				}
			default:
				return false
			}
		}
		return true
	}
	return false
}

func isCount(s string) bool {
	s = strings.TrimLeft(s, "+-")
	return isDigits(s)
}

func isAssignment(w string) bool {
	eq := strings.IndexByte(w, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		ch := w[i]
		if ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || i > 0 && ch >= '0' && ch <= '9' {
			continue
		}
		return false
	}
	return true
}

func splitShell(s string) ([][]string, bool) {
	var (
		stages [][]string
		words  []string
		cur    strings.Builder
		inWord bool
	)
	endWord := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == ' ' || ch == '\t':
			endWord()
		case ch == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			cur.WriteString(s[i+1 : i+1+j])
			inWord = true
			i += j + 1
		case ch == '"':
			inWord = true
			i++
			for ; i < len(s) && s[i] != '"'; i++ {
				switch s[i] {
				case '$', '`':
					return nil, false
				case '\\':
					if i+1 < len(s) && strings.IndexByte("$`\"\\", s[i+1]) >= 0 {
						i++
					} else if i+1 < len(s) && s[i+1] == '\n' {
						i++
						continue
					}
				}
				cur.WriteByte(s[i])
			}
			if i >= len(s) {
				return nil, false
			}
		case ch == '\\':
			if i+1 >= len(s) || s[i+1] == '\n' {
				return nil, false
			}
			i++
			cur.WriteByte(s[i])
			inWord = true
		case ch == '|':
			if i+1 < len(s) && (s[i+1] == '|' || s[i+1] == '&') {
				return nil, false
			}
			endWord()
			if len(words) == 0 {
				return nil, false
			}
			stages = append(stages, words)
			words = nil
		case ch == '>':

			if !inWord || cur.String() != "2" {
				return nil, false
			}
			switch {
			case strings.HasPrefix(s[i:], ">&1"):
				i += 2
			case strings.HasPrefix(s[i:], ">/dev/null") && (i+10 == len(s) || s[i+10] == ' ' || s[i+10] == '\t' || s[i+10] == '|'):
				i += 9
			default:
				return nil, false
			}
			cur.Reset()
			inWord = false
		case ch == '#' && !inWord:
			return nil, false
		case strings.IndexByte(";&<()`$\n\r{}", ch) >= 0:
			return nil, false
		default:
			cur.WriteByte(ch)
			inWord = true
		}
	}
	endWord()
	if len(words) == 0 {
		return nil, false
	}
	return append(stages, words), true
}

func NoteHasPrefix(e *engine.Context, ln string, names ...string) bool {
	i := strings.Index(ln, ": ")
	if i <= 0 {
		return false
	}
	prog := ln[:i]
	if len(e.Argv) > 0 && prog == e.Argv[0] || prog == e.Name() {
		return true
	}
	for _, n := range names {
		if prog == n {
			return true
		}
	}
	return false
}

func Unexplained(e *engine.Context, notes int) bool {
	return e.Exit != 0 && notes == 0
}
