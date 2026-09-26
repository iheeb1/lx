package fs

import (
	"container/heap"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/tokens"
)

// heavyDirs are dependency, VCS and build-output directories whose contents
// are almost never what the reader is looking for. The list matches
// engine.FactorPaths so filtered and generic output agree.
var heavyDirs = map[string]bool{
	"node_modules": true, ".git": true, "dist": true, "build": true, "target": true,
	".venv": true, "venv": true, "__pycache__": true, ".next": true, "coverage": true,
	"vendor": true,
}

const (
	// pathLineWidth wraps a directory's file names (same as FactorPaths).
	pathLineWidth = 160
	// DefaultTreeTarget is the token size RenderPaths aims for before it
	// starts capping depth and files per directory.
	DefaultTreeTarget = 4000
)

// Caps used when the lossless tree is over its token target.
const (
	capFiles = 20 // file names listed per expanded directory
	capDirs  = 40 // subdirectory lines per expanded directory
	// The listed directory itself (the top of the tree) gets more room:
	// it is what the user asked about.
	capRootFiles = 200
	capRootDirs  = 100
)

type pathNode struct {
	files map[string]bool
	dirs  map[string]*pathNode
	total int    // files at or below this node
	ndirs int    // directories below this node
	hist  string // memoized extHistogram of every file below (collapsed lines)
}

func newPathNode() *pathNode {
	return &pathNode{files: map[string]bool{}, dirs: map[string]*pathNode{}}
}

// PathTree is a set of paths ready to render.
type PathTree struct {
	root  *pathNode
	roots [][]string // explicit search roots, as segments
	count int        // distinct non-empty paths added
	// Entries: the listing does not mark every directory (find without
	// -type f, find -maxdepth, tree -L), so a leaf may be a directory.
	// Counts then say "entries" instead of "files", which would be false
	// for `find . -maxdepth 2` where node_modules/ holds package
	// directories.
	Entries bool
}

// NewPathTree builds a tree from paths as a listing tool printed them.
// roots are the directories the user explicitly asked to list (find/ls/rg
// operands): a heavy directory at or above one of them is never pruned.
//
// Normalization matches engine.FactorPaths: blank entries, "." and a
// leading "./" are dropped, a trailing "/" marks a directory, and a path
// that is a prefix of another path is a directory. Absolute paths keep
// their leading "/".
func NewPathTree(paths, roots []string) *PathTree {
	t := &PathTree{root: newPathNode()}
	seen := make(map[string]bool, len(paths))
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		segs, isDir, ok := splitPath(p)
		if !ok {
			continue
		}
		t.count++
		n := t.root
		for i, s := range segs {
			if i == len(segs)-1 && !isDir {
				n.files[s] = true
				break
			}
			child := n.dirs[s]
			if child == nil {
				child = newPathNode()
				n.dirs[s] = child
			}
			n = child
		}
	}
	for _, r := range roots {
		if segs, _, ok := splitPath(r); ok {
			t.roots = append(t.roots, segs)
		}
	}
	t.root.normalize()
	return t
}

// Len is the number of distinct paths in the tree.
func (t *PathTree) Len() int { return t.count }

// Files is the number of files (leaf entries not marked as directories).
func (t *PathTree) Files() int { return t.root.total }

func splitPath(p string) ([]string, bool, bool) {
	if strings.TrimSpace(p) == "" {
		return nil, false, false
	}
	isDir := strings.HasSuffix(p, "/")
	for strings.HasPrefix(p, "./") {
		p = p[2:]
		for strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") {
			p = p[1:] // ".//x" → "x"
		}
	}
	abs := strings.HasPrefix(p, "/")
	p = strings.Trim(p, "/")
	if p == "" || p == "." {
		return nil, false, false
	}
	raw := strings.Split(p, "/")
	segs := make([]string, 0, len(raw)+1)
	if abs {
		segs = append(segs, "")
	}
	for _, s := range raw {
		if s != "" {
			segs = append(segs, s)
		}
	}
	if len(segs) == 0 || len(segs) == 1 && segs[0] == "" {
		return nil, false, false
	}
	return segs, isDir, true
}

// normalize drops file entries that are also directories and computes totals.
func (n *pathNode) normalize() int {
	for name := range n.dirs {
		delete(n.files, name)
	}
	n.total = len(n.files)
	n.ndirs = len(n.dirs)
	for _, d := range n.dirs {
		n.total += d.normalize()
		n.ndirs += d.ndirs
	}
	return n.total
}

