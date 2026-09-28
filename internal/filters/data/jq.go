package data

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type jqFilter struct{}

func (jqFilter) Name() string    { return "jq" }
func (jqFilter) IsContent() bool { return true }

func (jqFilter) Match(c *engine.Context) bool { return isJQ(effective(c).Name()) }

var (
	jqDiagRe = lazyre.New(`^(?:jq|gojq|jaq): |^jq: error|^Error: |^parse error: `)

	jqGluedRe = lazyre.New(`(?:jq|gojq): (?:error|parse error)\b|parse error: .+ at line \d+, column \d+$`)
)

func jqGlued(ln string) int {
	if !strings.Contains(ln, "jq: ") && !strings.Contains(ln, "parse error: ") {
		return -1
	}
	if loc := jqGluedRe.FindStringIndex(ln); loc != nil {
		return loc[0]
	}
	return -1
}

func isJQDiag(ln string) bool { return jqDiagAt(ln) || jqGlued(ln) > 0 }

func jqDiagAt(ln string) bool {
	return ln != "" && strings.IndexByte("jgEp", ln[0]) >= 0 && jqDiagRe.MatchString(ln)
}

func jqRaw(args []string) bool {
	for _, a := range args {
		if a == "--" {
			break
		}
		switch a {
		case "--raw-output", "--join-output", "--raw-output0", "--ascii-output":
			return a != "--ascii-output"
		}
		if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], "rj") {
			return true
		}
	}
	return false
}

func (jqFilter) Apply(c *engine.Context, out string) (string, bool) {
	e := effective(c)
	total := countTokens(out)
	if total <= fileBudget {
		return out, true
	}
	lines := strings.Split(out, "\n")
	var diags, data []string
	for _, ln := range lines {
		switch k := jqGlued(ln); {
		case jqDiagAt(ln):
			diags = append(diags, ln)
		case k > 0:
			data = append(data, ln[:k])
			diags = append(diags, ln[k:])
		default:
			data = append(data, ln)
		}
	}
	if !jqRaw(e.Args()) {
		if j, ok := compactJSON(strings.Join(data, "\n"), fileBudget); ok {
			hdr := fmt.Sprintf("[lx: jq output condensed from ~%s tokens: tables for arrays of objects, long arrays cut; not valid JSON — narrow the filter for exact values]", humanTokens(total))
			res := append([]string{hdr}, strings.Split(j, "\n")...)
			return joinLines(append(res, diags...)), true
		}
	}
	w := defaultWindow("", "", isJQDiag)
	hdr := fmt.Sprintf("[lx: jq output, %s, ~%s tokens: showing the first and last lines exactly; omitted range marked below]",
		engine.Plural(len(lines), "line", "lines"), humanTokens(total))
	return hdr + "\n" + joinLines(w.apply(lines)), true
}
