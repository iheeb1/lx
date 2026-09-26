// Package search condenses code-search output: grep, egrep, fgrep, git grep
// and rg matches, and rg --files path lists.
//
// Both filters are Content filters: matched lines are data, and a line
// holding "error" is a search hit, not a failure.
package search

import (
	"fmt"
	"sort"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/filters/fs"
)

func init() {
	engine.Register(rgFiles{})
	engine.Register(matches{})
}

// Caps: all matches are shown when there are at most maxMatches in at most
// maxFiles files. Beyond that, files are shown in output order with at most
// perFile matches each until maxMatches or maxFiles is reached, and every
// remaining file gets a "path: N matches" line.
const (
	perFile    = 15
	maxMatches = 150
	maxFiles   = 60
	// maxHistogram bounds the per-file count lines; the rest are counted
	// per directory.
	maxHistogram = 100
	// maxNotes bounds diagnostics of one kind ("Binary file x matches").
	maxNotes = 20
	// minHeavy: dependency-directory hits are summarized only when there
	// are more than this many; a summary of one or two hits costs more
	// than the hits.
	minHeavy = 5
)

// heavyDirs hold dependencies, VCS data and build output. Matches there are
// summarized unless the user searched inside them explicitly.
var heavyDirs = map[string]bool{
	"node_modules": true, "bower_components": true, "jspm_packages": true, "vendor": true,
	"dist": true, ".git": true, ".venv": true, "venv": true, "site-packages": true,
	"__pycache__": true, ".next": true, ".nuxt": true, ".svelte-kit": true, ".yarn": true,
}

// matches groups search hits by file:
//
//	src/duration.js
//	  1: import { InvalidArgumentError, … } from "./errors.js";
//	  18: const INVALID = "Invalid Duration";
//	  … +131 more in this file
//
// with long lines cut to a window around the match, dependency directories
// summarized, and caps with exact counts plus a per-file histogram.
type matches struct{}

func (matches) Name() string    { return "search" }
func (matches) IsContent() bool { return true }

func (matches) Match(c *engine.Context) bool {
	e := fs.Effective(c)
	if engine.MachineReadable(e) {
		return false
	}
	t, args := tool(e)
	if t == "" {
		return false
	}
	o := parseOpts(t, e.Name(), args)
	return !o.bail && !o.files
}

type entry struct {
	num   string // line number ("" when not printed)
	col   string // column (--column / --vimgrep), "" otherwise
	text  string
	match bool // false: context line
	sep   bool // a "--" group separator
}

type fileGroup struct {
	path    string
	entries []entry
	matches int
	heavy   string // heavy root ("./node_modules/") when summarized
}

// isBinaryNote reports the "a binary file matched" diagnostics: grep's
// "Binary file x matches" (GNU < 3.5, BSD, ugrep, git grep), GNU ≥ 3.5's
// "grep: x: binary file matches" and rg's
// "x: binary file matches (found "\0" byte around offset 12)".
func isBinaryNote(ln string) bool {
	if strings.HasPrefix(ln, "Binary file ") && strings.HasSuffix(ln, " matches") {
		return true
	}
	i := strings.LastIndex(ln, ": binary file matches")
	if i < 0 {
		return false
	}
	rest := ln[i+len(": binary file matches"):]
	return rest == "" || strings.HasPrefix(rest, " (") && strings.HasSuffix(rest, ")")
}

// noteFunc returns the test for diagnostics the search tool prints into the
// output stream. The program prefix is matched both as invoked and as a
// base name: GNU grep prints its full argv[0] ("/usr/bin/grep: x: No such
// file or directory"), and such a line must never be read as a hit.
func noteFunc(e *engine.Context, tool string) func(string) bool {
	return func(ln string) bool {
		switch tool {
		case "grep":
			return fs.NoteHasPrefix(e, ln, "grep", "egrep", "fgrep", "ggrep", "ugrep") ||
				strings.HasPrefix(ln, "Binary file ") && strings.HasSuffix(ln, " matches")
		case "rg":
			return fs.NoteHasPrefix(e, ln, "rg") || isBinaryNote(ln) && !strings.HasPrefix(ln, "Binary file ")
		case "git-grep":
			return fs.NoteHasPrefix(e, ln, "fatal", "error", "warning", "git") ||
				strings.HasPrefix(ln, "Binary file ") && strings.HasSuffix(ln, " matches")
		}
		return false
	}
}

