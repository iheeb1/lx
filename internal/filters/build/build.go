// Package build condenses the output of build tools and compilers.
//
// Filters (first match wins, registered in this order):
//
//	make         make, gmake, mingw32-make (any target)
//	cmake-build  cmake --build
//	ninja        ninja
//	cc           cc, gcc, g++, c++, clang, clang++ (also versioned and
//	             target-prefixed names, and behind ccache/sccache/distcc)
//	cargo-test   cargo test
//	cargo        cargo build|check|clippy|install|fetch|update (and aliases)
//	gradle       gradle, gradlew, ./gradlew
//	maven        mvn, mvnw, ./mvnw
//
// The native build filters (make, cmake-build, ninja, cc) share one engine
// (native.go): recipe command echoes and progress steps are counted, except
// the command right before an error; make's directory chatter is counted;
// every make "***" line, linker error and compiler error block (message,
// source line, caret) is kept verbatim; repeated warnings are grouped with
// exact counts; output of inner tools (go test, go vet, npm test …) is
// handed to their own filter when it applies cleanly, and anything else goes
// through the generic reducer.
//
// make, gradle and maven implement engine.Streamer for invocations that
// start a dev server or never finish (make dev/serve/watch, gradle bootRun /
// run / --continuous, mvn spring-boot:run / quarkus:dev …).
//
// Every filter here is engine.Guarded: some lines they hide on purpose are
// error-class only by accident (a compiler command line with -Werror or
// -Wfatal-errors, "Compiling quick-error v2.0.1", "Running tests/errors.rs"),
// so each filter runs the error guard itself (guard.go) over every other
// line, and the tests prove on every fixture that it never has to re-add
// anything.
package build

import (
	"fmt"
	"path/filepath"
	"regexp"
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

// baseName is the executable name without directory and .exe/.cmd/.bat.
func baseName(argv0 string) string {
	n := filepath.Base(argv0)
	for _, ext := range []string{".exe", ".cmd", ".bat"} {
		n = strings.TrimSuffix(n, ext)
	}
	return n
}

// hasArg reports whether args (up to a "--") contain one of names exactly,
// or name+"=…" for long flags.
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

// splitLines splits normalized output into lines, dropping one trailing
// empty line.
func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// squash collapses whitespace runs, as the error guard compares lines.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// counter counts labels in first-seen order.
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

// String renders "gcc ×34, ar, ranlib": most frequent first, ties in
// first-seen order; a count of 1 is left implicit.
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

// hiddenNote renders the one marker line a filter adds to say what it
// counted instead of showing: "[lx: hidden: 36 recipe commands (gcc ×34,
// ar, ranlib), 4 directory lines]". Empty parts are skipped.
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

// countPart renders "N what" (plural aware) or "" for zero.
func countPart(n int, one, many string) string {
	if n == 0 {
		return ""
	}
	return engine.Plural(n, one, many)
}

// shellSafeRe matches command lines that can be split on spaces without a
// shell: no quoting, expansion, redirection, pipes or command lists.
var shellSafeRe = regexp.MustCompile(`^[\w@%+=:,./-]+(?: +[\w@%+=:,./*-]+)*$`)

// simpleArgv splits a recipe echo into argv when it is a plain command.
func simpleArgv(line string) ([]string, bool) {
	t := strings.TrimSpace(line)
	if t == "" || len(t) > 4096 || !shellSafeRe.MatchString(t) {
		return nil, false
	}
	return strings.Fields(t), true
}

// alsoAt renders the marker for a warning (or note) kept once for several
// locations: "[lx: same warning at 3 more locations: a.c:4:1, a.c:9:2, … +1
// more]" — the first maxAlsoAt locations, then an exact count of the rest.
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
