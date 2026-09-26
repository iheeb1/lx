package jstools

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/engine"
)

// npmLs condenses `npm ls` trees (and pnpm ls, yarn list, bun pm ls). The
// box-drawing prefixes ("│ │ ├─┬ ") cost more tokens than the package
// names, so the tree is re-drawn with two spaces per level. With
// --all/--depth, npm's "deduped" leaves (a package already shown elsewhere
// in the tree) are folded into a "[+N deduped]" count on their parent, so
// every installed package@version is still listed once. Without them
// nothing is folded (workspace roots show deduped entries at depth 1 even
// then, and the view must stay Faithful).
// UNMET/invalid/extraneous/missing/overridden entries and every npm error
// line are always kept. When packages are named (`npm ls debug`) the
// deduped lines are the answer and are kept.
//
// npm ls is Content: package names such as http-errors are not errors.
type npmLs struct{}

func (npmLs) Name() string { return "npm-ls" }

func (npmLs) Match(c *engine.Context) bool {
	_, sub, rest, ok := manager(c)
	return ok && sub == "ls" &&
		!hasArg(rest, "--json", "--parseable", "-p", "--long", "-l", "--help", "-h", "--porcelain")
}

func (npmLs) IsContent() bool { return true }

// Faithful: deduped leaves are folded only with --all/--depth (see
// lsFolds); otherwise re-drawing the tree loses nothing. The two must agree:
// a folded view that claimed to be faithful would print no receipt, and the
// folded entries could not be recovered.
func (npmLs) Faithful(c *engine.Context) bool { return !lsFolds(c) }

// lsFolds reports whether deduped leaves are folded: only for a full tree
// (--all/--depth) and never when packages are named (`npm ls debug`: the
// deduped lines are the answer). Without --all, npm ls still shows the
// dependencies of workspaces at depth 1, deduped ones included; those are
// few and stay.
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
	// npm, pnpm: "│ │ ├─┬ name@1.0.0 deduped": indent units "│ " or "  ",
	// then the branch.
	lsTreeRe = regexp.MustCompile(`^((?:│ |  )*)[├└]─[─┬] (.+)$`)
	// yarn: "│  └─ name@1.0.0": indent units of 3.
	lsYarnTreeRe = regexp.MustCompile(`^((?:│  |   )*)[├└]─ (.+)$`)
	// pnpm: "│" spacer and "│   dependencies:" section lines, and the
	// legend of colors that normalizing removed.
	lsPnpmBarRe     = regexp.MustCompile(`^│\s*$`)
	lsPnpmSectionRe = regexp.MustCompile(`^│\s+(\S.*:)$`)
	lsPnpmLegendRe  = regexp.MustCompile(`^Legend: production dependency, optional only, dev only$`)
	lsValueFlags    = map[string]bool{"--depth": true, "--filter": true, "-F": true, "--prefix": true, "-w": true,
		"--workspace": true, "--omit": true, "--include": true, "--dir": true, "-C": true, "--pattern": true}
	lsKeepRe  = regexp.MustCompile(`\b(?:invalid|extraneous|missing|overridden)\b|UNMET`)
	lsDedupRe = regexp.MustCompile(` deduped$`)
)

func (npmLs) Apply(c *engine.Context, s string) (string, bool) {
	fold := lsFolds(c)
	lines := strings.Split(s, "\n")
	var (
		r         []string
		counts    = map[int]int{} // output index → deduped children folded
		parents   []int           // output index of the last entry at each depth
		folded    int
		entries   int
		lastEntry int // index after the last tree line
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
				// spacer, and a legend for colors normalizing removed
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
