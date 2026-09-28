package jstest

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

func init() {
	engine.RegisterDetector(engine.Detector{Name: "jest", Detect: detectJest, Filter: jestFilter{}, Argv: []string{"jest"}})
	engine.RegisterDetector(engine.Detector{Name: "vitest", Detect: detectVitest, Filter: vitestFilter{}, Argv: []string{"vitest", "run"}})
	engine.RegisterDetector(engine.Detector{Name: "mocha", Detect: detectMocha, Filter: mochaFilter{}, Argv: []string{"mocha"}})
}

var vitestDurationRe = lazyre.New(`^   Duration  \d+(?:\.\d+)?(?:ms|s|m)\b`)

func jsonReport(s string) bool { return strings.Contains(s, `"numTotalTests"`) }

func detectJest(s string) bool {
	return !jsonReport(s) &&
		engine.HasLinePrefix(s, "Test Suites: ", jestSummaryRe.MatchString) &&
		engine.HasLinePrefix(s, "Tests: ", jestTestsRe.MatchString)
}

func detectVitest(s string) bool {
	if jsonReport(s) || engine.HasLinePrefix(s, " BENCH ", nil) {
		return false
	}
	return engine.HasLine(s, "Test Files  ", vitestFilesRe.MatchString) &&
		engine.HasLinePrefix(s, "   Duration  ", vitestDurationRe.MatchString)
}

func detectMocha(s string) bool {
	return !jsonReport(s) && engine.HasLine(s, " passing (", mochaPassingRe.MatchString)
}
