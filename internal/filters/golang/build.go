package golang

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tokens"
)

// build handles go build / go vet / go install / go test -c. Compiler and
// vet diagnostics are already compact, so they are kept verbatim with their
// "# pkg" headers and "exit status N" lines; exact duplicate diagnostics
// (the same error reported for a package and its test variant) are printed
// once with a count, and module download chatter is condensed.
type build struct{}

func (build) Name() string { return "go-build" }

func (build) Match(c *engine.Context) bool {
	if !isGo(c) {
		return false
	}
	sub, args := goArgs(c)
	switch sub {
	case "build", "vet", "install":
	case "test":
		if !flagSet(args, "c") || flagSet(args, "json") {
			return false
		}
	default:
		return false
	}
	return !flagSet(args, "json", "x", "n", "h", "help")
}

func (build) Apply(c *engine.Context, out string) (string, bool) {
	lines := splitLines(out)
	if len(lines) == 0 {
		return "", false
	}
	lines, _ = condenseFetch(lines)
	recognized := false
	count := map[string]int{}
	for _, ln := range lines {
		if goToolLine(ln) {
			recognized = true
		}
		if diagRe.MatchString(ln) {
			count[ln]++
		}
	}
	if !recognized {
		return "", false
	}
	if c.Failed() && !showsFailure(lines) {
		return "", false
	}
	res := make([]string, 0, len(lines))
	seen := map[string]bool{}
	for _, ln := range lines {
		if n := count[ln]; n > 0 {
			if seen[ln] {
				continue
			}
			seen[ln] = true
			if n > 1 {
				ln = fmt.Sprintf("%s [×%d]", ln, n)
			}
		}
		res = append(res, ln)
	}
	return engine.RelativizeNonErrors(c, strings.Join(res, "\n")), true
}

// goToolLine reports lines only the go command, the compiler or vet print.
func goToolLine(ln string) bool {
	return diagRe.MatchString(ln) || buildHdrRe.MatchString(ln) || exitRe.MatchString(ln) ||
		strings.HasPrefix(ln, "go: ") || strings.HasPrefix(ln, "[go: downloading ") ||
		strings.HasPrefix(ln, "[go -x: ") || strings.HasPrefix(ln, "# get ") ||
		strings.HasPrefix(ln, "vet: ") || ln == "too many errors"
}

// mod handles go mod tidy/download/vendor/verify/init and go get: the
// "go: downloading" lines and the -x "# get" trace are condensed into counted
// summary lines; every "go: upgraded/added/removed/downgraded … => …" line,
// every error and anything unrecognized is kept verbatim.
type mod struct{}

func (mod) Name() string { return "go-mod" }

func (mod) Match(c *engine.Context) bool {
	if !isGo(c) {
		return false
	}
	sub, args := goArgs(c)
	switch sub {
	case "get":
	case "mod":
		p := positionals(args)
		if len(p) == 0 {
			return false
		}
		switch p[0] {
		case "tidy", "download", "vendor", "verify", "init":
		default:
			return false
		}
	default:
		return false
	}
	return !flagSet(args, "json", "h", "help")
}

func (mod) Apply(c *engine.Context, out string) (string, bool) {
	lines := splitLines(out)
	if len(lines) == 0 {
		return "", false
	}
	recognized := false
	for _, ln := range lines {
		if goToolLine(ln) || ln == "all modules verified" {
			recognized = true
			break
		}
	}
	if !recognized {
		return "", false
	}
	lines, _ = condenseFetch(lines)
	if c.Failed() && !showsFailure(lines) {
		return "", false
	}
	return engine.RelativizeNonErrors(c, strings.Join(lines, "\n")), true
}

// showsFailure reports whether the condensed view of a failing go command
// still says why it failed: some line other than download / trace
// summaries, module updates and package headers. Without one (the error
// went elsewhere, the command was killed) the filter bails so the generic
// view shows everything, never a clean-looking summary with exit 1.
func showsFailure(lines []string) bool {
	for _, ln := range lines {
		switch {
		case strings.TrimSpace(ln) == "",
			strings.HasPrefix(ln, "[go: downloading "), strings.HasPrefix(ln, "[go -x: "),
			namedModRe.MatchString(ln), strings.HasPrefix(ln, "go: finding module "),
			buildHdrRe.MatchString(ln) && !engine.IsError(ln):
			continue
		}
		return true
	}
	return false
}

// list handles go list (packages or modules). The output is data — module
// paths such as github.com/pkg/errors are not errors — so it is marked as
// content and left as is apart from condensed download chatter; the budget
// stage trims very long listings with counted gaps.
type list struct{}

func (list) Name() string { return "go-list" }

func (list) Match(c *engine.Context) bool {
	if !isGo(c) {
		return false
	}
	sub, args := goArgs(c)
	return sub == "list" && !flagSet(args, "json", "f", "h", "help")
}

func (list) IsContent() bool { return true }

func (list) Apply(c *engine.Context, out string) (string, bool) {
	lines := splitLines(out)
	if len(lines) == 0 {
		return "", false
	}
	if l, changed := condenseFetch(lines); changed {
		return strings.Join(l, "\n"), true
	}
	return strings.Join(lines, "\n"), true
}

// machine keeps output the user asked the go command to format for a
// program verbatim: -json of any subcommand the go-test-json filter does not
// render (go list/vet/build/env/mod download/work edit -json, go test -json
// with -bench, -list, …) and go list -f templates. engine.MachineReadable
// exempts the go command's -json from its byte-for-byte passthrough (for
// go test -json), so without this filter such output reaches the generic
// reducer, which restructures JSON into tables. Returned unchanged, the
// never-worse gate passes it through untouched. Output too large for the
// token budget is left to the generic reducer (bail), whose structured
// summary reads better than JSON cut in the middle.
type machine struct{}

func (machine) Name() string { return "go-machine" }

func (machine) Match(c *engine.Context) bool {
	if !isGo(c) {
		return false
	}
	sub, args := goArgs(c)
	switch sub {
	case "", "run", "tool", "generate":
		// Flags after "go run pkg" / "go tool x" belong to that program.
		return false
	}
	return flagSet(args, "json") || sub == "list" && flagSet(args, "f")
}

// IsContent: the output is data; words like "error" in it are values.
func (machine) IsContent() bool { return true }

func (machine) Apply(c *engine.Context, out string) (string, bool) {
	if strings.TrimSpace(out) == "" || tokens.Count(out) > engine.DefaultBudget {
		return "", false
	}
	return out, true
}
