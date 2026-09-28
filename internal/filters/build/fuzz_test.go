package build

import (
	"regexp"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/textutil"
)

var (
	fuzzMakeErrRe  = regexp.MustCompile(`^(?:\S*/)?g?make(?:\[\d+\])?: \*\*\* `)
	fuzzFailedRe   = regexp.MustCompile(`^test result: FAILED\.|^test .+ \.\.\. FAILED$`)
	fuzzMvnErrorRe = regexp.MustCompile(`^\[ERROR\] `)
)

func FuzzFilters(f *testing.F) {
	for _, fx := range loadFixtures(f) {
		f.Add(fx.Clean(), fx.Meta.ExitCode)
	}
	f.Add("", 0)
	f.Add("make: *** [x] Error 1", 2)
	f.Add("In file included from a.c:1:\nIn file included from b.h:2:\nb.h:3:1: error: x\n    3 | y\n      | ^", 1)
	f.Add("a.c:1:1: warning: w 'a'\na.c:2:1: warning: w 'b'\na.c:3:1: warning: w 'c'\na.c:1:1: warning: w 'a'", 0)
	f.Add("for x in a b; do \\\n\techo $x; \\\n\tdone\ngo test ./...\nok  \tx\t0.1s", 0)
	f.Add("failures:\n\n---- t stdout ----\nthread 't' panicked at src/a.rs:1:1:\nboom\n\ntest result: FAILED. 0 passed; 1 failed; 0 ignored; 0 measured; 0 filtered out", 101)
	f.Fuzz(func(t *testing.T, in string, exit int) {
		in = textutil.Clean(in)
		for name, argv := range filterArgv {
			c := &engine.Context{Argv: argv, Exit: exit, Cwd: "/home/user/src/demo", Home: "/home/user"}
			flt := engine.Find(c)
			if flt == nil || flt.Name() != name {
				t.Fatalf("%v: found %v, want %s", argv, flt, name)
			}
			out1, ok1 := flt.Apply(c, in)
			out2, ok2 := flt.Apply(c, in)
			if out1 != out2 || ok1 != ok2 {
				t.Fatalf("%s is not deterministic", name)
			}
			if !ok1 {
				continue
			}
			squashed := squash(strings.ReplaceAll(out1, "\n", " \x00 "))
			for _, ln := range strings.Split(in, "\n") {
				var must bool
				switch name {
				case "make", "cmake-build":
					must = fuzzMakeErrRe.MatchString(ln)
				case "cargo-test":
					must = fuzzFailedRe.MatchString(ln)
				case "maven":

					msg := strings.TrimPrefix(ln, "[ERROR]")
					must = fuzzMvnErrorRe.MatchString(ln) && !mvnHelpFooter.MatchString(strings.TrimSpace(msg)) &&
						!(mvnFrameRe.MatchString(strings.TrimPrefix(msg, " ")) && strings.Contains(out1, "library frames"))
				}
				if must && !strings.Contains(squashed, squash(ln)) {
					t.Fatalf("%s lost %q:\n%s", name, ln, out1)
				}
			}
		}
	})
}
