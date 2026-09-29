package engine

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
)

const genericJSONBudget = 2 * SmallOutput

func Generic(c *Context, s string) string {
	out, _ := GenericShape(c, s)
	return out
}

func GenericShape(c *Context, s string) (string, string) {
	in := s
	clean := textutil.Clean(s)
	fm := c.focus()
	h0 := fm.mark()
	out, name, ok := Detect(c, clean)
	fm.rollback(h0)
	if ok && tokens.Count(out) <= tokens.Count(in) {
		return out, DetectedPrefix + name
	}
	s = RelativizeNonErrors(c, clean)
	out, shape := genericShape(c, s)
	if tokens.Count(out) > tokens.Count(in) {
		fm.rollback(h0)
		return in, ""
	}
	return out, shape
}

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
		logShaped = looksNDJSON(lines)
		if !logShaped {
			if j, ok := CompactJSON(s, genericJSONBudget); ok {
				return j, "json"
			}
		}
	}
	if l, ok := templateLogs(c, lines); ok {
		l = shortenAll(l)
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
	lines = collapseSimilar(lines, modeOf(c).knobs().similarRun, c.focus())
	lines = JudgeChunks(c, lines)
	return strings.TrimRight(strings.Join(shortenAll(lines), "\n"), "\n"), "lines"
}

func renderPaths(paths, other []string) string {
	out := append([]string(nil), other...)
	out = append(out, fmt.Sprintf("[%s]", Plural(len(paths), "path", "paths")))
	out = append(out, FactorPaths(paths)...)
	return strings.Join(out, "\n")
}

const hugeLine = 16 << 10

func shortenAll(lines []string) []string {
	for i, ln := range lines {
		lines[i] = shortenLine(ln, 400)
	}
	return lines
}

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
