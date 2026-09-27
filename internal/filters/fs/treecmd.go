package fs

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// treeCmd re-renders `tree` output in the compact path-tree format: every
// directory once as "dir/", its files joined on the lines below, heavy
// directories folded to a counted line, and depth / per-directory caps when
// the listing is large. tree's own report line ("12 directories, 80 files")
// is kept verbatim, last; "[error opening dir]" entries are kept verbatim,
// first.
type treeCmd struct{}

func (treeCmd) Name() string    { return "tree" }
func (treeCmd) IsContent() bool { return true }

func (treeCmd) Match(c *engine.Context) bool {
	e := Effective(c)
	if engine.MachineReadable(e) || e.Name() != "tree" {
		return false
	}
	for _, a := range e.Args() {
		if strings.HasPrefix(a, "--") {
			switch {
			case a == "--du", a == "--help", a == "--version", a == "--inodes", a == "--device",
				strings.HasPrefix(a, "--charset"), a == "--fromfile", a == "--info", a == "--hyperlink":
				return false
			}
			continue
		}
		if strings.HasPrefix(a, "-") && strings.ContainsAny(a[1:], "JXHpugshDiQNqoRT") {
			// JSON/XML/HTML, metadata columns, no indentation, output file.
			return false
		}
	}
	return true
}

var treeReportRe = lazyre.New(`^\d+ director(?:y|ies)(?:, \d+ files?)?$`)

type treeEntry struct {
	depth int
	name  string
}

// treeUnit is one 4-rune indentation step; NBSP is normalized to a space.
func treeUnit(r []rune) (kind byte, ok bool) {
	if len(r) < 4 {
		return 0, false
	}
	u := string(r[:4])
	u = strings.ReplaceAll(u, " ", " ")
	switch u {
	case "│   ", "|   ", "    ":
		return 'c', true // continuation
	case "├── ", "└── ", "|-- ", "`-- ":
		return 'e', true // entry marker
	}
	return 0, false
}

func (t treeCmd) Apply(c *engine.Context, out string) (string, bool) {
	e := Effective(c)
	if !t.Match(e) {
		return "", false
	}
	fullPath := false
	dirsOnly := false
	var operands []string
	args := e.Args()
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			operands = append(operands, args[i+1:]...)
			i = len(args)
		case a == "--filelimit" || a == "--sort" || a == "--timefmt":
			i++
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-"):
			fullPath = fullPath || strings.Contains(a, "f")
			dirsOnly = dirsOnly || strings.Contains(a, "d")
			if last := a[len(a)-1]; len(a) >= 2 && strings.IndexByte("LPI", last) >= 0 {
				i++ // -L 2, -I 'node_modules|dist': the value is the next word
			}
		default:
			operands = append(operands, a)
		}
	}

	var notes, report []string
	var paths []string
	var stack []string // names of the current ancestors, stack[0] = root
	var entries []treeEntry
	root := ""
	roots := 0
	flush := func() bool {
		// Directories are the entries followed by a deeper one.
		for i, en := range entries {
			isDir := dirsOnly || i+1 < len(entries) && entries[i+1].depth > en.depth
			name := en.name
			if strings.HasSuffix(name, " [error opening dir]") {
				name = strings.TrimSuffix(name, " [error opening dir]")
				isDir = true
			}
			if en.depth > len(stack) {
				return false
			}
			stack = stack[:en.depth]
			seg := name
			if isDir {
				if i := strings.Index(seg, " -> "); i > 0 {
					seg = seg[:i] // followed symlink: its children live under the link name
				}
				seg = strings.TrimSuffix(seg, "/")
			}
			var p string
			if fullPath {
				p = seg
			} else {
				p = strings.Join(append(append([]string{}, stack...), seg), "/")
				if root != "" && root != "." {
					p = strings.TrimSuffix(root, "/") + "/" + p
				}
			}
			if isDir {
				p += "/"
			}
			paths = append(paths, p)
			stack = append(stack, seg)
		}
		entries = entries[:0]
		stack = stack[:0]
		return true
	}
	for _, ln := range strings.Split(out, "\n") {
		if ln == "" {
			continue
		}
		if treeReportRe.MatchString(ln) {
			report = append(report, ln)
			continue
		}
		r := []rune(ln)
		depth := 0
		pos := 0
		entry := false
		for {
			k, ok := treeUnit(r[pos:])
			if !ok {
				break
			}
			pos += 4
			depth++
			if k == 'e' {
				entry = true
				break
			}
		}
		if !entry {
			if pos > 0 {
				return "", false
			}
			// A root line starts a new tree.
			if !flush() {
				return "", false
			}
			roots++
			root = ln
			if strings.HasSuffix(ln, " [error opening dir]") {
				notes = append(notes, ln)
			}
			continue
		}
		if roots == 0 {
			return "", false
		}
		name := string(r[pos:])
		if strings.HasSuffix(name, "[error opening dir]") || strings.HasSuffix(name, "[recursive, not followed]") {
			notes = append(notes, ln)
		}
		entries = append(entries, treeEntry{depth: depth - 1, name: name})
	}
	if !flush() || len(paths) == 0 || len(report) > roots || roots > len(operands)+1 || Unexplained(e, len(notes)) {
		return "", false
	}
	pt := NewPathTree(paths, operands)
	// tree marks no directory (unless -F), so a directory is known only
	// by the entries below it. A leaf directory (empty, past -L, not
	// followed) would read as a file: compare with tree's own count and,
	// when some are unaccounted for, say so and count "entries".
	var hdr []string
	if !dirsOnly {
		inferred := 0
		for _, p := range paths {
			if strings.HasSuffix(p, "/") {
				inferred++
			}
		}
		switch n := reportDirs(report); {
		case n < 0:
			pt.Entries = true
			hdr = append(hdr, "names without / may be directories (tree -F marks them)")
		case n > inferred:
			pt.Entries = true
			hdr = append(hdr, fmt.Sprintf("%s of the names without / %s (tree -F marks them)",
				commaInt(n-inferred), plural(n-inferred, "is a directory", "are directories")))
		case n < inferred:
			pt.Entries = true
		}
	}
	lines, capped := pt.Render(DefaultTreeTarget)
	var b strings.Builder
	for _, n := range CapNotes(notes) {
		b.WriteString(n + "\n")
	}
	if capped != "" {
		hdr = append(hdr, capped)
	}
	if len(hdr) > 0 {
		fmt.Fprintf(&b, "[tree: %s]\n", strings.Join(hdr, " · "))
	}
	b.WriteString(strings.Join(lines, "\n"))
	for _, rp := range report {
		b.WriteString("\n" + rp)
	}
	return b.String(), true
}

// reportDirs returns the directory count of tree's last report line, or -1
// without one (--noreport).
func reportDirs(report []string) int {
	if len(report) == 0 {
		return -1
	}
	rp := report[len(report)-1]
	n := 0
	for i := 0; i < len(rp) && rp[i] >= '0' && rp[i] <= '9'; i++ {
		n = n*10 + int(rp[i]-'0')
		if n > 1<<40 {
			return -1
		}
	}
	return n
}
