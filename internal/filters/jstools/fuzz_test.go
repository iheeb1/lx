package jstools

import (
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/textutil"
)

// fuzzArgv gives each filter a command line it matches.
var fuzzArgv = map[string][]string{
	"tsc":          {"npx", "tsc", "--noEmit"},
	"eslint":       {"npx", "eslint", "."},
	"npm-install":  {"npm", "install"},
	"npm-ls":       {"npm", "ls", "--all"},
	"npm-audit":    {"npm", "audit"},
	"npm-outdated": {"npm", "outdated"},
	"npm-run":      {"npm", "run", "build"},
}

// FuzzFilters: no filter panics, every filter is deterministic, and no
// error-class line disappears except through a documented reformatting,
// each checked on the input's structure:
//   - npm-install only folds npm's EUSAGE usage text and successful
//     "npm http" requests (installFoldedOnPurpose);
//   - tsc, eslint and npm-run only drop tsc code frames under a kept
//     diagnostic and the "Errors  Files" table after a "Found N errors"
//     line, and only factor eslint message lines (checked like the corpus:
//     every one keeps its file, line:col, message and rule, or an exact
//     count).
//
// Content filters (npm-ls, npm-audit, npm-outdated) show data; only
// determinism is checked for them.
func FuzzFilters(f *testing.F) {
	// Seeds: every capture, cut to 8 KB (on a line boundary) so that each
	// execution stays fast enough for the fuzzer to explore.
	for _, tc := range corpus {
		fc := tc.load(&testing.T{})
		raw := fc.Raw
		if len(raw) > 8192 {
			raw = raw[:strings.LastIndexByte(raw[:8192], '\n')+1]
		}
		f.Add(raw)
	}
	f.Add("src/a.ts:1:1 - error TS1005: ';' expected.\n\n1 x y\n    ~\n\n  src/b.ts:2:3\n    2 z\n      ~\n    related\n\nFound 1 error in src/a.ts:1\n")
	f.Add("/a/b.js\n  1:1  error  m  r\n  2:1  error  m  r\n  3:1  error  m  r\n  4:1  error  m  r\n\n✖ 4 problems (4 errors, 0 warnings)\n")
	f.Add("npm warn ERESOLVE overriding peer dependency\nnpm warn While resolving: a@1\nnpm warn Could not resolve dependency:\nnpm error x\n")
	f.Add("┌──┬──┐\n│ a │ b │\n├──┼──┤\n│ c │ d │\n└──┴──┘\n")
	f.Fuzz(func(t *testing.T, raw string) {
		in := textutil.Clean(raw)
		for _, fl := range engine.Filters() {
			argv, ok := fuzzArgv[fl.Name()]
			if !ok {
				continue
			}
			c := &engine.Context{Argv: argv, Exit: 1, Cwd: "/home/user/src/app", Home: "/home/user"}
			a, okA := fl.Apply(c, in)
			b, okB := fl.Apply(c, in)
			if a != b || okA != okB {
				t.Fatalf("%s: not deterministic", fl.Name())
			}
			if !okA {
				continue
			}
			missing := fixture.ErrorLinesMissing(in, a)
			switch fl.Name() {
			case "npm-install":
				missing = without(missing, func(ln string) bool { return installFoldedOnPurpose(in, a, ln) })
			case "tsc", "eslint", "npm-run":
				missing = checkESLintFactored(&testing.T{}, c, in, a, missing)
				frames, table := tscFrameLines(in, a), tscTableLines(in)
				missing = without(missing, func(ln string) bool {
					return frames[ln] || table[ln] || isRelativizedHeader(c, ln, a)
				})
			default:
				missing = nil
			}
			if len(missing) > 0 {
				t.Fatalf("%s lost error lines %q\n--- in ---\n%s\n--- out ---\n%s", fl.Name(), missing, in, a)
			}
		}
	})
}

// tscTableLines returns the lines of tsc's "Errors  Files" table (the
// header and its rows) that directly follows a "Found N errors" line,
// trimmed like ErrorLinesMissing's results.
func tscTableLines(in string) map[string]bool {
	table := map[string]bool{}
	lines := strings.Split(in, "\n")
	for i := 0; i < len(lines); i++ {
		if !tscFoundRe.MatchString(lines[i]) {
			continue
		}
		j := i + 1
		for j < len(lines) && lines[j] == "" {
			j++
		}
		if j >= len(lines) || !tscTableRe.MatchString(lines[j]) {
			continue
		}
		table[strings.TrimSpace(lines[j])] = true
		for j++; j < len(lines) && tscRowRe.MatchString(lines[j]); j++ {
			table[strings.TrimSpace(lines[j])] = true
		}
	}
	return table
}

// isRelativizedHeader: an eslint file header shown relative to the
// working directory.
func isRelativizedHeader(c *engine.Context, ln, out string) bool {
	rel := engine.Relativize(c, ln)
	return !strings.HasPrefix(ln, " ") && rel != ln && strings.Contains(out, rel)
}
