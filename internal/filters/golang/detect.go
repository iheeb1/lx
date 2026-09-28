package golang

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

func init() {
	engine.RegisterDetector(engine.Detector{
		Name:   "go-test",
		Detect: detectGoTest,
		Filter: testText{},
		Argv:   []string{"go", "test"},
	})
}

var (
	benchResultRe = lazyre.New(`^Benchmark\S*\s+\d+\s+\d+(?:\.\d+)? \S+/\S+`)

	listNameRe = lazyre.New(`^(?:Test|Benchmark|Example|Fuzz)[\p{L}\p{N}_]*$`)
)

func detectGoTest(s string) bool {
	if !engine.HasLinePrefix(s, "ok  \t", okRe.MatchString) &&
		!engine.HasLinePrefix(s, "FAIL\t", failPkgRe.MatchString) &&
		!engine.HasLinePrefix(s, "?   \t", noFilesRe.MatchString) {
		return false
	}
	switch {
	case engine.HasLinePrefix(s, "Benchmark", benchResultRe.MatchString),
		engine.HasLinePrefix(s, "goos: ", nil) && engine.HasLinePrefix(s, "goarch: ", nil),
		engine.HasLinePrefix(s, "fuzz: elapsed: ", nil),
		engine.HasLinePrefix(s, "WORK=", nil):
		return false
	}

	if !engine.HasLinePrefix(s, "=== RUN", nil) && !strings.Contains(s, "--- ") {
		for _, p := range []string{"Test", "Example", "Benchmark", "Fuzz"} {
			if engine.HasLinePrefix(s, p, listNameRe.MatchString) {
				return false
			}
		}
	}
	return true
}
