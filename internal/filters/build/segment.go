package build

import (
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// Unknown lines (a recipe's own output, compiler output in a format this
// package does not parse, a Gradle task's output) go through the generic
// reducer, with one guarantee added: a line that starts with a source
// location is a diagnostic and is never folded away.
//
// The generic reducer folds runs of lines that differ only in numbers
// ("… 8 similar lines …") unless the classifier calls them errors, and it
// does not: "./main.go:12:11: undefined: lookup" (go build) or "e:
// file:///…/A.kt:20:5 Type mismatch: …" (Kotlin) carry no error word, so
// eight compile errors would read as one. When the reducer's view lacks any
// such line, the segment is rendered conservatively instead: progress
// frames dropped, identical runs counted, library stack frames folded, and
// similar lines folded only between diagnostics.

// diagLineRe matches a line that starts with a source location: "path.ext:12",
// "path.ext:[12,5]" (javac via Maven), "path.ext(12,5)" (tsc, MSBuild),
// "path.ext: (12, 5)" (Kotlin 1.x), after an optional one-letter severity
// ("e: ", "w: " — Kotlin) or "vet: ", and an optional file:// scheme. Stack
// frames start with whitespace and never match.
var diagLineRe = lazyre.New(`^(?:[a-z]: |vet: )?(?:file://)?[^\s:]*[\w-]\.[A-Za-z][\w+]{0,9}(?::\d+|:\[\d+,\d+\]|\(\d+(?:,\d+)*\)|: \(\d+, \d+\))`)

// genericLines runs unknown lines through the generic reducer (short runs
// and data shapes are kept as they are).
//
// The reducer gets a context without Cwd/Home: its relativization rewrites
// non-error lines only, so a build log would mix absolute and relative
// spellings of one path, and it breaks file:// URIs (Gradle/Kotlin print
// "file:///home/u/app/src/A.kt:3:9"; with Cwd /home/u/app it becomes
// "file://src/A.kt:3:9", a URI whose host is "src").
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

// keepsDiagnostics reports whether every diagnostic-shaped line of seg is a
// line of out (whitespace runs collapsed).
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

// conservativeLines is the generic line pipeline with diagnostics pinned:
// DropProgress, CollapseRuns, FoldStacks, then CollapseSimilar and
// ShortenLine only on the runs between diagnostic lines.
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

// genericCtx is c without the directories the generic reducer relativizes.
func genericCtx(c *engine.Context) *engine.Context {
	return &engine.Context{Argv: c.Argv, Exit: c.Exit}
}
