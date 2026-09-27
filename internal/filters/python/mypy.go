package python

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// mypyFilter keeps mypy's report native: every error line verbatim with its
// --pretty source/caret lines, the "Found N errors in M files" / "Success:"
// line verbatim. What it removes is repetition: a note whose text was
// already shown (the "See https://mypy.readthedocs.io/…" link, "Use "->
// None" if function does not return a value", install hints) is printed
// once, then counted.
type mypyFilter struct{}

func (mypyFilter) Name() string { return "mypy" }

func (mypyFilter) Match(c *engine.Context) bool {
	inv := parseInvocation(c)
	if inv.tool != "mypy" {
		return false
	}
	if len(argValues(inv.args, "--output", "-O")) > 0 {
		return false // JSON output
	}
	return !hasArg(inv.args, "--version", "-V", "--help", "-h")
}

var (
	// file:line[:col][:end_line:end_col]: severity: message
	mypyDiagRe = lazyre.New(`^(\S(?:.*?\S)?):(\d+)(?::\d+)?(?::\d+:\d+)?: (error|note|warning): (.*)$`)
	// File-level and tool-level messages: "x.py: error: …", "mypy: error: …".
	mypyFileDiagRe = lazyre.New(`^(\S+): (error|note): (.*)$`)
	mypySummaryRe  = lazyre.New(`^(?:Found \d+ errors? in \d+ files?(?: \(.*\))?|Success: no issues found in \d+ source files?)$`)
	mypyToolRe     = lazyre.New(`^mypy: (?:can't read file|error:)`)
	// Notes that are generic advice: the same text says the same thing at
	// any location, so it is shown once. Other notes ("Revealed type is",
	// ""x" defined here", overload variants) depend on where they are and
	// are only dropped when the whole line repeats.
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
	dropping := false // inside the continuation lines of a hidden note
	summaryAt := -1
	for _, ln := range lines {
		if m := mypyDiagRe.FindStringSubmatch(ln); m != nil {
			dropping = false
			if m[3] == "note" {
				key := ln
				if mypyAdviceRe.MatchString(m[4]) {
					key = m[4]
				}
				// A note the classifier calls an error is never hidden: the
				// engine's guard would put it back under a heading anyway.
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
			// --pretty wraps a long note onto following lines: the rest of
			// the hidden note. Only then: without --pretty a hidden note
			// has no continuation, and whatever follows (a crash
			// traceback) is kept.
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
		// "Success: …", notes only, or nothing mypy-like: the status says
		// otherwise.
		out = append(out, fmt.Sprintf("[lx: mypy exited %d]", c.Exit))
	}
	return strings.Join(out, "\n"), true
}

func isMypyStart(ln string) bool {
	return mypyDiagRe.MatchString(ln) || mypySummaryRe.MatchString(ln) || mypyToolRe.MatchString(ln)
}
