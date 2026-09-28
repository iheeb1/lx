package golang

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tokens"
)

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

func goToolLine(ln string) bool {
	return diagRe.MatchString(ln) || buildHdrRe.MatchString(ln) || exitRe.MatchString(ln) ||
		strings.HasPrefix(ln, "go: ") || strings.HasPrefix(ln, "[go: downloading ") ||
		strings.HasPrefix(ln, "[go -x: ") || strings.HasPrefix(ln, "# get ") ||
		strings.HasPrefix(ln, "vet: ") || ln == "too many errors"
}

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

type machine struct{}

func (machine) Name() string { return "go-machine" }

func (machine) Match(c *engine.Context) bool {
	if !isGo(c) {
		return false
	}
	sub, args := goArgs(c)
	switch sub {
	case "", "run", "tool", "generate":
		return false
	}
	return flagSet(args, "json") || sub == "list" && flagSet(args, "f")
}

func (machine) IsContent() bool { return true }

func (machine) Apply(c *engine.Context, out string) (string, bool) {
	if strings.TrimSpace(out) == "" || tokens.Count(out) > engine.DefaultBudget {
		return "", false
	}
	return out, true
}
