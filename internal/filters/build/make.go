package build

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// makeFilter renders make output (any target) with the native engine:
//
//   - recipe echoes of build tools (cc/gcc/clang, ar, ranlib, ld, ln, …,
//     recursive $(MAKE)) and cmake/automake/kbuild progress steps are
//     counted in one "[lx: hidden: …]" line, except the command right before
//     an error, which is kept;
//   - "make[N]: Entering/Leaving directory" lines are counted; the one that
//     places a kept line in a sub-directory is shown before it;
//   - every "make: *** …" line and every other make message is kept;
//   - C/C++ diagnostics: each error block (message, source line, caret,
//     notes) is kept; repeated warnings are grouped with exact counts
//     (" [×N]", "same warning at N more locations: …"), each distinct
//     message is kept once with its location; long include chains keep
//     their first and last line; after the first three, source excerpts
//     quoting system headers (libc++, SDKs) are counted, their header
//     lines kept;
//   - linker errors are kept verbatim;
//   - output of inner tools echoed as plain commands (go vet, go test, npm
//     test …) or recognizable by content (go test in a shell loop) goes
//     through their own filter, up to the next make line, recipe echo,
//     progress step or C-toolchain diagnostic (a silent recipe's compiler
//     errors are not the inner tool's); data (Content) filters are never
//     used; anything else goes through the generic reducer, where lines
//     starting with a source location are never folded as "similar".
type makeFilter struct{}

func (makeFilter) Name() string { return "make" }

// GuardsErrors: hidden command echoes can be error-class by accident
// (-Werror, -Wfatal-errors in the flags); applyNative runs the guard over
// every other line (see selfGuard).
func (makeFilter) GuardsErrors() bool { return true }

func (makeFilter) Match(c *engine.Context) bool {
	if !makeNameRe.MatchString(baseName(c.Name())) {
		return false
	}
	// Dry runs print the commands (they are the content), -p/-q/-d/-v/-h
	// print data, answers or help.
	if hasArg(c.Args(), "--just-print", "--dry-run", "--recon", "--print-data-base", "--question",
		"--version", "--help", "--debug", "--trace") {
		return false
	}
	for _, a := range c.Args() {
		if a == "--" {
			break
		}
		if len(a) < 2 || a[0] != '-' || a[1] == '-' {
			continue
		}
		for k := 1; k < len(a); k++ {
			ch := a[k]
			if strings.IndexByte("CfIoWjlEO", ch) >= 0 {
				break // the rest is this flag's value
			}
			if strings.IndexByte("npqdvh", ch) >= 0 {
				return false
			}
		}
	}
	return true
}

// Stream: by convention these targets start a dev server or a watcher
// ("make dev", "make serve", "make watch"), which buffering would hang.
func (makeFilter) Stream(c *engine.Context) bool {
	for _, a := range c.Args() {
		switch a {
		case "--":
			return false
		case "dev", "serve", "server", "watch", "start", "run-dev", "dev-server":
			return true
		}
	}
	return false
}

func (makeFilter) Apply(c *engine.Context, out string) (string, bool) {
	return applyNative(c, out)
}

// cmakeBuild renders `cmake --build` (Makefile and Ninja generators).
type cmakeBuild struct{}

func (cmakeBuild) Name() string { return "cmake-build" }

func (cmakeBuild) GuardsErrors() bool { return true }

func (cmakeBuild) Match(c *engine.Context) bool {
	return baseName(c.Name()) == "cmake" && hasArg(c.Args(), "--build") && !hasArg(c.Args(), "--help", "-h", "--version")
}

func (cmakeBuild) Apply(c *engine.Context, out string) (string, bool) {
	return applyNative(c, out)
}

// ninjaFilter renders ninja builds: "[3/10] …" steps are counted, "FAILED:"
// with its command, the diagnostics and "ninja: build stopped" are kept.
type ninjaFilter struct{}

func (ninjaFilter) Name() string { return "ninja" }

func (ninjaFilter) GuardsErrors() bool { return true }

func (ninjaFilter) Match(c *engine.Context) bool {
	if baseName(c.Name()) != "ninja" {
		return false
	}
	// -t runs a tool (targets, query, graph …) whose output is data; -n is
	// a dry run whose steps are the content.
	return !hasArg(c.Args(), "-t", "-n", "--version", "-h", "--help") && !hasPrefixArg(c.Args(), "-t")
}

func (ninjaFilter) Apply(c *engine.Context, out string) (string, bool) {
	return applyNative(c, out)
}

// ccFilter renders direct compiler invocations: diagnostics grouped as for
// make, linker errors kept.
type ccFilter struct{}

func (ccFilter) Name() string { return "cc" }

func (ccFilter) GuardsErrors() bool { return true }

func (ccFilter) Match(c *engine.Context) bool {
	args := c.Args()
	name := baseName(c.Name())
	if ccWrappers[name] && len(args) > 0 {
		name, args = baseName(args[0]), args[1:]
	}
	if !ccNameRe.MatchString(name) || name == "cpp" {
		return false
	}
	for i, a := range args {
		switch {
		case a == "--":
			return true
		// Output that is data, not diagnostics: preprocessed source,
		// dependency lists, driver traces, versions, help, search paths.
		case a == "-E" || a == "-M" || a == "-MM" || a == "-###" || a == "-v" || a == "--version" ||
			a == "--help" || strings.HasPrefix(a, "--help=") || strings.HasPrefix(a, "-dump") ||
			strings.HasPrefix(a, "-print-") || strings.HasPrefix(a, "--print-"):
			return false
		case a == "-o" && i+1 < len(args) && (args[i+1] == "-" || args[i+1] == "/dev/stdout"):
			return false
		case a == "-o-" || a == "-o/dev/stdout":
			return false
		// Machine-readable diagnostics (gcc JSON/SARIF, clang SARIF) on the
		// terminal, and clang's AST dumps (-Xclang -ast-dump): data.
		case strings.HasPrefix(a, "-fdiagnostics-format=json") && !strings.HasSuffix(a, "-file"),
			strings.HasPrefix(a, "-fdiagnostics-format=sarif") && !strings.HasSuffix(a, "-file"),
			strings.HasPrefix(a, "-ast-"):
			return false
		}
	}
	return true
}

func (ccFilter) Apply(c *engine.Context, out string) (string, bool) {
	return applyNative(c, out)
}

// hasPrefixArg reports an argument starting with one of prefixes (short
// flags glued to their value, like -tquery).
func hasPrefixArg(args []string, prefixes ...string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		for _, p := range prefixes {
			if strings.HasPrefix(a, p) {
				return true
			}
		}
	}
	return false
}
