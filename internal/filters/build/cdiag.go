package build

import (
	"github.com/iheeb1/lx/internal/lazyre"
	"path"
	"strings"
)

var (
	cLocDiagRe  = lazyre.New(`^([^\s:][^:]*?):(\d+):(?:(\d+):)? (fatal error|error|warning|note|remark): (.*)$`)
	cToolDiagRe = lazyre.New(`^([\w.+-]+): (fatal error|error|warning|note): (.*)$`)
	cBareDiagRe = lazyre.New(`^(fatal error|error|warning|note): (.*)$`)

	inclRe     = lazyre.New(`^In file included from \S.*:\d+(?::\d+)?[:,]$`)
	inclFromRe = lazyre.New(`^\s+from \S.*:\d+(?::\d+)?[:,]$`)

	cContextRe  = lazyre.New(`^[^\s:][^:]*?: (?:In (?:function|member function|static member function|constructor|destructor|copy constructor|instantiation of|substitution of|lambda function|lambda)|At (?:top level|global scope))\b.*:$`)
	cRequiredRe = lazyre.New(`^[^\s:][^:]*?:\d+:(?:\d+:)?\s{2,}(?:required |recursively required |in (?:constexpr )?expansion |in requirements )`)

	gutterRe = lazyre.New(`^\s*(?:\d+|\+\+\+)?\s*\|`)
	caretRe  = lazyre.New(`^\s*[~^][~^ ]*$`)

	tuSummaryRe = lazyre.New(`^\d+ (?:warnings?|errors?)(?: and \d+ (?:warnings?|errors?))? generated\.$`)

	quotedRe = lazyre.New(`'[^']*'|‘[^’]*’|"[^"]*"|` + "`[^`]*`")
)

type cdiag struct {
	prefix     []int
	head       int
	sev        string
	loc        string
	normLoc    string
	msg        string
	body       []int
	notes      []*cdiag
	headerOnly bool
	hidden     bool
}

func (d *cdiag) isError() bool { return d.sev == "error" || d.sev == "fatal error" }

func (d *cdiag) end() int {
	e := d.head + 1
	if len(d.body) > 0 {
		e = d.body[len(d.body)-1] + 1
	}
	if len(d.notes) > 0 {
		e = d.notes[len(d.notes)-1].end()
	}
	return e
}

func parseCHeader(ln string) (sev, loc, normLoc, msg string, ok bool) {
	if ln == "" || ln[0] == ' ' || ln[0] == '\t' || !hasSeverityWord(ln) {
		return "", "", "", "", false
	}
	if m := cLocDiagRe.FindStringSubmatch(ln); m != nil {
		loc = m[1] + ":" + m[2]
		norm := cleanPath(m[1]) + ":" + m[2]
		if m[3] != "" {
			loc += ":" + m[3]
			norm += ":" + m[3]
		}
		return m[4], loc, norm, m[5], true
	}
	if m := cToolDiagRe.FindStringSubmatch(ln); m != nil {
		return m[2], "", "", m[1] + ": " + m[3], true
	}
	if m := cBareDiagRe.FindStringSubmatch(ln); m != nil {
		return m[1], "", "", m[2], true
	}
	return "", "", "", "", false
}

func hasSeverityWord(ln string) bool {
	return strings.Contains(ln, "error: ") || strings.Contains(ln, "warning: ") ||
		strings.Contains(ln, "note: ") || strings.Contains(ln, "remark: ")
}

func cleanPath(p string) string {
	if strings.HasPrefix(p, "<") {
		return p
	}
	return path.Clean(p)
}

func isCPrefix(ln string, afterInclude bool) bool {
	switch {
	case ln == "":
		return false
	case strings.HasPrefix(ln, "In file included from "):
		return inclRe.MatchString(ln)
	case afterInclude && (ln[0] == ' ' || ln[0] == '\t'):
		return inclFromRe.MatchString(ln)
	case ln[0] == ' ' || ln[0] == '\t':
		return false
	}
	return strings.HasSuffix(ln, ":") && cContextRe.MatchString(ln) || (strings.Contains(ln, ":  ") || strings.Contains(ln, ":\t")) && cRequiredRe.MatchString(ln)
}

func parseCDiag(lines []string, i int) (d *cdiag, stop int, ok bool) {
	return parseCDiagAt(lines, i, true)
}

func parseCDiagAt(lines []string, i int, withNotes bool) (*cdiag, int, bool) {
	j := i
	var prefix []int
	afterIncl := false
	for j < len(lines) && isCPrefix(lines[j], afterIncl) {
		afterIncl = strings.HasPrefix(lines[j], "In file included from ") || afterIncl && lines[j] != "" && (lines[j][0] == ' ' || lines[j][0] == '\t')
		prefix = append(prefix, j)
		j++
	}
	if j >= len(lines) {
		return nil, j, false
	}
	sev, loc, norm, msg, ok := parseCHeader(lines[j])
	if !ok {
		return nil, j, false
	}
	d := &cdiag{prefix: prefix, head: j, sev: sev, loc: loc, normLoc: norm, msg: msg}
	j++
	d.body, j = parseExcerpt(lines, j)
	if d.sev == "note" || !withNotes {
		return d, j, true
	}

	for j < len(lines) {
		n, _, ok := parseCDiagAt(lines, j, false)
		if !ok || n.sev != "note" {
			break
		}
		d.notes = append(d.notes, n)
		j = n.end()
	}
	return d, j, true
}

func parseExcerpt(lines []string, j int) ([]int, int) {
	var body []int
	if j < len(lines) && gutterRe.MatchString(lines[j]) {
		for j < len(lines) && gutterRe.MatchString(lines[j]) {
			body = append(body, j)
			j++
		}
		return body, j
	}

	if j+1 < len(lines) && lines[j] != "" && caretRe.MatchString(lines[j+1]) {
		if _, _, _, _, hdr := parseCHeader(lines[j]); !hdr {
			return []int{j, j + 1}, j + 2
		}
	}
	return nil, j
}

func cTemplate(msg string) string {
	return quotedRe.ReplaceAllString(msg, "'…'")
}