// leadingNum splits "NN<sep>rest" for sep ':' or '-'.
func leadingNum(s string) (num string, sep byte, rest string, ok bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(s) || s[i] != ':' && s[i] != '-' {
		return "", 0, "", false
	}
	return s[:i], s[i], s[i+1:], true
}

// splitPathNum finds "path:NN:" at the start of s, the path being the
// shortest non-empty prefix that works (as the regexp ^(.+?):(\d+): would,
// without its cost on long lines). It is right unless a path itself holds
// ":<digits>:".
func splitPathNum(s string) (pathEnd, numEnd int, ok bool) {
	for i := 1; i < len(s); i++ {
		if s[i] != ':' {
			continue
		}
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j > i+1 && j < len(s) && s[j] == ':' {
			return i, j, true
		}
	}
	return 0, 0, false
}

// parseLimit: only this much of a line is examined for the path prefix.
const parseLimit = 4096

func (m matches) Apply(c *engine.Context, out string) (string, bool) {
	e := fs.Effective(c)
	if !m.Match(e) || out == "" {
		return "", false
	}
	t, args := tool(e)
	o := parseOpts(t, e.Name(), args)
	lines := strings.Split(out, "\n")
	isNote := noteFunc(e, t)

	withFile := o.withFile
	if withFile == 0 {
		withFile = detectWithFile(lines, o, isNote)
	}
	var (
		notes  []string
		groups []*fileGroup
		byPath = map[string]*fileGroup{}
	)
	get := func(p string) *fileGroup {
		g := byPath[p]
		if g == nil {
			g = &fileGroup{path: p}
			byPath[p] = g
			groups = append(groups, g)
		}
		return g
	}

	// Known paths (from match lines) resolve context lines, whose
	// "path-NN-text" form is ambiguous on its own.
	known := map[string]bool{}
	if withFile > 0 {
		cands := map[string]bool{}
		for _, ln := range lines {
			if ln == "" || ln == o.groupSep || isNote(ln) {
				continue
			}
			if p, _, _, _, ok := parseMatch(ln, o, withFile); ok {
				cands[p] = true
			}
		}
		for p := range cands {
			if o.context && isContextMisparse(p, cands, o) {
				continue
			}
			known[p] = true
		}
		if !pathsUnderOperands(known, o) {
			return "", false
		}
	}

	var cur *fileGroup
	fallbacks := 0
	var byLength []string // known paths, longest first (context fallback)
	// Each file's lines come out once each and in file order (grep, git
	// grep, and rg per file, even with threads). A line number repeating
	// or going backwards within one run of a path means the lines were
	// split at the wrong colon ("logs/12:30:00.log:5:x" read as file
	// "logs/12", line 30) or reordered by a pipe (| sort): the grouping
	// would mislead, so bail. Only -o and --column/--vimgrep print one
	// line per match, repeating a line number.
	repeats := o.only || o.column
	lastPath, lastNum := "\x00", -1
	inOrder := func(p, num string) bool {
		if num == "" {
			return true
		}
		n := atoi(num)
		if p != lastPath {
			lastPath, lastNum = p, n
			return true
		}
		if n < lastNum || n == lastNum && !repeats {
			return false
		}
		lastNum = n
		return true
	}
	for i, ln := range lines {
		switch {
		case ln == "" && i == len(lines)-1:
			continue
		case o.groupSep != "" && ln == o.groupSep && o.context:
			if cur != nil {
				cur.entries = append(cur.entries, entry{sep: true})
			}
			continue
		case isNote(ln) && !(withFile > 0 && startsWithKnown(ln, known)):
			notes = append(notes, ln)
			continue
		}
		if withFile <= 0 {
			if cur == nil {
				cur = get("")
			}
			en, ok := parseBare(ln, o)
			if !ok || !inOrder("", en.num) {
				return "", false
			}
			cur.entries = append(cur.entries, en)
			if en.match {
				cur.matches++
			}
			continue
		}
		if p, num, col, text, ok := parseMatch(ln, o, withFile); ok && known[p] {
			if !inOrder(p, num) {
				return "", false
			}
			cur = get(p)
			cur.entries = append(cur.entries, entry{num: num, col: col, text: text, match: true})
			cur.matches++
			continue
		}
		if !o.context {
			return "", false
		}
		// A context line: try the current file, then the next match's
		// file (before-context lines come first), then every known path.
		var cands []string
		if cur != nil {
			cands = append(cands, cur.path)
		}
		if p := nextMatchPath(lines[i+1:], o, withFile, known); p != "" {
			cands = append(cands, p)
		}
		p, num, text, ok := parseContext(ln, cands, o)
		if !ok {
			if fallbacks++; fallbacks > 1000 {
				return "", false
			}
			if byLength == nil {
				byLength = sortedKnown(known)
			}
			p, num, text, ok = parseContext(ln, byLength, o)
			if !ok {
				return "", false
			}
		}
		if !inOrder(p, num) {
			return "", false
		}
		cur = get(p)
		cur.entries = append(cur.entries, entry{num: num, text: text})
	}
	total, nfiles := 0, 0
	for _, g := range groups {
		total += g.matches
		if g.matches > 0 {
			nfiles++
		}
	}
	if total == 0 && len(notes) == 0 {
		return "", false
	}
	// grep, rg and git grep exit 1 for "no match" and ≥ 2 for trouble,
	// which they report. Trouble without a diagnostic here means the
	// search was cut short (timeout, signal, grep -s): the counts would
	// claim a complete result, so leave it to the generic reducer.
	if e.Exit >= 2 {
		diag := 0
		for _, n := range notes {
			if !isBinaryNote(n) {
				diag++
			}
		}
		if diag == 0 {
			return "", false
		}
	}
	r := render{o: o, mt: newMatcher(o), withFile: withFile > 0}
	return r.run(notes, groups, total, nfiles), true
}

