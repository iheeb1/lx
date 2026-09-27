package engine

import (
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/lazyre"
)

// Location and message metrics shared by the corpus benchmark, the filter
// fidelity tests (through internal/fixture) and `lx discover --fidelity`.
// They measure; nothing on lx's runtime path calls them.

// LocRe matches file:line[:col] locations in compiler, linter, test and
// stack-trace output.
var LocRe = lazyre.New(`[\w./@-]+\.(?:go|ts|tsx|js|jsx|mjs|cjs|py|rs|rb|java|kt|c|h|cc|cpp|cs|php|swift|vue|svelte)[:(]\d+`)

// locExt reports whether ext is one of LocRe's extensions (a switch, not
// a map: no init cost for lx's startup).
func locExt(ext string) bool {
	switch ext {
	case "go", "ts", "tsx", "js", "jsx", "mjs", "cjs", "py", "rs", "rb", "java", "kt",
		"c", "h", "cc", "cpp", "cs", "php", "swift", "vue", "svelte":
		return true
	}
	return false
}

// FindLocs is LocRe.FindAllString(s, -1), computed faster: a match never
// spans lines, so the regexp runs only on the lines that can hold one (an
// extension of LocRe's, then ':' or '(' and a digit), and the result is
// identical.
func FindLocs(s string) []string {
	var out []string
	for len(s) > 0 {
		line := s
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			line, s = s[:i], s[i+1:]
		} else {
			s = ""
		}
		if mayHoldLoc(line) {
			out = append(out, LocRe.FindAllString(line, -1)...)
		}
	}
	return out
}

// mayHoldLoc is a necessary condition for a LocRe match in line.
func mayHoldLoc(line string) bool {
	for i := 1; i < len(line)-1; i++ {
		if c := line[i]; (c == ':' || c == '(') && line[i+1] >= '0' && line[i+1] <= '9' {
			lo := max(0, i-7) // the longest extension, "svelte", plus its dot
			if j := strings.LastIndexByte(line[lo:i], '.'); j >= 0 && locExt(line[lo+j+1:i]) {
				return true
			}
		}
	}
	return false
}

// libLocRe marks locations inside dependencies, language runtimes and test
// harnesses — frames lx folds into counts by design.
var libLocRe = lazyre.New(`node_modules/|site-packages/|dist-packages/|/lib/python\d|/libexec/src/|/go/src/|/go\d[\w.]*/src/|_testmain\.go|node:internal|\.cargo/registry|/rustc/|/pkg/mod/`)

// LocKey is the identity of a LocRe match: base name + ":" + line, so a
// relativized path still matches ("/src/app/a.go:12" and "app/a.go:12"
// are both "a.go:12").
func LocKey(m string) string {
	m = strings.Replace(m, "(", ":", 1)
	return filepath.Base(m)
}

// LocationsMissing returns file:line locations present in in but absent
// from out. Locations are compared by base name + line so relativized paths
// still match.
func LocationsMissing(in, out string) []string {
	have := map[string]bool{}
	for _, m := range FindLocs(out) {
		have[LocKey(m)] = true
	}
	var missing []string
	seen := map[string]bool{}
	for _, m := range FindLocs(in) {
		k := LocKey(m)
		if !have[k] && !seen[k] {
			seen[k] = true
			missing = append(missing, m)
		}
	}
	return missing
}

// ErrorMessagesMissing reports the error-class lines of in whose message —
// the line's words with every token that contains a digit removed
// (positions, counts, durations) — does not appear in out. It suits
// filters that regroup diagnostics; locations are checked separately by
// LocationsMissing. The benchmark scores every strategy (lx, rtk,
// head/tail) with this same function.
func ErrorMessagesMissing(in, out string) []string {
	var norm strings.Builder
	for _, ln := range strings.Split(out, "\n") {
		norm.WriteString(messageOf(ln))
		norm.WriteByte('\n')
	}
	have := norm.String()
	var missing []string
	seen := map[string]bool{}
	for _, ln := range strings.Split(in, "\n") {
		m := messageOf(ln)
		if m == "" || seen[m] || !IsError(ln) {
			continue
		}
		seen[m] = true
		if !strings.Contains(have, m) {
			missing = append(missing, strings.TrimSpace(ln))
		}
	}
	return missing
}

