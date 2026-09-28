package engine

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

var heavyDirs = map[string]bool{
	"node_modules": true, ".git": true, "dist": true, "build": true, "target": true,
	".venv": true, "venv": true, "__pycache__": true, ".next": true, "coverage": true,
	"vendor": true,
}

const (
	pathLineWidth = 160

	minNumberedGroup = 6

	minHeavyPrune = 20
)

type pathNode struct {
	files map[string]bool
	dirs  map[string]*pathNode
	total int
}

func newPathNode() *pathNode {
	return &pathNode{files: map[string]bool{}, dirs: map[string]*pathNode{}}
}

func FactorPaths(paths []string) []string {
	root := newPathNode()
	for _, p := range paths {
		p = strings.TrimSpace(p)
		isDir := strings.HasSuffix(p, "/")
		for strings.HasPrefix(p, "./") {
			p = p[2:]
		}
		abs := strings.HasPrefix(p, "/")
		p = strings.Trim(p, "/")
		if p == "" || p == "." {
			continue
		}
		segs := strings.Split(p, "/")
		if abs {
			segs = append([]string{""}, segs...)
		}
		n := root
		for i, s := range segs {
			last := i == len(segs)-1
			if last && !isDir {
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
	root.normalize()

	var out []string

	n, indent := root, ""
	var chain []string
	for len(n.files) == 0 && len(n.dirs) == 1 {
		for name, child := range n.dirs {
			chain = append(chain, name)
			n = child
		}
	}
	if len(chain) > 0 {
		out = append(out, strings.Join(chain, "/")+"/")
		indent = "  "
	}
	n.render(indent, &out)
	return out
}

func (n *pathNode) normalize() int {
	for name := range n.dirs {
		delete(n.files, name)
	}
	n.total = len(n.files)
	for _, d := range n.dirs {
		n.total += d.normalize()
	}
	return n.total
}

func (n *pathNode) render(indent string, out *[]string) {
	*out = append(*out, wrapNames(indent, groupNumbered(sortedKeys(n.files)))...)
	dirs := make([]string, 0, len(n.dirs))
	for name := range n.dirs {
		dirs = append(dirs, name)
	}
	sort.Strings(dirs)
	for _, name := range dirs {
		d := n.dirs[name]
		label := name
		for !(heavyDirs[lastSeg(label)] && d.total > minHeavyPrune) && len(d.files) == 0 && len(d.dirs) == 1 {
			for sub, child := range d.dirs {
				label += "/" + sub
				d = child
			}
		}
		if heavyDirs[lastSeg(label)] && d.total > minHeavyPrune {
			unit := "files"
			if d.total == 1 {
				unit = "file"
			}
			*out = append(*out, fmt.Sprintf("%s%s/ [%s %s]", indent, label, commaInt(d.total), unit))
			continue
		}
		*out = append(*out, indent+label+"/")
		d.render(indent+"  ", out)
	}
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

var nameVarRe = lazyre.New(`[0-9a-fA-F]{7,}|\d+`)

func maskName(name string) string {
	return nameVarRe.ReplaceAllStringFunc(name, func(m string) string {
		switch {
		case isAllDigits(m):
			return "<N>"
		case hasDigit(m):
			return "<H>"
		}
		return m
	})
}

func groupNumbered(names []string) []string {
	if len(names) < minNumberedGroup {
		return names
	}
	masks := make([]string, len(names))
	count := map[string]int{}
	for i, nm := range names {
		masks[i] = maskName(nm)
		if masks[i] != nm {
			count[masks[i]]++
		}
	}
	out := make([]string, 0, len(names))
	done := map[string]bool{}
	for i, nm := range names {
		m := masks[i]
		if count[m] < minNumberedGroup {
			out = append(out, nm)
			continue
		}
		if !done[m] {
			done[m] = true
			var members []string
			for k, x := range names {
				if masks[k] == m {
					members = append(members, x)
				}
			}
			if b, ok := braceGroup(m, members); ok {
				out = append(out, b)
			} else {
				out = append(out, fmt.Sprintf("%s ×%d%s", m, count[m], numberRange(names, masks, m)))
			}
		}
	}
	return out
}

func numberRange(names, masks []string, m string) string {
	if strings.Count(m, "<N>") != 1 || strings.Contains(m, "<H>") {
		return ""
	}
	lo, hi := -1, -1
	var loS, hiS string
	for i, nm := range names {
		if masks[i] != m {
			continue
		}
		d := nameVarRe.FindString(nm)
		for _, x := range nameVarRe.FindAllString(nm, -1) {
			if isAllDigits(x) {
				d = x
			}
		}
		v, err := strconv.Atoi(d)
		if err != nil {
			return ""
		}
		if lo < 0 || v < lo {
			lo, loS = v, d
		}
		if v > hi {
			hi, hiS = v, d
		}
	}
	return fmt.Sprintf(" (%s–%s)", loS, hiS)
}

func wrapNames(indent string, names []string) []string {
	var out []string
	var b strings.Builder
	for _, nm := range names {
		if b.Len() > 0 && b.Len()+2+len(nm) > pathLineWidth {
			out = append(out, b.String())
			b.Reset()
		}
		if b.Len() == 0 {
			b.WriteString(indent)
		} else {
			b.WriteString("  ")
		}
		b.WriteString(nm)
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
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

var extRe = lazyre.New(`\.[A-Za-z][A-Za-z0-9]{0,7}$`)

func isPathLike(line string) bool {
	if line == "" || len(line) > 1024 || line != strings.TrimSpace(line) {
		return false
	}
	if strings.ContainsAny(line, "\t:\"<>|*?;`\\") || strings.Contains(line, "  ") || strings.Contains(line, " -> ") {
		return false
	}
	alnum := false
	for _, r := range line {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			alnum = true
			break
		}
	}
	if !alnum {
		return false
	}
	if sp := strings.IndexByte(line, ' '); sp >= 0 {
		if strings.IndexByte(line[sp:], '/') >= 0 {
			return true
		}
		return extRe.MatchString(path.Base(line))
	}
	return true
}

func LooksLikePathList(lines []string) bool {
	nonEmpty, like, slash, spaced := 0, 0, 0, 0
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		nonEmpty++
		if isPathLike(ln) {
			like++
			if strings.Contains(ln, "/") {
				slash++
			}
			if strings.Contains(ln, " ") {
				spaced++
			}
		}
	}

	return nonEmpty >= 4 && like*10 >= nonEmpty*8 && slash*2 >= like && spaced*5 <= like
}

func lsRecursivePaths(lines []string) ([]string, bool) {
	type block struct {
		dir   string
		names []string
	}
	var blocks []*block
	cur := &block{}
	blocks = append(blocks, cur)
	headers, names, bad := 0, 0, 0
	prevBlank := true
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			prevBlank = true
			continue
		}
		if prevBlank && strings.HasSuffix(ln, ":") && isPathLike(strings.TrimSuffix(ln, ":")) {
			cur = &block{dir: strings.TrimSuffix(ln, ":")}
			blocks = append(blocks, cur)
			headers++
			prevBlank = false
			continue
		}
		prevBlank = false
		if isPathLike(ln) && !strings.Contains(ln, "/") {
			cur.names = append(cur.names, ln)
			names++
		} else {
			bad++
		}
	}
	if headers == 0 || names < 4 || bad*20 > names {
		return nil, false
	}

	if first := blocks[0]; len(first.names) > 0 {
		dir := ""
		for _, b := range blocks[1:] {
			if slices.Contains(first.names, path.Base(b.dir)) {
				dir = path.Dir(b.dir)
				break
			}
		}
		if dir != "." {
			first.dir = dir
		}
	}
	var out []string
	for _, b := range blocks {
		for _, nm := range b.names {
			if b.dir == "" {
				out = append(out, nm)
			} else {
				out = append(out, strings.TrimSuffix(b.dir, "/")+"/"+nm)
			}
		}
	}
	return out, true
}

func braceGroup(mask string, members []string) (string, bool) {
	if strings.Count(mask, "<N>") != 1 || strings.Contains(mask, "<H>") {
		return "", false
	}
	pre, suf, _ := strings.Cut(mask, "<N>")
	type num struct {
		s string
		v int
	}
	var nums []num
	width := -1
	for _, m := range members {
		if !strings.HasPrefix(m, pre) || !strings.HasSuffix(m, suf) || len(m) < len(pre)+len(suf) {
			return "", false
		}
		d := m[len(pre) : len(m)-len(suf)]
		v, err := strconv.Atoi(d)
		if err != nil || !isAllDigits(d) {
			return "", false
		}
		if len(d) > 1 && d[0] == '0' {
			if width >= 0 && width != len(d) {
				return "", false
			}
			width = len(d)
		}
		nums = append(nums, num{d, v})
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i].v < nums[j].v })
	contiguous := true
	for i := 1; i < len(nums); i++ {
		if nums[i].v != nums[i-1].v+1 {
			contiguous = false
			break
		}
	}
	if contiguous && width >= 0 {
		for _, n := range nums {
			if len(n.s) != width {
				contiguous = false
			}
		}
	}
	if contiguous && len(nums) > 2 {
		return fmt.Sprintf("%s{%s..%s}%s", pre, nums[0].s, nums[len(nums)-1].s, suf), true
	}
	if len(nums) <= 12 {
		vals := make([]string, len(nums))
		for i, n := range nums {
			vals[i] = n.s
		}
		return fmt.Sprintf("%s{%s}%s", pre, strings.Join(vals, ","), suf), true
	}
	return "", false
}
