package data

import (
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

func effective(c *engine.Context) *engine.Context {
	e, _ := unwrap(c)
	return e
}

func unwrap(c *engine.Context) (*engine.Context, bool) {
	if c == nil || len(c.Argv) < 3 {
		return c, false
	}
	switch c.Name() {
	case "sh", "bash", "zsh", "dash", "ksh":
	default:
		return c, false
	}
	flag := c.Argv[1]
	if len(flag) < 2 || flag[0] != '-' || flag[1] == '-' || !strings.Contains(flag, "c") {
		return c, false
	}
	for _, ch := range flag[1:] {
		if !strings.ContainsRune("celxuv", ch) {
			return c, false
		}
	}
	stages, ok := splitShell(c.Argv[2])
	if !ok || len(stages) == 0 {
		return c, false
	}
	for i := range stages {
		st := stages[i]
		for len(st) > 0 && isAssignment(st[0]) {
			st = st[1:]
		}
		for len(st) > 0 && (st[0] == "exec" || st[0] == "command") {
			st = st[1:]
		}
		if len(st) == 0 {
			return c, false
		}
		stages[i] = st
	}

	shape := len(stages) - 1
	for shape > 0 && sliceConsumer(stages[shape]) {
		shape--
	}
	if shape > 0 && !isJQ(filepath.Base(stages[shape][0])) {
		return c, false
	}
	renumbered := false
	for _, st := range stages[shape+1:] {
		if b := filepath.Base(st[0]); b == "tail" || b == "sort" {
			renumbered = true
		}
	}
	return &engine.Context{Argv: stages[shape], Exit: c.Exit, Cwd: c.Cwd, Home: c.Home}, renumbered
}

func isJQ(name string) bool { return name == "jq" || name == "gojq" || name == "jaq" }

func sliceConsumer(st []string) bool {
	switch filepath.Base(st[0]) {
	case "head", "cat", "sort":
		return true
	case "tail":
		return !isFollow(st[1:])
	}
	return false
}

func isFollow(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--follow" || strings.HasPrefix(a, "--follow=") || a == "--retry" {
			return true
		}
		if len(a) > 1 && a[0] == '-' && a[1] != '-' {
			for _, ch := range a[1:] {
				if ch == 'f' || ch == 'F' {
					return true
				}
				if ch == 'n' || ch == 'c' || ch == 's' {
					break
				}
			}
		}
	}
	return false
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

			if !inWord || cur.String() != "2" || !strings.HasPrefix(s[i:], ">&1") {
				return nil, false
			}
			cur.Reset()
			inWord = false
			i += 2
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
