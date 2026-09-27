// Package jstest condenses the output of JavaScript test runners (jest,
// vitest, mocha) and of Node.js crashes.
//
// Filters (first match wins, in this order):
//
//	jest       jest / npx jest / pnpm exec jest / yarn jest / bunx jest /
//	           react-scripts test / node node_modules/.bin/jest
//	vitest     vitest [run] and the same wrappers
//	mocha      mocha / _mocha and the same wrappers (spec/dot/list reporters)
//	npm-test   npm test, npm t, npm run test[:x], yarn test, pnpm test,
//	           bun test, bun run test: the runner is detected from the output
//	           (the "> command" echo line, or each runner's summary lines);
//	           unrecognized output bails to the generic reducer
//	node-crash node <script> ending in an uncaught exception
//	           ("Node.js vX" footer)
//
// Every runner view keeps the failing tests (message, expected/received,
// diff, the failing source line with its caret, application stack frames),
// every suite/file that failed to load, unhandled errors, and the runner's
// own summary lines verbatim; it drops passing tests with a count, source
// context around the failing line, and library stack frames (folded into
// one counted line). A non-zero exit with no failure in the view gets a
// "[lx: <runner> exited N but reported no failing test …]" line (console
// output of tests never counts as a failure, even when it holds "Error:").
// Views over briefAbove tokens degrade in announced levels (levels.go):
// failures after the first ten keep only messages, values and locations,
// then only names and new messages, then are counted as not shown.
//
// npm workspaces run a runner once per package: each run is rendered on
// its own (its own passing marker, no de-duplication across runs).
//
// The runner filters implement engine.Guarded: test titles, suite titles and
// source code are often "error"-looking text ("✓ handles errors",
// describe("error handling"), "12 |   } catch (error) {"), so instead of the
// generic guard every view runs its own safety net that re-adds any
// error-class line it did not structurally identify as such benign text
// (see doc.finish). node-crash keeps every non-frame line and relies on the
// engine guard.
package jstest

import (
	"github.com/iheeb1/lx/internal/lazyre"
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

func init() {
	engine.Register(jestFilter{})
	engine.Register(vitestFilter{})
	engine.Register(mochaFilter{})
	engine.Register(npmTestFilter{})
	engine.Register(nodeFilter{})
}

// invocation is argv with env assignments, `env` and package-manager
// wrappers peeled off.
type invocation struct {
	runner string   // "jest", "vitest", "mocha", "script" (npm test & co), "node" or ""
	args   []string // arguments after the runner / script name
}

var assignRe = lazyre.New(`^[A-Za-z_][A-Za-z0-9_]*=`)

// peelEnv removes leading VAR=value words and an `env [-i] [-u NAME] …`
// or `cross-env VAR=value …` wrapper.
func peelEnv(argv []string) []string {
	i := 0
	for i < len(argv) {
		a := argv[i]
		if assignRe.MatchString(a) {
			i++
			continue
		}
		if b := filepath.Base(a); b == "cross-env" || b == "cross-env-shell" {
			i++
			continue // its VAR=value words are peeled by the loop
		}
		if filepath.Base(a) == "env" {
			i++
			for i < len(argv) {
				b := argv[i]
				switch {
				case b == "-u" || b == "--unset" || b == "-C" || b == "--chdir":
					i += 2
				case b == "--":
					i++
				case strings.HasPrefix(b, "-"), assignRe.MatchString(b):
					i++
				default:
					goto next
				}
			}
		next:
			continue
		}
		break
	}
	return argv[i:]
}

// runnerName maps an executable or package name to a runner.
func runnerName(s string) string {
	s = filepath.Base(s)
	if at := strings.LastIndexByte(s, '@'); at > 0 {
		s = s[:at] // npx jest@29
	}
	s = strings.TrimSuffix(strings.TrimSuffix(s, ".js"), ".cmd")
	switch s {
	case "jest", "jest-cli":
		return "jest"
	case "vitest":
		return "vitest"
	case "mocha", "_mocha":
		return "mocha"
	}
	return ""
}

// skipFlags returns the index of the first non-flag argument, skipping the
// values of the given flags.
func skipFlags(args []string, withValue ...string) int {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			return i + 1
		}
		if !strings.HasPrefix(a, "-") {
			return i
		}
		for _, f := range withValue {
			if a == f {
				i++
				break
			}
		}
	}
	return len(args)
}

