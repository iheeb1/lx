// Package hook integrates lx with coding agents: it rewrites the shell
// commands an agent is about to run so supported ones go through lx, answers
// Claude Code's PreToolUse hook (honoring the user's permission rules), and
// installs that hook into settings.json without disturbing anything else.
package hook

import (
	"path/filepath"
	"strings"
)

// Inspection is how Rewrite sees a command. Reporting tools (lx discover,
// lx rewrite -v) use it; hooks only need Rewrite.
type Inspection struct {
	Rewritten string     // the command with "lx " spliced in (== input when !Changed)
	Changed   bool       // at least one command was prefixed
	Reason    string     // why the whole string was left alone, if it was
	Targets   [][]string // argv (after env/wrapper peeling) of each command lx will wrap
	Commands  [][]string // argv of every simple command, best effort even when unsafe
	AlreadyLx bool       // some command already runs through lx
}

// Rewrite returns cmd with "lx " inserted before each command lx supports,
// preserving every other byte. ok is false when nothing changes.
func Rewrite(cmd string) (string, bool) {
	in := Inspect(cmd)
	return in.Rewritten, in.Changed
}

// Inspect analyzes cmd the same way Rewrite does and reports the details.
func Inspect(cmd string) Inspection {
	a := analyze(cmd)
	in := Inspection{Rewritten: cmd}
	for _, s := range a.segs {
		if len(s.argv) > 0 {
			in.Commands = append(in.Commands, s.argv)
		}
		if s.isLx {
			in.AlreadyLx = true
		}
	}
	targets, reason := a.plan()
	in.Reason = reason
	if len(targets) == 0 {
		return in
	}
	for _, t := range targets {
		in.Targets = append(in.Targets, t.argv)
	}
	in.Rewritten, in.Changed = splice(cmd, targets), true
	return in
}

// splice inserts "lx " before each target's command word.
func splice(src string, targets []*segment) string {
	var b strings.Builder
	b.Grow(len(src) + 3*len(targets))
	last := 0
	for _, t := range targets {
		at := t.words[t.cmdIdx].start
		b.WriteString(src[last:at])
		b.WriteString("lx ")
		last = at
	}
	b.WriteString(src[last:])
	return b.String()
}

// segment is a simple command plus what peeling learned about it.
type segment struct {
	*simple
	cmdIdx   int      // index in words of the command word, -1 if none
	argv     []string // word values from cmdIdx on (redirections excluded)
	envNames []string // peeled NAME=value assignments (incl. those after env)
	wrapped  bool     // a wrapper (time, timeout, nice, …) was peeled
	lxRaw    bool     // LX_RAW / LX_OFF assignment: user wants the raw command
	isLx     bool     // the command word is lx itself
}

type analysis struct {
	src    string
	lx     lexed
	sc     script
	segs   []*segment
	bySimp map[*simple]*segment
	unsafe []string
}

func analyze(src string) *analysis {
	a := &analysis{src: src, lx: lex(src)}
	a.unsafe = append(a.unsafe, a.lx.unsafe...)
	// Multi-line: a newline outside quotes followed by more tokens.
	for k, t := range a.lx.toks {
		if t.kind == tOp && t.text == "\n" {
			for _, u := range a.lx.toks[k+1:] {
				if !(u.kind == tOp && u.text == "\n") {
					a.unsafe = append(a.unsafe, "multi-line script")
					break
				}
			}
			break
		}
	}
	a.sc = parse(a.lx.toks)
	a.bySimp = map[*simple]*segment{}
	for _, l := range a.sc.lists {
		for _, p := range l.pipes {
			for _, c := range p.cmds {
				s := peel(c)
				a.segs = append(a.segs, s)
				a.bySimp[c] = s
				if len(c.words) > 0 {
					if kw := c.words[0].text; controlWords[kw] {
						a.unsafe = append(a.unsafe, "shell keyword "+kw)
					}
				}
			}
		}
	}
	return a
}

// controlWords start compound commands whose bodies this lexer does not
// model (and in which && < > can mean something else, as in [[ a < b ]]).
var controlWords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"for": true, "while": true, "until": true, "do": true, "done": true,
	"case": true, "esac": true, "in": true, "function": true, "select": true,
	"coproc": true, "{": true, "}": true, "[[": true, "]]": true, "!": true,
}

// plan picks the commands to prefix. A non-empty reason means the whole
// string is off limits.
func (a *analysis) plan() ([]*segment, string) {
	switch {
	case strings.TrimSpace(a.src) == "":
		return nil, "empty command"
	case a.lx.broken:
		return nil, "unterminated quote"
	case len(a.unsafe) > 0:
		return nil, a.unsafe[0]
	case a.sc.syntaxErr:
		return nil, "shell syntax error"
	}
	var out []*segment
	for _, l := range a.sc.lists {
		if l.bg {
			continue // backgrounded: output interleaves, lx must not buffer it
		}
		for _, p := range l.pipes {
			first := a.bySimp[p.cmds[0]]
			if first.isLx || first.lxRaw || first.cmdIdx < 0 {
				continue
			}
			ok := true
			for k, c := range p.cmds {
				if !redirsOK(c) || (k > 0 && !downstreamOK(c)) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			cw := first.words[first.cmdIdx]
			if cw.expand || strings.ContainsAny(cw.val, "*?[") || !Supported(first.argv) {
				continue
			}
			out = append(out, first)
		}
	}
	if len(out) == 0 {
		return nil, "no supported command"
	}
	return out, ""
}

// redirsOK allows only 2>&1: any other redirection sends output somewhere
// other than the agent (or reads input from a file), so lx stays out.
func redirsOK(c *simple) bool {
	for _, r := range c.redirs {
		if r.op.text != "2>&" || r.target.text != "1" {
			return false
		}
	}
	return true
}

// downstreamOK: pipeline stages after the first may only be head, tail
// (not following) or cat — consumers that just show a slice of lines, so
// condensing upstream cannot change what they compute.
func downstreamOK(c *simple) bool {
	if len(c.words) == 0 {
		return false
	}
	w := c.words[0]
	if w.expand {
		return false
	}
	args := vals(c.words[1:])
	switch filepath.Base(w.val) {
	case "head", "cat":
		return true
	case "tail":
		return !tailFollows(args)
	}
	return false
}

func tailFollows(args []string) bool {
	for _, a := range args {
		if a == "--follow" || strings.HasPrefix(a, "--follow=") || a == "--retry" {
			return true
		}
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "fF") {
			return true
		}
	}
	return false
}

