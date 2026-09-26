package search

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/iheeb1/lx/internal/filters/fs"
	"github.com/iheeb1/lx/internal/tokens"
)

// detailBudget: tokens for the line-by-line part of the output; the
// histogram and summaries come on top.
const detailBudget = 4000

type render struct {
	o        opts
	mt       *matcher
	withFile bool
	b        strings.Builder
}

func (r *render) line(s string) {
	r.b.WriteString(s)
	r.b.WriteByte('\n')
}

func (r *render) run(notes []string, groups []*fileGroup, total, nfiles int) string {
	// Dependency directories the user did not target are summarized, unless
	// that would leave nothing to show.
	var normal, heavy []*fileGroup
	if r.withFile {
		for _, g := range groups {
			if root := heavyRoot(g.path); root != "" && !explicitlySearched(root, r.o) {
				g.heavy = root
			}
		}
	}
	for _, g := range groups {
		if g.heavy != "" {
			heavy = append(heavy, g)
		} else {
			normal = append(normal, g)
		}
	}
	nMatches, nFiles := 0, 0
	for _, g := range normal {
		nMatches += g.matches
		if g.matches > 0 {
			nFiles++
		}
	}
	heavyMatches := total - nMatches
	var heavyRoots []string
	if nMatches == 0 || heavyMatches <= minHeavy {
		// Nothing else to show, or so few dependency hits that the
		// summary line would cost as much as the hits: show them.
		for _, g := range heavy {
			g.heavy = ""
		}
		normal, heavy = groups, nil
		nMatches, nFiles = total, nfiles
	}

	// Which files are shown in full, which are capped, which only counted.
	// Everything is shown when it is within the match, file and token caps;
	// otherwise files are taken in output order, ≤perFile matches each,
	// until one of the caps is reached.
	type shownGroup struct {
		g    *fileGroup
		text string
		n    int // matches shown
	}
	var shown []shownGroup
	var rest []*fileGroup
	shownMatches, used := 0, 0
	capped := nMatches > maxMatches || nFiles > maxFiles
	if !capped {
		for _, g := range normal {
			txt, n := r.group(g, 0)
			shown = append(shown, shownGroup{g, txt, n})
			used += tokens.Count(txt)
		}
		if used > detailBudget {
			capped, shown, used = true, nil, 0
		} else {
			shownMatches = nMatches
		}
	}
	limit := 0
	if capped {
		limit = maxMatches
		if nFiles > 1 {
			limit = perFile
		}
		for i, g := range normal {
			if len(shown) >= maxFiles || shownMatches >= maxMatches {
				rest = append(rest, normal[i:]...)
				break
			}
			txt, n := r.group(g, limit)
			cost := tokens.Count(txt)
			if used+cost > detailBudget {
				if len(shown) > 0 {
					rest = append(rest, normal[i:]...)
					break
				}
				// The first file alone is too big (long context blocks):
				// show fewer of its matches.
				for k := limit / 2; k >= 1 && used+cost > detailBudget; k /= 2 {
					txt, n = r.group(g, k)
					cost = tokens.Count(txt)
				}
			}
			shown = append(shown, shownGroup{g, txt, n})
			shownMatches += n
			used += cost
		}
	}

	// Header, only when something is not shown line by line.
	var hdr []string
	heavyMatches, heavyRoots = 0, []string{}
	seenRoot := map[string]bool{}
	for _, g := range heavy {
		heavyMatches += g.matches
		if !seenRoot[g.heavy] {
			seenRoot[g.heavy] = true
			heavyRoots = append(heavyRoots, g.heavy)
		}
	}
	if len(heavy) > 0 || capped {
		hdr = append(hdr, fmt.Sprintf("%s in %s", plural(total, "match", "matches"), plural(nfiles, "file", "files")))
	}
	if len(heavy) > 0 {
		names := heavyRoots
		if len(names) > 3 {
			names = append(names[:3:3], "…")
		}
		hdr = append(hdr, fmt.Sprintf("%s in %s summarized at the end (not searched explicitly)",
			plural(heavyMatches, "match", "matches"), strings.Join(names, ", ")))
	}
	if capped {
		s := fmt.Sprintf("showing %d from the first %s", shownMatches, plural(len(shown), "file", "files"))
		if len(rest) == 0 {
			s = fmt.Sprintf("showing %d", shownMatches)
		}
		if limit < maxMatches && nFiles > 1 {
			s += fmt.Sprintf(", ≤%d per file", limit)
		}
		if len(rest) > 0 {
			s += fmt.Sprintf("; match counts for the other %s at the end", plural(len(rest), "file", "files"))
		}
		hdr = append(hdr, s)
	}
	if len(hdr) > 0 {
		r.line("[" + strings.Join(hdr, " · ") + "]")
	}

	r.notes(notes)
	// Files with a single hit and no context keep grep's own one-line
	// form; a heading would cost more than the path it factors out.
	prevGroup := false
	for i, sg := range shown {
		single := r.withFile && len(sg.g.entries) == 1 && sg.g.entries[0].match
		if i > 0 && r.withFile && (!single || prevGroup) {
			r.line("")
		}
		if single {
			en := sg.g.entries[0]
			text := en.text
			if !r.o.invert {
				text = strings.TrimLeft(text, " \t")
			}
			r.line(sg.g.path + ":" + prefix(en, r.o) + window(text, r.mt))
		} else {
			r.b.WriteString(sg.text)
		}
		prevGroup = !single
	}
	if len(heavy) > 0 || len(rest) > 0 {
		r.line("")
	}
	r.heavy(heavy, heavyRoots)
	if len(rest) > 0 {
		r.line(fmt.Sprintf("[%s, by match count]", plural(len(rest), "more file", "more files")))
	}
	r.histogram(rest)
	if capped {
		r.line("[narrow the search: " + narrowHint(r.o) + "]")
	}
	return strings.TrimRight(r.b.String(), "\n")
}

