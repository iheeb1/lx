// Package hook connects lx to coding agents.
package hook

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type Inspection struct {
	Rewritten string
	Changed   bool
	Reason    string
	Targets   [][]string

	Fits      []int
	FitCuts   []engine.Cut
	Commands  [][]string
	AlreadyLx bool
}

func Rewrite(cmd string) (string, bool) {
	in := Inspect(cmd)
	return in.Rewritten, in.Changed
}

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
		in.Fits = append(in.Fits, t.fit)
		in.FitCuts = append(in.FitCuts, t.fitCut)
	}
	in.Rewritten, in.Changed = splice(cmd, targets, "lx"), true
	return in
}

func splice(src string, targets []*segment, prefix string) string {
	var b strings.Builder
	b.Grow(len(src) + (len(prefix)+12)*len(targets))
	last := 0
	for _, t := range targets {
		at := t.words[t.cmdIdx].start
		b.WriteString(src[last:at])
		b.WriteString(prefix)
		if t.fit > 0 {
			b.WriteString(" --fit ")
			b.WriteString(engine.FitArg(t.fit, t.fitCut))
		}
		b.WriteByte(' ')
		last = at
	}
	b.WriteString(src[last:])
	return b.String()
}

type segment struct {
	*simple
	cmdIdx   int
	argv     []string
	envNames []string
	wrapped  bool
	lxRaw    bool
	isLx     bool
	fit      int
	fitCut   engine.Cut
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

var controlWords = map[string]bool{
	"if": true, "then": true, "else": true, "elif": true, "fi": true,
	"for": true, "while": true, "until": true, "do": true, "done": true,
	"case": true, "esac": true, "in": true, "function": true, "select": true,
	"coproc": true, "{": true, "}": true, "[[": true, "]]": true, "!": true,
}

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
	cutStderr := false
	for _, l := range a.sc.lists {
		if l.bg {
			continue
		}
		for _, p := range l.pipes {
			first := a.bySimp[p.cmds[0]]
			if first.isLx || first.lxRaw || first.cmdIdx < 0 {
				continue
			}
			if cutsLines(p) && !a.stderrMerged(p) {
				cutStderr = true
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

			first.fit, first.fitCut = 0, engine.CutEither
			if cutsLines(p) {
				first.fit, first.fitCut = sliceFit(p)
			}
			out = append(out, first)
		}
	}
	if len(out) == 0 {
		if cutStderr {
			return nil, "a head/tail after it would cut stderr, which lx prints on stdout (write 2>&1 before the pipe)"
		}
		return nil, "no supported command"
	}
	return out, ""
}

func cutsLines(p pipeline) bool {
	for _, c := range p.cmds[1:] {
		if len(c.words) > 0 {
			switch filepath.Base(c.words[0].val) {
			case "head", "tail":
				return true
			}
		}
	}
	return false
}

func sliceFit(p pipeline) (int, engine.Cut) {
	fit, cut, headers := 0, engine.CutEither, 0
	var ends []engine.Cut
	for _, c := range p.cmds[1:] {
		st := stageLines(c)
		if !st.stdin {
			break
		}
		if st.n > 0 {
			n := st.n
			if st.end == engine.CutHead {
				n = max(n-headers, 1)
			}
			if fit == 0 || n < fit {
				fit = n
			}
			ends = append(ends, st.end)
		}
		if st.header {
			headers++
		}
	}
	for i, e := range ends {
		if i == 0 {
			cut = e
		} else if e != cut {
			cut = engine.CutEither
		}
	}
	return fit, cut
}

type stage struct {
	n      int
	end    engine.Cut
	stdin  bool
	header bool
}

func stageLines(c *simple) stage {
	if len(c.words) == 0 {
		return stage{}
	}
	name := filepath.Base(c.words[0].val)
	args := c.words[1:]
	if name == "cat" {
		for _, w := range args {
			if w.val != "-" && !strings.HasPrefix(w.val, "-") {
				return stage{}
			}
		}
		return stage{stdin: true}
	}
	st := stage{n: 10, end: engine.CutHead, stdin: true}
	if name == "tail" {
		st.end = engine.CutTail
	}
	known := true
	count := func(w token) {
		v, ok := lineCount(w)
		st.n, known = v, known && ok
	}
	for i := 0; i < len(args); i++ {
		w := args[i]
		a := w.val
		switch {
		case a == "-":
		case a == "--":
			if i+1 < len(args) {
				return stage{}
			}
		case a == "-n" || a == "--lines":
			if i+1 >= len(args) {
				return stage{stdin: true, header: st.header}
			}
			i++
			count(args[i])
		case strings.HasPrefix(a, "--lines="):
			count(token{val: a[len("--lines="):], expand: w.expand})
		case strings.HasPrefix(a, "-n"):
			count(token{val: a[2:], expand: w.expand})
		case a == "-v" || a == "--verbose":
			st.header = true
		case a == "-q" || a == "--quiet" || a == "--silent":
			st.header = false
		case len(a) > 1 && a[0] == '-' && isDigits(a[1:]):
			count(token{val: a[1:], expand: w.expand})
		case strings.HasPrefix(a, "-"):
			known = false
			if a == "-c" || a == "--bytes" {
				i++
			}
		default:
			return stage{}
		}
	}
	if !known {
		st.n = 0
	}
	return st
}

func lineCount(w token) (int, bool) {
	if w.expand || !isDigits(w.val) {
		return 0, false
	}
	n, err := strconv.Atoi(w.val)
	return n, err == nil && n >= 1
}

func (a *analysis) stderrMerged(p pipeline) bool {
	c := p.cmds[0]
	for _, r := range c.redirs {
		if r.op.text == "2>&" && r.target.text == "1" {
			return true
		}
	}
	return strings.Contains(a.src[c.end:p.cmds[1].start], "|&")
}

func redirsOK(c *simple) bool {
	for _, r := range c.redirs {
		if r.op.text != "2>&" || r.target.text != "1" {
			return false
		}
	}
	return true
}

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
		switch t.text {
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
				break
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
	if name == "LX_MODE" && !modeOK(val) {
		s.lxRaw = true
	}
}

func modeOK(v string) bool {
	_, ok := engine.ParseMode(v)
	return v == "" || ok
}

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
