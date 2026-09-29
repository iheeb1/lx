package ci

import (
	"strconv"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/lazyre"
)

const unknownStep = "UNKNOWN STEP"

type logLine struct {
	text string
	ts   string
}

type step struct {
	name    string
	display string
	lines   []logLine
	post    bool
	setup   bool

	run     string
	script  []string
	head    []bool
	failed  bool
	exit    int
	uses    bool
	started bool
	known   bool
	base    int
}

type job struct {
	name  string
	steps []*step
}

func cutStamp(s string) (ts, rest string, ok bool) {
	s = strings.TrimPrefix(s, "\ufeff")
	if len(s) < 20 || s[4] != '-' || s[7] != '-' || s[10] != 'T' || s[13] != ':' || s[16] != ':' {
		return "", "", false
	}
	for _, i := range [...]int{0, 1, 2, 3, 5, 6, 8, 9, 11, 12, 14, 15, 17, 18} {
		if s[i] < '0' || s[i] > '9' {
			return "", "", false
		}
	}
	i := 19
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if i >= len(s) || s[i] != 'Z' {
		return "", "", false
	}
	ts, rest = s[:i+1], s[i+1:]
	if rest == "" {
		return ts, "", true
	}
	if rest[0] != ' ' {
		return "", "", false
	}
	return ts, rest[1:], true
}

func splitCols(ln string) (jobName, stepName, rest string, ok bool) {
	i := strings.IndexByte(ln, '\t')
	if i <= 0 {
		return "", "", "", false
	}
	j := strings.IndexByte(ln[i+1:], '\t')
	if j < 0 {
		return "", "", "", false
	}
	return ln[:i], ln[i+1 : i+1+j], ln[i+2+j:], true
}

