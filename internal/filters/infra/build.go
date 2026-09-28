package infra

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type dockerBuild struct{}

func (dockerBuild) Name() string { return "docker-build" }

func (dockerBuild) Match(c *engine.Context) bool {
	return dockerSub(c, "build", "buildx build", "image build", "builder build", "compose build", "buildx b")
}

var (
	bkLineRe    = lazyre.New(`^#(\d+) (.*)$`)
	bkBracketRe = lazyre.New(`^\[([^\]]+)\] `)
	bkStatusRe  = lazyre.New(`^(?:DONE [\d.]+s|CACHED|CANCELED|ERROR(?:: .*)?)$`)
	bkOutputRe  = lazyre.New(`^\d+\.\d+(?: |$)`)
	bkStepNumRe = lazyre.New(`(?:^|\s)\d+/\d+$`)

	bkNoiseRe = lazyre.New(`^(?:sha256:[0-9a-f]+ .*|extracting sha256:.*|resolve \S+ .*|transferring \S+: .*|` +
		`exporting (?:layers|manifest|config|attestation manifest|manifest list|cache|to \S+ .*)\b.*|preparing layers for inline cache.*|` +
		`pushing .*|unpacking to .*|writing layer .*|loading layer .*|copying .*|computing cache key.*|done$|` +
		`writing cache image manifest .*|preparing build cache for export.*|sending tarball.*|importing to docker.*|` +
		`resolving provenance for metadata file.*|\[\d+/\d+\] .*)`)
	bkKeepRe = lazyre.New(`^(?:naming to |writing image )`)

	legacyStepRe    = lazyre.New(`^Step \d+/\d+ : `)
	legacyStepNumRe = lazyre.New(`^Step (\d+)/(\d+) : `)
	legacyNoiseRe   = lazyre.New(`^ ---> (?:[0-9a-f]{12}$|Running in [0-9a-f]{12}$|Removed intermediate container [0-9a-f]{12}$)|^Removing intermediate container [0-9a-f]{12}$|^Sending build context to Docker daemon`)
	legacyHintRe    = lazyre.New(`^ {2,}(?:Install the buildx component|https://docs\.docker\.com/go/buildx/)`)
)

const (
	failShowAll  = 150
	failShowHead = 20
	failShowTail = 80
)

type bkStep struct {
	id      string
	header  string
	kind    byte
	status  string
	statusL string
	out     []string
	keep    []string
	noise   int
}

func (dockerBuild) Apply(c *engine.Context, out string) (string, bool) {
	lines := strings.Split(out, "\n")
	for _, ln := range lines {
		if bkLineRe.MatchString(ln) {
			return buildkit(c, lines)
		}
	}
	for _, ln := range lines {
		if legacyStepRe.MatchString(ln) {
			return legacyBuild(c, lines)
		}
	}
	return "", false
}

