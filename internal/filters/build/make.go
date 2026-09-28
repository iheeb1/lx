package build

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type makeFilter struct{}

func (makeFilter) Name() string { return "make" }

func (makeFilter) GuardsErrors() bool { return true }

func (makeFilter) Match(c *engine.Context) bool {
	if !makeNameRe.MatchString(baseName(c.Name())) {
		return false
	}

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
				break
			}
			if strings.IndexByte("npqdvh", ch) >= 0 {
				return false
			}
		}
	}
	return true
}

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

type cmakeBuild struct{}

func (cmakeBuild) Name() string { return "cmake-build" }

func (cmakeBuild) GuardsErrors() bool { return true }

func (cmakeBuild) Match(c *engine.Context) bool {
	return baseName(c.Name()) == "cmake" && hasArg(c.Args(), "--build") && !hasArg(c.Args(), "--help", "-h", "--version")
}

func (cmakeBuild) Apply(c *engine.Context, out string) (string, bool) {
	return applyNative(c, out)
}

type ninjaFilter struct{}

func (ninjaFilter) Name() string { return "ninja" }

func (ninjaFilter) GuardsErrors() bool { return true }

func (ninjaFilter) Match(c *engine.Context) bool {
	if baseName(c.Name()) != "ninja" {
		return false
	}

	return !hasArg(c.Args(), "-t", "-n", "--version", "-h", "--help") && !hasPrefixArg(c.Args(), "-t")
}

func (ninjaFilter) Apply(c *engine.Context, out string) (string, bool) {
	return applyNative(c, out)
}

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

		case a == "-E" || a == "-M" || a == "-MM" || a == "-###" || a == "-v" || a == "--version" ||
			a == "--help" || strings.HasPrefix(a, "--help=") || strings.HasPrefix(a, "-dump") ||
			strings.HasPrefix(a, "-print-") || strings.HasPrefix(a, "--print-"):
			return false
		case a == "-o" && i+1 < len(args) && (args[i+1] == "-" || args[i+1] == "/dev/stdout"):
			return false
		case a == "-o-" || a == "-o/dev/stdout":
			return false

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