// notes prints diagnostics verbatim (a flood is counted by message, see
// fs.CapNotes); runs of "Binary file x matches" are capped with a count.
func (r *render) notes(notes []string) {
	var other, bin []string
	for _, n := range notes {
		if isBinaryNote(n) {
			bin = append(bin, n)
		} else {
			other = append(other, n)
		}
	}
	for _, n := range fs.CapNotes(other) {
		r.line(n)
	}
	for i, n := range bin {
		if i == maxNotes {
			r.line(fmt.Sprintf("… +%d more binary files matched", len(bin)-maxNotes))
			break
		}
		r.line(n)
	}
}

// group renders one file's entries, the first limit matches (0: all), and
// returns the text and the number of matches shown.
func (r *render) group(g *fileGroup, limit int) (string, int) {
	var b strings.Builder
	line := func(s string) {
		b.WriteString(s)
		b.WriteByte('\n')
	}
	// rg's --heading layout: the path once, then its lines as "NN:text"
	// (match) / "NN-text" (context), a blank line between files.
	indent := ""
	if r.withFile {
		line(g.path)
		if !r.o.numbered && !r.o.context {
			indent = "  " // unnumbered hits must not look like path lines
		}
	}
	entries := g.entries
	shownMatches := 0
	cut := len(entries)
	if limit > 0 && g.matches > limit {
		for i, en := range entries {
			if en.match {
				if shownMatches == limit {
					cut = i
					break
				}
				shownMatches++
			}
		}
		// Context lines before the first hidden match stay: they may also
		// be the after-context of the last shown one.
		entries = entries[:cut]
	} else {
		shownMatches = g.matches
	}
	// Trim separators at the edges.
	for len(entries) > 0 && entries[0].sep {
		entries = entries[1:]
	}
	for len(entries) > 0 && entries[len(entries)-1].sep {
		entries = entries[:len(entries)-1]
	}

	// Indentation: hits alone are stripped; with context (and for -v,
	// whose "hits" are the file's other lines) each contiguous block loses
	// only its common indentation, so the structure stays visible.
	blocks := r.o.context || r.o.invert
	for start := 0; start < len(entries); {
		end := start
		for end < len(entries) && !entries[end].sep && (end == start || !blocks || contiguous(entries[end-1], entries[end])) {
			end++
		}
		if end == start { // a separator
			line(indent + "--")
			start++
			continue
		}
		block := entries[start:end]
		strip := ""
		if blocks {
			strip = commonIndent(block)
		}
		for _, en := range block {
			text := en.text
			if blocks {
				text = strings.TrimPrefix(text, strip)
			} else {
				text = strings.TrimLeft(text, " \t")
			}
			text = window(text, r.mt)
			line(strings.TrimRight(indent+prefix(en, r.o)+text, " "))
		}
		start = end
	}
	if more := g.matches - shownMatches; more > 0 {
		line(fmt.Sprintf("%s… +%d more in this file", indent, more))
	}
	return b.String(), shownMatches
}

