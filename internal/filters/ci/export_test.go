package ci

import "github.com/iheeb1/lx/internal/engine"

type FailedStep = failedStep

func RenderLog(c *engine.Context, s string) (out string, texts []string, exempt map[int]bool, failed []FailedStep, ok bool) {
	jobs, stray, ok := parseLog(s)
	if !ok {
		return "", nil, nil, nil, false
	}
	r := newLogRender(c)
	out = r.render(jobs, stray)
	return out, r.texts, r.exempt, r.failed, true
}

func RenderTasks(c *engine.Context, s string) (out string, texts []string, exempt map[int]bool, ok bool) {
	style := styleOf(s)
	if style == "" {
		return "", nil, nil, false
	}
	d, ok := splitTasks(splitLines(s), style)
	if !ok {
		return "", nil, nil, false
	}
	return d.render(c), d.texts, d.exempt, true
}

var (
	RestoreAnnotations = restoreAnnotations
	StripCaret         = stripCaret
	CutStamp           = cutStamp
	ShellWords         = shellWords
	StyleOf            = styleOf
)