// gh prints escapes as "^[[36;1m" when stdout is not a terminal.
func stripCaret(s string) string {
	if !strings.Contains(s, "^[[") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '^' && i+2 < len(s) && s[i+1] == '[' && s[i+2] == '[' {
			j := i + 3
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ';' || s[j] == '?') {
				j++
			}
			if j < len(s) && s[j] >= '@' && s[j] <= '~' && (j > i+3 || s[j] == 'm' || s[j] == 'K') {
				i = j + 1
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

type logFormat uint8

const (
	fmtNone logFormat = iota
	fmtGH
	fmtRaw
)

func sniffFormat(lines []string) logFormat {
	gh, raw, seen := 0, 0, 0
	for _, ln := range lines {
		if ln == "" {
			continue
		}
		if seen++; seen > 200 {
			break
		}
		if _, _, rest, ok := splitCols(ln); ok {
			if _, _, ok := cutStamp(rest); ok {
				gh++
				continue
			}
		}
		if _, _, ok := cutStamp(ln); ok {
			raw++
		}
	}
	switch {
	case gh > 0 && gh >= raw:
		return fmtGH
	case raw > 0:
		return fmtRaw
	}
	return fmtNone
}

func parseLog(s string) (jobs []*job, stray []string, ok bool) {
	lines := strings.Split(s, "\n")
	form := sniffFormat(lines)
	if form == fmtNone {
		return nil, nil, false
	}
	var (
		cur        *job
		curStep    *step
		lastJob    string
		lastStep   string
		postPhase  bool
		nonEmpty   int
		recognized int
	)
	newJob := func(name string) {
		cur = &job{name: name}
		jobs = append(jobs, cur)
		curStep, postPhase, lastStep = nil, false, ""
	}
	newStep := func(name string) *step {
		st := &step{name: name, exit: -1, post: postPhase}
		cur.steps = append(cur.steps, st)
		curStep = st
		return st
	}
	for _, ln := range lines {
		if ln == "" {
			continue
		}
		nonEmpty++
		var jobName, stepName, rest string
		if form == fmtGH {
			var ok bool
			if jobName, stepName, rest, ok = splitCols(ln); !ok {
				if j, st, one := strings.Cut(ln, "\t"); one && cur != nil && j == lastJob && (st == lastStep || st == unknownStep) {
					recognized++
					continue
				}
				stray = append(stray, ln)
				continue
			}
		} else {
			rest = ln
		}
		bom := strings.HasPrefix(rest, "\ufeff")
		ts, text, ok := cutStamp(rest)
		if !ok {
			if curStep != nil && (form == fmtRaw || jobName == lastJob) {
				if form == fmtGH {
					recognized++
				}
				curStep.lines = append(curStep.lines, logLine{text: stripCaret(rest)})
				continue
			}
			stray = append(stray, ln)
			continue
		}
		recognized++
		text = stripCaret(text)
		if cur == nil || form == fmtGH && jobName != lastJob || form == fmtRaw && bom && strings.HasPrefix(text, "Current runner version:") {
			newJob(jobName)
			lastJob = jobName
		}
		switch {
		case form == fmtGH && stepName != unknownStep:
			if curStep == nil || stepName != lastStep {
				st := newStep(stepName)
				st.known = true
				st.setup = stepName == "Set up job" || stepName == "Complete job"
				st.post = strings.HasPrefix(stepName, "Post ")
			}
			lastStep = stepName
		default:
			if name, start := markerStep(text, curStep); start {
				if name == "Post job cleanup" {
					postPhase = true
				}
				st := newStep(name)
				st.setup = name == "Set up job" || name == "Complete job"
				if strings.HasPrefix(text, "##[start-action ") {
					st.display = name
				}
			} else if strings.HasPrefix(text, "##[end-action ") {
				curStep.started = true
			}
		}
		curStep.lines = append(curStep.lines, logLine{text: text, ts: ts})
	}
	if recognized == 0 || form == fmtGH && recognized*10 < nonEmpty*9 || form == fmtRaw && recognized*2 < nonEmpty {
		return nil, nil, false
	}
	for _, j := range jobs {
		j.mergeUserGroups()
		for _, st := range j.steps {
			st.analyze()
		}
	}
	return jobs, stray, true
}

// a script's own ::group::Run … is output, not a new step
func (j *job) mergeUserGroups() {
	steps := j.steps[:0]
	for _, st := range j.steps {
		if len(steps) > 0 && !st.known && st.display == "" && len(st.lines) > 0 &&
			strings.HasPrefix(st.lines[0].text, "##[group]Run ") && runHeader(st.lines, 0) < 0 {
			prev := steps[len(steps)-1]
			prev.lines = append(prev.lines, st.lines...)
			continue
		}
		steps = append(steps, st)
	}
	j.steps = steps
}

// the runner's header: script, then only shell:/with:/env: and their values
func runHeader(lines []logLine, r int) int {
	end := -1
	for j := r + 1; j < len(lines) && j < r+400; j++ {
		if lines[j].text == "##[endgroup]" {
			end = j
			break
		}
	}
	if end < 0 {
		return -1
	}
	meta := -1
	for j := r + 1; j < end; j++ {
		t := lines[j].text
		if meta < 0 && !headerKey(t) {
			continue
		}
		if meta < 0 {
			meta = j
		}
		if !headerKey(t) && !strings.HasPrefix(t, "  ") && lines[j].ts != "" {
			return -1
		}
	}
	if meta < 0 && end > r+1 {
		return -1
	}
	return end
}

func headerKey(t string) bool {
	return strings.HasPrefix(t, "shell: ") || t == "with:" || t == "env:"
}

func markerStep(text string, cur *step) (string, bool) {
	switch {
	case cur == nil:
		return "Set up job", true
	case strings.HasPrefix(text, "##[start-action "):
		return actionDisplay(text), true
	case strings.HasPrefix(text, "##[group]Run "):
		if cur.display != "" && !cur.started {
			cur.started = true
			return "", false
		}
		return "Run " + strings.TrimPrefix(text, "##[group]Run "), true
	case text == "Post job cleanup.":
		return "Post job cleanup", true
	case text == "Cleaning up orphan processes":
		return "Complete job", true
	}
	return "", false
}

func actionDisplay(text string) string {
	d := text[strings.Index(text, " ")+1:]
	if v, ok := strings.CutPrefix(d, "display="); ok {
		if k := strings.LastIndex(v, ";id="); k >= 0 {
			return v[:k]
		}
		return strings.TrimSuffix(v, "]")
	}
	return "step"
}

var (
	exitRe      = lazyre.New(`^##\[error\]Process completed with exit code (\d+)\.$`)
	actionRefRe = lazyre.New(`^(?:[\w.-]+/[\w.-]+(?:/[^@\s]+)?@\S+|\./[^\s]+|docker://\S+)$`)
	shaRefRe    = lazyre.New(`@[0-9a-f]{40}$`)
)

func (st *step) analyze() {
	st.exit = -1
	st.head = make([]bool, len(st.lines))
	seen := false
	for r := 0; r < len(st.lines); r++ {
		run, ok := strings.CutPrefix(st.lines[r].text, "##[group]Run ")
		if !ok {
			continue
		}
		end := runHeader(st.lines, r)
		if end < 0 {
			continue
		}
		st.head[r] = true
		first := !seen
		seen = true
		if first {
			st.run = run
		}
		var script []string
		inScript := true
		for j := r + 1; j <= end; j++ {
			t := st.lines[j].text
			if headerKey(t) || j == end {
				inScript = false
			}
			if inScript {
				script = append(script, t)
			}
			st.head[j] = true
		}
		if first {
			st.script = script
			st.uses = len(script) == 0 && actionRefRe.MatchString(run)
		}
		r = end
	}
	for _, l := range st.lines {
		t := l.text
		if strings.HasPrefix(t, "##[error]") {
			st.failed = true
			if m := exitRe.FindStringSubmatch(t); m != nil {
				st.exit, _ = strconv.Atoi(m[1])
			}
		} else if strings.HasPrefix(t, "##[end-action ") && strings.Contains(t, "outcome=failure") {
			st.failed = true
		}
	}
}

func (st *step) boilerplate() bool {
	return !st.failed && (st.setup || st.post || st.uses)
}

func (st *step) label() string {
	switch {
	case st.known:
		return st.name
	case st.display != "":
		if ref, ok := strings.CutPrefix(st.display, "Run "); ok && actionRefRe.MatchString(ref) {
			return shaRefRe.ReplaceAllString(ref, "")
		}
		return st.display
	case st.uses:
		return shaRefRe.ReplaceAllString(st.run, "")
	case strings.HasPrefix(st.name, "Run ") && st.run != "":
		return "Run " + shorten(st.run, 100)
	}
	return st.name
}

func (st *step) duration() string {
	var first, last string
	for _, l := range st.lines {
		if l.ts != "" {
			if first == "" {
				first = l.ts
			}
			last = l.ts
		}
	}
	if first == "" || first == last {
		return ""
	}
	a, err1 := time.Parse(time.RFC3339Nano, first)
	b, err2 := time.Parse(time.RFC3339Nano, last)
	if err1 != nil || err2 != nil || b.Before(a) {
		return ""
	}
	return fmtDur(b.Sub(a))
}

func fmtDur(d time.Duration) string {
	switch {
	case d < time.Second:
		return d.Round(10 * time.Millisecond).String()
	case d < time.Minute:
		return d.Round(100 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}
