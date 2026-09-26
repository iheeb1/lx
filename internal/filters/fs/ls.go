package fs

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tokens"
)

// ls condenses directory listings.
//
//   - Long format (-l, -la, -lh, …): columns that are the same on every row
//     (owner, group) are named once in a header, link counts are dropped,
//     permissions are shown only where they differ from the common file /
//     directory mode, directories get a trailing "/", symlinks keep
//     "name -> target". Sizes, dates and names are kept. Above lsNamesOnly
//     entries only the names remain, several per line.
//   - Recursive short format (-R): a directory tree (see PathTree).
//   - Plain short format: already compact, returned unchanged.
//
// Diagnostics ("ls: x: No such file or directory") are kept verbatim, first.
type ls struct{}

// Long listings above lsNamesOnly rows whose compact long form would
// still cost more than lsLongTarget tokens keep names only (with the
// dropped columns named in the header). Below that, sizes and dates stay:
// `ls -lt` / `ls -lS` are asked for exactly those.
const (
	lsNamesOnly  = 80
	lsLongTarget = 3000
)

func (ls) Name() string    { return "ls" }
func (ls) IsContent() bool { return true }

func (ls) Match(c *engine.Context) bool {
	e := Effective(c)
	if engine.MachineReadable(e) {
		return false
	}
	if e.Name() != "ls" && e.Name() != "gls" {
		return false
	}
	for _, a := range e.Args() {
		if a == "--help" || a == "--version" || a == "--zero" || a == "--dired" || a == "-D" {
			return false
		}
	}
	return true
}

type lsFlags struct {
	long, recursive  bool
	noOwner, noGroup bool // GNU -g / -o / -G
	inode, blocks    bool
	columns          bool // -C, -x, -m: several names per line
	operands         []string
}

func parseLsFlags(args []string) lsFlags {
	var f lsFlags
	dashdash := false
	for _, a := range args {
		switch {
		case dashdash || a == "-" || !strings.HasPrefix(a, "-"):
			f.operands = append(f.operands, a)
		case a == "--":
			dashdash = true
		case strings.HasPrefix(a, "--"):
			switch {
			case a == "--recursive":
				f.recursive = true
			case a == "--inode":
				f.inode = true
			case a == "--size":
				f.blocks = true
			case a == "--numeric-uid-gid", a == "--format=long", a == "--format=verbose", a == "--full-time":
				f.long = true
			case a == "--no-group":
				f.noGroup = true
			case strings.HasPrefix(a, "--format="):
				f.columns = true
			}
		default:
			for _, ch := range a[1:] {
				switch ch {
				case 'l', 'n':
					f.long = true
				case 'g':
					f.long, f.noOwner = true, true
				case 'o':
					f.long, f.noGroup = true, true
				case 'G':
					f.noGroup = true
				case 'R':
					f.recursive = true
				case 'i':
					f.inode = true
				case 's':
					f.blocks = true
				case 'C', 'x', 'm':
					f.columns = true
				case '1':
					f.columns = false
				}
			}
		}
	}
	return f
}

var (
	months = `(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)`
	// lsDate: "Sep 26 00:48", "Jan  3  2024", "Sep 26 00:48:12 2026" (-T),
	// "26 Sep 00:48", "2026-09-26 00:48[:12.123 +0200]" (long-/full-iso),
	// "09-26 00:48" (iso).
	lsDate = `(?:` + months + ` +\d{1,2} +(?:\d{1,2}:\d{2}(?::\d{2})?(?: +\d{4})?|\d{4})` +
		`|\d{1,2} +` + months + ` +(?:\d{1,2}:\d{2}|\d{4})` +
		`|\d{4}-\d{2}-\d{2}(?: +\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?: +[+-]\d{4})?)?` +
		`|\d{2}-\d{2} +\d{2}:\d{2})`
	lsRowRe = regexp.MustCompile(`^([-bcdlpsDw?])([-rwxsStTlL]{9})([@+.]?) +(.*?) +` +
		`(\d+(?:[.,]\d+)?[BkKMGTPEZY]?|\d+, *\d+|0x[0-9a-fA-F]+|-) +(` + lsDate + `) (.*)$`)
	lsTotalRe = regexp.MustCompile(`^total \d+(?:[.,]\d+)?[BkKMGTPEZY]?$`)
)

