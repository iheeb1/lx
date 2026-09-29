package ci

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

var (
	turboFrameRe   = lazyre.New(`^\s*• (?:Running \S.* in \d+ packages?|Packages in scope: )|^\s*Tasks:\s+\d+ successful, \d+ total`)
	pnpmFrameRe    = lazyre.New(`^Scope: (?:all )?\d+(?: of \d+)? workspace projects?$`)
	nxFrameRe      = lazyre.New(`^\s*(?:NX|Lerna \(powered by Nx\))\s+(?:Running|Successfully ran) targets? `)
	composeFrameRe = lazyre.New(`^Attaching to \S`)
	nxEndRe        = lazyre.New(`^\s*(?:>\s+)?(?:NX|Lerna \(powered by Nx\))\s+\S|^\s*—{3,}\s*$`)

	turboLineRe   = lazyre.New(`^([@\w][\w@./-]*):([A-Za-z][\w.:-]*?):(?: (.*))?$`)
	turboNoteRe   = lazyre.New(`^([@\w][\w@./-]*)#([A-Za-z][\w.:-]*): +(.*)$`)
	turboExitRe   = lazyre.New(`\bexited \((\d+)\)$`)
	pnpmLineRe    = lazyre.New(`^(\.|\S*/\S*) ([\w:.-]+)([:$])(?: (.*))?$`)
	composeLineRe = lazyre.New(`^([A-Za-z0-9][\w.-]*) +\|(?: (.*))?$`)
	composeExitRe = lazyre.New(`^([A-Za-z0-9][\w.-]*) exited with code (\d+)$`)
	composeLifeRe = lazyre.New(`^\s*(?:Container|Network|Volume|Image) \S+ +(?:Creating|Created|Starting|Started|Stopping|Stopped|Removing|Removed|Recreate|Recreated|Running|Waiting|Healthy|Pulling|Pulled|Built|Building)\s*$`)
	nxHeadRe      = lazyre.New(`^(❌\s*|✖\s*)?> nx run (\S+)`)
	nxDoneRe      = lazyre.New(`^\s*✔\s+nx run (\S+)`)
	lernaHeadRe   = lazyre.New(`^> ([@\w][\w@./-]*:\S+)$`)
	projLineRe    = lazyre.New(`^([@\w][\w@./-]*): (.*)$`)
	npmEchoRe     = lazyre.New(`^> (?:@[^/\s]+/)?[^@\s]+@\S+ \S+(?: /\S.*)?$`)
	turboCacheRe  = lazyre.New(`^cache (?:hit|miss|bypass)\b`)
	failedTaskRe  = lazyre.New(`^\s*- (?:nx run )?(\S+:\S+)$`)
)

type tasksFilter struct{}

func (tasksFilter) Name() string { return "monorepo" }

func (tasksFilter) GuardsErrors() bool { return true }

func (tasksFilter) Match(*engine.Context) bool { return false }

func (tasksFilter) Apply(c *engine.Context, out string) (string, bool) {
	style := styleOf(out)
	if style == "" {
		return "", false
	}
	d, ok := splitTasks(splitLines(out), style)
	if !ok {
		return "", false
	}
	res := d.render(c)
	return selfGuard(d.texts, d.exempt, res), true
}

func detectTasks(s string) bool {
	style := styleOf(s)
	if style == "" {
		return false
	}
	_, ok := splitTasks(splitLines(s), style)
	return ok
}

func styleOf(s string) string {
	switch {
	case engine.HasLinePrefix(s, "Attaching to ", composeFrameRe.MatchString):
		return "compose"
	case engine.HasLine(s, "• Running ", turboFrameRe.MatchString) || engine.HasLine(s, "• Packages in scope: ", turboFrameRe.MatchString) ||
		engine.HasLine(s, "Tasks:", func(ln string) bool {
			return strings.HasPrefix(strings.TrimLeft(ln, " "), "Tasks:") && turboFrameRe.MatchString(ln)
		}):
		return "turbo"
	case engine.HasLinePrefix(s, "Scope: ", pnpmFrameRe.MatchString):
		return "pnpm"
	case engine.HasLine(s, "Running target", nxFrameRe.MatchString) || engine.HasLine(s, "Successfully ran target", nxFrameRe.MatchString):
		return "nx"
	}
	return ""
}

type task struct {
	key     string
	cmd     string
	lines   []int
	trailer []int
	exit    int
	failed  bool
	cached  bool
}

type tasksDoc struct {
	style  string
	texts  []string
	exempt map[int]bool
	global []int
	tasks  []*task
	byKey  map[string]*task
	first  int
	passed []string
}

func (d *tasksDoc) add(t string) int {
	d.texts = append(d.texts, t)
	return len(d.texts) - 1
}

func (d *tasksDoc) get(key string) *task {
	if t := d.byKey[key]; t != nil {
		return t
	}
	t := &task{key: key, exit: -1}
	d.byKey[key] = t
	d.tasks = append(d.tasks, t)
	return t
}

