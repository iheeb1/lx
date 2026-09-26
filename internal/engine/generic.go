package engine

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
)

// genericJSONBudget: JSON documents at or under this many tokens are left to
// the line pipeline; restructuring small documents is not worth the churn.
const genericJSONBudget = 2 * SmallOutput

// Generic is the reducer for commands without a dedicated filter, and the
// fallback when a filter bails. It is shape-aware but conservative: it only
// removes things that are provably noise (progress frames, exact repeats)
// or folds things with a count (similar runs, library stack frames, log
// templates, goroutine groups, path trees), and it never drops an
// error-class line outside the JSON path (where values are data).
//
// Order: normalize → Relativize → the first shape that applies:
//
//  1. CompactJSON (whole output is JSON / NDJSON; log-shaped NDJSON skips to 2)
//  2. TemplateLogs (log-shaped output)
//  3. FactorPaths (path lists and `ls -R` output)
//  4. line pipeline: DropProgress → CollapseRuns → GroupGoroutines, else
//     FoldStacks → CollapseSimilar → ShortenLine(400)
//
// Stacks are folded before similar lines are collapsed so that frames are
// recognized intact. The result never has more tokens than s; if it would,
// s is returned unchanged.
func Generic(c *Context, s string) string {
	out, _ := GenericShape(c, s)
	return out
}

// GenericShape is Generic that also reports which shape it used: "json",
// "log", "paths", "lines", or "" when s was returned unchanged. For "json"
// and "paths" the output is data, where words like "error" are content
// (issue titles, file names), so a caller may treat it like a Content filter
// and skip the error guard.
func GenericShape(c *Context, s string) (string, string) {
	in := s
	s = RelativizeNonErrors(c, textutil.Clean(s))
	out, shape := genericShape(c, s)
	if tokens.Count(out) > tokens.Count(in) {
		return in, ""
	}
	return out, shape
}

// RelativizeNonErrors applies Relativize line by line, leaving error-class
// lines untouched so they still match the original output verbatim (the
// error guard compares against the unrelativized text). Lines over 16 KiB
// (minified code, data) are left as they are too. c may be nil.
func RelativizeNonErrors(c *Context, s string) string {
	if c == nil {
		return s
	}
	var keys []string
	if c.Cwd != "" && c.Cwd != "/" {
		keys = append(keys, strings.TrimRight(c.Cwd, "/")+"/")
	}
	if c.Home != "" && c.Home != "/" {
		keys = append(keys, strings.TrimRight(c.Home, "/")+"/")
	}
	if len(keys) == 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	changed := false
	for i, ln := range lines {
		for _, k := range keys {
			if strings.Contains(ln, k) {
				if len(ln) <= hugeLine && !IsError(ln) {
					lines[i] = Relativize(c, ln)
					changed = true
				}
				break
			}
		}
	}
	if !changed {
		return s
	}
	return strings.Join(lines, "\n")
}

func genericShape(c *Context, s string) (string, string) {
	lines := strings.Split(s, "\n")

	logShaped := false
	if t := strings.TrimSpace(s); t != "" && (t[0] == '{' || t[0] == '[') {
		// NDJSON logs (one object per line, with time/level fields) read
		// better as templates than as a table cut at 20 rows.
		logShaped = looksNDJSON(lines)
		if !logShaped {
			if j, ok := CompactJSON(s, genericJSONBudget); ok {
				return j, "json"
			}
		}
	}
	if l, ok := TemplateLogs(lines); ok {
		l = shortenAll(l)
		// Keep the templated view only when it clearly pays off; otherwise
		// the line pipeline (which keeps every line's shape) is better.
		if tokens.Count(strings.Join(l, "\n")) < tokens.Count(s)*3/4 {
			return strings.Join(l, "\n"), "log"
		}
	}
	if paths, ok := lsRecursivePaths(lines); ok {
		return renderPaths(paths, nil), "paths"
	}
	if LooksLikePathList(lines) {
		var paths, other []string
		for _, ln := range lines {
			switch {
			case strings.TrimSpace(ln) == "":
			case isPathLike(ln):
				paths = append(paths, ln)
			default:
				other = append(other, ln)
			}
		}
		return renderPaths(paths, other), "paths"
	}

	lines = dropProgress(lines)
	lines = CollapseRuns(lines)
	if g, ok := GroupGoroutines(lines); ok {
		lines = g
	} else {
		lines = FoldStacks(c, lines)
	}
	lines = CollapseSimilar(lines)
	return strings.TrimRight(strings.Join(shortenAll(lines), "\n"), "\n"), "lines"
}

// renderPaths prints non-path lines (e.g. "find: permission denied") first,
// then the path tree under a count header.
func renderPaths(paths, other []string) string {
	out := append([]string(nil), other...)
	out = append(out, fmt.Sprintf("[%s]", Plural(len(paths), "path", "paths")))
	out = append(out, FactorPaths(paths)...)
	return strings.Join(out, "\n")
}

// hugeLine: past this many bytes a line is minified code or a data blob.
// Classifying it in full costs ~0.5 s/MB, so Generic looks only at the part
// it would show.
const hugeLine = 16 << 10

func shortenAll(lines []string) []string {
	for i, ln := range lines {
		lines[i] = shortenLine(ln, 400)
	}
	return lines
}

// shortenLine is ShortenLine, except that for huge lines the 3× room for
// error lines is granted when the text that 3× room would show (head and
// tail) is error-class, instead of classifying the whole line.
func shortenLine(ln string, max int) string {
	if len(ln) <= hugeLine {
		return ShortenLine(ln, max)
	}
	r := []rune(ln)
	n := len(r)
	room := max
	if big := max * 3; IsError(string(r[:big*3/4])) || IsError(string(r[n-big/5:])) {
		room = big
	}
	headN, tailN := room*3/4, room/5
	return fmt.Sprintf("%s …[+%d chars]… %s", string(r[:headN]), n-headN-tailN, string(r[n-tailN:]))
}

// dropProgress is DropProgress for normal-length lines; progress frames are
// never huge, so huge lines skip the check.
func dropProgress(lines []string) []string {
	out := lines[:0:0]
	for _, ln := range lines {
		if len(ln) <= hugeLine && IsProgress(ln) {
			continue
		}
		out = append(out, ln)
	}
	return out
}

// looksNDJSON reports whether most non-empty lines are single-line JSON
// objects carrying a timestamp or level (a JSON-lines log).
func looksNDJSON(lines []string) bool {
	n, obj := 0, 0
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		n++
		if strings.HasPrefix(t, "{") && strings.HasSuffix(t, "}") && isLogLine(t) {
			obj++
		}
	}
	return n >= 2 && obj*10 >= n*9
}