func prefix(en entry, o opts) string {
	sep := ":"
	if !en.match {
		sep = "-"
	}
	switch {
	case en.num != "" && en.col != "":
		return en.num + sep + en.col + sep // --column / --vimgrep
	case en.num != "":
		return en.num + sep
	case o.context:
		return sep // grep's own marker, path factored out
	}
	return ""
}

// contiguous reports whether b directly follows a in the file.
func contiguous(a, b entry) bool {
	if a.num == "" || b.num == "" {
		return true
	}
	return lineBefore(a.num, b.num)
}

func lineBefore(a, b string) bool {
	x, y := atoi(a), atoi(b)
	return x >= 0 && y == x+1
}

func atoi(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' || n > 1<<40 {
			return -1
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// commonIndent is the leading whitespace shared by every non-blank line.
func commonIndent(block []entry) string {
	common, first := "", true
	for _, en := range block {
		if strings.TrimSpace(en.text) == "" {
			continue
		}
		ind := en.text[:len(en.text)-len(strings.TrimLeft(en.text, " \t"))]
		if first {
			common, first = ind, false
			continue
		}
		i := 0
		for i < len(common) && i < len(ind) && common[i] == ind[i] {
			i++
		}
		common = common[:i]
	}
	return common
}

// heavy prints one line per dependency directory root with its top files.
func (r *render) heavy(groups []*fileGroup, roots []string) {
	if len(groups) == 0 {
		return
	}
	type fc struct {
		path string
		n    int
	}
	by := map[string][]fc{}
	for _, g := range groups {
		by[g.heavy] = append(by[g.heavy], fc{strings.TrimPrefix(g.path, g.heavy), g.matches})
	}
	const maxRoots = 10
	for i, root := range roots {
		if i == maxRoots {
			rem, files := 0, 0
			for _, rt := range roots[maxRoots:] {
				for _, f := range by[rt] {
					rem += f.n
					files++
				}
			}
			r.line(fmt.Sprintf("… +%d more dependency directories: %s in %s", len(roots)-maxRoots, plural(rem, "match", "matches"), plural(files, "file", "files")))
			break
		}
		files := by[root]
		n := 0
		for _, f := range files {
			n += f.n
		}
		sort.SliceStable(files, func(a, b int) bool { return files[a].n > files[b].n })
		var top []string
		for j, f := range files {
			if j == 5 {
				top = append(top, "…")
				break
			}
			top = append(top, fmt.Sprintf("%s (%d)", f.path, f.n))
		}
		r.line(fmt.Sprintf("%s: %s in %s: %s", root, plural(n, "match", "matches"), plural(len(files), "file", "files"), strings.Join(top, ", ")))
	}
}

// histogram prints "path: N matches" for every file not shown, most first.
func (r *render) histogram(rest []*fileGroup) {
	if len(rest) == 0 {
		return
	}
	sorted := append([]*fileGroup(nil), rest...)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].matches > sorted[b].matches })
	for i, g := range sorted {
		if i == maxHistogram {
			r.tail(sorted[i:])
			return
		}
		r.line(fmt.Sprintf("%s: %s", g.path, plural(g.matches, "match", "matches")))
	}
}

// tail counts the files past the histogram cap per parent directory.
func (r *render) tail(rest []*fileGroup) {
	type dc struct {
		dir      string
		files, n int
	}
	idx := map[string]int{}
	var dirs []dc
	total := 0
	for _, g := range rest {
		d := path.Dir(g.path) + "/"
		i, ok := idx[d]
		if !ok {
			i = len(dirs)
			idx[d] = i
			dirs = append(dirs, dc{dir: d})
		}
		dirs[i].files++
		dirs[i].n += g.matches
		total += g.matches
	}
	sort.SliceStable(dirs, func(a, b int) bool { return dirs[a].n > dirs[b].n })
	var parts []string
	for i, d := range dirs {
		if i == 10 {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, fmt.Sprintf("%s %d in %s", d.dir, d.n, plural(d.files, "file", "files")))
	}
	r.line(fmt.Sprintf("… +%s with %s, by directory: %s", plural(len(rest), "more file", "more files"), plural(total, "match", "matches"), strings.Join(parts, ", ")))
}

func narrowHint(o opts) string {
	switch o.tool {
	case "rg":
		return "add a path, -g '*.ext' or -t TYPE, or a more specific pattern"
	case "git-grep":
		return "add a pathspec (-- 'dir/*.ext') or a more specific pattern"
	}
	return "add a path, --include='*.ext' / --exclude-dir=DIR, or a more specific pattern"
}