func splitTasks(lines []string, style string) (*tasksDoc, bool) {
	d := &tasksDoc{style: style, exempt: map[int]bool{}, byKey: map[string]*task{}, first: -1}
	taskLines := 0
	put := func(t *task, text string) {
		i := d.add(text)
		t.lines = append(t.lines, i)
		taskLines++
		if d.first < 0 {
			d.first = len(d.global)
		}
	}
	projects := map[string]string{}
	if style == "nx" {
		for _, ln := range lines {
			if m := nxHeadRe.FindStringSubmatch(ln); m != nil {
				projects[project(m[2])] = m[2]
			} else if m := lernaHeadRe.FindStringSubmatch(ln); m != nil {
				projects[project(m[1])] = m[1]
			}
		}
	}
	var section *task
	failedList := false
	for _, ln := range lines {
		switch style {
		case "compose":
			if m := composeLineRe.FindStringSubmatch(ln); m != nil {
				put(d.get(m[1]), m[2])
				continue
			}
			if m := composeExitRe.FindStringSubmatch(ln); m != nil {
				t := d.get(m[1])
				t.exit, _ = strconv.Atoi(m[2])
				t.trailer = append(t.trailer, d.add(ln))
				continue
			}
		case "turbo":
			if m := turboLineRe.FindStringSubmatch(ln); m != nil {
				t := d.get(m[1] + ":" + m[2])
				put(t, m[3])
				if strings.HasPrefix(m[3], "ERROR: command finished with error") {
					t.failed = true
					if e := turboExitRe.FindStringSubmatch(m[3]); e != nil {
						t.exit, _ = strconv.Atoi(e[1])
					}
				}
				continue
			}
			if m := turboNoteRe.FindStringSubmatch(ln); m != nil {
				t := d.get(m[1] + ":" + m[2])
				t.failed = true
				if e := turboExitRe.FindStringSubmatch(m[3]); e != nil {
					t.exit, _ = strconv.Atoi(e[1])
				}
				t.trailer = append(t.trailer, d.add(ln))
				continue
			}
		case "pnpm":
			if m := pnpmLineRe.FindStringSubmatch(ln); m != nil {
				t := d.get(m[1] + " " + m[2])
				switch {
				case m[3] == "$" && t.cmd == "" && len(t.lines) == 0:
					t.cmd = m[4]
					d.exempt[d.add(ln)] = true
				case m[4] == "Failed":
					t.failed = true
					t.trailer = append(t.trailer, d.add(ln))
				case m[4] == "Done":
					d.exempt[d.add(ln)] = true
				default:
					put(t, m[4])
				}
				continue
			}
		case "nx":
			if m := nxHeadRe.FindStringSubmatch(ln); m != nil {
				section = d.get(m[2])
				section.failed = section.failed || m[1] != ""
				d.exempt[d.add(ln)] = true
				continue
			}
			if m := nxDoneRe.FindStringSubmatch(ln); m != nil {
				section = nil
				d.passed = append(d.passed, m[1])
				d.exempt[d.add(ln)] = true
				continue
			}
			if m := lernaHeadRe.FindStringSubmatch(ln); m != nil {
				section = d.get(m[1])
				d.exempt[d.add(ln)] = true
				continue
			}
			if m := projLineRe.FindStringSubmatch(ln); m != nil && projects[m[1]] != "" {
				put(d.get(projects[m[1]]), m[2])
				continue
			}
			if nxEndRe.MatchString(ln) {
				section = nil
			}
			if ln == "Failed tasks:" || strings.HasSuffix(ln, " failed, including the following:") {
				failedList = true
			} else if m := failedTaskRe.FindStringSubmatch(ln); failedList && m != nil {
				if t := d.byKey[m[1]]; t != nil {
					t.failed = true
				}
			} else if ln != "" {
				failedList = false
			}
			if section != nil {
				put(section, ln)
				continue
			}
		}
		d.global = append(d.global, d.add(ln))
	}
	if len(d.tasks) == 0 || taskLines < 2 && len(d.passed) == 0 {
		return nil, false
	}
	return d, true
}

func project(key string) string {
	if i := strings.IndexByte(key, ':'); i > 0 {
		return key[:i]
	}
	return key
}

func (d *tasksDoc) render(c *engine.Context) string {
	var out []string
	var failed []string
	for _, t := range d.tasks {
		d.trim(t)
		if t.exit > 0 || t.failed {
			failed = append(failed, t.key)
		}
	}
	head := fmt.Sprintf("[lx: %s (%s)", plural(len(d.tasks)+len(d.passed), "task", "tasks"), d.style)
	if len(failed) > 0 {
		head += fmt.Sprintf(", %d failed: %s", len(failed), nameList(failed, 8, ", "))
	}
	out = append(out, head+" · output regrouped by task, prefixes removed]")
	cut := d.first
	if cut < 0 {
		cut = len(d.global)
	}
	out = append(out, d.globals(d.global[:cut])...)
	if len(d.passed) > 0 {
		out = append(out, fmt.Sprintf("[lx: %s without output: %s]", plural(len(d.passed), "passed task", "passed tasks"), nameList(d.passed, 6, ", ")))
	}
	for _, t := range d.tasks {
		out = append(out, d.task(c, t)...)
	}
	out = append(out, d.globals(d.global[cut:])...)
	return strings.Join(out, "\n")
}