type lsRow struct {
	typ  byte
	mode string // permission bits plus the xattr/ACL marker: "rw-r--r--@"
	mid  []string
	size string
	date string
	name string
}

type lsBlock struct {
	dir       string // header without the colon; "" when the block has none
	hasHeader bool
	total     string
	rows      []lsRow
	names     []string
}

func (l ls) Apply(c *engine.Context, out string) (string, bool) {
	e := Effective(c)
	if !l.Match(e) || strings.TrimSpace(out) == "" {
		return "", false
	}
	f := parseLsFlags(e.Args())
	lines := strings.Split(out, "\n")
	isNote := func(ln string) bool { return NoteHasPrefix(e, ln, "ls") }
	switch {
	case f.long:
		if f.inode || f.blocks {
			return "", false // leading number columns: not modeled
		}
		return lsLong(e, lines, f, isNote)
	case f.recursive:
		if f.inode || f.blocks || f.columns {
			return "", false
		}
		return lsRecursive(e, lines, f, isNote)
	}
	// Plain listings are already compact; claiming them keeps the generic
	// reducer from folding similar file names.
	return out, true
}

// isHeader reports whether ln opens a block: "dir:" after a blank line, or
// as the first line when it names an operand (GNU prints ".:" for -R).
func isHeader(ln string, prevBlank, first bool, f lsFlags) bool {
	if !strings.HasSuffix(ln, ":") || len(ln) < 2 {
		return false
	}
	if prevBlank && !first {
		return true
	}
	if !first {
		return false
	}
	d := strings.TrimSuffix(ln, ":")
	if len(f.operands) == 0 {
		return d == "."
	}
	for _, o := range f.operands {
		if o == d {
			return true
		}
	}
	return false
}

