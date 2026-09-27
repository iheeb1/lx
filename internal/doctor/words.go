package doctor

import (
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/hook"
	"github.com/iheeb1/lx/internal/lazyre"
)

// hookKind is how doctor classifies a PreToolUse hook command.
type hookKind int

const (
	notLx      hookKind = iota
	lxVerified          // exactly `<…/lx> hook claude [--readonly] [--prefix V]`, plain words only
	lxOther             // mentions `lx hook` but is not that exact form: never executed
)

// lxHookCmd is a verified lx hook command, parsed.
type lxHookCmd struct {
	bin       string   // argv[0] after quote removal and ~ / $HOME expansion
	args      []string // argv[1:]: hook claude [flags]
	readOnly  bool
	prefix    string
	hasPrefix bool
}

// classify decides whether cmd is lx's own Claude Code hook. Only the
// lxVerified kind is ever executed by doctor, and that kind is, by
// construction, a single simple command of literal words: `sh -c cmd` can
// do nothing but exec argv[0] with those arguments. (Doctor does not even
// rely on that: it execs the parsed argv itself, without a shell.)
func classify(cmd, home string) (lxHookCmd, hookKind) {
	if words, ok := shellWords(cmd, home); ok {
		if h, ok := parseLxHookArgv(words); ok {
			return h, lxVerified
		}
	}
	if mentionsLxHook(cmd) {
		return lxHookCmd{}, lxOther
	}
	return lxHookCmd{}, notLx
}

// parseLxHookArgv accepts `lx hook claude` followed only by --readonly,
// --prefix V or --prefix=V (the flags `lx init` may write).
func parseLxHookArgv(w []string) (lxHookCmd, bool) {
	if len(w) < 3 || filepath.Base(w[0]) != "lx" || strings.Contains(w[0], "=") || w[1] != "hook" || w[2] != "claude" {
		return lxHookCmd{}, false
	}
	h := lxHookCmd{bin: w[0], args: w[1:]}
	for i := 3; i < len(w); i++ {
		switch a := w[i]; {
		case a == "--readonly":
			h.readOnly = true
		case a == "--prefix":
			if i+1 >= len(w) || w[i+1] == "" {
				return lxHookCmd{}, false
			}
			i++
			h.prefix, h.hasPrefix = w[i], true
		case strings.HasPrefix(a, "--prefix=") && len(a) > len("--prefix="):
			h.prefix, h.hasPrefix = strings.TrimPrefix(a, "--prefix="), true
		default:
			return lxHookCmd{}, false
		}
	}
	return h, true
}

var reLxHookText = lazyre.New(`(^|[\s/'"])lx['"]?\s+['"]?hook\b`)

// mentionsLxHook reports whether some command in cmd runs `lx hook …`,
// whatever surrounds it. It never leads to execution; it only keeps doctor
// from saying "no lx hook" about a hook it merely cannot verify.
func mentionsLxHook(cmd string) (found bool) {
	if reLxHookText.MatchString(cmd) {
		return true
	}
	defer func() {
		if recover() != nil {
			found = false
		}
	}()
	for _, argv := range hook.Inspect(cmd).Commands {
		if len(argv) >= 2 && filepath.Base(argv[0]) == "lx" && argv[1] == "hook" {
			return true
		}
	}
	return false
}

// shellWords parses cmd as one simple command made only of literal words.
// It accepts unquoted characters from a safe set, backslash-escaped
// characters, '…' strings, "…" strings without expansions or escapes, a
// leading ~ or ~/, and $HOME / ${HOME} (replaced by home). It returns
// ok=false for anything else a shell would interpret: operators
// (; & | && ||), redirections, other expansions ($x, $(…), `…`), globs,
// comments, newlines, brace expansion and ~user.
func shellWords(cmd, home string) (words []string, ok bool) {
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	// expandHome handles $HOME or ${HOME} at s[i] ('$'); it returns the
	// number of bytes consumed, or 0 if this is some other expansion.
	expandHome := func(s string, i int, quoted bool) int {
		n := 0
		switch rest := s[i+1:]; {
		case strings.HasPrefix(rest, "{HOME}"):
			n = len("${HOME}")
		case strings.HasPrefix(rest, "HOME") && (len(rest) == 4 || !isNameByte(rest[4])):
			n = len("$HOME")
		default:
			return 0
		}
		if home == "" || (!quoted && strings.ContainsAny(home, " \t\n*?[")) {
			return 0 // unquoted, the shell would split or glob it
		}
		cur.WriteString(home)
		return n
	}
	for i := 0; i < len(cmd); {
		c := cmd[i]
		switch {
		case c == ' ' || c == '\t':
			flush()
			i++
		case c == '\'':
			j := strings.IndexByte(cmd[i+1:], '\'')
			if j < 0 {
				return nil, false
			}
			cur.WriteString(cmd[i+1 : i+1+j])
			inWord = true
			i += j + 2
		case c == '"':
			j := i + 1
			for {
				if j >= len(cmd) {
					return nil, false
				}
				d := cmd[j]
				if d == '"' {
					break
				}
				switch d {
				case '`', '\\', '!':
					return nil, false
				case '$':
					n := expandHome(cmd, j, true)
					if n == 0 {
						return nil, false
					}
					j += n
					continue
				}
				cur.WriteByte(d)
				j++
			}
			inWord = true
			i = j + 1
		case c == '\\':
			// An escaped character is literal (as `lx init` quotes a ').
			if i+1 >= len(cmd) || cmd[i+1] == '\n' {
				return nil, false
			}
			// In a legacy multibyte locale (Shift_JIS, GBK, Big5) a
			// backslash can be the second byte of a character, and one
			// before a non-ASCII byte escapes a whole character: `ぁ\;x`
			// is then `ぁ\` `;` `x` to the shell. Refuse rather than
			// guess the shell's locale.
			if cmd[i+1] >= 0x80 || (i > 0 && cmd[i-1] >= 0x80) {
				return nil, false
			}
			cur.WriteByte(cmd[i+1])
			inWord = true
			i += 2
		case c == '$':
			n := expandHome(cmd, i, false)
			if n == 0 {
				return nil, false
			}
			inWord = true
			i += n
		case c == '~' && !inWord:
			// Tilde expansion: only ~ alone or ~/…; ~user is refused.
			if i+1 < len(cmd) && cmd[i+1] != '/' && cmd[i+1] != ' ' && cmd[i+1] != '\t' {
				return nil, false
			}
			if home == "" {
				return nil, false
			}
			cur.WriteString(home)
			inWord = true
			i++
		case safeByte(c):
			cur.WriteByte(c)
			inWord = true
			i++
		default:
			return nil, false
		}
	}
	flush()
	return words, len(words) > 0
}

// safeByte: characters a POSIX shell treats literally outside quotes.
func safeByte(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	case c >= 0x80: // UTF-8 path bytes
		return true
	}
	return strings.IndexByte("_@%+=:,./-~", c) >= 0
}

func isNameByte(c byte) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

var reShellSafe = lazyre.New(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellQuote quotes s for a POSIX shell (the same rule `lx init` uses).
func shellQuote(s string) string {
	if reShellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
