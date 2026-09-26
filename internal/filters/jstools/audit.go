package jstools

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// npmAudit condenses `npm audit` (and the report printed by `npm audit fix`).
// Per advisory it keeps the package and range line, the severity, every
// distinct advisory title (the GitHub advisory URL shortened to its
// GHSA id; npm repeats a title once per affected copy), the fix line and
// "Will install …" note, and the dependents ("Depends on vulnerable
// versions of …"). An install path is dropped when it is the only one and
// just node_modules/<package>; beyond 3 paths the rest are counted. The
// "N vulnerabilities (…)" summary and the fix commands are verbatim.
//
// pnpm and yarn print one box-drawn table per advisory (yarn: per advisory
// and dependency path). Each becomes
//
//	high  body-parser  Patched in: >=1.20.3 · Dependency of: express
//	  body-parser vulnerable to denial of service … - https://www.npmjs.com/advisories/1099520
//	  Path: express > body-parser
//
// with tables for the same advisory merged (their paths listed together,
// at most 5 and a count). Summary lines are verbatim.
//
// npm audit is Content: advisory titles ("Uncaught crash…", "…crashes with
// TypeError…") are data, not errors of this run. npm error lines are kept.
type npmAudit struct{}

func (npmAudit) Name() string { return "npm-audit" }

func (npmAudit) Match(c *engine.Context) bool {
	pm, sub, rest, ok := manager(c)
	if !ok || sub != "audit" || pm == "bun" {
		return false
	}
	if len(rest) > 0 && rest[0] == "signatures" {
		return false
	}
	return !hasArg(rest, "--json", "--parseable", "--help", "-h")
}

func (npmAudit) IsContent() bool { return true }

var (
	auditHeadRe    = regexp.MustCompile(`^(\S+)  (\S.*)$`)
	auditDepHeadRe = regexp.MustCompile(`^  (\S+)  (\S.*)$`)
	auditSevRe     = regexp.MustCompile(`^Severity: \w+$`)
	auditGHSARe    = regexp.MustCompile(` - https://github\.com/advisories/(GHSA-[\w-]+)$`)
	auditFixLineRe = regexp.MustCompile("^(?:fix available via `.+`|No fix available|Will install .+, which is .+)$")
	auditPathRe    = regexp.MustCompile(`^(\s*)(\S*node_modules/\S+)$`)
	auditDependsRe = regexp.MustCompile(`^  Depends on vulnerable versions of \S+$`)
	auditSumRe     = regexp.MustCompile(`^(?:\d+ (?:\w+ severity )?vulnerabilit(?:y|ies)|found \d+ vulnerabilit)`)
	auditOtherRe   = regexp.MustCompile(`^(?:No known vulnerabilities found|yarn audit v\d|\d+ vulnerabilities found)`)
)

func (npmAudit) Apply(c *engine.Context, s string) (string, bool) {
	lines := strings.Split(s, "\n")
	if strings.Contains(s, "┌") {
		if r, ok := boxAudit(lines); ok {
			return r, true
		}
	}
	start := -1
	recognized := false
	for i, ln := range lines {
		if ln == "# npm audit report" {
			start = i
			break
		}
		if auditSumRe.MatchString(ln) || installMarkRe.MatchString(ln) || auditOtherRe.MatchString(ln) {
			recognized = true
		}
	}
	if start < 0 && !recognized {
		return "", false
	}
	if start < 0 {
		// No report: "found 0 vulnerabilities", errors, or audit fix
		// output without remaining issues.
		var o out
		installLines(lines, &o)
		return o.String(), true
	}
	var o out
	installLines(lines[:start], &o)
	o.add(lines[start])
	i := start + 1
	for i < len(lines) {
		ln := lines[i]
		if ln == "" {
			o.blank()
			i++
			continue
		}
		if auditSumRe.MatchString(ln) {
			break
		}
		if m := auditHeadRe.FindStringSubmatch(ln); m != nil && !strings.HasPrefix(ln, "npm ") {
			i = auditBlock(lines, i, &o)
			continue
		}
		o.add(ln)
		i++
	}
	// Summary and fix commands, verbatim.
	o.add(lines[i:]...)
	return o.String(), true
}