// detectWithFile decides between "path:NN:text" and "NN:text" / "text"
// when the command line cannot tell (a single glob or ambiguous operand):
// paths must parse on every line and look like paths.
func detectWithFile(lines []string, o opts, isNote func(string) bool) int {
	n := 0
	for _, ln := range lines {
		if ln == "" || ln == o.groupSep || isNote(ln) {
			continue
		}
		p, _, _, _, ok := parseMatch(ln, o, 1)
		if !ok {
			if o.context {
				continue // context lines are checked against known paths later
			}
			return -1
		}
		if strings.TrimSpace(p) != p || !strings.ContainsAny(p, "/.") || digitsRe.MatchString(p) {
			return -1
		}
		n++
	}
	if n == 0 {
		return -1
	}
	return 1
}

// parseMatch splits a match line "path:NN:[COL:]text" / "path:text".
func parseMatch(ln string, o opts, withFile int) (p, num, col, text string, ok bool) {
	head := ln
	if len(head) > parseLimit {
		head = head[:parseLimit]
	}
	if o.numbered {
		pe, ne, ok := splitPathNum(head)
		if !ok {
			return "", "", "", "", false
		}
		p, num = ln[:pe], ln[pe+1:ne]
		rest := ln[ne+1:]
		if o.column {
			if c, sep, r, ok := leadingNum(rest); ok && sep == ':' {
				col, rest = c, r
			}
		}
		return p, num, col, rest, true
	}
	i := strings.IndexByte(head, ':')
	if i <= 0 {
		return "", "", "", "", false
	}
	return ln[:i], "", "", ln[i+1:], true
}

// isContextMisparse reports a candidate path that is really a context line
// of a known file whose text holds ":NN:" ("a.go-12-at 10:30:00" parsed as
// path "a.go-12-at 10", line 30). Only the prefixes of p ending before a
// "-" are looked up, so this is linear in len(p).
func isContextMisparse(p string, cands map[string]bool, o opts) bool {
	for j := strings.IndexByte(p, '-'); j > 0; {
		if k := p[:j]; cands[k] {
			rest := p[j+1:]
			if !o.numbered {
				return true
			}
			if _, sep, _, ok := leadingNum(rest); ok && sep == '-' {
				return true
			}
		}
		n := strings.IndexByte(p[j+1:], '-')
		if n < 0 {
			break
		}
		j += 1 + n
	}
	return false
}