func lsLong(e *engine.Context, lines []string, f lsFlags, isNote func(string) bool) (string, bool) {
	var notes []string
	blocks := []*lsBlock{{}}
	cur := blocks[0]
	prevBlank, first := false, true
	nmid := -1
	for _, ln := range lines {
		if ln == "" {
			prevBlank = true
			continue
		}
		if isNote(ln) {
			notes = append(notes, ln)
			continue
		}
		switch m := lsRowRe.FindStringSubmatch(ln); {
		case m != nil:
			r := lsRow{typ: m[1][0], mode: m[2] + m[3], mid: strings.Fields(m[4]), size: m[5], date: m[6], name: m[7]}
			if nmid < 0 {
				nmid = len(r.mid)
			}
			if len(r.mid) != nmid || r.name == "" {
				return "", false // owner/group with spaces, or a mixed format
			}
			cur.rows = append(cur.rows, r)
		case lsTotalRe.MatchString(ln) && len(cur.rows) == 0 && cur.total == "":
			cur.total = ln
		case isHeader(ln, prevBlank, first, f):
			cur = &lsBlock{dir: strings.TrimSuffix(ln, ":"), hasHeader: true}
			blocks = append(blocks, cur)
		default:
			return "", false
		}
		prevBlank, first = false, false
	}
	var rows []*lsRow
	for _, b := range blocks {
		for i := range b.rows {
			rows = append(rows, &b.rows[i])
		}
	}
	// A failing run must be explained by a diagnostic the listing keeps
	// (see Unexplained); otherwise it was cut short (timeout, signal) and
	// counts would claim a complete listing.
	if len(rows) == 0 || nmid < 1 || nmid > 4 || Unexplained(e, len(notes)) {
		return "", false
	}

	// Columns between the mode and the size: the link count (dropped),
	// then owner / group / BSD file flags.
	var labels []string
	switch nmid {
	case 1:
		labels = []string{"links"}
	case 2:
		if f.noOwner {
			labels = []string{"links", "group"}
		} else {
			labels = []string{"links", "owner"}
		}
	case 3:
		labels = []string{"links", "owner", "group"}
	case 4:
		labels = []string{"links", "owner", "group", "flags"}
	}
	for _, r := range rows {
		if !isDigits(r.mid[0]) {
			return "", false
		}
	}
	var notesHdr []string
	keepCol := make([]bool, nmid)
	for col := 1; col < nmid; col++ {
		v := rows[0].mid[col]
		same := true
		for _, r := range rows[1:] {
			if r.mid[col] != v {
				same = false
				break
			}
		}
		if same {
			notesHdr = append(notesHdr, labels[col]+" "+v)
		} else {
			keepCol[col] = true
		}
	}
	// Varying owner/group columns: when one combination covers at least
	// half the rows, name it once and print the columns only on the rows
	// that differ (root-owned devices in a user's directory).
	tuple := func(r *lsRow) string {
		var parts []string
		for col := 1; col < nmid; col++ {
			if keepCol[col] {
				parts = append(parts, r.mid[col])
			}
		}
		return strings.Join(parts, " ")
	}
	majority := ""
	ownerNote := "" // how the names-only view describes varying owners
	if slicesContains(keepCol, true) {
		ownerNote = "owners vary"
		count := map[string]int{}
		for _, r := range rows {
			count[tuple(r)]++
		}
		best := ""
		for k, n := range count {
			if n > count[best] || n == count[best] && k < best {
				best = k
			}
		}
		if count[best]*2 >= len(rows) {
			majority = best
			var parts []string
			for col, v := 1, strings.Fields(best); col < nmid; col++ {
				if keepCol[col] {
					parts = append(parts, labels[col]+" "+v[0])
					v = v[1:]
				}
			}
			ownerNote = "mostly " + strings.Join(parts, ", ")
		}
	}

	// The usual mode per type; rows that differ show theirs.
	defMode := map[byte]string{}
	for _, t := range []byte{'-', 'd'} {
		count := map[string]int{}
		for _, r := range rows {
			if r.typ == t {
				count[r.mode]++
			}
		}
		best := ""
		for m, n := range count {
			if n > count[best] || n == count[best] && m < best {
				best = m
			}
		}
		if best != "" {
			defMode[t] = best
		}
	}
	var modeParts []string
	if m, ok := defMode['-']; ok {
		modeParts = append(modeParts, "-"+m+" files")
	}
	if m, ok := defMode['d']; ok {
		modeParts = append(modeParts, "d"+m+" dirs")
	}
	showMode := func(r *lsRow) string {
		if r.typ == 'l' {
			return "" // "name -> target" says it is a link
		}
		if m, ok := defMode[r.typ]; ok && m == r.mode {
			return ""
		}
		return string(r.typ) + r.mode
	}

	var b strings.Builder
	for _, n := range CapNotes(notes) {
		b.WriteString(n + "\n")
	}
	var cb strings.Builder
	hdr := append([]string(nil), notesHdr...)
	if majority != "" {
		hdr = append(hdr, strings.TrimPrefix(ownerNote, "mostly ")+" unless shown")
	}
	anyMode := false
	for _, r := range rows {
		if showMode(r) != "" {
			anyMode = true
			break
		}
	}
	if len(modeParts) > 0 {
		s := "mode " + strings.Join(modeParts, ", ")
		if anyMode {
			s += " unless shown"
		}
		hdr = append(hdr, s)
	}
	hdr = append(hdr, "link counts dropped")
	fmt.Fprintf(&cb, "[ls: %s]\n", strings.Join(hdr, " · "))

	// Columns are separated by one space and not padded: alignment costs
	// about two tokens per row and models do not need it.
	wrote := false
	for _, blk := range blocks {
		if blk.hasHeader {
			if wrote {
				cb.WriteByte('\n')
			}
			cb.WriteString(blk.dir + ":\n")
		}
		wrote = wrote || blk.hasHeader || len(blk.rows) > 0
		if blk.total != "" {
			cb.WriteString(blk.total + "\n")
		}
		for i := range blk.rows {
			r := &blk.rows[i]
			if m := showMode(r); m != "" {
				cb.WriteString(m + " ")
			}
			if tp := tuple(r); tp != "" && tp != majority {
				cb.WriteString(tp + " ")
			}
			cb.WriteString(r.size + " " + r.date + " " + displayName(r) + "\n")
		}
	}
	longTokens := tokens.Count(cb.String())
	if len(rows) > lsNamesOnly && longTokens > lsLongTarget {
		if f.recursive {
			if ownerNote != "" {
				notesHdr = append(notesHdr, ownerNote)
			}
			return lsLongAsTree(&b, blocks, notesHdr, f, longTokens)
		}
		byType := map[byte]int{}
		for _, r := range rows {
			if r.name != "." && r.name != ".." {
				byType[r.typ]++
			}
		}
		var kinds []string
		if n := byType['d']; n > 0 {
			kinds = append(kinds, plural2(n, "dir", "dirs")+" (marked /)")
		}
		if n := byType['-']; n > 0 {
			kinds = append(kinds, plural2(n, "file", "files"))
		}
		if n := byType['l']; n > 0 {
			kinds = append(kinds, plural2(n, "symlink", "symlinks")+" (name -> target)")
		}
		other := 0
		for t, n := range byType {
			if t != 'd' && t != '-' && t != 'l' {
				other += n
			}
		}
		if other > 0 {
			kinds = append(kinds, plural2(other, "other", "others"))
		}
		total := byType['d'] + byType['-'] + byType['l'] + other
		hdr := []string{fmt.Sprintf("%s: %s", plural2(total, "entry", "entries"), strings.Join(kinds, ", "))}
		hdr = append(hdr, notesHdr...)
		if ownerNote != "" {
			hdr = append(hdr, ownerNote)
		}
		hdr = append(hdr, fmt.Sprintf("names only: modes, link counts, sizes and dates dropped (~%s tokens in long form)", commaInt(longTokens)))
		fmt.Fprintf(&b, "[ls: %s]\n", strings.Join(hdr, " · "))
		wrote := false
		used := 0
		for _, blk := range blocks {
			if blk.hasHeader {
				if wrote {
					b.WriteByte('\n')
				}
				b.WriteString(blk.dir + ":\n")
			}
			wrote = wrote || blk.hasHeader || len(blk.rows) > 0
			var names []string
			for _, r := range blk.rows {
				if r.name == "." || r.name == ".." {
					continue
				}
				names = append(names, displayName(&r))
			}
			lines, more := capNames(names, DefaultTreeTarget-used)
			for _, ln := range lines {
				b.WriteString(ln + "\n")
				used += tokens.Count(ln) + 1
			}
			if more > 0 {
				fmt.Fprintf(&b, "… +%s\n", plural2(more, "more entry", "more entries"))
			}
		}
		return strings.TrimRight(b.String(), "\n"), true
	}

	b.WriteString(cb.String())
	return strings.TrimRight(b.String(), "\n"), true
}