func (d *tasksDoc) globals(idx []int) []string {
	var out []string
	life := 0
	for k := 0; k < len(idx); k++ {
		i := idx[k]
		t := d.texts[i]
		if d.style == "compose" && composeLifeRe.MatchString(t) {
			life++
			d.exempt[i] = true
			continue
		}
		if n := d.run(idx[k:], isBoxRow); n >= 4 {
			var keep []string
			for _, j := range idx[k : k+n] {
				if row := d.texts[j]; strings.ContainsAny(row, "✖❌") || engine.IsError(row) {
					keep = append(keep, row)
				}
			}
			out = append(out, fmt.Sprintf("[lx: %s not shown]", plural(n-len(keep), "table line", "table lines")))
			out = append(out, keep...)
			k += n - 1
			continue
		}
		if n := d.run(idx[k:], isListItem); n >= 6 && lastText(out) != "Failed tasks:" {
			for _, j := range idx[k : k+3] {
				out = append(out, d.texts[j])
			}
			out = append(out, fmt.Sprintf("[lx: +%d more]", n-3))
			for _, j := range idx[k+3 : k+n] {
				if engine.IsError(d.texts[j]) {
					out = append(out, d.texts[j])
				}
			}
			k += n - 1
			continue
		}
		if len(out) > 0 && t == "" && out[len(out)-1] == "" {
			continue
		}
		out = append(out, t)
	}
	if life > 0 {
		out = append([]string{fmt.Sprintf("[lx: %s not shown]", plural(life, "container lifecycle line", "container lifecycle lines"))}, out...)
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func lastText(out []string) string {
	for i := len(out) - 1; i >= 0; i-- {
		if out[i] != "" {
			return out[i]
		}
	}
	return ""
}

func (d *tasksDoc) run(idx []int, match func(string) bool) int {
	n := 0
	for n < len(idx) && match(d.texts[idx[n]]) {
		n++
	}
	return n
}

func isBoxRow(s string) bool {
	s = strings.TrimLeft(s, " ")
	return strings.HasPrefix(s, "┌") || strings.HasPrefix(s, "│") || strings.HasPrefix(s, "├") || strings.HasPrefix(s, "└")
}

func isListItem(s string) bool {
	return len(s) > 2 && s[0] == '-' && s[1] == ' ' && !strings.ContainsAny(s[2:], " \t")
}

func (d *tasksDoc) trim(t *task) {
	ls := t.lines
	for len(ls) > 0 && strings.TrimSpace(d.texts[ls[0]]) == "" {
		ls = ls[1:]
	}
	if len(ls) > 0 && d.style == "turbo" && turboCacheRe.MatchString(d.texts[ls[0]]) {
		t.cached = strings.HasPrefix(d.texts[ls[0]], "cache hit")
		d.exempt[ls[0]] = true
		ls = ls[1:]
		for len(ls) > 0 && strings.TrimSpace(d.texts[ls[0]]) == "" {
			ls = ls[1:]
		}
	}
	switch {
	case len(ls) >= 2 && npmEchoRe.MatchString(d.texts[ls[0]]) && strings.HasPrefix(d.texts[ls[1]], "> "):
		if t.cmd == "" {
			t.cmd = strings.TrimPrefix(d.texts[ls[1]], "> ")
		}
		d.exempt[ls[0]], d.exempt[ls[1]] = true, true
		ls = ls[2:]
	case len(ls) >= 1 && d.style == "nx" && t.cmd == "" && strings.HasPrefix(d.texts[ls[0]], "> ") && !npmEchoRe.MatchString(d.texts[ls[0]]):
		t.cmd = strings.TrimPrefix(d.texts[ls[0]], "> ")
		d.exempt[ls[0]] = true
		ls = ls[1:]
	}
	for len(ls) > 0 && strings.TrimSpace(d.texts[ls[0]]) == "" {
		ls = ls[1:]
	}
	for len(ls) > 0 && strings.TrimSpace(d.texts[ls[len(ls)-1]]) == "" {
		ls = ls[:len(ls)-1]
	}
	t.lines = ls
}

func (d *tasksDoc) task(c *engine.Context, t *task) []string {
	mark := "▸ "
	if t.exit > 0 || t.failed {
		mark = "✗ "
	}
	hdr := mark + t.key
	if t.cmd != "" {
		hdr += " · " + t.cmd
	}
	if t.cached {
		hdr += " · cache hit, replayed"
	}
	out := []string{hdr}
	body := make([]string, len(t.lines))
	for k, i := range t.lines {
		body[k] = d.texts[i]
	}
	exit := t.exit
	if exit < 0 {
		exit = 0
		if t.failed {
			exit = 1
		}
	}
	var argv []string
	if w, ok := shellWords(t.cmd); ok {
		argv = w
	}
	view, vouched, _ := viewOf(c, argv, body, exit, false)
	for k, i := range t.lines {
		if k >= vouched {
			d.exempt[i] = true
		}
	}
	out = append(out, view...)
	for _, i := range t.trailer {
		out = append(out, d.texts[i])
	}
	return out
}