func buildkit(c *engine.Context, lines []string) (string, bool) {
	steps := map[string]*bkStep{}
	var order []*bkStep
	var after []string
	for _, ln := range lines {
		m := bkLineRe.FindStringSubmatch(ln)
		if m == nil {
			after = append(after, ln)
			continue
		}
		id, rest := m[1], m[2]
		st := steps[id]
		if st == nil {
			st = &bkStep{id: id, header: ln, kind: 'o'}
			switch b := bkBracketRe.FindStringSubmatch(rest); {
			case b != nil && (b[1] == "internal" || b[1] == "auth" || strings.HasSuffix(b[1], " internal")):
				st.kind = 'i'
			case b != nil && bkStepNumRe.MatchString(b[1]):
				st.kind = 'b'
			case id == "0" || strings.HasPrefix(rest, "building with "):
				st.kind = 'i'
			case strings.HasPrefix(rest, "exporting to ") || strings.HasPrefix(rest, "exporting cache"):
				st.kind = 'x'
			}
			steps[id] = st
			order = append(order, st)
			continue
		}
		switch {
		case bkStatusRe.MatchString(rest):
			st.status, st.statusL = rest, ln
		case bkOutputRe.MatchString(rest):
			st.out = append(st.out, ln)
		case bkKeepRe.MatchString(rest):
			st.keep = append(st.keep, ln)
		case bkNoiseRe.MatchString(rest) && !engine.IsError(ln):
			st.noise++
		default:
			st.out = append(st.out, ln)
		}
	}
	if len(order) == 0 {
		return "", false
	}

	var res []string
	shown := map[string]bool{}
	built, cached, internal, noise, unfinished := 0, 0, 0, 0, 0
	anyFailed := false
	for _, st := range order {
		anyFailed = anyFailed || strings.HasPrefix(st.status, "ERROR")
	}
	for _, st := range order {
		noise += st.noise
		failed := strings.HasPrefix(st.status, "ERROR")

		if c.Failed() && !anyFailed && st.status == "" && st.kind != 'i' && len(st.out) > 0 {
			unfinished++
			if st.kind == 'b' {
				built++
			}
			res = append(res, st.header+"  [lx: did not finish]")
			kept := failedOutput(st.out)
			for _, ln := range kept {
				shown[ln] = true
			}
			res = append(res, kept...)
			res = append(res, st.keep...)
			continue
		}
		if st.kind == 'i' && !failed && !hasErrorLine(st.out) && !engine.IsError(st.header) {
			internal++
			noise += len(st.out)
			continue
		}
		if st.kind == 'b' {
			built++
			if st.status == "CACHED" {
				cached++
			}
		}
		head := st.header
		if st.status != "" && !failed {
			head += "  " + st.status
		}
		if failed {
			res = append(res, head)
			kept := failedOutput(st.out)
			for _, ln := range kept {
				shown[ln] = true
			}
			res = append(res, kept...)
			res = append(res, st.keep...)
			res = append(res, st.statusL)
			continue
		}
		var notable []string
		hidden := 0
		counts := map[string]int{}
		for _, ln := range st.out {
			switch {
			case engine.IsError(ln):
				notable = append(notable, ln)
			case engine.IsWarning(ln):
				body := bkOutputRe.ReplaceAllString(strings.TrimPrefix(ln, "#"+st.id+" "), "")
				if counts[body] == 0 {
					notable = append(notable, ln)
				}
				counts[body]++
			default:
				hidden++
			}
		}
		if hidden > 0 {
			head += fmt.Sprintf("  [lx: %s not shown]", engine.Plural(hidden, "output line", "output lines"))
		}
		res = append(res, head)
		for _, ln := range notable {
			body := bkOutputRe.ReplaceAllString(strings.TrimPrefix(ln, "#"+st.id+" "), "")
			if n := counts[body]; n > 1 && !engine.IsError(ln) {
				ln += fmt.Sprintf(" [×%d]", n)
			}
			res = append(res, ln)
			shown[ln] = true
		}
		res = append(res, st.keep...)
	}
	res = append(res, buildSummaryBlock(after, shown)...)
	if c.Failed() && !anyFailed && !hasErrorLine(lines) {
		res = append(res, fmt.Sprintf("[lx: docker exited %d%s but no step reported an error: the build was interrupted or killed%s]",
			c.Exit, signalNote(c.Exit), map[bool]string{true: "; the output of the unfinished steps is shown above"}[unfinished > 0]))
	}
	note := fmt.Sprintf("[lx: %s", engine.Plural(built, "build step", "build steps"))
	if cached > 0 {
		note += fmt.Sprintf(" (%d CACHED)", cached)
	}
	if internal > 0 {
		note += fmt.Sprintf("; %s not shown", engine.Plural(internal, "internal step", "internal steps"))
	}
	if noise > 0 {
		note += fmt.Sprintf("; %s not shown", engine.Plural(noise, "progress line", "progress lines"))
	}
	res = append(res, note+"]")
	return strings.TrimRight(strings.Join(res, "\n"), "\n"), true
}

func signalNote(exit int) string {
	switch exit {
	case 137:
		return " (SIGKILL, often out of memory)"
	case 130:
		return " (SIGINT)"
	case 143:
		return " (SIGTERM)"
	}
	return ""
}

func hasErrorLine(lines []string) bool {
	for _, ln := range lines {
		if engine.IsError(ln) {
			return true
		}
	}
	return false
}

func failedOutput(out []string) []string {
	out = engine.CollapseRuns(out)
	if len(out) > failPreWindow {

		out = preWindow(out)
	}

	out = engine.CollapseSimilar(out)
	if len(out) <= failShowAll {
		return out
	}
	var res []string
	gap := 0
	flush := func() {
		if gap > 0 {
			res = append(res, gapNote(gap))
			gap = 0
		}
	}
	for i, ln := range out {
		if i < failShowHead || i >= len(out)-failShowTail || engine.IsError(ln) || engine.IsWarning(ln) {
			flush()
			res = append(res, ln)
			continue
		}
		gap += foldedWeight(ln)
	}
	flush()
	return res
}

const (
	failPreWindow = 4000
	failPreKeep   = 1000
)

var (
	similarLineRe = lazyre.New(`^\s*… (\d+) similar lines …$`)
	runLineRe     = lazyre.New(` \[×(\d+)\]$`)
	gapNoteRe     = lazyre.New(`^\[lx: (\d+) lines? of this step's output not shown\]$`)
)

