package python

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// ruffFilter condenses `ruff check` in its default ("full") format. Every
// diagnostic keeps its header line ("F401 [*] `os` imported but unused" or,
// in ruff < 0.12, "path:1:8: F401 [*] …") and its location, and its "help:"
// line; the first 10 diagnostics keep the snippet's flagged code line and
// caret line (syntax errors keep the whole snippet), the suggested-fix diff
// is dropped. The summary lines
// ("Found N errors.", "[*] N fixable …", "All checks passed!") are kept
// verbatim. Concise and grouped output is already one line per
// diagnostic and passes through unchanged.
//
// Snippet lines are source code that may contain words like "error", so the
// filter implements engine.Guarded and runs ensureErrors with exactly the
// snippet and fix-diff lines it hides exempted.
type ruffFilter struct{}

func (ruffFilter) Name() string { return "ruff" }

func (ruffFilter) GuardsErrors() bool { return true }

func (ruffFilter) Match(c *engine.Context) bool {
	inv := parseInvocation(c)
	if inv.tool != "ruff" {
		return false
	}
	sub, rest := "", []string(nil)
	for i := 0; i < len(inv.args); i++ {
		a := inv.args[i]
		if a == "--config" {
			i++
			continue
		}
		if !strings.HasPrefix(a, "-") {
			sub, rest = a, inv.args[i+1:]
			break
		}
	}
	if sub != "check" {
		return false
	}
	if hasArg(rest, "--watch", "-w", "--diff", "--show-settings", "--show-files", "--statistics", "--help", "-h") {
		return false
	}
	for _, f := range append(argValues(rest, "--output-format", ""), argValues(rest, "--format", "")...) {
		switch strings.ToLower(f) {
		case "full", "concise", "grouped", "text":
		default:
			return false // json, sarif, github, …: machine output
		}
	}
	return true
}

const ruffSnippets = 10

var (
	// Concise / pre-0.12 full header: path:line:col: CODE [*] message.
	ruffOldHeadRe = regexp.MustCompile(`^(\S(?:.*?\S)?):(\d+):(\d+): ([A-Z]+\d+|[a-z][a-z0-9]*(?:-[a-z0-9]+)*:?|SyntaxError:) (.*)$`)
	// 0.12+ full format location line under the header.
	ruffArrowRe   = regexp.MustCompile(`^\s*--> (\S(?:.*?\S)?):(\d+):(\d+)$`)
	ruffHelpRe    = regexp.MustCompile(`^\s*(?:= )?(?:help|note|info|warning|error): `)
	ruffSummaryRe = regexp.MustCompile(`^(?:Found \d+ errors?(?: \(\d+ fixed, \d+ remaining\))?\.|All checks passed!|\[\*\] \d+ fixable with the .*|No fixes available .*|\d+ files? (?:would be )?(?:reformatted|left unchanged).*)$`)
)

type ruffDiag struct {
	idx    []int // every line of the diagnostic block
	header int   // header line index
	arrow  int   // "-->" line index, -1 for the old format
	line   int   // the diagnostic's line number
	syntax bool
}

var (
	// "15 |     tags: List[str] = …" (code line of a snippet).
	ruffCodeRe = regexp.MustCompile(`^\s*(\d+) \|(.*)$`)
	// Marker line of a snippet: "   |           ^^^^", "  | |___^",
	// "  |        -- previous definition of `os` here" (secondary label).
	ruffMarkRe = regexp.MustCompile(`^\s*\|\s*(?:[\^~_\-/\\]|\|.*[\^_])`)
	// Secondary location of a multi-span diagnostic: "  ::: other.py:6:5".
	ruffSecondaryRe = regexp.MustCompile(`^\s*::: \S`)
)

// snippetLine reports whether a snippet line is worth keeping: the code
// line the diagnostic points at, every code line an annotation marks (the
// line above a marker line: "previous definition of `f` here"), the lines
// of a marked multi-line range ("1 | / import a", "2 | | import b") and the
// caret/underline/label lines. The surrounding context lines and bare "|"
// rulers are dropped.
func (d ruffDiag) snippetLine(ln, next string) bool {
	if ruffMarkRe.MatchString(ln) {
		return true
	}
	m := ruffCodeRe.FindStringSubmatch(ln)
	if m == nil {
		return false
	}
	if m[1] == strconv.Itoa(d.line) || ruffMarkRe.MatchString(next) {
		return true
	}
	return strings.HasPrefix(m[2], " / ") || strings.HasPrefix(m[2], " | ")
}

