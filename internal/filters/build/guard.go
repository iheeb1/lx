package build

import (
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

var countSuffixRe = lazyre.New(` \[×\d+\]$`)

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

func isErrorLine(ln string) bool {
	return engine.IsError(neutralizeSeverity(ln))
}

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
