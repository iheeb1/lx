package jstools

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

type npmLs struct{}

func (npmLs) Name() string { return "npm-ls" }

func (npmLs) Match(c *engine.Context) bool {
	_, sub, rest, ok := manager(c)
	return ok && sub == "ls" &&
		!hasArg(rest, "--json", "--parseable", "-p", "--long", "-l", "--help", "-h", "--porcelain")
}

func (npmLs) IsContent() bool { return true }

func (npmLs) Faithful(c *engine.Context) bool { return !lsFolds(c) }

func lsFolds(c *engine.Context) bool {
	_, _, rest, _ := manager(c)
	if !hasArg(rest, "--all", "-a", "--depth") {
		return false
	}
	for i := 0; i < len(rest); i++ {
		switch a := rest[i]; {
		case a == "--":
			return true
		case lsValueFlags[a]:
			i++
		case !strings.HasPrefix(a, "-"):
			return false
		}
	}
	return true
}

var (
	lsTreeRe = lazyre.New(`^((?:│ |  )*)[├└]─[─┬] (.+)$`)

	lsYarnTreeRe = lazyre.New(`^((?:│  |   )*)[├└]─ (.+)$`)

	lsPnpmBarRe     = lazyre.New(`^│\s*$`)
	lsPnpmSectionRe = lazyre.New(`^│\s+(\S.*:)$`)
	lsPnpmLegendRe  = lazyre.New(`^Legend: production dependency, optional only, dev only$`)
	lsValueFlags    = map[string]bool{"--depth": true, "--filter": true, "-F": true, "--prefix": true, "-w": true,
		"--workspace": true, "--omit": true, "--include": true, "--dir": true, "-C": true, "--pattern": true}
	lsKeepRe  = lazyre.New(`\b(?:invalid|extraneous|missing|overridden)\b|UNMET`)
	lsDedupRe = lazyre.New(` deduped$`)
)

func (npmLs) Apply(c *engine.Context, s string) (string, bool) {
	fold := lsFolds(c)
	lines := strings.Split(s, "\n")
	var (
		r         []string
		counts    = map[int]int{}
		parents   []int
		folded    int
		entries   int
		lastEntry int
	)
	var o out
	for _, ln := range lines {
		unit := 2
		m := lsTreeRe.FindStringSubmatch(ln)
		if m == nil {
			m, unit = lsYarnTreeRe.FindStringSubmatch(ln), 3
		}
		if m == nil {
			parents = parents[:0]
			switch {
			case lsPnpmBarRe.MatchString(ln), lsPnpmLegendRe.MatchString(ln):
			case lsPnpmSectionRe.MatchString(ln):
				r = append(r, lsPnpmSectionRe.FindStringSubmatch(ln)[1])
			default:
				r = append(r, ln)
			}
			continue
		}
		entries++
		depth := utf8.RuneCountInString(m[1]) / unit
		text := m[2]
		if fold && lsDedupRe.MatchString(text) && !lsKeepRe.MatchString(text) && depth > 0 && depth <= len(parents) {
			counts[parents[depth-1]]++
			folded++
			continue
		}
		parents = append(parents[:min(depth, len(parents))], len(r))
		r = append(r, strings.Repeat("  ", depth+1)+text)
		lastEntry = len(r)
	}
	if entries == 0 {
		return "", false
	}
	for idx, n := range counts {
		r[idx] += fmt.Sprintf(" [+%d deduped]", n)
	}
	if folded > 0 {
		note := fmt.Sprintf("[%s folded into [+N deduped] counts]", engine.Plural(folded, "deduped entry", "deduped entries"))
		r = append(r[:lastEntry], append([]string{note}, r[lastEntry:]...)...)
	}
	o.add(r...)
	return o.String(), true
}