func (ruffFilter) Apply(c *engine.Context, text string) (string, bool) {
	lines := strings.Split(text, "\n")
	exempt := make([]bool, len(lines))
	var diags []ruffDiag
	isHeader := func(i int) (arrow int, ok bool) {
		ln := lines[i]
		if ln == "" || ln[0] == ' ' || ln[0] == '\t' {
			return -1, false
		}
		if i+1 < len(lines) && ruffArrowRe.MatchString(lines[i+1]) {
			return i + 1, true
		}
		if ruffOldHeadRe.MatchString(ln) {
			return -1, true
		}
		return -1, false
	}
	recognized := false
	// Blocks: a header plus the snippet lines up to the next blank line or
	// header. Everything else is kept in place.
	type item struct {
		diag int // index into diags, or -1 for a plain line
		line int
	}
	var items []item
	for i := 0; i < len(lines); {
		arrow, ok := isHeader(i)
		if !ok {
			if ruffSummaryRe.MatchString(lines[i]) {
				recognized = true
			}
			items = append(items, item{-1, i})
			i++
			continue
		}
		recognized = true
		d := ruffDiag{header: i, arrow: arrow, idx: []int{i}}
		hl := lines[i]
		if arrow >= 0 {
			d.line, _ = strconv.Atoi(ruffArrowRe.FindStringSubmatch(lines[arrow])[2])
		} else {
			d.line, _ = strconv.Atoi(ruffOldHeadRe.FindStringSubmatch(hl)[2])
		}
		d.syntax = strings.HasPrefix(hl, "invalid-syntax") || strings.Contains(hl, "SyntaxError") ||
			strings.Contains(hl, ": E999 ") || strings.Contains(hl, ": invalid-syntax")
		j := i + 1
		for j < len(lines) {
			if strings.TrimSpace(lines[j]) == "" {
				break
			}
			if _, ok := isHeader(j); ok {
				break
			}
			if ruffSummaryRe.MatchString(lines[j]) {
				break
			}
			d.idx = append(d.idx, j)
			j++
		}
		items = append(items, item{len(diags), -1})
		diags = append(diags, d)
		i = j
	}
	if !recognized {
		return "", false
	}

	var out []string
	snippets, hiddenSnippets, hiddenDiffs := 0, 0, 0
	for _, it := range items {
		if it.diag < 0 {
			if ln := lines[it.line]; strings.TrimSpace(ln) != "" {
				out = append(out, ln)
			}
			continue
		}
		d := diags[it.diag]
		if len(d.idx) == 1 {
			out = append(out, lines[d.header]) // concise line
			continue
		}
		showSnippet := d.syntax || snippets < ruffSnippets
		if showSnippet {
			snippets++
		}
		inFix, hadSnippet, hadDiff := false, false, false
		for k, i := range d.idx {
			ln := lines[i]
			next := ""
			if k+1 < len(d.idx) {
				next = lines[d.idx[k+1]]
			}
			switch {
			case i == d.header || i == d.arrow || ruffSecondaryRe.MatchString(ln):
				inFix = false // a new span of the same diagnostic
				out = append(out, ln)
			case ruffHelpRe.MatchString(ln):
				out = append(out, ln)
				inFix = true // what follows a help line is the fix diff
			case inFix:
				exempt[i] = true
				hadDiff = true
			case d.syntax:
				out = append(out, ln) // syntax errors: the whole snippet
			case showSnippet && d.snippetLine(ln, next):
				out = append(out, ln)
			case showSnippet:
				exempt[i] = true // context line of a kept snippet
			default:
				exempt[i] = true
				hadSnippet = true
			}
		}
		if hadSnippet {
			hiddenSnippets++
		}
		if hadDiff {
			hiddenDiffs++
		}
		if d.arrow >= 0 || showSnippet {
			out = append(out, "") // blank line between full-format diagnostics
		}
	}
	out = trimBlank(collapseBlank(out))
	var notes []string
	if hiddenSnippets > 0 {
		notes = append(notes, fmt.Sprintf("source snippets of %s", engine.Plural(hiddenSnippets, "diagnostic", "diagnostics")))
	}
	if hiddenDiffs > 0 {
		notes = append(notes, engine.Plural(hiddenDiffs, "fix diff", "fix diffs"))
	}
	if len(notes) > 0 {
		out = append(out, "[lx: hid "+strings.Join(notes, " and ")+"]")
	}
	out, _ = ensureErrors(lines, exempt, out)
	if c.Exit != 0 && !anyError(out) {
		// "All checks passed!" with a failing status (--exit-non-zero-on-fix,
		// a crash after the report), or -q output without "Found N errors.":
		// say so.
		out = append(out, fmt.Sprintf("[lx: ruff exited %d]", c.Exit))
	}
	return strings.Join(out, "\n"), true
}

// collapseBlank squeezes runs of blank lines and drops a blank line right
// before a summary line.
func collapseBlank(lines []string) []string {
	var out []string
	for i, ln := range lines {
		if ln == "" {
			if len(out) == 0 || out[len(out)-1] == "" {
				continue
			}
			if i+1 < len(lines) && ruffSummaryRe.MatchString(lines[i+1]) {
				continue
			}
		}
		out = append(out, ln)
	}
	return out
}