// Render draws the tree. The lossless form is used when it fits in target
// tokens (target <= 0: always). Otherwise directories are expanded
// breadth-first (shallow before deep, then path order) while the budget
// lasts; a directory that does not fit stays one counted line, and each
// expanded directory lists at most capFiles names and capDirs
// subdirectories. It returns the lines and a short description of the caps
// ("" when everything is shown apart from pruned heavy directories).
//
// Format (the same as engine.FactorPaths, minus its lossy name masking):
//
//	root.go  go.mod
//	internal/engine/
//	  engine.go  guard.go
//	  testdata/ [37 files: .log×30 .json×7]      ← not expanded
//	  … +12 more files: .go×10 .md×2           ← past the per-directory cap
//	node_modules/ [2,310 files]                ← pruned heavy directory
func (t *PathTree) Render(target int) ([]string, string) {
	all := func(*pathNode) bool { return true }
	// Every path costs at least one token, so past target paths the
	// lossless form cannot fit: skip rendering and counting it.
	if target <= 0 || t.count <= target {
		full := t.render(all, 0, 0)
		if target <= 0 || tokens.Count(strings.Join(full, "\n")) <= target {
			return full, ""
		}
	}

	start, _ := t.chain()
	expanded := map[*pathNode]bool{start: true}
	isExpanded := func(n *pathNode) bool { return expanded[n] }
	used := tokens.Count(strings.Join(t.render(isExpanded, capFiles, capDirs), "\n"))
	// Biggest directory first, among those whose parent is shown: the
	// budget goes where most of the paths are, and every directory left
	// folded still gets its counted line. Ties: shallower, then path order.
	var q expandQueue
	seq := 0
	push := func(n *pathNode, path []string, indent string) {
		_, dirCap := t.caps(n, capFiles, capDirs)
		for _, sub := range t.subdirs(n, path, dirCap) {
			if sub.pruned || sub.n.total == 0 && len(sub.n.dirs) == 0 {
				continue
			}
			heap.Push(&q, expandItem{sub.n, sub.path, sub.label, indent, seq})
			seq++
		}
	}
	_, chain := t.chain()
	indent := ""
	if len(chain) > 0 {
		indent = "  "
	}
	push(start, chain, indent)
	for q.Len() > 0 {
		it := heap.Pop(&q).(expandItem)
		if t.tryExpand(it.n, it.path, it.label, it.indent, target, &used) {
			expanded[it.n] = true
			push(it.n, it.path, it.indent+"  ")
		}
	}
	out := t.render(isExpanded, capFiles, capDirs)
	return out, fmt.Sprintf("large tree: some directories shown as counts, ≤%d %s and ≤%d subdirectories listed per directory", capFiles, t.leaves(2), capDirs)
}

// leaves returns the noun for n leaf paths: "file(s)" or "entry/entries".
func (t *PathTree) leaves(n int) string {
	if t.Entries {
		return plural(n, "entry", "entries")
	}
	return plural(n, "file", "files")
}

// countLeaves formats n leaf paths: "1 file", "2,310 entries".
func (t *PathTree) countLeaves(n int) string {
	return commaInt(n) + " " + t.leaves(n)
}

type expandItem struct {
	n      *pathNode
	path   []string
	label  string
	indent string
	seq    int
}

type expandQueue []expandItem

func (q expandQueue) Len() int { return len(q) }
func (q expandQueue) Less(i, j int) bool {
	a, b := q[i], q[j]
	if a.n.total != b.n.total {
		return a.n.total > b.n.total
	}
	if len(a.path) != len(b.path) {
		return len(a.path) < len(b.path)
	}
	return a.seq < b.seq
}
func (q expandQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *expandQueue) Push(x any)   { *q = append(*q, x.(expandItem)) }
func (q *expandQueue) Pop() any {
	old := *q
	it := old[len(old)-1]
	*q = old[:len(old)-1]
	return it
}

// tryExpand adds the cost of showing n's contents instead of its counted
// line to *used when that stays within target.
func (t *PathTree) tryExpand(n *pathNode, path []string, label, indent string, target int, used *int) bool {
	collapsed := t.collapsedLine(indent, label, n)
	body := []string{indent + label + "/"}
	t.renderNode(n, path, indent+"  ", func(x *pathNode) bool { return x == n }, capFiles, capDirs, &body)
	cost := tokens.Count(strings.Join(body, "\n")) - tokens.Count(collapsed)
	if *used+cost > target {
		return false
	}
	*used += cost
	return true
}