func isTestScript(s string) bool {
	return s == "test" || s == "tests" || s == "t" || s == "tst" ||
		strings.HasPrefix(s, "test:") || strings.HasPrefix(s, "test-") || strings.HasPrefix(s, "tests:")
}

// parseInvocation recognizes the runner behind argv.
func parseInvocation(c *engine.Context) invocation {
	argv := peelEnv(c.Argv)
	if len(argv) == 0 {
		return invocation{}
	}
	name := filepath.Base(argv[0])
	rest := argv[1:]
	if r := runnerName(argv[0]); r != "" {
		return invocation{runner: r, args: rest}
	}
	switch name {
	case "npx", "bunx", "pnpx":
		i := skipFlags(rest, "-p", "--package", "-c", "--call")
		if i < len(rest) {
			// npx jest@29, npx react-scripts test, npx cross-env CI=1 jest
			return wrapped(c, rest[i:])
		}
	case "react-scripts", "craco", "rescripts":
		if i := skipFlags(rest); i < len(rest) && rest[i] == "test" {
			return invocation{runner: "jest", args: rest[i+1:]}
		}
	case "node", "nodejs":
		i := skipFlags(rest, "-r", "--require", "--import", "--loader", "--experimental-loader", "-C", "--conditions")
		if i < len(rest) {
			p := rest[i]
			if strings.Contains(p, "node_modules/") || strings.HasPrefix(p, "node_modules") {
				for _, seg := range strings.Split(p, "/") {
					if r := runnerName(seg); r != "" {
						return invocation{runner: r, args: rest[i+1:]}
					}
				}
			}
		}
		for _, a := range rest {
			if a == "--" {
				break
			}
			if a == "--test" || strings.HasPrefix(a, "--test-") || a == "-e" || a == "--eval" ||
				a == "-p" || a == "--print" || a == "-v" || a == "--version" || a == "-h" || a == "--help" {
				return invocation{}
			}
		}
		if i < len(rest) {
			return invocation{runner: "node", args: rest[i:]}
		}
	case "npm", "pnpm", "yarn", "bun":
		i := skipFlags(rest, "--prefix", "-C", "--dir", "--filter", "-F", "-w", "--workspace", "--cwd")
		if i >= len(rest) {
			if name == "yarn" {
				return invocation{} // bare `yarn` is install
			}
			return invocation{}
		}
		sub := rest[i]
		after := rest[i+1:]
		switch {
		case sub == "exec" || sub == "x" || sub == "dlx":
			j := skipFlags(after, "-p", "--package", "-c", "--call")
			if j < len(after) {
				return wrapped(c, after[j:])
			}
		case sub == "run" || sub == "run-script" || sub == "rum" || sub == "urn":
			j := skipFlags(after)
			if j < len(after) && isTestScript(after[j]) {
				return invocation{runner: "script", args: after[j+1:]}
			}
		case name == "yarn" && sub == "workspace" && len(after) >= 2 && isTestScript(after[1]):
			return invocation{runner: "script", args: after[2:]}
		case name == "bun" && sub == "test":
			// bun's own runner prints jest-like but different output; the
			// npm-test filter bails unless it recognizes it.
			return invocation{runner: "script", args: after}
		case sub == "test" || sub == "t" || sub == "tst" || (name != "npm" && isTestScript(sub)):
			return invocation{runner: "script", args: after}
		case name != "npm":
			// yarn jest, pnpm vitest, bun mocha, yarn react-scripts test
			return wrapped(c, rest[i:])
		}
	}
	return invocation{}
}

// wrapped parses the command a package runner (npx, npm exec, yarn <bin>)
// runs. It never returns "script": `npx npm test` is not a test script run
// by the package manager that lx sees.
func wrapped(c *engine.Context, argv []string) invocation {
	sub := *c
	sub.Argv = argv
	if inv := parseInvocation(&sub); inv.runner != "script" {
		return inv
	}
	return invocation{}
}

// flagValue returns the value of --name=value / --name value / short, or "".
func flagValue(args []string, names ...string) (string, bool) {
	for i, a := range args {
		if a == "--" {
			break
		}
		for _, n := range names {
			if a == n {
				if i+1 < len(args) {
					return args[i+1], true
				}
				return "", true
			}
			if strings.HasPrefix(a, n+"=") {
				return a[len(n)+1:], true
			}
		}
	}
	return "", false
}

