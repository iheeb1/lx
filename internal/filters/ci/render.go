package ci

import (
	"fmt"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

type runLog struct{}

func (runLog) Name() string { return "gh-run-log" }

func (runLog) GuardsErrors() bool { return true }

func (runLog) Match(c *engine.Context) bool {
	w := ghWords(c)
	if len(w) < 2 || !plainGH(c) {
		return false
	}
	switch {
	case w[0] == "run" && w[1] == "view":
		return c.HasFlag("--log", "--log-failed")
	case w[0] == "api":
		for _, x := range w[1:] {
			if jobLogsRe.MatchString(x) {
				return true
			}
		}
	}
	return false
}

func (runLog) Apply(c *engine.Context, out string) (string, bool) {
	jobs, stray, ok := parseLog(out)
	if !ok {
		return "", false
	}
	r := newLogRender(c)
	res := r.render(jobs, stray)
	return selfGuard(r.texts, r.exempt, res), true
}

const maxScript = 8

type logRender struct {
	c       *engine.Context
	out     []string
	texts   []string
	exempt  map[int]bool
	sigs    map[string]shown
	printed map[string]bool
	failed  []failedStep
}

type shown struct {
	job  string
	view []string
}

func newLogRender(c *engine.Context) *logRender {
	return &logRender{c: c, sigs: map[string]shown{}, printed: map[string]bool{}, exempt: map[int]bool{}}
}

type failedStep struct {
	Job, Label, Body string
	Trailer          []string
	Deduped          bool
}

func (r *logRender) emit(lines ...string) {
	for _, ln := range lines {
		r.out = append(r.out, ln)
		r.printed[squash(ln)] = true
	}
}

func (r *logRender) note(s string) {
	r.out = append(r.out, s)
}

func (r *logRender) record(t string, exempt bool) {
	if exempt {
		r.exempt[len(r.texts)] = true
	}
	r.texts = append(r.texts, t)
}

func isMarker(t string) bool {
	return t == "##[endgroup]" || strings.HasPrefix(t, "##[start-action ") || strings.HasPrefix(t, "##[end-action ")
}

func isDebug(t string) bool { return strings.HasPrefix(t, "##[debug]") }

func annotation(t string) (string, bool) {
	for _, p := range [...]string{"##[error]", "##[warning]", "##[notice]"} {
		if rest, ok := strings.CutPrefix(t, p); ok {
			return rest, true
		}
	}
	return "", false
}

func visible(t string) string {
	if g, ok := strings.CutPrefix(t, "##[group]"); ok {
		return g
	}
	return t
}

func (r *logRender) render(jobs []*job, stray []string) string {
	var first, last string
	failed, groups, debug := 0, 0, 0
	for _, j := range jobs {
		for _, st := range j.steps {
			if st.failed {
				failed++
			}
			st.base = len(r.texts)
			for i, l := range st.lines {
				if strings.HasPrefix(l.text, "##[group]") {
					groups++
				}
				if isDebug(l.text) {
					debug++
				}
				if l.ts != "" && (first == "" || l.ts < first) {
					first = l.ts
				}
				if l.ts > last {
					last = l.ts
				}
				r.record(visible(l.text), st.head[i] || isMarker(l.text))
			}
		}
	}
	for _, s := range stray {
		r.record(s, false)
	}
	head := fmt.Sprintf("[lx: %s, %s", plural(len(jobs), "job", "jobs"), plural(failed, "failed step", "failed steps"))
	if span := timeSpan(first, last); span != "" {
		head += " · per-line timestamps removed (" + span + ")"
	}
	if groups > 0 {
		head += " · " + plural(groups, "##[group] block", "##[group] blocks") + " unwrapped"
	}
	if debug > 0 {
		head += " · " + plural(debug, "##[debug] line", "##[debug] lines") + " hidden unless error-like"
	}
	r.note(head + "]")
	named := len(jobs) > 1 || len(jobs) == 1 && jobs[0].name != ""
	for _, j := range jobs {
		r.job(j, named)
	}
	r.emit(stray...)
	return strings.Join(r.out, "\n")
}

func timeSpan(a, b string) string {
	ta, err1 := time.Parse(time.RFC3339Nano, a)
	tb, err2 := time.Parse(time.RFC3339Nano, b)
	if err1 != nil || err2 != nil {
		return ""
	}
	if ta.Format("2006-01-02") == tb.Format("2006-01-02") {
		return ta.Format("2006-01-02 15:04:05") + " → " + tb.Format("15:04:05") + " UTC"
	}
	return ta.Format("2006-01-02 15:04:05") + " → " + tb.Format("2006-01-02 15:04:05") + " UTC"
}

func (r *logRender) job(j *job, named bool) {
	if named {
		r.note("== " + j.name + " ==")
	}
	failedJob := false
	for _, st := range j.steps {
		failedJob = failedJob || st.failed
	}
	var fold []*step
	for _, st := range j.steps {
		if st.failed || !failedJob && !st.boilerplate() && st.hasBody() {
			if v := r.prepare(st); st.failed || v.known {
				r.fold(fold)
				fold = nil
				r.show(j, st, v)
				continue
			}
		}
		fold = append(fold, st)
	}
	r.fold(fold)
}

func (st *step) hasBody() bool {
	for i, l := range st.lines {
		if !st.head[i] && !isMarker(l.text) && !isDebug(l.text) && strings.TrimSpace(visible(l.text)) != "" {
			return true
		}
	}
	return false
}

func (r *logRender) fold(steps []*step) {
	if len(steps) == 0 {
		return
	}
	n := 0
	var names, keep []string
	seen := map[string]bool{}
	for _, st := range steps {
		n += len(st.lines)
		if l := st.label(); len(names) == 0 || names[len(names)-1] != l {
			names = append(names, l)
		}
		for i, l := range st.lines {
			t := visible(l.text)
			if st.head[i] || isMarker(t) {
				continue
			}
			if _, ann := annotation(t); !ann && !engine.IsError(t) {
				continue
			}
			if k := squash(t); !seen[k] {
				seen[k] = true
				keep = append(keep, t)
			}
		}
	}
	r.note(fmt.Sprintf("[lx: %s folded (%s): %s]", plural(len(steps), "step", "steps"), plural(n, "line", "lines"), nameList(names, 8, " · ")))
	for _, t := range keep {
		r.emit("  " + t)
	}
}

type stepView struct {
	view, debug, trailer []string
	body                 string
	known                bool
}

func (r *logRender) prepare(st *step) stepView {
	end := len(st.lines)
	for i := len(st.lines) - 1; i >= 0; i-- {
		if !st.head[i] && exitRe.MatchString(st.lines[i].text) {
			end = i
			break
		}
	}
	for end > 0 && !st.head[end-1] {
		t := st.lines[end-1].text
		if _, ann := annotation(t); !ann && !isMarker(t) {
			break
		}
		end--
	}
	var body, anns, orig, debug []string
	var at []int
	for i := 0; i < end; i++ {
		t := st.lines[i].text
		if st.head[i] || isMarker(t) {
			continue
		}
		if isDebug(t) {
			if engine.IsError(t) {
				debug = append(debug, t)
			}
			continue
		}
		at = append(at, i)
		if a, ok := annotation(t); ok {
			anns, orig = append(anns, a), append(orig, t)
			t = a
		}
		body = append(body, visible(t))
	}
	exit := st.exit
	if exit < 0 {
		exit = 0
		if st.failed {
			exit = 1
		}
	}
	view, vouched, known := viewOf(r.c, stepArgv(st.script), body, exit, true)
	for k, i := range at {
		if k >= vouched {
			r.exempt[st.base+i] = true
		}
	}
	v := stepView{view: restoreAnnotations(view, anns, orig), debug: debug, body: strings.Join(body, "\n"), known: known}
	for _, l := range st.lines[end:] {
		if !isMarker(l.text) {
			v.trailer = append(v.trailer, l.text)
		}
	}
	return v
}

func (r *logRender) show(j *job, st *step, v stepView) {
	mark := "▸ "
	if st.failed {
		mark = "✗ "
	}
	hdr := mark + st.label()
	if d := st.duration(); d != "" {
		hdr += " · " + d
	}
	r.note(hdr)
	if !(len(st.script) == 1 && st.label() == "Run "+st.script[0]) {
		for k, s := range st.script {
			if k == maxScript {
				r.note(fmt.Sprintf("[lx: +%s]", plural(len(st.script)-k, "more script line", "more script lines")))
				break
			}
			r.emit("$ " + s)
		}
	}
	view := v.view
	deduped := false
	if sig := signature(view); st.failed && sig != "" {
		if first, ok := r.sigs[sig]; ok {
			var keep []string
			for i, ln := range view {
				if (engine.IsError(ln) || strings.HasPrefix(ln, "##[")) && !r.printed[squash(ln)] || pathless(ln) != pathless(first.view[i]) {
					keep = append(keep, ln)
				}
			}
			if n := len(view) - len(keep); n > 0 {
				deduped = true
				view = append([]string{fmt.Sprintf("[lx: same failure as %s above; %s not repeated]", first.job, plural(n, "line", "lines"))}, keep...)
			}
		} else {
			r.sigs[sig] = shown{job: j.name, view: view}
		}
	}
	if st.failed {
		r.failed = append(r.failed, failedStep{Job: j.name, Label: st.label(), Body: v.body, Trailer: v.trailer, Deduped: deduped})
	}
	r.emit(view...)
	r.emit(v.debug...)
	r.emit(v.trailer...)
}

func restoreAnnotations(view, anns, orig []string) []string {
	if len(anns) == 0 {
		return view
	}
	at := make(map[string][]int, len(view))
	for i, v := range view {
		k := squash(v)
		at[k] = append(at[k], i)
	}
	for k, a := range anns {
		sa := squash(a)
		if idx := at[sa]; len(idx) > 0 {
			view[idx[0]] = orig[k]
			at[sa] = idx[1:]
			continue
		}
		view = append(view, orig[k])
	}
	return view
}

var (
	runnerRootRe = lazyre.New(`(?:[A-Za-z]:)?/(?:home/runner/work|Users/runner/work|a)/[^/\s]+/[^/\s]+/`)
	timingRe     = lazyre.New(`(?:\d+(?:\.\d+)?(?:ns|µs|us|ms|s|m|h))+\b|\b\d+:\d\d:\d\d\b|\b0x[0-9a-fA-F]+\b`)
	digitsRe     = lazyre.New(`\d+`)
)

func pathless(v string) string {
	return squash(runnerRootRe.ReplaceAllString(strings.ReplaceAll(v, `\`, "/"), ""))
}

// show() still prints every line that differs beyond runner paths
func signature(view []string) string {
	var b strings.Builder
	errs := false
	for _, v := range view {
		errs = errs || engine.IsError(v)
		v = pathless(v)
		v = timingRe.ReplaceAllString(v, "#")
		if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
			v = digitsRe.ReplaceAllString(v, "#")
		}
		b.WriteString(v)
		b.WriteByte('\n')
	}
	if !errs {
		return ""
	}
	return b.String()
}
