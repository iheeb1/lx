package build

import (
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

var diagLineRe = lazyre.New(`^(?:[a-z]: |vet: )?(?:file://)?[^\s:]*[\w-]\.[A-Za-z][\w+]{0,9}(?::\d+|:\[\d+,\d+\]|\(\d+(?:,\d+)*\)|: \(\d+, \d+\))`)

func genericLines(c *engine.Context, seg []string) []string {
	if len(seg) <= 3 {
		return seg
	}
	g, shape := engine.GenericShape(genericCtx(c), strings.Join(seg, "\n"))
	if shape == "json" || shape == "paths" {
		return seg
	}
	out := strings.Split(g, "\n")
	if keepsDiagnostics(seg, out) {
		return out
	}
	return conservativeLines(c, seg)
}

func keepsDiagnostics(seg, out []string) bool {
	var have map[string]bool
	for _, ln := range seg {
		if !diagLineRe.MatchString(ln) {
			continue
		}
		if have == nil {
			have = make(map[string]bool, len(out))
			for _, o := range out {
				have[squash(o)] = true
			}
		}
		if !have[squash(ln)] {
			return false
		}
	}
	return true
}

func conservativeLines(c *engine.Context, seg []string) []string {
	lines, _ := engine.DropProgress(seg)
	lines = engine.CollapseRuns(lines)
	lines = engine.FoldStacks(genericCtx(c), lines)
	out := make([]string, 0, len(lines))
	start := 0
	flush := func(end int) {
		if end <= start {
			return
		}
		for _, ln := range engine.CollapseSimilar(lines[start:end]) {
			out = append(out, engine.ShortenLine(ln, 400))
		}
	}
	for i, ln := range lines {
		if diagLineRe.MatchString(ln) {
			flush(i)
			out = append(out, ln)
			start = i + 1
		}
	}
	flush(len(lines))
	return out
}

func genericCtx(c *engine.Context) *engine.Context {
	return &engine.Context{Argv: c.Argv, Exit: c.Exit, Mode: c.Mode}
}