// messageOf drops tokens containing digits and collapses whitespace.
func messageOf(line string) string {
	f := strings.Fields(line)
	out := f[:0]
	for _, w := range f {
		if strings.ContainsAny(w, "0123456789") {
			continue
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}

// AppLocations returns the distinct file:line locations in s that point at
// application code (not dependencies or runtimes), distinct by LocKey, in
// order of first appearance.
func AppLocations(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range FindLocs(s) {
		k := LocKey(m)
		if libLocRe.MatchString(m) || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, m)
	}
	return out
}

// AppLocationsMissing is LocationsMissing restricted to application code.
func AppLocationsMissing(in, out string) []string {
	have := map[string]bool{}
	for _, m := range FindLocs(out) {
		have[LocKey(m)] = true
	}
	var missing []string
	for _, m := range AppLocations(in) {
		if !have[LocKey(m)] {
			missing = append(missing, m)
		}
	}
	return missing
}

// SplitLoc splits a LocRe match into its path and line: "src/a.ts(12" →
// ("src/a.ts", "12"). ok is false for a string LocRe did not produce.
func SplitLoc(m string) (path, line string, ok bool) {
	i := strings.LastIndexAny(m, ":(")
	if i <= 0 || i == len(m)-1 {
		return "", "", false
	}
	for _, c := range m[i+1:] {
		if c < '0' || c > '9' {
			return "", "", false
		}
	}
	return m[:i], m[i+1:], true
}

// ViewLocKeys returns the LocKeys of the locations a reader finds in a
// view: every LocRe match, plus the locations of grouped listings, where a
// heading line holds the path alone and the lines under it start with the
// line number — lx's grep/rg view ("a/x.go" then "12:text", "40-context"),
// and eslint's stylish layout ("src/a.js" then "  12:5  error …"). The
// grouped form is read the way an agent reads it, so a location a grouped
// view shows counts as shown.
func ViewLocKeys(view string) map[string]bool {
	keys := map[string]bool{}
	for _, m := range FindLocs(view) {
		keys[LocKey(m)] = true
	}
	heading := "" // base name of the current group's path
	for _, ln := range strings.Split(view, "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case t == "":
			heading = ""
		case heading != "" && startsWithLineNo(t):
			n := 0
			for n < len(t) && t[n] >= '0' && t[n] <= '9' {
				n++
			}
			keys[heading+":"+t[:n]] = true
		case isBarePath(ln):
			heading = filepath.Base(ln)
		default:
			if !strings.HasPrefix(ln, " ") && !strings.HasPrefix(ln, "\t") && !strings.HasPrefix(t, "…") {
				heading = "" // an unindented line that is neither ends the group
			}
		}
	}
	return keys
}

// startsWithLineNo: "12:…", "12-…" or "12" followed by a space, as in the
// entries of a grouped listing.
func startsWithLineNo(t string) bool {
	n := 0
	for n < len(t) && t[n] >= '0' && t[n] <= '9' {
		n++
	}
	if n == 0 || n > 9 {
		return false
	}
	if n == len(t) {
		return false
	}
	return t[n] == ':' || t[n] == '-'
}

// isBarePath: an unindented line that is a single path with a source
// extension LocRe knows (the heading of a grouped listing).
func isBarePath(ln string) bool {
	if ln == "" || ln[0] == ' ' || ln[0] == '\t' || strings.ContainsAny(ln, " \t:") {
		return false
	}
	m := LocRe.FindString(ln + ":1")
	return m == ln+":1"
}