func gapNote(n int) string {
	return fmt.Sprintf("[lx: %s of this step's output not shown]", engine.Plural(n, "line", "lines"))
}

func foldedWeight(ln string) int {
	for _, re := range []*lazyre.Regexp{similarLineRe, gapNoteRe, runLineRe} {
		if m := re.FindStringSubmatch(ln); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
				return n
			}
		}
	}
	return 1
}

func preWindow(out []string) []string {
	res := append([]string(nil), out[:failPreKeep]...)
	gap := 0
	for _, ln := range out[failPreKeep : len(out)-failPreKeep] {
		if engine.IsError(ln) || engine.IsWarning(ln) {
			if gap > 0 {
				res = append(res, gapNote(gap))
				gap = 0
			}
			res = append(res, ln)
			continue
		}
		gap += foldedWeight(ln)
	}
	if gap > 0 {
		res = append(res, gapNote(gap))
	}
	return append(res, out[len(out)-failPreKeep:]...)
}

func buildSummaryBlock(after []string, shown map[string]bool) []string {
	suffixes := map[string]bool{}
	for ln := range shown {
		if m := bkLineRe.FindStringSubmatch(ln); m != nil {
			suffixes[m[2]] = true
		}
	}
	var res []string
	inExcerpt, repeated := false, 0
	for i, ln := range after {
		switch {
		case ln == "------" && i+1 < len(after) && strings.HasPrefix(after[i+1], " > [") && strings.HasSuffix(after[i+1], ":"):
			inExcerpt = true
			res = append(res, ln)
			continue
		case inExcerpt && ln == "------":
			if repeated > 0 {
				res = append(res, fmt.Sprintf("[lx: %s of the step's output shown above]", engine.Plural(repeated, "line", "lines")))
			}
			inExcerpt, repeated = false, 0
		case inExcerpt && suffixes[ln]:
			repeated++
			continue
		}
		if strings.TrimSpace(ln) == "" && (len(res) == 0 || strings.TrimSpace(res[len(res)-1]) == "") {
			continue
		}
		res = append(res, ln)
	}
	return res
}

func legacyBuild(c *engine.Context, lines []string) (string, bool) {
	type step struct {
		header string
		cached bool
		out    []string
	}
	var (
		pre, post []string
		steps     []*step
		noise     int
	)
	for _, ln := range lines {
		switch {
		case legacyStepRe.MatchString(ln):
			steps = append(steps, &step{header: ln})
		case legacyNoiseRe.MatchString(ln), legacyHintRe.MatchString(ln):
			noise++
		case ln == " ---> Using cache" && len(steps) > 0:
			steps[len(steps)-1].cached = true
		case strings.HasPrefix(ln, "Successfully built ") || strings.HasPrefix(ln, "Successfully tagged ") ||
			strings.HasPrefix(ln, "The command '") && strings.Contains(ln, "returned a non-zero code"):
			post = append(post, ln)
		case len(steps) == 0:
			if strings.TrimSpace(ln) != "" {
				pre = append(pre, ln)
			}
		default:
			steps[len(steps)-1].out = append(steps[len(steps)-1].out, ln)
		}
	}
	res := append([]string(nil), pre...)
	cached := 0
	for i, st := range steps {
		failed := c.Failed() && i == len(steps)-1
		head := st.header
		if st.cached {
			head += "  CACHED"
			cached++
		}
		if failed {
			res = append(res, head)
			res = append(res, failedOutput(st.out)...)
			continue
		}
		var notable []string
		hidden := 0
		for _, ln := range st.out {
			if engine.IsError(ln) || engine.IsWarning(ln) {
				notable = append(notable, ln)
			} else if strings.TrimSpace(ln) != "" {
				hidden++
			}
		}
		if hidden > 0 {
			head += fmt.Sprintf("  [lx: %s not shown]", engine.Plural(hidden, "output line", "output lines"))
		}
		res = append(res, head)
		res = append(res, notable...)
	}
	res = append(res, post...)
	note := fmt.Sprintf("[lx: %s", engine.Plural(len(steps), "step", "steps"))
	if len(steps) > 0 {

		if m := legacyStepNumRe.FindStringSubmatch(steps[len(steps)-1].header); m != nil && m[1] != m[2] {
			note = fmt.Sprintf("[lx: %s of %s steps ran", m[1], m[2])
		}
	}
	if cached > 0 {
		note += fmt.Sprintf(" (%d CACHED)", cached)
	}
	if noise > 0 {
		note += fmt.Sprintf("; %s not shown", engine.Plural(noise, "intermediate container/image line", "intermediate container/image lines"))
	}
	res = append(res, note+"]")
	return strings.Join(res, "\n"), true
}