// chain returns the node rendering starts from and the directories every
// path shares (printed as one "a/b/c/" line).
func (t *PathTree) chain() (*pathNode, []string) {
	n := t.root
	var chain []string
	for len(n.files) == 0 && len(n.dirs) == 1 {
		for name, child := range n.dirs {
			chain = append(chain, name)
			n = child
		}
	}
	return n, chain
}

func (t *PathTree) render(expand func(*pathNode) bool, maxFiles, maxDirs int) []string {
	var out []string
	n, chain := t.chain()
	indent := ""
	if len(chain) > 0 {
		label := strings.Join(chain, "/")
		if label == "" {
			label = "/" // the tree is the filesystem root itself
		} else {
			label += "/"
		}
		out = append(out, label)
		indent = "  "
	}
	t.renderNode(n, chain, indent, expand, maxFiles, maxDirs, &out)
	return out
}

type subdir struct {
	n      *pathNode
	path   []string
	label  string
	pruned bool
}

// subdirs lists n's subdirectories in display order, with single-child
// chains collapsed into one label ("a/b/c"). Past maxDirs (0: no cap) the
// rest is left out; renderNode counts them.
func (t *PathTree) subdirs(n *pathNode, path []string, maxDirs int) []subdir {
	names := make([]string, 0, len(n.dirs))
	for name := range n.dirs {
		names = append(names, name)
	}
	sort.Strings(names)
	if maxDirs > 0 && len(names) > maxDirs {
		names = names[:maxDirs]
	}
	out := make([]subdir, 0, len(names))
	for _, name := range names {
		d := n.dirs[name]
		p := append(path[:len(path):len(path)], name)
		label := name
		for len(d.files) == 0 && len(d.dirs) == 1 && !t.pruned(lastSeg(label), p) {
			for sub, child := range d.dirs {
				label += "/" + sub
				p = append(p, sub)
				d = child
			}
		}
		out = append(out, subdir{d, p, label, t.pruned(lastSeg(label), p)})
	}
	return out
}

func (t *PathTree) collapsedLine(indent, label string, d *pathNode) string {
	if d.total == 0 {
		return fmt.Sprintf("%s%s/ [%s]", indent, label, countDirs(d.ndirs))
	}
	if d.hist == "" {
		// Render and tryExpand ask for the same directories many times.
		d.hist = extHistogram(d.allFiles(nil), 3)
	}
	return fmt.Sprintf("%s%s/ [%s: %s]", indent, label, t.countLeaves(d.total), d.hist)
}

// prunedLine folds a heavy directory to its counts.
func (t *PathTree) prunedLine(indent, label string, d *pathNode) string {
	if d.total == 0 {
		return fmt.Sprintf("%s%s/ [%s]", indent, label, countDirs(d.ndirs))
	}
	return fmt.Sprintf("%s%s/ [%s]", indent, label, t.countLeaves(d.total))
}

func countDirs(n int) string {
	if n == 1 {
		return "1 directory"
	}
	return commaInt(n) + " directories"
}

// caps returns the file and subdirectory caps for n (0: none).
func (t *PathTree) caps(n *pathNode, maxFiles, maxDirs int) (int, int) {
	if maxFiles > 0 {
		if start, _ := t.chain(); n == start {
			return capRootFiles, capRootDirs
		}
	}
	return maxFiles, maxDirs
}

