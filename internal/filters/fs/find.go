package fs

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type find struct{}

func (find) Name() string    { return "find" }
func (find) IsContent() bool { return true }

func (find) Match(c *engine.Context) bool {
	e := Effective(c)
	if engine.MachineReadable(e) {
		return false
	}
	switch e.Name() {
	case "find", "gfind":
		for _, a := range e.Args() {
			switch a {
			case "-exec", "-execdir", "-ok", "-okdir", "-delete", "-print0", "-printf",
				"-fprint", "-fprint0", "-fprintf", "-fls", "-ls":
				return false
			}
		}
		return true
	case "fd", "fdfind":
		for _, a := range e.Args() {
			switch a {
			case "-x", "--exec", "-X", "--exec-batch", "-l", "--list-details", "-0", "--print0",
				"--format", "-h", "--help", "-V", "--version", "--gen-completions":
				return false
			}
			if strings.HasPrefix(a, "--format=") || strings.HasPrefix(a, "--exec") {
				return false
			}
		}
		return true
	}
	return false
}

func (f find) Apply(c *engine.Context, out string) (string, bool) {
	e := Effective(c)
	if !f.Match(e) {
		return "", false
	}
	tool := e.Name()
	var notes, paths []string
	for _, ln := range strings.Split(out, "\n") {
		switch {
		case strings.TrimSpace(ln) == "":
		case NoteHasPrefix(e, ln, "find"),
			strings.HasPrefix(ln, "[fd error]"), strings.HasPrefix(ln, "[fd warning]"):
			notes = append(notes, ln)
		default:
			paths = append(paths, ln)
		}
	}

	if len(paths) == 0 || Unexplained(e, len(notes)) {
		return "", false
	}
	var roots []string
	dirsOnly, filesOnly := false, false
	if tool == "fd" || tool == "fdfind" {
		roots, dirsOnly, filesOnly = fdArgs(e.Args())
	} else {
		roots, dirsOnly = findArgs(e.Args())
		filesOnly = findSelects(e.Args(), "f")
	}
	if dirsOnly {
		for i, p := range paths {
			if !strings.HasSuffix(p, "/") {
				paths[i] = p + "/"
			}
		}
	}
	t := NewPathTree(paths, roots)
	if t.Len() == 0 {
		return "", false
	}

	t.Entries = !filesOnly && !dirsOnly
	lines, capped := t.Render(DefaultTreeTarget)
	var b strings.Builder
	for _, n := range CapNotes(notes) {
		b.WriteString(n + "\n")
	}
	head := commaInt(t.Len()) + " " + plural(t.Len(), "path", "paths")
	if t.Entries {

		head += " · names without / may be directories"
	}
	if capped != "" {
		head += " · " + capped
	}
	fmt.Fprintf(&b, "[%s]\n", head)
	b.WriteString(strings.Join(lines, "\n"))
	return strings.TrimRight(b.String(), "\n"), true
}

func findArgs(args []string) (roots []string, dirsOnly bool) {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-H" || a == "-L" || a == "-P" || a == "-E" || a == "-X" || a == "-d" || a == "-s" || a == "-x":
			i++
			continue
		case a == "-D" || a == "-f":
			if a == "-f" && i+1 < len(args) {
				roots = append(roots, args[i+1])
			}
			i += 2
			continue
		case strings.HasPrefix(a, "-O"):
			i++
			continue
		}
		break
	}
	for ; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") || a == "(" || a == "!" || a == "," || a == "\\(" {
			break
		}
		roots = append(roots, a)
	}
	return roots, findSelects(args[i:], "d")
}

func findSelects(args []string, typ string) bool {
	sel := false
	for j := 0; j+1 < len(args); j++ {
		if args[j] == "-type" && args[j+1] == typ && (j == 0 || args[j-1] != "!" && args[j-1] != "-not") {
			sel = true
		}
		if args[j] == "-o" || args[j] == "-or" || args[j] == "," {
			return false
		}
	}
	return sel
}

var fdValueFlags = map[string]bool{
	"-e": true, "--extension": true, "-t": true, "--type": true, "-d": true, "--max-depth": true,
	"--min-depth": true, "--exact-depth": true, "-E": true, "--exclude": true, "-S": true,
	"--size": true, "--changed-within": true, "--changed-before": true, "-o": true, "--owner": true,
	"-j": true, "--threads": true, "--max-results": true, "--base-directory": true,
	"--search-path": true, "--path-separator": true, "-c": true, "--color": true,
	"--ignore-file": true, "--batch-size": true, "--max-buffer-time": true,
}

func fdArgs(args []string) (roots []string, dirsOnly, filesOnly bool) {
	var pos []string
	types := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			pos = append(pos, args[i+1:]...)
			i = len(args)
		case fdValueFlags[a]:
			if i+1 < len(args) {
				v := args[i+1]
				if a == "--search-path" {
					roots = append(roots, v)
				}
				if a == "-t" || a == "--type" {
					types++
					dirsOnly = dirsOnly || v == "d" || v == "directory"
					filesOnly = filesOnly || v == "f" || v == "file"
				}
			}
			i++
		case strings.HasPrefix(a, "--type="):
			types++
			v := strings.TrimPrefix(a, "--type=")
			dirsOnly = dirsOnly || v == "d" || v == "directory"
			filesOnly = filesOnly || v == "f" || v == "file"
		case strings.HasPrefix(a, "--search-path="):
			roots = append(roots, strings.TrimPrefix(a, "--search-path="))
		case strings.HasPrefix(a, "-"):
		default:
			pos = append(pos, a)
		}
	}
	if types > 1 {
		dirsOnly, filesOnly = false, false
	}
	if len(pos) > 1 {
		roots = append(roots, pos[1:]...)
	}
	return roots, dirsOnly, filesOnly
}
