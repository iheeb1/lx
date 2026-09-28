package git

import (
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/filters/focus"
	"github.com/iheeb1/lx/internal/lazyre"
)

func init() { engine.Register(diffFilter{}) }

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

var verbatimDiffFlags = []string{
	"--stat", "--stat=", "--numstat", "--shortstat", "--dirstat", "--dirstat=", "--summary",
	"--compact-summary", "--name-only", "--name-status", "--raw", "--check", "-z",
	"--binary", "--full-index", "--word-diff", "--word-diff=", "--word-diff-regex=",
	"--color-words", "--color-words=", "--output=", "--patch-with-raw", "--patch-with-stat",
}

func (diffFilter) Apply(c *engine.Context, out string) (string, bool) {
	if hasArg(c, verbatimDiffFlags...) || c.Sub() == "stash" && !hasArg(c, "-p", "--patch", "-u") {
		return out, true
	}
	lines := strings.Split(out, "\n")
	doc, ok := parseDiffDoc(lines)
	if !ok {
		return out, true
	}
	if doc.files == 0 {
		return "", false
	}
	return join(renderDiffParts(doc.parts, diffBudget, focus.New(c.Focus))), true
}

const diffBudget = 7000

type part struct {
	file *fileDiff
	raw  string
}

type fileDiff struct {
	head       string
	combined   bool
	ext        []string
	minus      string
	plus       string
	binary     string
	isNew      bool
	isDeleted  bool
	renamed    bool
	name       string
	hunks      []*hunk
	adds, dels int
	btok       int
	btokDone   bool
	sum        int8
}

type hunk struct {
	header    string
	body      []string
	adds      int
	dels      int
	conflicts []conflictLine
}

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

var (
	diffHeadRe  = lazyre.New(`^diff --git (.+)$`)
	diffCCRe    = lazyre.New(`^diff --(?:cc|combined) (.+)$`)
	hunkRe      = lazyre.New(`^@@ -\d+(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(?: .*)?$`)
	hunkCCRe    = lazyre.New(`^(@@@+) ((?:-\d+(?:,\d+)? )+)\+(\d+)(?:,(\d+))? @@@+(?: .*)?$`)
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

func parseFile(lines []string, i int) (*fileDiff, int, bool) {
	f := &fileDiff{head: lines[i]}
	if m := diffCCRe.FindStringSubmatch(lines[i]); m != nil {
		f.combined, f.name = true, m[1]
	} else if m := diffHeadRe.FindStringSubmatch(lines[i]); m != nil {
		f.name = headNewPath(m[1])
	}
	var renameFrom, renameTo string
	i++

	for i < len(lines) {
		ln := lines[i]
		if strings.HasPrefix(ln, "@@") || isDiffStart(ln) {
			break
		}
		switch {
		case ln == "GIT binary patch":
			return nil, 0, false
		case strings.HasPrefix(ln, "index "):
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
				f.renamed = true
			}
			f.ext = append(f.ext, ln)
		default:
			return f.finish(renameFrom, renameTo), i, true
		}
		i++
	}

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

func (f *fileDiff) finish(renameFrom, renameTo string) *fileDiff {
	if !f.combined {
		if m := diffHeadRe.FindStringSubmatch(f.head); m != nil {
			rest := m[1]

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

func parseHunk(lines []string, i int, combined bool) (*hunk, int, bool) {
	h := &hunk{header: lines[i]}
	var need []int
	width := 1
	newLine := 0
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

	for i < len(lines) && strings.HasPrefix(lines[i], `\`) {
		h.body = append(h.body, lines[i])
		i++
	}
	return h, i, true
}

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

const maxConflictLines = 20

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

var lockfiles = map[string]bool{
	"package-lock.json": true, "npm-shrinkwrap.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
	"bun.lock": true, "Cargo.lock": true, "go.sum": true, "poetry.lock": true, "uv.lock": true,
	"Pipfile.lock": true, "composer.lock": true, "Gemfile.lock": true, "pubspec.lock": true,
	"Podfile.lock": true, "flake.lock": true, "packages.lock.json": true, "gradle.lockfile": true,
	"mix.lock": true, "Package.resolved": true, "deno.lock": true, "go.work.sum": true,
}

var generatedRe = lazyre.New(`\.min\.(?:js|css|mjs)$|\.(?:js|css|mjs)\.map$|(?:^|/)dist/|` +
	`\.pb\.go$|\.pb\.gw\.go$|_pb2(?:_grpc)?\.pyi?$|\.pb\.(?:cc|h)$|\.g\.dart$|\.freezed\.dart$|` +
	`\.generated\.|(?:^|/)__generated__/`)

const summarizeMin = 400

const summarizeMinLines = 10

func isLockfile(name string) bool { return lockfiles[path.Base(name)] }

func isGenerated(name string) bool { return generatedRe.MatchString(name) }

const (
	lvlBase = iota
	lvlNewFiles
	lvlLargeFiles
	lvlFileList
)

func renderDiffParts(parts []part, budget int, fx *focus.Set) []string {
	var out []string
	parts, keep := focusDiff(parts, fx)
	groups := identicalGroups(parts)
	fit := func(from int, keep func(*fileDiff) bool) bool {
		for lvl := from; lvl <= lvlFileList; lvl++ {
			caps := []int{0}
			switch lvl {
			case lvlLargeFiles:
				caps = []int{800, 400, 200, 100}
			case lvlFileList:
				caps = []int{100}
			}
			for _, hc := range caps {
				out = renderLevel(parts, groups, lvl, hc, budget, keep)
				if fitsBudget(out, budget) {
					return true
				}
			}
		}
		return false
	}
	if fit(lvlBase, keep) || keep == nil {
		return out
	}
	fit(lvlNewFiles, nil)
	return out
}

const maxListed = 300

func renderLevel(parts []part, groups map[*fileDiff][]*fileDiff, lvl, hunkCap, budget int, keep func(*fileDiff) bool) []string {
	var out []string
	var rest []statRow
	used := 0
	if lvl >= lvlFileList {
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
			continue
		}
		flvl, fcap := lvl, hunkCap
		if keep != nil && keep(f) {
			flvl, fcap = lvlBase, 0
		}
		fl := renderFile(f, flvl, fcap)
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
		return "0"
	}
}

func listDesc(f *fileDiff) string {
	if cs := f.conflicts(); len(cs) > 0 {
		return countDesc(f) + fmt.Sprintf(" (%s)", engine.Plural(len(cs), "conflict-marker line", "conflict-marker lines"))
	}
	return countDesc(f)
}

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

func renderFile(f *fileDiff, lvl, hunkCap int) []string {
	out, head := renderFileBody(f, lvl, hunkCap)
	if note := conflictNote(f, out[head:]); note != nil {
		out = append(out[:head:head], append(note, out[head:]...)...)
	}
	return out
}

func renderFileBody(f *fileDiff, lvl, hunkCap int) (out []string, head int) {
	if f.binary != "" && !f.renamed && onlyNewDeleted(f.ext) {
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