func (t *PathTree) renderNode(n *pathNode, path []string, indent string, expand func(*pathNode) bool, maxFiles, maxDirs int, out *[]string) {
	fileCap, dirCap := t.caps(n, maxFiles, maxDirs)
	names := sortedKeys(n.files)
	shown := names
	if fileCap > 0 && len(names) > fileCap {
		shown = names[:fileCap]
	}
	*out = append(*out, wrapNames(indent, shown)...)
	if len(shown) < len(names) {
		rest := names[len(shown):]
		*out = append(*out, fmt.Sprintf("%s… +%s more %s: %s", indent, commaInt(len(rest)), t.leaves(len(rest)), extHistogram(rest, 4)))
	}
	subs := t.subdirs(n, path, dirCap)
	for _, sd := range subs {
		switch {
		case len(sd.n.files) == 0 && len(sd.n.dirs) == 0:
			*out = append(*out, indent+sd.label+"/") // listed, nothing inside
		case sd.pruned:
			*out = append(*out, t.prunedLine(indent, sd.label, sd.n))
		case expand(sd.n):
			*out = append(*out, indent+sd.label+"/")
			t.renderNode(sd.n, sd.path, indent+"  ", expand, maxFiles, maxDirs, out)
		default:
			*out = append(*out, t.collapsedLine(indent, sd.label, sd.n))
		}
	}
	if extra := len(n.dirs) - len(subs); extra > 0 {
		files := 0
		var all []string
		names := make([]string, 0, len(n.dirs))
		for name := range n.dirs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names[len(subs):] {
			files += n.dirs[name].total
			all = n.dirs[name].allFiles(all)
		}
		// Their names are cheap and are what the reader navigates by.
		if files == 0 {
			*out = append(*out, fmt.Sprintf("%s… +%s more %s:", indent, commaInt(extra), plural(extra, "directory", "directories")))
		} else {
			*out = append(*out, fmt.Sprintf("%s… +%s more %s [%s: %s]:", indent, commaInt(extra), plural(extra, "directory", "directories"), t.countLeaves(files), extHistogram(all, 3)))
		}
		dirNames := make([]string, 0, extra)
		for _, name := range names[len(subs):] {
			dirNames = append(dirNames, name+"/")
		}
		*out = append(*out, wrapNames(indent+"  ", dirNames)...)
	}
}

// pruned reports whether the heavy directory at path should be folded: it
// is heavy and no explicit root lies at or below it.
func (t *PathTree) pruned(name string, path []string) bool {
	if !heavyDirs[name] {
		return false
	}
	for _, r := range t.roots {
		if len(r) >= len(path) && equalSegs(r[:len(path)], path) {
			return false
		}
	}
	return true
}

func equalSegs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (n *pathNode) allFiles(acc []string) []string {
	for f := range n.files {
		acc = append(acc, f)
	}
	for _, d := range n.dirs {
		acc = d.allFiles(acc)
	}
	return acc
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// extHistogram summarizes file names by extension: ".ts×200 .json×22 …".
func extHistogram(names []string, top int) string {
	count := map[string]int{}
	for _, nm := range names {
		count[extOf(nm)]++
	}
	keys := make([]string, 0, len(count))
	for k := range count {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if count[keys[i]] != count[keys[j]] {
			return count[keys[i]] > count[keys[j]]
		}
		return keys[i] < keys[j]
	})
	var parts []string
	for i, k := range keys {
		if i == top {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, fmt.Sprintf("%s×%d", k, count[k]))
	}
	return strings.Join(parts, " ")
}

// extOf returns ".ext" for names with a short alphanumeric extension and
// "other" otherwise (Makefile, LICENSE, .gitignore).
func extOf(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i <= 0 || len(name)-i > 9 || i == len(name)-1 {
		return "other"
	}
	for _, ch := range name[i+1:] {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9') {
			return "other"
		}
	}
	return name[i:]
}

func lastSeg(label string) string {
	if i := strings.LastIndexByte(label, '/'); i >= 0 {
		return label[i+1:]
	}
	return label
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// wrapNames joins names with two spaces into lines of about pathLineWidth.
// A name that would be ambiguous there (holding two spaces or a tab,
// starting or ending with a space, or starting with a quote) is written as
// a quoted Go string.
func wrapNames(indent string, names []string) []string {
	out, _ := wrapNamesCounted(indent, names)
	return out
}

// wrapNamesCounted is wrapNames that also returns how many names each
// line holds.
func wrapNamesCounted(indent string, names []string) ([]string, []int) {
	var out []string
	var counts []int
	var b strings.Builder
	n := 0
	for _, nm := range names {
		if strings.Contains(nm, "  ") || nm != strings.TrimSpace(nm) || strings.HasPrefix(nm, `"`) || strings.ContainsRune(nm, '\t') {
			nm = strconv.Quote(nm)
		}
		if b.Len() > 0 && b.Len()+2+len(nm) > pathLineWidth {
			out = append(out, b.String())
			counts = append(counts, n)
			b.Reset()
			n = 0
		}
		if b.Len() == 0 {
			b.WriteString(indent)
		} else {
			b.WriteString("  ")
		}
		b.WriteString(nm)
		n++
	}
	if b.Len() > 0 {
		out = append(out, b.String())
		counts = append(counts, n)
	}
	return out, counts
}

// commaInt formats 2310 as "2,310".
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