func hasArgPrefix(args []string, prefix string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if strings.HasPrefix(a, prefix) {
			return true
		}
	}
	return false
}

func hasArg(args []string, names ...string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		for _, n := range names {
			if a == n || strings.HasPrefix(n, "--") && strings.HasPrefix(a, n+"=") {
				return true
			}
		}
	}
	return false
}

// firstPositional returns the first non-flag argument.
func firstPositional(args []string) string {
	for _, a := range args {
		if a == "--" {
			return ""
		}
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

type jestFilter struct{}

func (jestFilter) Name() string { return "jest" }

func (jestFilter) Match(c *engine.Context) bool {
	inv := parseInvocation(c)
	if inv.runner != "jest" {
		return false
	}
	// Listings, config dumps and machine formats are not test runs.
	return !hasArg(inv.args, "--listTests", "--showConfig", "--json", "--version", "-v", "--help", "-h", "--init", "--clearCache")
}

func (jestFilter) Stream(c *engine.Context) bool {
	return watching(parseInvocation(c).args, "--watch", "--watchAll")
}

// watching reports a watch flag that is not explicitly turned off
// (react-scripts test --watchAll=false).
func watching(args []string, names ...string) bool {
	for _, a := range args {
		for _, n := range names {
			if a == n || strings.HasPrefix(n, "--") && strings.HasPrefix(a, n+"=") && !strings.HasSuffix(a, "=false") {
				return true
			}
		}
	}
	return false
}

func (jestFilter) GuardsErrors() bool { return true }

func (jestFilter) Apply(c *engine.Context, out string) (string, bool) {
	r, ok := renderJest(c, out)
	return r.out, ok
}

type vitestFilter struct{}

func (vitestFilter) Name() string { return "vitest" }

// vitestTextReporters print the default reporter's failure section.
var vitestTextReporters = map[string]bool{"": true, "default": true, "verbose": true, "basic": true, "dot": true, "agent": true, "tree": true, "minimal": true}

func (vitestFilter) Match(c *engine.Context) bool {
	inv := parseInvocation(c)
	if inv.runner != "vitest" {
		return false
	}
	switch firstPositional(inv.args) {
	case "list", "bench", "init":
		return false
	}
	// Machine reporters (json, junit, …) are fine only when they write to a
	// file next to a text reporter.
	text, machine := false, false
	for i, a := range inv.args {
		if a == "--" {
			break
		}
		v, ok := "", false
		switch {
		case a == "--reporter" && i+1 < len(inv.args):
			v, ok = inv.args[i+1], true
		case strings.HasPrefix(a, "--reporter="):
			v, ok = a[len("--reporter="):], true
		}
		if ok {
			if vitestTextReporters[v] {
				text = true
			} else {
				machine = true
			}
		}
	}
	if machine && !(text && hasArgPrefix(inv.args, "--outputFile")) {
		return false
	}
	return !hasArg(inv.args, "--version", "-v", "--help", "-h")
}

func (vitestFilter) Stream(c *engine.Context) bool {
	inv := parseInvocation(c)
	p := firstPositional(inv.args)
	return p == "watch" || p == "dev" || watching(inv.args, "--watch", "-w")
}

func (vitestFilter) GuardsErrors() bool { return true }

func (vitestFilter) Apply(c *engine.Context, out string) (string, bool) {
	r, ok := renderVitest(c, out)
	return r.out, ok
}

type mochaFilter struct{}

func (mochaFilter) Name() string { return "mocha" }

// mochaBaseReporters end with mocha's Base epilogue (passing/failing counts
// and numbered failures), which is what the mocha view parses.
var mochaBaseReporters = map[string]bool{"": true, "spec": true, "dot": true, "list": true, "progress": true, "min": true, "landing": true, "nyan": true}

func (mochaFilter) Match(c *engine.Context) bool {
	inv := parseInvocation(c)
	if inv.runner != "mocha" {
		return false
	}
	if v, ok := flagValue(inv.args, "--reporter", "-R"); ok && !mochaBaseReporters[v] {
		return false
	}
	return !hasArg(inv.args, "--version", "-V", "--help", "-h", "--list-files", "--list-reporters", "--list-interfaces", "--dry-run")
}

func (mochaFilter) Stream(c *engine.Context) bool {
	return watching(parseInvocation(c).args, "--watch", "-w")
}

func (mochaFilter) GuardsErrors() bool { return true }

func (mochaFilter) Apply(c *engine.Context, out string) (string, bool) {
	r, ok := renderMocha(c, out)
	return r.out, ok
}

// npmTestFilter handles package-manager test scripts, whose runner is only
// known from the output.
type npmTestFilter struct{}

func (npmTestFilter) Name() string { return "npm-test" }

func (npmTestFilter) Match(c *engine.Context) bool {
	return parseInvocation(c).runner == "script"
}

func (npmTestFilter) GuardsErrors() bool { return true }

// Stream: `npm test -- --watch` and the like keep running. "-w" is a watch
// flag only among the script's own arguments: before "--", npm's -w is
// --workspace (`npm test -w web`).
func (npmTestFilter) Stream(c *engine.Context) bool {
	inv := parseInvocation(c)
	if inv.runner != "script" {
		return false
	}
	if watching(inv.args, "--watch", "--watchAll") {
		return true
	}
	args := inv.args
	if name := filepath.Base(peelEnv(c.Argv)[0]); name == "npm" {
		k := 0
		for k < len(args) && args[k] != "--" {
			k++
		}
		args = args[min(k, len(args)):]
	}
	return watching(args, "-w")
}

func (npmTestFilter) Apply(c *engine.Context, out string) (string, bool) {
	r, ok := renderScript(c, out)
	return r.out, ok
}

// renderScript detects the runner of a test script from its output.
func renderScript(c *engine.Context, out string) (result, bool) {
	switch detectRunner(out) {
	case "jest":
		return renderJest(c, out)
	case "vitest":
		return renderVitest(c, out)
	case "mocha":
		return renderMocha(c, out)
	}
	return result{}, false
}

var (
	echoCmdRe      = lazyre.New(`^> (?:\S+/)?(?:npx |pnpm (?:exec )?|yarn |bunx |cross-env (?:\S+=\S+ )*|(?:[A-Z_]+=\S+ )+)?(jest|vitest|mocha|_mocha|react-scripts test|craco test)\b`)
	jestSummaryRe  = lazyre.New(`^Test Suites: .*\d+ total$`)
	jestTestsRe    = lazyre.New(`^Tests: +.*\d+ total$`)
	vitestFilesRe  = lazyre.New(`^ *Test Files {2}.*\(\d+\)$`)
	vitestRunRe    = lazyre.New(`^ RUN {2}v\d+\.\d+`)
	mochaPassingRe = lazyre.New(`^ {2}\d+ passing \(\d+(?:\.\d+)?(?:ms|s|m|h)\)$`)
)

// hasLine reports whether a line of s containing sub matches re. It looks
// only at lines holding sub, which keeps it linear and fast on huge output
// (a (?m) regexp over the whole text is not).
func hasLine(s, sub string, re *lazyre.Regexp) bool {
	for off := 0; off < len(s); {
		k := strings.Index(s[off:], sub)
		if k < 0 {
			return false
		}
		k += off
		start := strings.LastIndexByte(s[off:k], '\n') + 1 + off
		end := strings.IndexByte(s[k:], '\n')
		if end < 0 {
			end = len(s)
		} else {
			end += k
		}
		if re.MatchString(s[start:end]) {
			return true
		}
		off = end + 1
	}
	return false
}

// hasJestSummary: jest's "Test Suites:" and "Tests:" summary lines.
func hasJestSummary(s string) bool {
	return hasLine(s, "Test Suites: ", jestSummaryRe) && hasLine(s, "Tests: ", jestTestsRe)
}

// hasVitestSummary: vitest's "Test Files  3 failed | 10 passed (13)" line.
func hasVitestSummary(s string) bool { return hasLine(s, "Test Files  ", vitestFilesRe) }

// detectRunner names the runner whose output this is, or "".
func detectRunner(out string) string {
	for _, ln := range strings.SplitN(out, "\n", 40) {
		if m := echoCmdRe.FindStringSubmatch(ln); m != nil {
			switch m[1] {
			case "jest", "react-scripts test", "craco test":
				return "jest"
			case "vitest":
				return "vitest"
			default:
				return "mocha"
			}
		}
	}
	switch {
	case hasJestSummary(out):
		return "jest"
	case hasVitestSummary(out) || hasLine(out, " RUN  v", vitestRunRe):
		return "vitest"
	case hasLine(out, " passing (", mochaPassingRe):
		return "mocha"
	}
	return ""
}
