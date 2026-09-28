package data

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type fileFilter struct{}

func (fileFilter) Name() string    { return "cat" }
func (fileFilter) IsContent() bool { return true }

func (fileFilter) Match(c *engine.Context) bool {
	e := effective(c)
	switch e.Name() {
	case "cat", "bat", "batcat", "head", "tail":
		return true
	}
	return false
}

func (fileFilter) Stream(c *engine.Context) bool {
	e := effective(c)
	return e.Name() == "tail" && isFollow(e.Args())
}

const lockfileBudget = 2000

var (
	fileDiagRe = lazyre.New(`^(?:cat|head|tail|bat|batcat|gcat|ghead|gtail): |^\[bat (?:error|warning)\]: `)

	multiHeaderRe = lazyre.New(`^==> .+ <==$`)

	fileDiagGluedRe = lazyre.New(`(?:cat|head|tail|gcat|ghead|gtail): .*(?:No such file or directory|Is a directory|Permission denied|Input/output error|Operation not permitted)$|\[bat error\]: .+$`)
)

func keepFileLine(ln string) bool {
	if ln == "" {
		return false
	}
	switch ln[0] {
	case 'c', 'h', 't', 'b', 'g', '[':
		if fileDiagRe.MatchString(ln) {
			return true
		}
	case '=':
		if multiHeaderRe.MatchString(ln) {
			return true
		}
	}
	return isConflictMarker(ln) || mayHaveFileDiag(ln) && fileDiagGluedRe.MatchString(ln)
}

func mayHaveFileDiag(ln string) bool {
	return strings.Contains(ln, "cat: ") || strings.Contains(ln, "head: ") || strings.Contains(ln, "tail: ") ||
		strings.Contains(ln, "[bat error]: ")
}

type fileArgs struct {
	operands []string

	sameLines bool

	slice bool
}

var fileValueFlags = map[string]map[string]bool{
	"head": {"-n": true, "-c": true, "--lines": true, "--bytes": true},
	"tail": {"-n": true, "-c": true, "--lines": true, "--bytes": true, "-s": true, "--sleep-interval": true, "--pid": true, "--max-unchanged-stats": true},
	"bat": {"-l": true, "--language": true, "-H": true, "--highlight-line": true, "--file-name": true, "--diff-context": true,
		"--tabs": true, "--wrap": true, "--terminal-width": true, "--color": true, "--italic-text": true, "--decorations": true,
		"--paging": true, "-m": true, "--map-syntax": true, "--ignored-suffix": true, "--theme": true, "--style": true,
		"-r": true, "--line-range": true, "--pager": true},
}

func parseFileArgs(e *engine.Context) fileArgs {
	name := e.Name()
	if name == "batcat" {
		name = "bat"
	}
	vf := fileValueFlags[name]
	var fa fileArgs
	numbered := false
	args := e.Args()
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			fa.operands = append(fa.operands, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && a != "-" {
			flag, _, hasVal := strings.Cut(a, "=")
			if vf[flag] && !hasVal {
				i++
			}
			switch {
			case name == "cat" && strings.ContainsAny(strings.TrimLeft(a, "-"), "nbsvetAET") && !strings.HasPrefix(a, "--"):
				numbered = true
			case name == "cat" && strings.HasPrefix(a, "--"):
				numbered = true
			case flag == "-r" || flag == "--line-range":
				fa.slice = true
			}
			continue
		}
		fa.operands = append(fa.operands, a)
	}
	switch name {
	case "head", "tail":
		fa.slice = true
	}
	fa.sameLines = len(fa.operands) == 1 && fa.operands[0] != "-" && !numbered &&
		(name == "cat" || name == "bat" || name == "head")
	if name == "bat" && fa.slice {
		fa.sameLines = false
	}
	return fa
}

