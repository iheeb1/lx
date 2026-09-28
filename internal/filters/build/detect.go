package build

import (
	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

func init() {
	engine.RegisterDetector(engine.Detector{
		Name:   "cargo-test",
		Detect: detectCargoTest,
		Filter: cargoTest{},
		Argv:   []string{"cargo", "test"},
	})
}

func detectCargoTest(s string) bool {
	return engine.HasLinePrefix(s, "running ", runningNRe.MatchString) &&
		engine.HasLinePrefix(s, "test result: ", testResultRe.MatchString) &&
		!engine.HasLine(s, " ... bench: ", func(ln string) bool { return testLineRe.MatchString(ln) }) &&
		!nextestReport(s)
}

var (
	nextestStartRe   = lazyre.New(`^ +Starting \d+ tests? across \d+ binar(?:y|ies)`)
	nextestSummaryRe = lazyre.New(`^ +Summary \[ *\d+(?:\.\d+)?s\] \d+ tests? run: `)
	nextestOutputRe  = lazyre.New(`^(?:─{2,}|-{3}) (?:STDOUT|STDERR|OUTPUT): +\S`)
)

func nextestReport(s string) bool {
	return engine.HasLine(s, "Nextest run ID ", func(string) bool { return true }) ||
		engine.HasLine(s, "Starting ", nextestStartRe.MatchString) ||
		engine.HasLine(s, "Summary [", nextestSummaryRe.MatchString) ||
		engine.HasLine(s, "STDOUT:", nextestOutputRe.MatchString) ||
		engine.HasLine(s, "STDERR:", nextestOutputRe.MatchString)
}
