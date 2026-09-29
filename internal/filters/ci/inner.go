package ci

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

const smallBody = 30

// resolves to runLog, so GenericShape skips detection
var genericArgv = []string{"gh", "run", "view", "--log"}

// body[vouched:] is covered by a Guarded filter's own error guarantee.
func viewOf(c *engine.Context, argv, body []string, exit int, allowTasks bool) (view []string, vouched int, known bool) {
	if len(body) == 0 {
		return nil, 0, false
	}
	text := strings.Join(body, "\n")
	ctx := func(a []string) *engine.Context {
		return &engine.Context{Argv: a, Exit: exit, Cwd: c.Cwd, Home: c.Home, Budget: c.Budget, Mode: c.Mode}
	}
	if allowTasks && detectTasks(text) {
		if res, ok := safeApply(tasksFilter{}, ctx([]string{"monorepo"}), text); ok {
			return splitLines(res), 0, true
		}
	}
	if len(argv) > 0 {
		ic := ctx(argv)
		if !engine.MachineReadableAny(ic) {
			if f, fc := engine.Resolve(ic); f != nil && !isContent(f) && !own(f) {
				if res, ok := safeApply(f, fc, text); ok {
					if guards(f) {
						return splitLines(res), 0, true
					}
					res, _ = engine.Guard(text, res)
					return splitLines(res), len(body), true
				}
			}
		}
	}
	if kept, verdicts, n := splitMage(body); n > 0 {
		if v, from, ok := viewOf(c, nil, kept, exit, false); ok && from == 0 {
			v = append([]string{fmt.Sprintf("[lx: %s of mage's command echo (exec: …) not shown]", plural(n, "line", "lines"))}, v...)
			return append(v, verdicts...), 0, true
		}
	}
	for _, d := range engine.Detectors() {
		if d.Name == "monorepo" || !safeDetect(d, text) {
			continue
		}
		if res, ok := safeApply(d.Filter, ctx(d.Argv), text); ok {
			if guards(d.Filter) {
				return splitLines(res), 0, true
			}
			res, _ = engine.Guard(text, res)
			return splitLines(res), len(body), true
		}
	}
	if v, from, ok := echoed(body, ctx); ok {
		return v, from, true
	}
	if len(body) <= smallBody {
		return similar(body, text), len(body), false
	}
	return genericView(c, body, text, exit), len(body), false
}

var mageVerdictRe = lazyre.New(`^Error: running ".*" failed with exit code \d+$`)

// mage -v prints its "Error: running" verdict mid-report
func splitMage(body []string) (kept, verdicts []string, echoes int) {
	for _, b := range body {
		if strings.HasPrefix(b, "exec: ") {
			echoes++
		}
	}
	if echoes < 10 {
		return body, nil, 0
	}
	kept = make([]string, 0, len(body)-echoes)
	for _, b := range body {
		switch {
		case mageVerdictRe.MatchString(b):
			verdicts = append(verdicts, b)
		case !strings.HasPrefix(b, "exec: "):
			kept = append(kept, b)
		}
	}
	return kept, verdicts, echoes
}

func guards(f engine.Filter) bool {
	g, ok := f.(engine.Guarded)
	return ok && g.GuardsErrors()
}

func similar(body []string, text string) []string {
	if len(body) < 4 {
		return body
	}
	lines := engine.CollapseSimilar(engine.CollapseRuns(append([]string(nil), body...)))
	if len(lines) == len(body) || len(engine.MissingErrorLines(text, strings.Join(lines, "\n"))) > 0 {
		return body
	}
	return lines
}

// command echo of tox, nox, yarn/pnpm scripts and set -x
var echoRe = lazyre.New(`^(?:[\w.-]+: commands(?:_pre|_post)?\[\d+\]> |nox > |\$ |\+ )(\S.*)$`)

func echoed(body []string, ctx func([]string) *engine.Context) ([]string, int, bool) {
	for k := len(body) - 1; k >= 0; k-- {
		m := echoRe.FindStringSubmatch(body[k])
		if m == nil {
			continue
		}
		argv, ok := shellWords(m[1])
		if !ok {
			continue
		}
		ic := ctx(argv)
		if engine.MachineReadableAny(ic) {
			continue
		}
		f, fc := engine.Resolve(ic)
		if f == nil || isContent(f) || own(f) {
			continue
		}
		tool := strings.Join(body[k+1:], "\n")
		res, ok := safeApply(f, fc, tool)
		if !ok {
			continue
		}
		from := k + 1
		if !guards(f) {
			res, _ = engine.Guard(tool, res)
			from = len(body)
		}
		return append(runnerHead(body[:k]), append([]string{body[k]}, splitLines(res)...)...), from, true
	}
	return nil, 0, false
}

func runnerHead(head []string) []string {
	var errs []string
	for _, h := range head {
		if engine.IsError(h) {
			errs = append(errs, h)
		}
	}
	if len(errs) == 0 && len(head) > 3 {
		return []string{fmt.Sprintf("[lx: %s of runner setup before the command not shown]", plural(len(head), "line", "lines"))}
	}
	if len(head) > smallBody {
		return genericView(nil, head, strings.Join(head, "\n"), 0)
	}
	return similar(head, strings.Join(head, "\n"))
}

func genericView(c *engine.Context, body []string, text string, exit int) []string {
	gc := &engine.Context{Argv: genericArgv, Exit: exit}
	if c != nil {
		gc.Cwd, gc.Home, gc.Mode = c.Cwd, c.Home, c.Mode
	}
	if g, shape := engine.GenericShape(gc, text); shape != "json" && shape != "paths" && len(engine.MissingErrorLines(text, g)) == 0 {
		return splitLines(g)
	}
	lines := engine.CollapseRuns(append([]string(nil), body...))
	lines = engine.FoldStacks(gc, lines)
	lines = engine.CollapseSimilar(lines)
	if len(engine.MissingErrorLines(text, strings.Join(lines, "\n"))) == 0 {
		return lines
	}
	return body
}

func stepArgv(script []string) []string {
	var cmd string
	for _, s := range script {
		s = strings.TrimSpace(s)
		if s == "" || s[0] == '#' {
			continue
		}
		if cmd != "" {
			return nil
		}
		cmd = s
	}
	if w, ok := shellWords(cmd); ok {
		return w
	}
	return nil
}

func shellWords(s string) ([]string, bool) {
	if len(s) > 4096 {
		return nil, false
	}
	var words []string
	var cur strings.Builder
	in := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == ' ' || ch == '\t':
			if in {
				words = append(words, cur.String())
				cur.Reset()
				in = false
			}
		case ch == '\'' || ch == '"':
			j := strings.IndexByte(s[i+1:], ch)
			if j < 0 {
				return nil, false
			}
			q := s[i+1 : i+1+j]
			if ch == '"' && strings.ContainsAny(q, "$`\\!") {
				return nil, false
			}
			cur.WriteString(q)
			i += j + 1
			in = true
		case strings.IndexByte("|&;<>()`$\\*?[]{}!", ch) >= 0, !in && (ch == '#' || ch == '~'):
			return nil, false
		default:
			cur.WriteByte(ch)
			in = true
		}
	}
	if in {
		words = append(words, cur.String())
	}
	return words, len(words) > 0
}