// lsLongAsTree renders a large `ls -lR` as a path tree of names only.
func lsLongAsTree(b *strings.Builder, blocks []*lsBlock, notesHdr []string, f lsFlags, longTokens int) (string, bool) {
	var paths []string
	entries := 0
	for i, blk := range blocks {
		dir, ok := blockDir(i, blk, f)
		if !ok {
			return "", false
		}
		for _, r := range blk.rows {
			if r.name == "." || r.name == ".." {
				continue
			}
			entries++
			name := r.name
			if r.typ == 'd' && !strings.HasSuffix(name, "/") {
				name += "/"
			}
			paths = append(paths, joinDir(dir, name))
		}
	}
	t := NewPathTree(paths, f.operands)
	lines, capped := t.Render(DefaultTreeTarget)
	listed := 0 // directories listed: every header block, and a header-less first block with rows
	for i, blk := range blocks {
		if blk.hasHeader || i == 0 && (len(blk.rows) > 0 || blk.total != "") {
			listed++
		}
	}
	hdr := []string{fmt.Sprintf("%s in %s", plural2(entries, "entry", "entries"), plural2(listed, "directory", "directories"))}
	hdr = append(hdr, notesHdr...)
	hdr = append(hdr, fmt.Sprintf("names only: modes, sizes and dates dropped (~%s tokens in long form)", commaInt(longTokens)))
	if capped != "" {
		hdr = append(hdr, capped)
	}
	fmt.Fprintf(b, "[ls: %s]\n", strings.Join(hdr, " · "))
	b.WriteString(strings.Join(lines, "\n"))
	return strings.TrimRight(b.String(), "\n"), true
}

func displayName(r *lsRow) string {
	switch {
	case r.typ == 'd' && !strings.HasSuffix(r.name, "/"):
		return r.name + "/"
	}
	return r.name
}

