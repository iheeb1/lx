package search

import (
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/filters/fs"
)

type rgFiles struct{}

func (rgFiles) Name() string    { return "rg-files" }
func (rgFiles) IsContent() bool { return true }

func (rgFiles) Match(c *engine.Context) bool {
	e := fs.Effective(c)
	if engine.MachineReadable(e) || e.Name() != "rg" {
		return false
	}
	o := parseOpts("rg", "rg", e.Args())
	return o.files && !o.bail
}

func (f rgFiles) Apply(c *engine.Context, out string) (string, bool) {
	e := fs.Effective(c)
	if !f.Match(e) {
		return "", false
	}
	o := parseOpts("rg", "rg", e.Args())
	var notes, paths []string
	for _, ln := range strings.Split(out, "\n") {
		switch {
		case ln == "":
		case fs.NoteHasPrefix(e, ln, "rg"):
			notes = append(notes, ln)
		default:
			paths = append(paths, ln)
		}
	}

	if len(paths) == 0 || fs.Unexplained(e, len(notes)) {
		return "", false
	}

	t := fs.NewPathTree(paths, o.operands)
	lines, capped := t.Render(fs.DefaultTreeTarget)
	var b strings.Builder
	for _, n := range fs.CapNotes(notes) {
		b.WriteString(n + "\n")
	}
	head := plural(t.Len(), "file", "files")
	if capped != "" {
		head += " · " + capped
	}
	b.WriteString("[" + head + "]\n")
	b.WriteString(strings.Join(lines, "\n"))
	return b.String(), true
}
