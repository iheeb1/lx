package doctor

import (
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/hook"
)

func (s *state) checkLimits() {
	const id = "limit"
	if v := strings.TrimSpace(s.e.Getenv("LX_MAX_CHARS")); v != "" {
		n, err := strconv.Atoi(v)
		switch {
		case err != nil:
			s.add(id, Warn, "LX_MAX_CHARS="+v+" is not a number of characters, so lx ignores it", "set it like LX_MAX_CHARS=20000, or unset it")
		case n <= 0:
			s.add(id, OK, "LX_MAX_CHARS="+v+": views have no length limit", "")
			return
		default:
			s.add(id, OK, "LX_MAX_CHARS="+v+": views stay under "+groupInt(max(n, 1000))+" characters, receipt included, whether the command passes or fails", "")
			return
		}
	}
	bashMax := strings.TrimSpace(s.e.Getenv("BASH_MAX_OUTPUT_LENGTH"))
	dir := s.e.Getenv("CLAUDE_PROJECT_DIR")
	if dir == "" {
		dir = s.e.Cwd
	}
	l := hook.OutputLimitsFrom(s.e.ManagedPath, dir, s.e.Home, s.e.ConfigDir, bashMax)
	if s.e.Getenv("CLAUDECODE") != "1" && l.Setting == 0 && bashMax == "" {
		return
	}
	msg := "Claude Code shows " + groupInt(l.Pass) + " characters of a command's output inline, " + groupInt(l.Fail) + " when the command fails"
	n, err := strconv.Atoi(bashMax)
	switch {
	case l.Setting != 0 && bashMax != "":
		msg += " (bashOutputMaxChars in " + s.show(l.From) + "; it ignores BASH_MAX_OUTPUT_LENGTH)"
	case l.Setting != 0:
		msg += " (bashOutputMaxChars in " + s.show(l.From) + ")"
	case err == nil && n > l.Pass:
		msg += " (BASH_MAX_OUTPUT_LENGTH=" + bashMax + " enlarges only the window a failing command's excerpt is cut from; the bashOutputMaxChars setting raises the inline limit)"
	case err == nil && n > 0:
		msg += " (BASH_MAX_OUTPUT_LENGTH=" + bashMax + ")"
	}
	s.add(id, OK, msg+"; lx keeps each view to 90% of that", "")
}