func vals(ts []token) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.val
	}
	return out
}

// peel skips env assignments and transparent wrappers to find the command
// word. A wrapper whose options it cannot parse is left as the command word
// (and is then simply not supported).
func peel(c *simple) *segment {
	s := &segment{simple: c, cmdIdx: -1}
	w := c.words
	i := 0
	for i < len(w) {
		t := w[i]
		if name, val, ok := assignment(t); ok {
			s.addEnv(name, val)
			i++
			continue
		}
		next, ok := i, false
		switch t.text { // raw text: a quoted "time" is not the keyword
		case "time":
			next, ok = i+1, true
			for next < len(w) && (w[next].text == "-p" || w[next].text == "--") {
				next++
			}
		case "nohup", "noglob":
			next, ok = i+1, true
		case "command":
			if i+1 < len(w) && !strings.HasPrefix(w[i+1].val, "-") {
				next, ok = i+1, true
			}
		case "nice":
			next, ok = skipNice(w, i+1)
		case "timeout":
			next, ok = skipTimeout(w, i+1)
		case "env":
			j := i + 1
			for j < len(w) {
				name, val, isAssign := assignment(w[j])
				if !isAssign {
					break
				}
				s.addEnv(name, val)
				j++
			}
			if j < len(w) && strings.HasPrefix(w[j].val, "-") {
				break // env -i, env -u X, env -S …: not transparent
			}
			next, ok = j, true
		}
		if !ok {
			break
		}
		s.wrapped = true
		i = next
	}
	if i < len(w) {
		s.cmdIdx = i
		s.argv = vals(w[i:])
		s.isLx = filepath.Base(w[i].val) == "lx"
	}
	return s
}

func (s *segment) addEnv(name, val string) {
	s.envNames = append(s.envNames, name)
	if (name == "LX_RAW" || name == "LX_OFF") && val != "" && val != "0" {
		s.lxRaw = true
	}
}

// assignment recognizes NAME=value (and NAME+=value). The name must be
// unquoted literal text; the value may be quoted.
func assignment(t token) (name, val string, ok bool) {
	raw := t.text
	i := 0
	for i < len(raw) {
		c := raw[i]
		if c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9' {
			i++
			continue
		}
		break
	}
	if i == 0 || i >= len(raw) {
		return "", "", false
	}
	nameEnd := i
	if raw[i] == '+' {
		i++
	}
	if i >= len(raw) || raw[i] != '=' {
		return "", "", false
	}
	name = raw[:nameEnd]
	if k := strings.IndexByte(t.val, '='); k >= 0 {
		val = t.val[k+1:]
	}
	return name, val, true
}

// skipNice parses nice's options: -n N, -nN, -N, --adjustment[=]N.
func skipNice(w []token, i int) (int, bool) {
	for i < len(w) {
		a := w[i].val
		switch {
		case a == "--":
			return i + 1, true
		case a == "-n" || a == "--adjustment":
			i += 2
		case strings.HasPrefix(a, "--adjustment="), strings.HasPrefix(a, "-n") && len(a) > 2,
			len(a) > 1 && a[0] == '-' && isDigits(strings.TrimPrefix(a[1:], "-")):
			i++
		case strings.HasPrefix(a, "-"):
			return i, false
		default:
			return i, true
		}
	}
	return i, true
}

// skipTimeout parses timeout's options and its mandatory DURATION.
func skipTimeout(w []token, i int) (int, bool) {
	for i < len(w) {
		a := w[i].val
		switch {
		case a == "--":
			i++
		case a == "-s" || a == "--signal" || a == "-k" || a == "--kill-after":
			i += 2
			continue
		case strings.HasPrefix(a, "--signal="), strings.HasPrefix(a, "--kill-after="),
			a == "--preserve-status", a == "--foreground", a == "-v", a == "--verbose",
			strings.HasPrefix(a, "-s") && len(a) > 2, strings.HasPrefix(a, "-k") && len(a) > 2:
			i++
			continue
		case strings.HasPrefix(a, "-"):
			return i, false
		}
		if i < len(w) && isDuration(w[i].val) {
			return i + 1, true
		}
		return i, false
	}
	return i, false
}

func isDuration(s string) bool {
	s = strings.TrimRight(s, "smhd")
	if s == "" {
		return false
	}
	dot := false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] >= '0' && s[i] <= '9':
		case s[i] == '.' && !dot:
			dot = true
		default:
			return false
		}
	}
	return true
}
