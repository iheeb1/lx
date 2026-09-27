package build

import (
	"github.com/iheeb1/lx/internal/lazyre"
	"path"
	"strings"
)

// C-family (gcc, clang, and tools that print their format) diagnostics.
//
// A diagnostic block is
//
//	[prefix]  "In file included from a.c:1:" chain (clang), or gcc's
//	          "In file included from a.h:3,\n                 from a.c:1:",
//	          "a.c: In function 'main':", "a.cpp:9:13:   required from here"
//	header    "file:line[:col]: (fatal error|error|warning|note|remark): msg",
//	          "tool: error: msg" (clang: error: …, collect2: error: …) or a
//	          bare "warning: msg" (clang driver)
//	[body]    source excerpt: gutter lines ("  605 |     int x;" /
//	          "      |     ^~~") or the older "source line" + "caret line"
//	[notes]   note blocks that directly follow, each with its own prefix/body
var (
	cLocDiagRe  = lazyre.New(`^([^\s:][^:]*?):(\d+):(?:(\d+):)? (fatal error|error|warning|note|remark): (.*)$`)
	cToolDiagRe = lazyre.New(`^([\w.+-]+): (fatal error|error|warning|note): (.*)$`)
	cBareDiagRe = lazyre.New(`^(fatal error|error|warning|note): (.*)$`)

	inclRe     = lazyre.New(`^In file included from \S.*:\d+(?::\d+)?[:,]$`)
	inclFromRe = lazyre.New(`^\s+from \S.*:\d+(?::\d+)?[:,]$`)
	// gcc context lines printed before a diagnostic.
	cContextRe  = lazyre.New(`^[^\s:][^:]*?: (?:In (?:function|member function|static member function|constructor|destructor|copy constructor|instantiation of|substitution of|lambda function|lambda)|At (?:top level|global scope))\b.*:$`)
	cRequiredRe = lazyre.New(`^[^\s:][^:]*?:\d+:(?:\d+:)?\s{2,}(?:required |recursively required |in (?:constexpr )?expansion |in requirements )`)

	// Source excerpt lines: gcc ≥ 9 / clang ≥ 16 gutter, gcc fix-it "+++ |+".
	gutterRe = lazyre.New(`^\s*(?:\d+|\+\+\+)?\s*\|`)
	caretRe  = lazyre.New(`^\s*[~^][~^ ]*$`)

	// clang's per-translation-unit summary.
	tuSummaryRe = lazyre.New(`^\d+ (?:warnings?|errors?)(?: and \d+ (?:warnings?|errors?))? generated\.$`)

	// quotedRe masks quoted names so "unused variable 'a'" and "… 'b'" share a template.
	quotedRe = lazyre.New(`'[^']*'|‘[^’]*’|"[^"]*"|` + "`[^`]*`")
)

type cdiag struct {
	prefix     []int // include chain / context line indexes
	head       int
	sev        string // "fatal error", "error", "warning", "note", "remark"
	loc        string // location as printed ("src/a.c:12:5"); "" for tool/driver diagnostics
	normLoc    string // loc with the path cleaned (src/../lib/x.h → lib/x.h)
	msg        string
	body       []int // excerpt line indexes
	notes      []*cdiag
	headerOnly bool // rendered as the header line only
	hidden     bool // (notes) repeated note, not rendered
}

func (d *cdiag) isError() bool { return d.sev == "error" || d.sev == "fatal error" }

// end is the index after the block's last line.
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

// parseCHeader recognizes a diagnostic header line.
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

// hasSeverityWord is a cheap necessary condition for the header regexes:
// each requires "error: ", "warning: ", "note: " or "remark: ".
func hasSeverityWord(ln string) bool {
	return strings.Contains(ln, "error: ") || strings.Contains(ln, "warning: ") ||
		strings.Contains(ln, "note: ") || strings.Contains(ln, "remark: ")
}

func cleanPath(p string) string {
	if strings.HasPrefix(p, "<") { // <command line>, <built-in>
		return p
	}
	return path.Clean(p)
}

// isCPrefix reports include-chain and gcc context lines. from reports gcc's
// "                 from b.c:1:" continuation, valid only after an include line.
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

// parseCDiag parses the diagnostic block starting at lines[i] (prefix
// included) with the notes that follow it. It returns ok=false when
// lines[i] does not start one; stop is then the last index known not to
// start one either (see parseCDiagAt).
func parseCDiag(lines []string, i int) (d *cdiag, stop int, ok bool) {
	return parseCDiagAt(lines, i, true)
}

// parseCDiagAt is parseCDiag; withNotes=false parses one block only (used to
// probe for a note without walking every diagnostic that follows).
//
// On failure, stop is where the prefix scan ended: no diagnostic starts at
// any index in [i, stop] either (a scan from inside the prefix run ends at
// the same header candidate, or earlier on a "from" continuation line, which
// is never a header). Callers skip that range, which keeps parsing linear
// on long runs of include-chain or context lines without a header.
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
	// Notes that directly follow belong to this diagnostic.
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

// parseExcerpt consumes the source excerpt lines starting at j.
func parseExcerpt(lines []string, j int) ([]int, int) {
	var body []int
	if j < len(lines) && gutterRe.MatchString(lines[j]) {
		for j < len(lines) && gutterRe.MatchString(lines[j]) {
			body = append(body, j)
			j++
		}
		return body, j
	}
	// Older format: the source line, then a caret line.
	if j+1 < len(lines) && lines[j] != "" && caretRe.MatchString(lines[j+1]) {
		if _, _, _, _, hdr := parseCHeader(lines[j]); !hdr {
			return []int{j, j + 1}, j + 2
		}
	}
	return nil, j
}

// cTemplate is the grouping key of a message: quoted names masked.
func cTemplate(msg string) string {
	return quotedRe.ReplaceAllString(msg, "'…'")
}
