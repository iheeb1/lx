package python

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

type mypyFilter struct{}

func (mypyFilter) Name() string { return "mypy" }

func (mypyFilter) Match(c *engine.Context) bool {
	inv := parseInvocation(c)
	if inv.tool != "mypy" {
		return false
	}
	if len(argValues(inv.args, "--output", "-O")) > 0 {
		return false
	}
	return !hasArg(inv.args, "--version", "-V", "--help", "-h")
}

var (
	mypyDiagRe = lazyre.New(`^(\S(?:.*?\S)?):(\d+)(?::\d+)?(?::\d+:\d+)?: (error|note|warning): (.*)$`)

	mypyFileDiagRe = lazyre.New(`^(\S+): (error|note): (.*)$`)
	mypySummaryRe  = lazyre.New(`^(?:Found \d+ errors? in \d+ files?(?: \(.*\))?|Success: no issues found in \d+ source files?)$`)
	mypyToolRe     = lazyre.New(`^mypy: (?:can't read file|error:)`)

	mypyAdviceRe = lazyre.New(`^(?:See https?://\S+(?: for more info)?$|Hint: "|\(or run "mypy --install-types"|Use "-> None" if function does not return a value$|By default the bodies of untyped functions are not checked|Consider using "(?:Sequence|Mapping)" instead|"(?:List|Dict)" is invariant|This violates the Liskov substitution principle$)`)
)

func (mypyFilter) Apply(c *engine.Context, text string) (string, bool) {
	lines := strings.Split(text, "\n")
	recognized := false
	for _, ln := range lines {
		if mypyDiagRe.MatchString(ln) || mypySummaryRe.MatchString(ln) || mypyToolRe.MatchString(ln) {
			recognized = true
			break
		}
	}
	if !recognized {
		return "", false
	}
	pretty := hasArg(parseInvocation(c).args, "--pretty")
	var out []string
	seenNote := map[string]bool{}
	hidden := 0
	dropping := false
	summaryAt := -1
	for _, ln := range lines {
		if m := mypyDiagRe.FindStringSubmatch(ln); m != nil {
			dropping = false
			if m[3] == "note" {
				key := ln
				if mypyAdviceRe.MatchString(m[4]) {
					key = m[4]
				}

				if seenNote[key] && !engine.IsError(ln) {
					hidden++
					dropping = true
					continue
				}
				seenNote[key] = true
			}
			out = append(out, ln)
			continue
		}
		if mypySummaryRe.MatchString(ln) {
			dropping = false
			summaryAt = len(out)
			out = append(out, ln)
			continue
		}
		if dropping && pretty && strings.TrimSpace(ln) != "" && !isMypyStart(ln) && !mypyFileDiagRe.MatchString(ln) &&
			!strings.HasPrefix(ln, "Traceback ") && !engine.IsError(ln) {
			continue
		}
		dropping = false
		out = append(out, ln)
	}
	if hidden > 0 {
		note := fmt.Sprintf("[lx: %s hidden]", engine.Plural(hidden, "repeated note", "repeated notes"))
		if summaryAt >= 0 {
			out = append(out[:summaryAt], append([]string{note}, out[summaryAt:]...)...)
		} else {
			out = append(out, note)
		}
	}
	out = trimBlank(out)
	if c.Exit != 0 && !anyError(out) {
		out = append(out, fmt.Sprintf("[lx: mypy exited %d]", c.Exit))
	}
	return strings.Join(out, "\n"), true
}

func isMypyStart(ln string) bool {
	return mypyDiagRe.MatchString(ln) || mypySummaryRe.MatchString(ln) || mypyToolRe.MatchString(ln)
}