func (fileFilter) Apply(c *engine.Context, out string) (string, bool) {
	e, renumbered := unwrap(c)
	fa := parseFileArgs(e)
	if renumbered {
		fa.sameLines, fa.slice = false, true
	}
	path := ""
	if len(fa.operands) == 1 && fa.operands[0] != "-" {
		path = fa.operands[0]
	}
	lines := strings.Split(out, "\n")

	conflicted := hasConflict(lines)

	if looksBinary(out) {
		var b []string
		for _, ln := range lines {
			if !strings.ContainsRune(ln, 0) && fileDiagRe.MatchString(ln) {
				b = append(b, ln)
			}
		}
		b = append(b, fmt.Sprintf("[lx: binary data (%s, not UTF-8 text) not shown]", humanBytes(len(out))))
		return joinLines(b), true
	}

	total := countTokens(out)
	base := filepath.Base(path)
	if path != "" && !fa.slice && total > lockfileBudget && !conflicted {
		if kind := lockfileKind(base); kind != "" {
			if s, ok := summarizeLockfile(kind, path, out); ok {
				return s, true
			}
		}
	}
	if total <= fileBudget {
		return out, true
	}
	if !fa.slice && isJSONPath(base, out) {
		if j, ok := compactJSON(out, fileBudget); ok {
			hdr := fmt.Sprintf("[lx: JSON condensed from ~%s tokens: tables for arrays of objects, long arrays cut; not valid JSON — read exact values with jq]", humanTokens(total))
			return hdr + "\n" + j, true
		}
	}
	ext := commonExt(fa.operands)
	switch ext {
	case ".log", ".out", ".txt", "":
		if conflicted {
			break
		}
		if l, ok := engine.TemplateLogs(lines); ok {
			if s := joinLines(l); countTokens(s) <= fileBudget {
				return s, true
			}
		}
	}
	wpath := ""
	if fa.sameLines {
		wpath = path
	}
	w := defaultWindow(wpath, ext, keepFileLine)
	if conflicted {
		w.keepIdx = conflictHunks(lines)
	}
	cut := w.apply(lines)
	what := e.Name() + " output"
	if fa.sameLines && !fa.slice {
		what = path
	}
	how := "first and last lines shown exactly; the omitted range, its outline and how to read it are marked below"
	if !hasOmission(cut) {
		how = "lines over 2000 characters shortened in place (…[+N chars]…)"
	}
	if conflicted {
		how += "; it has git merge-conflict markers: each is listed with its line number, and small conflict hunks in full"
	}
	hdr := fmt.Sprintf("[lx: %s — %s, ~%s tokens: %s]", what, pluralInt(len(lines), "line", "lines"), humanTokens(total), how)
	return hdr + "\n" + joinLines(cut), true
}

const (
	maxHunk = 60

	maxHunkLines = 400
)

func conflictHunks(lines []string) map[int]bool {
	idx := map[int]bool{}
	for i := 0; i < len(lines) && len(idx) < maxHunkLines; i++ {
		if !strings.HasPrefix(lines[i], "<<<<<<<") || !isConflictMarker(lines[i]) {
			continue
		}
		for j := i + 1; j < len(lines) && j-i < maxHunk; j++ {
			if strings.HasPrefix(lines[j], ">>>>>>>") && isConflictMarker(lines[j]) {
				if len(idx)+j-i+3 <= maxHunkLines {
					for k := max(i-2, 0); k <= j; k++ {
						idx[k] = true
					}
				}
				i = j
				break
			}
		}
	}
	return idx
}

func hasOmission(cut []string) bool {
	for _, ln := range cut {
		if strings.HasPrefix(ln, "… lines ") && strings.Contains(ln, " omitted (") {
			return true
		}
	}
	return false
}

func commonExt(operands []string) string {
	ext := ""
	for i, op := range operands {
		e := langOf(op)
		if i > 0 && e != ext {
			return ""
		}
		ext = e
	}
	return ext
}

func isJSONPath(base, out string) bool {
	switch strings.ToLower(filepath.Ext(base)) {
	case ".json", ".geojson", ".har", ".ipynb", ".webmanifest", ".jsonl", ".ndjson":
		return true
	case "":
		t := strings.TrimSpace(out)
		return t != "" && (t[0] == '{' || t[0] == '[')
	}
	return false
}
