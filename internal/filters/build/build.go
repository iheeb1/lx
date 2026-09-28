// Package build handles make, C compilers, cargo, gradle and maven.
package build

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"path/filepath"
	"sort"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

func init() {
	engine.Register(makeFilter{})
	engine.Register(cmakeBuild{})
	engine.Register(ninjaFilter{})
	engine.Register(ccFilter{})
	engine.Register(cargoTest{})
	engine.Register(cargoFilter{})
	engine.Register(gradleFilter{})
	engine.Register(mavenFilter{})
}

func baseName(argv0 string) string {
	n := filepath.Base(argv0)
	for _, ext := range []string{".exe", ".cmd", ".bat"} {
		n = strings.TrimSuffix(n, ext)
	}
	return n
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

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

type counter struct {
	n     map[string]int
	order []string
}

func (c *counter) add(label string, k int) {
	if c.n == nil {
		c.n = map[string]int{}
	}
	if _, ok := c.n[label]; !ok {
		c.order = append(c.order, label)
	}
	c.n[label] += k
}

func (c *counter) total() int {
	t := 0
	for _, v := range c.n {
		t += v
	}
	return t
}

func (c *counter) String() string {
	labels := append([]string(nil), c.order...)
	sort.SliceStable(labels, func(i, j int) bool { return c.n[labels[i]] > c.n[labels[j]] })
	parts := make([]string, 0, len(labels))
	for _, l := range labels {
		if c.n[l] > 1 {
			parts = append(parts, fmt.Sprintf("%s ×%d", l, c.n[l]))
		} else {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, ", ")
}

func hiddenNote(parts ...string) string {
	var p []string
	for _, s := range parts {
		if s != "" {
			p = append(p, s)
		}
	}
	if len(p) == 0 {
		return ""
	}
	return "[lx: hidden: " + strings.Join(p, ", ") + "]"
}

func countPart(n int, one, many string) string {
	if n == 0 {
		return ""
	}
	return engine.Plural(n, one, many)
}

var shellSafeRe = lazyre.New(`^[\w@%+=:,./-]+(?: +[\w@%+=:,./*-]+)*$`)

func simpleArgv(line string) ([]string, bool) {
	t := strings.TrimSpace(line)
	if t == "" || len(t) > 4096 || !shellSafeRe.MatchString(t) {
		return nil, false
	}
	return strings.Fields(t), true
}

func alsoAt(sev string, locs []string) string {
	shown := locs
	if len(shown) > maxAlsoAt {
		shown = shown[:maxAlsoAt]
	}
	s := fmt.Sprintf("[lx: same %s at %d more %s: %s", sev, len(locs), plural(len(locs), "location", "locations"), strings.Join(shown, ", "))
	if len(locs) > len(shown) {
		s += fmt.Sprintf(", … +%d more", len(locs)-len(shown))
	}
	return s + "]"
}
