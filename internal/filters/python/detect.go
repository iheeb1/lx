package python

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

func init() {
	engine.RegisterDetector(engine.Detector{
		Name:   "pytest",
		Detect: detectPytest,
		Filter: pytestFilter{},
		Argv:   []string{"pytest"},
	})
}

var (
	ptSessionRe = lazyre.New(`^=+ test session starts(?: \([^)]*\))? =+$`)

	ptWrappedSummaryRe = lazyre.New(`^=+ (?:\d+ (?:subtests? )?(?:failed|passed|skipped|deselected|xfailed|xpassed|warnings?|errors?|rerun)(?:, )?)+ in \d+(?:\.\d+)?s(?:econds)?(?: \([\d:.]+\))? =+$`)
)

func detectPytest(s string) bool {
	sessions, at := 0, -1
	engine.ScanLines(s, " test session starts", func(ln string) bool {
		if ln != "" && ln[0] == '=' && ptSessionRe.MatchString(ln) {
			sessions++
			if at < 0 {
				at = strings.Index(s, ln)
			}
		}
		return sessions < 2
	})
	if sessions != 1 {
		return false
	}
	return engine.HasLine(s[at:], " in ", func(ln string) bool {
		return ln != "" && ln[0] == '=' && ptWrappedSummaryRe.MatchString(ln)
	})
}
