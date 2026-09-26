package git

import (
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

func init() { engine.Register(diffFilter{}) }

// diffFilter renders `git diff` (and `git stash show -p`, `git diff
// --no-index`) as git's own unified diff with the per-file header folded to
// one line:
//
//	diff --git a/x b/x          ← kept; index, ---/+++ lines dropped when
//	@@ -1,4 +1,4 @@ func f()      they only repeat the paths above
//	 hunks verbatim …
//
// Hunks are never rewritten, so an agent can edit against them. Lockfiles
// and generated files become one summary (+/- counts and, for lockfiles, the
// package versions that changed); binary files one line; files whose hunks
// are identical to an earlier file's are listed under that file. Only when
// the result is still over diffBudget are long new files, then very large
// files and long hunks shortened, each with a counted marker; the files that
// still do not fit are listed with their +/- counts (the first maxListed of
// them by name, the rest counted). Deleted files longer than
// summarizeMinLines always show only their line count. Conflict markers
// (<<<<<<< ======= >>>>>>>) added by a file whose lines are not all shown are
// listed with their new-file line numbers, so a lockfile committed with an
// unresolved merge never reads as a clean version bump. `--stat` and other
// summary formats, and binary patches, are left as they are.
type diffFilter struct{}

func (diffFilter) Name() string    { return "git-diff" }
func (diffFilter) IsContent() bool { return true }

func (diffFilter) Match(c *engine.Context) bool {
	if !isGit(c) || engine.MachineReadable(c) {
		return false
	}
	switch c.Sub() {
	case "diff":
		return true
	case "stash":
		p := positionals(c)
		return len(p) > 0 && p[0] == "show"
	}
	return false
}

// verbatimDiffFlags select output that is not a plain unified diff, or that
// is meant to be applied as a patch; such output is left untouched.
var verbatimDiffFlags = []string{
	"--stat", "--stat=", "--numstat", "--shortstat", "--dirstat", "--dirstat=", "--summary",
	"--compact-summary", "--name-only", "--name-status", "--raw", "--check", "-z",
	"--binary", "--full-index", "--word-diff", "--word-diff=", "--word-diff-regex=",
	"--color-words", "--color-words=", "--output=", "--patch-with-raw", "--patch-with-stat",
}

func (diffFilter) Apply(c *engine.Context, out string) (string, bool) {
	if hasArg(c, verbatimDiffFlags...) || c.Sub() == "stash" && !hasArg(c, "-p", "--patch", "-u") {
		// Summary formats (a plain `git stash show` is a diffstat) are
		// already compact.
		return out, true
	}
	lines := strings.Split(out, "\n")
	doc, ok := parseDiffDoc(lines)
	if !ok {
		return out, true // a binary patch: data for git apply, byte-exact
	}
	if doc.files == 0 {
		// Not a diff (an error message, a --stat block from diff.stat
		// config, …): let the generic reducer decide.
		return "", false
	}
	return join(renderDiffParts(doc.parts, diffBudget)), true
}

// diffBudget is the token size diff output is shortened to. The engine's
// budget stage (8000) would cut a diff mid-hunk; staying under it keeps
// every shown hunk whole.
const diffBudget = 7000

// ---- model -----------------------------------------------------------------

// part is one element of a parsed diff: a file, or a line that is not part
// of any file (printed verbatim).
type part struct {
	file *fileDiff
	raw  string
}

type fileDiff struct {
	head       string   // "diff --git a/x b/y", "diff --cc x"
	combined   bool     // diff --cc / --combined
	ext        []string // extended header lines worth printing
	minus      string   // "--- a/x" when it adds information
	plus       string   // "+++ b/x" when it adds information
	binary     string   // "Binary files a/x and b/x differ"
	isNew      bool
	isDeleted  bool
	renamed    bool // rename or copy
	name       string
	hunks      []*hunk
	adds, dels int
	btok       int // body tokens, computed on first use
	btokDone   bool
	sum        int8 // summarized(): 0 unknown, 1 no, 2 yes
}

type hunk struct {
	header    string
	body      []string
	adds      int
	dels      int
	conflicts []conflictLine // added conflict-marker lines
}

// conflictLine is an added line that is a merge conflict marker, with its
// line number in the new file.
type conflictLine struct {
	line int
	text string
}

func (f *fileDiff) bodyLines() int {
	n := 0
	for _, h := range f.hunks {
		n += len(h.body)
	}
	return n
}

// ---- parsing ---------------------------------------------------------------

var (
	diffHeadRe  = regexp.MustCompile(`^diff --git (.+)$`)
	diffCCRe    = regexp.MustCompile(`^diff --(?:cc|combined) (.+)$`)
	hunkRe      = regexp.MustCompile(`^@@ -\d+(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?: .*)?$`)
	hunkCCRe    = regexp.MustCompile(`^(@@@+) ((?:-\d+(?:,\d+)? )+)\+(\d+)(?:,(\d+))? @@@+(?: .*)?$`)
	extPrefixes = []string{
		"old mode ", "new mode ", "deleted file mode ", "new file mode ", "similarity index ",
		"dissimilarity index ", "rename from ", "rename to ", "copy from ", "copy to ", "mode ",
	}
)

func isDiffStart(ln string) bool {
	return strings.HasPrefix(ln, "diff --git ") || strings.HasPrefix(ln, "diff --cc ") || strings.HasPrefix(ln, "diff --combined ")
}

type diffDoc struct {
	parts []part
	files int
}

// parseDiffDoc parses a whole diff. Lines outside any file are kept as raw
// parts. It fails (ok=false) on output that must not be reshaped, such as
// a binary patch.
func parseDiffDoc(lines []string) (diffDoc, bool) {
	var d diffDoc
	for i := 0; i < len(lines); {
		if isDiffStart(lines[i]) {
			f, next, ok := parseFile(lines, i)
			if !ok {
				return d, false
			}
			d.parts = append(d.parts, part{file: f})
			d.files++
			i = next
			continue
		}
		d.parts = append(d.parts, part{raw: lines[i]})
		i++
	}
	return d, true
}

// parseFile parses one file section starting at lines[i] ("diff --git …").
// It stops at the first line that cannot belong to the file; that line is
// left for the caller. ok is false for binary patches (--binary), which are
// data for `git apply` and must stay byte-exact.
func parseFile(lines []string, i int) (*fileDiff, int, bool) {
	f := &fileDiff{head: lines[i]}
	if m := diffCCRe.FindStringSubmatch(lines[i]); m != nil {
		f.combined, f.name = true, m[1]
	} else if m := diffHeadRe.FindStringSubmatch(lines[i]); m != nil {
		f.name = headNewPath(m[1])
	}
	var renameFrom, renameTo string
	i++
	// Extended header.
	for i < len(lines) {
		ln := lines[i]
		if strings.HasPrefix(ln, "@@") || isDiffStart(ln) {
			break
		}
		switch {
		case ln == "GIT binary patch":
			return nil, 0, false
		case strings.HasPrefix(ln, "index "):
			// Blob ids: never needed to read or apply the change.
		case strings.HasPrefix(ln, "--- "):
			f.minus = ln
		case strings.HasPrefix(ln, "+++ "):
			f.plus = ln
		case strings.HasPrefix(ln, "Binary files ") && strings.HasSuffix(ln, " differ"):
			f.binary = ln
		case hasAnyPrefix(ln, extPrefixes):
			switch {
			case strings.HasPrefix(ln, "new file mode "):
				f.isNew = true
			case strings.HasPrefix(ln, "deleted file mode "):
				f.isDeleted = true
			case strings.HasPrefix(ln, "rename from "):
				f.renamed = true
				renameFrom = ln
			case strings.HasPrefix(ln, "rename to "):
				renameTo = ln
			case strings.HasPrefix(ln, "copy from "):
				// A copy's from/to lines are kept: without them a copy
				// (the source still exists) reads like a rename.
				f.renamed = true
			}
			f.ext = append(f.ext, ln)
		default:
			// Not a header line: the file (a mode-only change, an empty
			// file) ends here.
			return f.finish(renameFrom, renameTo), i, true
		}
		i++
	}
	// Hunks.
	for i < len(lines) && strings.HasPrefix(lines[i], "@@") {
		h, next, ok := parseHunk(lines, i, f.combined)
		if !ok {
			break
		}
		f.hunks = append(f.hunks, h)
		f.adds += h.adds
		f.dels += h.dels
		i = next
	}
	return f.finish(renameFrom, renameTo), i, true
}

// finish drops header lines that only repeat what the diff --git line says.
// Only a rename's "rename from/to" pair is dropped (the header's a/ and b/
// paths plus "similarity index" say the same); "copy from/to" always stay.
func (f *fileDiff) finish(renameFrom, renameTo string) *fileDiff {
	if !f.combined {
		if m := diffHeadRe.FindStringSubmatch(f.head); m != nil {
			rest := m[1]
			// "rename from x" / "rename to y" repeat "diff --git a/x b/y" when
			// the paths are plain (no spaces or quotes to split on).
			if renameFrom != "" && renameTo != "" {
				from := strings.TrimPrefix(renameFrom, "rename from ")
				to := strings.TrimPrefix(renameTo, "rename to ")
				if !strings.ContainsAny(from+to, " \"\t") && rest == "a/"+from+" b/"+to {
					f.ext = removeLines(f.ext, renameFrom, renameTo)
				}
			}
			if redundantPath(f.minus, "--- ", rest, f.isNew) {
				f.minus = ""
			}
			if redundantPath(f.plus, "+++ ", rest, f.isDeleted) {
				f.plus = ""
			}
		}
	} else {
		if f.minus == "--- a/"+f.name || f.minus == "--- "+f.name {
			f.minus = ""
		}
		if f.plus == "+++ b/"+f.name || f.plus == "+++ "+f.name {
			f.plus = ""
		}
	}
	return f
}

// redundantPath reports whether a ---/+++ line names a path already on the
// diff --git line (or /dev/null for an added/deleted file).
func redundantPath(ln, prefix, head string, devNull bool) bool {
	if ln == "" {
		return true
	}
	p := strings.TrimPrefix(ln, prefix)
	if p == "/dev/null" {
		return devNull
	}
	return strings.HasPrefix(head, p+" ") || strings.HasSuffix(head, " "+p)
}

// headNewPath extracts the new path from "a/x b/x" (best effort: used only
// for naming files in markers and classifying lockfiles).
func headNewPath(rest string) string {
	if i := strings.LastIndex(rest, " b/"); i >= 0 {
		return strings.Trim(rest[i+3:], `"`)
	}
	if f := strings.Fields(rest); len(f) == 2 {
		return strings.Trim(f[1], `"`)
	}
	return rest
}

func hasAnyPrefix(s string, ps []string) bool {
	for _, p := range ps {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func removeLines(lines []string, drop ...string) []string {
	out := lines[:0:0]
	for _, ln := range lines {
		keep := true
		for _, d := range drop {
			if ln == d {
				keep = false
			}
		}
		if keep {
			out = append(out, ln)
		}
	}
	return out
}

// parseHunk consumes one hunk using the line counts in its header, so blank
// context lines (whose single space normalization trimmed away) are counted
// correctly and a following non-diff line is never swallowed. A hunk cut
// short by the end of the output or a foreign line simply ends there.
func parseHunk(lines []string, i int, combined bool) (*hunk, int, bool) {
	h := &hunk{header: lines[i]}
	var need []int // remaining lines per side: parents..., result
	width := 1
	newLine := 0 // new-file line number of the next result-side line
	if combined {
		m := hunkCCRe.FindStringSubmatch(lines[i])
		if m == nil {
			return nil, i, false
		}
		width = len(m[1]) - 1
		for _, r := range strings.Fields(m[2]) {
			need = append(need, rangeCount(r))
		}
		if len(need) != width {
			return nil, i, false
		}
		need = append(need, countOr1(m[4]))
		newLine, _ = strconv.Atoi(m[3])
	} else {
		m := hunkRe.FindStringSubmatch(lines[i])
		if m == nil {
			return nil, i, false
		}
		need = []int{countOr1(m[1]), countOr1(m[3])}
		newLine, _ = strconv.Atoi(m[2])
	}
	i++
	pending := func() bool {
		for _, n := range need {
			if n > 0 {
				return true
			}
		}
		return false
	}
	for i < len(lines) && pending() {
		ln := lines[i]
		if strings.HasPrefix(ln, `\`) {
			h.body = append(h.body, ln)
			i++
			continue
		}
		prefix := ln
		if len(prefix) > width {
			prefix = prefix[:width]
		}
		prefix += strings.Repeat(" ", width-len(prefix))
		if strings.Trim(prefix, " +-") != "" {
			break
		}
		minus := strings.Contains(prefix, "-")
		for k := 0; k < width; k++ {
			if prefix[k] == '-' || !minus && prefix[k] == ' ' {
				need[k]--
			}
		}
		if !minus {
			need[width]--
		}
		if strings.Contains(prefix, "+") {
			h.adds++
			if isConflictMarker(ln, width) {
				h.conflicts = append(h.conflicts, conflictLine{line: newLine, text: ln})
			}
		} else if minus {
			h.dels++
		}
		if !minus {
			newLine++
		}
		h.body = append(h.body, ln)
		i++
	}
	// "\ No newline at end of file" after the last line.
	for i < len(lines) && strings.HasPrefix(lines[i], `\`) {
		h.body = append(h.body, lines[i])
		i++
	}
	return h, i, true
}

// isConflictMarker reports whether a diff line with a width-column prefix
// is a merge conflict marker: <<<<<<<, |||||||, ======= or >>>>>>> at the
// start of the content, alone or followed by a space.
func isConflictMarker(ln string, width int) bool {
	if len(ln) < width+7 {
		return false
	}
	c := ln[width:]
	switch c[:7] {
	case "<<<<<<<", "|||||||", "=======", ">>>>>>>":
		return len(c) == 7 || c[7] == ' '
	}
	return false
}

// conflicts returns the conflict-marker lines f adds. A lone "=======" is
// also a Markdown or reStructuredText heading underline, so markers count
// only when the file adds at least one <<<<<<< or >>>>>>> line.
func (f *fileDiff) conflicts() []conflictLine {
	var out []conflictLine
	real := false
	for _, h := range f.hunks {
		for _, c := range h.conflicts {
			if strings.Contains(c.text, "<<<<<<<") || strings.Contains(c.text, ">>>>>>>") {
				real = true
			}
		}
		out = append(out, h.conflicts...)
	}
	if !real {
		return nil
	}
	return out
}

// maxConflictLines caps the conflict markers listed for one file.
const maxConflictLines = 20

// conflictNote lists f's added conflict markers when the rendering in
// shown does not include all of them (a lockfile or generated-file summary,
// a capped hunk, a hunk shown by its header only).
func conflictNote(f *fileDiff, shown []string) []string {
	cs := f.conflicts()
	if len(cs) == 0 {
		return nil
	}
	width := 1
	if f.combined {
		width = 0
		if len(f.hunks) > 0 {
			width = len(f.hunks[0].header) - len(strings.TrimLeft(f.hunks[0].header, "@")) - 1
		}
	}
	n := 0
	for _, ln := range shown {
		if isConflictMarker(ln, width) {
			n++
		}
	}
	if n >= len(cs) {
		return nil
	}
	out := []string{fmt.Sprintf("[unresolved conflict markers added (%s), by new-file line:", engine.Plural(len(cs), "line", "lines"))}
	for i, c := range cs {
		if i == maxConflictLines {
			out = append(out, fmt.Sprintf("  … +%d more", len(cs)-i))
			break
		}
		out = append(out, fmt.Sprintf("  %d: %s", c.line, c.text))
	}
	out[len(out)-1] += "]"
	return out
}

// rangeCount parses "-12,4" → 4, "-12" → 1.
func rangeCount(r string) int {
	if _, n, ok := strings.Cut(r, ","); ok {
		v, _ := strconv.Atoi(n)
		return v
	}
	return 1
}

func countOr1(s string) int {
	if s == "" {
		return 1
	}
	v, _ := strconv.Atoi(s)
	return v
}

// ---- classification --------------------------------------------------------

var lockfiles = map[string]bool{
	"package-lock.json": true, "npm-shrinkwrap.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
	"bun.lock": true, "Cargo.lock": true, "go.sum": true, "poetry.lock": true, "uv.lock": true,
	"Pipfile.lock": true, "composer.lock": true, "Gemfile.lock": true, "pubspec.lock": true,
	"Podfile.lock": true, "flake.lock": true, "packages.lock.json": true, "gradle.lockfile": true,
	"mix.lock": true, "Package.resolved": true, "deno.lock": true, "go.work.sum": true,
}

var generatedRe = regexp.MustCompile(`\.min\.(?:js|css|mjs)$|\.(?:js|css|mjs)\.map$|(?:^|/)dist/|` +
	`\.pb\.go$|\.pb\.gw\.go$|_pb2(?:_grpc)?\.pyi?$|\.pb\.(?:cc|h)$|\.g\.dart$|\.freezed\.dart$|` +
	`\.generated\.|(?:^|/)__generated__/`)

// summarizeMin: lockfile/generated diffs costing at most this many tokens
// are shown in full; a summary would save little and could hide a detail
// (a small lockfile edit is often the project's own version bump).
const summarizeMin = 400

// summarizeMinLines: deleted-file bodies up to this many lines stay; longer
// ones show their line count only (the content is in the old revision).
const summarizeMinLines = 10

func isLockfile(name string) bool { return lockfiles[path.Base(name)] }

func isGenerated(name string) bool { return generatedRe.MatchString(name) }

// ---- rendering ---------------------------------------------------------------

// Shortening levels, applied only while the output is over budget.
const (
	lvlBase       = iota // header folding, lockfile/generated/binary/deleted summaries, identical hunks
	lvlNewFiles          // new files over 80 lines → first 40 lines
	lvlLargeFiles        // files with >300 changed lines → first 2 hunks; every hunk capped (800…100 lines)
	lvlFileList          // files past the budget → one stat list
)

// renderDiffParts renders parsed diff parts, shortening level by level until
// the result fits budget tokens.
func renderDiffParts(parts []part, budget int) []string {
	var out []string
	groups := identicalGroups(parts)
	for lvl := lvlBase; lvl <= lvlFileList; lvl++ {
		// Hunk length caps, loosest first, for the levels that cap hunks.
		caps := []int{0}
		switch lvl {
		case lvlLargeFiles:
			caps = []int{800, 400, 200, 100}
		case lvlFileList:
			caps = []int{100}
		}
		for _, hc := range caps {
			out = renderLevel(parts, groups, lvl, hc, budget)
			if fitsBudget(out, budget) {
				return out
			}
		}
	}
	return out
}

// maxListed caps the "not shown" file list of lvlFileList.
const maxListed = 300

func renderLevel(parts []part, groups map[*fileDiff][]*fileDiff, lvl, hunkCap, budget int) []string {
	var out []string
	var rest []statRow // files not shown at lvlFileList
	used := 0
	if lvl >= lvlFileList {
		// Keep room for the list of the files that will not be shown
		// (about 8 tokens each).
		files := 0
		for _, p := range parts {
			if p.file != nil {
				files++
			}
		}
		budget -= min(files, maxListed) * 8
	}
	for _, p := range parts {
		if p.file == nil {
			out = append(out, p.raw)
			continue
		}
		f := p.file
		g, grouped := groups[f]
		if grouped && g == nil {
			continue // listed under the first file with the same hunks
		}
		fl := renderFile(f, lvl, hunkCap)
		if grouped {
			names := make([]string, len(g))
			for i, o := range g {
				names[i] = o.name
			}
			marker := wrapList("", capItems(groupByDir(names, nil), 50), statWidth)
			marker[0] = fmt.Sprintf("[same hunks also in %s: %s", engine.Plural(len(g), "file", "files"), marker[0])
			marker[len(marker)-1] += "]"
			fl = append(fl[:1:1], append(marker, fl[1:]...)...)
		}
		if lvl >= lvlFileList {
			cost := countTokens(fl)
			if used+cost > budget-400 && used > 0 || len(rest) > 0 {
				rest = append(rest, statRow{path: f.name, label: listDesc(f)})
				for _, o := range g {
					rest = append(rest, statRow{path: o.name, label: listDesc(o)})
				}
				continue
			}
			used += cost
		}
		out = append(out, fl...)
	}
	if len(rest) > 0 {
		out = append(out, fmt.Sprintf("[%s not shown (diff too large), with their changed lines:", engine.Plural(len(rest), "more file", "more files")))
		listed := rest
		if len(listed) > maxListed {
			// Past the cap, files adding conflict markers are still named.
			listed = append([]statRow(nil), rest[:maxListed]...)
			for _, r := range rest[maxListed:] {
				if strings.Contains(r.label, "conflict-marker") {
					listed = append(listed, r)
				}
			}
		}
		out = append(out, renderStat(listed, "  ", len(rest)-len(listed))...)
		out[len(out)-1] += "]"
	}
	return out
}

func countDesc(f *fileDiff) string {
	switch {
	case f.binary != "":
		return "binary"
	case f.adds > 0 && f.dels > 0:
		return fmt.Sprintf("+%d -%d", f.adds, f.dels)
	case f.dels > 0:
		return fmt.Sprintf("-%d", f.dels)
	case f.adds > 0:
		return fmt.Sprintf("+%d", f.adds)
	default:
		return "0" // a mode change, a pure rename, an empty file: git's stat says 0
	}
}

// listDesc is countDesc for the list of files not shown, flagging files
// that add conflict markers.
func listDesc(f *fileDiff) string {
	if cs := f.conflicts(); len(cs) > 0 {
		return countDesc(f) + fmt.Sprintf(" (%s)", engine.Plural(len(cs), "conflict-marker line", "conflict-marker lines"))
	}
	return countDesc(f)
}

// identicalGroups maps the first of several modified files whose hunks are
// byte-identical to the list of the others, and each of the others to nil.
func identicalGroups(parts []part) map[*fileDiff][]*fileDiff {
	first := map[string]*fileDiff{}
	groups := map[*fileDiff][]*fileDiff{}
	for _, p := range parts {
		f := p.file
		if f == nil || f.combined || f.isNew || f.isDeleted || f.renamed || f.binary != "" ||
			len(f.ext) > 0 || f.minus != "" || f.plus != "" || len(f.hunks) == 0 || summarized(f) {
			continue
		}
		var b strings.Builder
		for _, h := range f.hunks {
			b.WriteString(h.header)
			b.WriteByte('\n')
			for _, ln := range h.body {
				b.WriteString(ln)
				b.WriteByte('\n')
			}
		}
		key := b.String()
		if f0, ok := first[key]; ok {
			groups[f0] = append(groups[f0], f)
			groups[f] = nil
			continue
		}
		first[key] = f
	}
	return groups
}

// summarized reports whether f is shown as a summary at every level
// (computed once per file: the renderer asks at every level).
func summarized(f *fileDiff) bool {
	if f.sum == 0 {
		f.sum = 1
		if !f.combined && f.binary == "" && (isLockfile(f.name) || isGenerated(f.name)) && f.bodyTokens() > summarizeMin {
			f.sum = 2
		}
	}
	return f.sum == 2
}

func (f *fileDiff) bodyTokens() int {
	if !f.btokDone {
		for _, h := range f.hunks {
			f.btok += countTokens(h.body)
		}
		f.btokDone = true
	}
	return f.btok
}

// renderFile renders one file at a shortening level; hunkCap bounds hunk
// bodies from lvlLargeFiles on. Conflict markers the rendering leaves out
// are listed right under the file's header.
func renderFile(f *fileDiff, lvl, hunkCap int) []string {
	out, head := renderFileBody(f, lvl, hunkCap)
	if note := conflictNote(f, out[head:]); note != nil {
		out = append(out[:head:head], append(note, out[head:]...)...)
	}
	return out
}

// renderFileBody renders f and reports how many of the lines are its header.
func renderFileBody(f *fileDiff, lvl, hunkCap int) (out []string, head int) {
	if f.binary != "" && !f.renamed && onlyNewDeleted(f.ext) {
		// "Binary files /dev/null and b/x differ" says it all.
		return []string{f.binary}, 1
	}
	out = append(out, f.head)
	out = append(out, f.ext...)
	if f.minus != "" {
		out = append(out, f.minus)
	}
	if f.plus != "" {
		out = append(out, f.plus)
	}
	if f.binary != "" {
		out = append(out, f.binary)
	}
	head = len(out)
	switch {
	case summarized(f):
		if isLockfile(f.name) {
			return append(out, lockfileSummary(f)...), head
		}
		return append(out, fmt.Sprintf("[generated file: %s lines in %s not shown]", countDesc(f), engine.Plural(len(f.hunks), "hunk", "hunks"))), head
	case f.isDeleted && f.bodyLines() > summarizeMinLines:
		return append(out, fmt.Sprintf("[deleted file: %d lines not shown]", f.dels)), head
	case lvl >= lvlNewFiles && f.isNew && len(f.hunks) == 1 && len(f.hunks[0].body) > 80:
		h := f.hunks[0]
		out = append(out, h.header)
		out = append(out, h.body[:40]...)
		return append(out, fmt.Sprintf("[… +%d more lines of this new file not shown]", len(h.body)-40)), head
	case lvl >= lvlLargeFiles && f.adds+f.dels > 300:
		for i, h := range f.hunks {
			if i >= 2 {
				out = append(out, fmt.Sprintf("%s [%s not shown]", h.header, hunkDesc(h)))
				continue
			}
			out = append(out, capHunk(h, hunkCap)...)
		}
		return out, head
	}
	for _, h := range f.hunks {
		if lvl >= lvlLargeFiles {
			out = append(out, capHunk(h, hunkCap)...)
			continue
		}
		out = append(out, h.header)
		out = append(out, h.body...)
	}
	return out, head
}

func hunkDesc(h *hunk) string {
	return fmt.Sprintf("%d lines, +%d -%d", len(h.body), h.adds, h.dels)
}

// capHunk keeps a hunk's first max body lines.
func capHunk(h *hunk, max int) []string {
	out := []string{h.header}
	if len(h.body) <= max {
		return append(out, h.body...)
	}
	out = append(out, h.body[:max]...)
	return append(out, fmt.Sprintf("[… +%d more lines of this hunk not shown]", len(h.body)-max))
}

func onlyNewDeleted(ext []string) bool {
	for _, e := range ext {
		if !strings.HasPrefix(e, "new file mode ") && !strings.HasPrefix(e, "deleted file mode ") {
			return false
		}
	}
	return true
}
