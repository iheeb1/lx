package build

import (
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// countSuffixRe is the " [×N]" count this package (and CollapseRuns)
// appends to a line kept once for N occurrences.
var countSuffixRe = lazyre.New(` \[×\d+\]$`)

// selfGuard is the engine's error guard restricted to the lines a filter
// answers for. Every error-class line of lines that is not exempt and whose
// text (whitespace runs collapsed) does not occur in out is appended in the
// engine's own "[lx: error lines from the full output]" section.
//
// exempt(i) marks lines a filter hides on purpose although the classifier
// calls them errors: recognized command echoes (-Werror, -Wfatal-errors in
// the flags), progress/status lines whose names contain error words
// ("Compiling quick-error v2.0.1") and output handed to another filter that
// guards its own errors. Each filter documents its exemptions.
//
// Lines kept whole are looked up in a set first, so the cost stays linear on
// large outputs; only the rest (reformatted or really missing) reach
// engine.Guard, which searches the whole output per line.
func selfGuard(lines []string, exempt func(i int) bool, out string) string {
	kept := make(map[string]bool, strings.Count(out, "\n")+1)
	for _, ln := range strings.Split(out, "\n") {
		kept[squash(ln)] = true
		if countSuffixRe.MatchString(ln) {
			kept[squash(countSuffixRe.ReplaceAllString(ln, ""))] = true
		}
	}
	var b strings.Builder
	seen := map[string]bool{}
	for i, ln := range lines {
		if exempt != nil && exempt(i) {
			continue
		}
		s := squash(ln)
		if s == "" || kept[s] || seen[s] || !engine.IsError(ln) {
			continue
		}
		seen[s] = true
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		return out
	}
	res, _ := engine.Guard(b.String(), out)
	return res
}

// isErrorLine is engine.IsError(ln), computed faster for warning lines.
//
// The classifier's fast path skips lines without an error or warning stem;
// every warning line has one ("warning"), so each takes the slow path
// (~20–50 µs), which dominates on builds with tens of thousands of distinct
// warnings. The severity token itself is never part of an error match: no
// error pattern contains "warning", and the token is a whole word between
// separators in every shape rewritten here. It can belong to a benign span
// in one way only, "warning: 0" (\bwarnings?\s*[:=]\s*0\b), which then
// swallows the "0" of "0 errors" and leaves "errors" error-class; lines
// whose message starts with a lone "0" are therefore left as they are. With
// the token replaced by "W" the line classifies the same and usually takes
// the fast path. TestIsErrorLineAgrees and FuzzIsErrorLine check the
// equivalence against engine.IsError on every fixture line and on fuzzed
// input.
func isErrorLine(ln string) bool {
	return engine.IsError(neutralizeSeverity(ln))
}

// neutralizeSeverity replaces the severity word of a warning line by "W":
// "a.c:3:5: warning: …", "warning: …" / "warning[E0001]: …" (rustc) and
// "[WARNING] …" (Maven). Other lines are returned unchanged.
func neutralizeSeverity(ln string) string {
	switch {
	case strings.HasPrefix(ln, "[WARNING] "):
		return "[W] " + ln[len("[WARNING] "):]
	case strings.HasPrefix(ln, "warning["):
		return "W" + ln[len("warning"):]
	case strings.HasPrefix(ln, "warning: "):
		if loneZero(ln[len("warning: "):]) {
			return ln
		}
		return "W" + ln[len("warning"):]
	}
	if i := strings.Index(ln, ": warning: "); i >= 0 && !loneZero(ln[i+len(": warning: "):]) {
		return ln[:i] + ": W: " + ln[i+len(": warning: "):]
	}
	return ln
}

// loneZero reports a message that starts (after blanks) with "0" not
// followed by a word character, which "warning: " would turn into a benign
// span.
func loneZero(msg string) bool {
	msg = strings.TrimLeft(msg, " \t\f\r\v")
	if !strings.HasPrefix(msg, "0") {
		return false
	}
	if len(msg) == 1 {
		return true
	}
	c := msg[1]
	return !(c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z')
}