// blockDir is the directory a block lists: its header, or for the
// header-less first block the single operand (or "." with none). A first
// block without header under several operands lists file operands, whose
// names are already paths.
func blockDir(i int, blk *lsBlock, f lsFlags) (string, bool) {
	if blk.hasHeader {
		return blk.dir, true
	}
	if i != 0 {
		return "", false
	}
	switch len(f.operands) {
	case 0:
		return ".", true
	case 1:
		if strings.ContainsAny(f.operands[0], "*?[") {
			return "", false
		}
		return f.operands[0], true
	}
	return "", true
}

func joinDir(dir, name string) string {
	if dir == "" || dir == "." {
		return name
	}
	return strings.TrimSuffix(dir, "/") + "/" + name
}

// lsRecursive turns `ls -R` blocks into a path tree.
func lsRecursive(e *engine.Context, lines []string, f lsFlags, isNote func(string) bool) (string, bool) {
	var notes []string
	blocks := []*lsBlock{{}}
	cur := blocks[0]
	prevBlank, first := false, true
	for _, ln := range lines {
		if ln == "" {
			prevBlank = true
			continue
		}
		if isNote(ln) {
			notes = append(notes, ln)
			continue
		}
		if isHeader(ln, prevBlank, first, f) {
			cur = &lsBlock{dir: strings.TrimSuffix(ln, ":"), hasHeader: true}
			blocks = append(blocks, cur)
		} else {
			if prevBlank && !first {
				return "", false // entries right after a blank line: not ls -R
			}
			cur.names = append(cur.names, ln)
		}
		prevBlank, first = false, false
	}
	if len(blocks) < 2 || Unexplained(e, len(notes)) {
		return "", false
	}
	// A name is a directory when a later block lists it.
	isDir := map[string]bool{}
	for _, blk := range blocks[1:] {
		isDir[path.Clean(blk.dir)] = true
	}
	// Subdirectories are the block headers other than the listed roots
	// (GNU prints ".:" or "src:" first; several operands each get one).
	roots := map[string]bool{}
	for _, o := range f.operands {
		roots[path.Clean(o)] = true
	}
	if len(f.operands) == 0 {
		roots["."] = true
	}
	dirs := 0
	for _, blk := range blocks[1:] {
		if !roots[path.Clean(blk.dir)] {
			dirs++
		}
	}
	var paths []string
	files := 0
	for i, blk := range blocks {
		dir, ok := blockDir(i, blk, f)
		if !ok {
			return "", false
		}
		for _, nm := range blk.names {
			if nm == "." || nm == ".." {
				continue
			}
			p := joinDir(dir, nm)
			if isDir[path.Clean(p)] || strings.HasSuffix(nm, "/") {
				if !strings.HasSuffix(p, "/") {
					p += "/"
				}
			} else {
				files++
			}
			paths = append(paths, p)
		}
		if blk.hasHeader && len(blk.names) == 0 {
			paths = append(paths, strings.TrimSuffix(blk.dir, "/")+"/")
		}
	}
	if len(paths) == 0 {
		return "", false
	}
	t := NewPathTree(paths, f.operands)
	tl, capped := t.Render(DefaultTreeTarget)
	var b strings.Builder
	for _, n := range CapNotes(notes) {
		b.WriteString(n + "\n")
	}
	hdr := fmt.Sprintf("ls -R: %s in %s", plural2(files, "file", "files"), plural2(dirs, "subdirectory", "subdirectories"))
	if capped != "" {
		hdr += " · " + capped
	}
	fmt.Fprintf(&b, "[%s]\n", hdr)
	b.WriteString(strings.Join(tl, "\n"))
	return strings.TrimRight(b.String(), "\n"), true
}

func plural2(n int, one, many string) string {
	return commaInt(n) + " " + plural(n, one, many)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func slicesContains(xs []bool, v bool) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// capNames wraps names (see wrapNames) and keeps whole lines while they
// fit in budget tokens. It returns the lines and how many names were left
// out, counted from the names themselves (a quoted name may hold "  ").
func capNames(names []string, budget int) ([]string, int) {
	lines, counts := wrapNamesCounted("", names)
	used := 0
	for i, ln := range lines {
		used += tokens.Count(ln) + 1
		if used > budget && i > 0 {
			more := 0
			for _, n := range counts[i:] {
				more += n
			}
			return lines[:i], more
		}
	}
	return lines, 0
}