// auditBlock renders the advisory block starting at lines[i] and returns
// the index after it.
func auditBlock(lines []string, i int, o *out) int {
	pkg := auditHeadRe.FindStringSubmatch(lines[i])[1]
	o.add(lines[i])
	seen := map[string]bool{}
	var paths []string
	flushPaths := func(owner, indent string) {
		switch {
		case len(paths) == 1 && paths[0] == "node_modules/"+owner:
			// The package's default location says nothing.
		case len(paths) > 3:
			o.add(indent+paths[0], indent+paths[1])
			o.add(fmt.Sprintf("%s[+%d more paths]", indent, len(paths)-2))
		default:
			for _, p := range paths {
				o.add(indent + p)
			}
		}
		paths = paths[:0]
	}
	owner, indent := pkg, ""
	j := i + 1
	for ; j < len(lines) && lines[j] != ""; j++ {
		ln := lines[j]
		if m := auditPathRe.FindStringSubmatch(ln); m != nil {
			paths = append(paths, m[2])
			indent = m[1]
			continue
		}
		flushPaths(owner, indent)
		switch {
		case auditSevRe.MatchString(ln), auditFixLineRe.MatchString(ln), auditDependsRe.MatchString(ln):
			o.add(ln)
		case auditDepHeadRe.MatchString(ln):
			owner = auditDepHeadRe.FindStringSubmatch(ln)[1]
			o.add(ln)
		default:
			// An advisory title: once per block, URL shortened.
			if seen[ln] {
				continue
			}
			seen[ln] = true
			o.add(auditGHSARe.ReplaceAllString(ln, " - $1"))
		}
	}
	flushPaths(owner, indent)
	return j
}

// advisory is one pnpm/yarn advisory table.
type advisory struct {
	sev, title, pkg, info string
	fields                []string            // "Key: value", in table order
	multi                 map[string][]string // merged per-path fields
	multiOrder            []string
	multiSeen             map[string]bool
}

// merge adds the values of a per-path field, once each.
func (a *advisory) merge(k string, vals []string) {
	if _, ok := a.multi[k]; !ok {
		a.multiOrder = append(a.multiOrder, k)
	}
	for _, v := range vals {
		if !a.multiSeen[k+"\x00"+v] {
			a.multiSeen[k+"\x00"+v] = true
			a.multi[k] = append(a.multi[k], v)
		}
	}
}

// boxAuditMerged are fields that differ between the tables of one advisory.
var boxAuditMerged = map[string]bool{"Path": true, "Paths": true, "Dependency of": true}

const maxAuditPaths = 5

// boxAudit renders pnpm/yarn audit tables. ok is false when a table does
// not have the advisory shape (severity and title, then key/value rows).
func boxAudit(lines []string) (string, bool) {
	var (
		o      out
		advs   []*advisory
		byKey  = map[string]*advisory{}
		tables int
	)
	flush := func() {
		for _, a := range advs {
			head := a.sev + "  " + a.pkg
			if len(a.fields) > 0 {
				head += "  " + strings.Join(a.fields, " · ")
			}
			o.add(head)
			t := "  " + a.title
			if a.info != "" {
				t += " - " + strings.TrimPrefix(a.info, "https://github.com/advisories/")
			}
			o.add(t)
			for _, k := range a.multiOrder {
				v := a.multi[k]
				s := "  " + k + ": " + strings.Join(v[:min(len(v), maxAuditPaths)], ", ")
				if len(v) > maxAuditPaths {
					s += fmt.Sprintf(" … +%d more", len(v)-maxAuditPaths)
				}
				o.add(s)
			}
		}
		advs = nil
		byKey = map[string]*advisory{}
	}
	for i := 0; i < len(lines); {
		rows, end, ok := parseBoxTable(lines, i)
		if !ok {
			flush()
			o.add(lines[i])
			i++
			continue
		}
		if len(rows[0]) != 2 || len(rows[0][0]) != 1 {
			return "", false
		}
		a := &advisory{sev: rows[0][0].text(), title: rows[0][1].text(), multi: map[string][]string{}, multiSeen: map[string]bool{}}
		for _, r := range rows[1:] {
			k := r[0].text()
			switch {
			case k == "Package":
				a.pkg = r[1].text()
			case k == "More info":
				a.info = r[1].text()
			case boxAuditMerged[k]:
				a.merge(k, r[1])
			default:
				a.fields = append(a.fields, k+": "+r[1].text())
			}
		}
		if a.pkg == "" {
			return "", false
		}
		key := strings.Join(append([]string{a.sev, a.title, a.pkg, a.info}, a.fields...), "\x00")
		if prev := byKey[key]; prev != nil {
			for _, k := range a.multiOrder {
				prev.merge(k, a.multi[k])
			}
		} else {
			byKey[key] = a
			advs = append(advs, a)
		}
		tables++
		i = end
	}
	flush()
	if tables == 0 {
		return "", false
	}
	return o.String(), true
}