func startsWithKnown(ln string, known map[string]bool) bool {
	if i := strings.IndexByte(ln, ':'); i > 0 && known[ln[:i]] {
		return true
	}
	return false
}

// pathsUnderOperands checks parsed paths against the search operands, which
// guards against misreading "text:with:colons" as paths.
func pathsUnderOperands(known map[string]bool, o opts) bool {
	if o.tool == "git-grep" || len(o.operands) == 0 {
		return true
	}
	var roots []string
	for _, op := range o.operands {
		if hasGlob(op) {
			return true
		}
		roots = append(roots, strings.TrimRight(op, "/"))
	}
	for p := range known {
		ok := false
		for _, r := range roots {
			// A searched directory prefixes every path it yields with
			// itself ("./a.go" for "."), a searched file is printed as is.
			if r == "" || p == r || strings.HasPrefix(p, r+"/") || r == "." && strings.HasPrefix(p, "./") {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func nextMatchPath(rest []string, o opts, withFile int, known map[string]bool) string {
	for i, ln := range rest {
		if i > 64 || ln == o.groupSep && o.groupSep != "" {
			return ""
		}
		if p, _, _, _, ok := parseMatch(ln, o, withFile); ok && known[p] {
			return p
		}
	}
	return ""
}

// parseContext splits "path-NN-text" / "path-text" for one of cands.
func parseContext(ln string, cands []string, o opts) (p, num, text string, ok bool) {
	for _, k := range cands {
		if !strings.HasPrefix(ln, k+"-") {
			continue
		}
		rest := ln[len(k)+1:]
		if !o.numbered {
			return k, "", rest, true
		}
		num, sep, text, ok := leadingNum(rest)
		if !ok || sep != '-' {
			continue
		}
		return k, num, text, true
	}
	return "", "", "", false
}

func sortedKnown(known map[string]bool) []string {
	out := make([]string, 0, len(known))
	for k := range known {
		out = append(out, k)
	}
	// Longest first: "a-1-b.go" must win over "a".
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) > len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// parseBare parses a line without a path prefix: "NN:text", "NN-text" or
// plain text.
func parseBare(ln string, o opts) (entry, bool) {
	if !o.numbered {
		return entry{text: ln, match: true}, true
	}
	num, sep, rest, ok := leadingNum(ln)
	if !ok {
		return entry{}, false
	}
	if sep == ':' {
		col := ""
		if o.column {
			if c, sep2, r, ok := leadingNum(rest); ok && sep2 == ':' {
				col, rest = c, r
			}
		}
		return entry{num: num, col: col, text: rest, match: true}, true
	}
	if !o.context {
		return entry{}, false
	}
	return entry{num: num, text: rest}, true
}

// heavyRoot returns the path prefix up to and including the first heavy
// directory ("./node_modules/"), or "".
func heavyRoot(p string) string {
	off := 0
	for {
		i := strings.IndexByte(p[off:], '/')
		if i < 0 {
			return ""
		}
		seg := p[off : off+i]
		if heavyDirs[seg] {
			return p[:off+i+1]
		}
		off += i + 1
	}
}

// explicitlySearched reports whether an operand lies at or inside root.
func explicitlySearched(root string, o opts) bool {
	r := strings.TrimSuffix(strings.TrimPrefix(root, "./"), "/")
	for _, op := range o.operands {
		op = strings.TrimSuffix(strings.TrimPrefix(op, "./"), "/")
		if op == r || strings.HasPrefix(op, r+"/") {
			return true
		}
	}
	return false
}

func commaInt(n int) string {
	s := fmt.Sprint(n)
	if n < 1000 {
		return s
	}
	var b strings.Builder
	for i, ch := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(ch)
	}
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return commaInt(n) + " " + one
	}
	return commaInt(n) + " " + many
}
