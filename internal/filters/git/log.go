package git

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tokens"
)

func init() {
	engine.Register(logFilter{})
	engine.Register(showFilter{})
}

// logFilter renders the default (medium) `git log` format one line per
// commit:
//
//	1bd0ecf 2026-09-25 [HazyLab] fix(context): load Keys under the mutex (#4854) [+9 body lines]
//	    Fixes #4760                                  ← meaningful body lines kept
//
// The subject is never truncated. Body lines that carry meaning (breaking
// changes, Fixes/Closes/Refs #N, reverts, security, CVEs, deprecations) are
// kept; the rest are counted per commit. Merge commits are marked with
// their parents. With --stat the file list is folded by directory with its
// +/- counts and git's own "N files changed" line; with -p the diff goes
// through the git-diff rules (stat rows keep git's one-space indent, so they
// never read as message lines). Formats the user chose (--oneline, --pretty,
// --graph) are already compact and are left as they are. Very long
// histories are cut at a whole commit with an exact count of the rest; with
// -p/--stat the commits whose changes do not fit are first listed by their
// header line under "[… the next N commits without their changes …]".
type logFilter struct{}

func (logFilter) Name() string    { return "git-log" }
func (logFilter) IsContent() bool { return true }

func (logFilter) Match(c *engine.Context) bool {
	return isGit(c) && c.Sub() == "log" && !engine.MachineReadable(c)
}

// userFormatFlags pick a format other than the default medium one; git's
// output is then printed as it is.
var userFormatFlags = []string{
	"--oneline", "--pretty", "--pretty=", "--format=", "--graph", "--word-diff", "--word-diff=",
	"--color-words", "--binary", "--raw", "-z", "--patch-with-raw", "-g", "--walk-reflogs",
}

// headLines returns out unchanged when it fits budget tokens; otherwise
// its first lines up to the budget and an exact count of the rest. Logs
// list the newest commits first, so the head is what matters (the engine's
// generic cut would keep 30 head and 60 tail lines).
func headLines(out string, budget int) string {
	if tokens.Count(out) <= budget {
		return out
	}
	lines := strings.Split(out, "\n")
	used, i := 0, 0
	for ; i < len(lines); i++ {
		c := tokens.Count(lines[i]) + 1
		if used+c > budget-50 {
			break
		}
		used += c
	}
	kept := append([]string(nil), lines[:i]...)
	// git's own diagnostics are never cut.
	var diag []string
	for _, ln := range lines[i:] {
		if gitDiagRe.MatchString(ln) {
			diag = append(diag, ln)
		}
	}
	kept = append(kept, fmt.Sprintf("[… +%d more lines not shown (%d total)]", len(lines)-i, len(lines)))
	return join(append(kept, capItems(diag, 20)...))
}

// userFormat reports whether the user picked a format other than medium.
func userFormat(c *engine.Context) bool {
	for _, a := range subArgs(c) {
		switch a {
		case "--pretty", "--pretty=medium", "--format=medium":
			continue
		}
		for _, f := range userFormatFlags {
			if a == f || strings.HasSuffix(f, "=") && strings.HasPrefix(a, f) {
				return true
			}
		}
	}
	return false
}

func (logFilter) Apply(c *engine.Context, out string) (string, bool) {
	if userFormat(c) {
		return headLines(out, logBudget), true
	}
	doc, ok := parseLog(strings.Split(out, "\n"))
	if !ok {
		return "", false
	}
	return join(renderLog(doc, false)), true
}

// showFilter renders `git show` for commits: the log header line (with the
// author's email), the whole message (blank-line runs collapsed, capped at
// 40 lines), then the diff under the git-diff rules. Blobs (`git show
// rev:path`) print as they are; an annotated tag's header and message are
// kept verbatim above its commit; output with no commit (a tree listing, an
// error) is left to the generic reducer.
type showFilter struct{}

func (showFilter) Name() string    { return "git-show" }
func (showFilter) IsContent() bool { return true }

func (showFilter) Match(c *engine.Context) bool {
	return isGit(c) && c.Sub() == "show" && !engine.MachineReadable(c)
}

func (showFilter) Apply(c *engine.Context, out string) (string, bool) {
	if userFormat(c) || hasArg(c, "--word-diff", "--word-diff=", "--color-words", "--color-words=", "--full-index", "--output=") {
		return out, true
	}
	for _, p := range positionals(c) {
		if strings.Contains(p, ":") {
			return out, true // blob content: a file, shown as it is
		}
	}
	doc, ok := parseLog(strings.Split(out, "\n"))
	if !ok {
		return "", false // a tree listing, an error: not a commit
	}
	return join(renderLog(doc, true)), true
}

// ---- model -----------------------------------------------------------------

type commit struct {
	sha     string
	decor   string   // "HEAD -> main, origin/main"
	parents []string // from "Merge:"
	author  string   // "Name <email>"
	date    string
	other   []string // header lines kept verbatim (Reflog:, Commit:, …)
	body    []string // message lines without the 4-space indent
	parts   []part   // what follows the message: stat rows, diffs, other lines
}

type logDoc struct {
	pre     []part // lines before the first commit
	commits []*commit
}

var (
	commitRe = lazyre.New(`^commit (?:[<>-] )?([0-9a-f]{7,64})(?: \((.*)\))?$`)
	headerRe = lazyre.New(`^([A-Z][A-Za-z]*(?: [a-z]+)?):(?: +(.*))?$`)
)

// parseLog parses git's medium format (the default for log and show). It
// fails when no commit header is recognized.
func parseLog(lines []string) (logDoc, bool) {
	var d logDoc
	var cur *commit
	add := func(p part) {
		if cur == nil {
			d.pre = append(d.pre, p)
		} else {
			cur.parts = append(cur.parts, p)
		}
	}
	for i := 0; i < len(lines); {
		ln := lines[i]
		if m := commitRe.FindStringSubmatch(ln); m != nil {
			cur = &commit{sha: m[1], decor: m[2]}
			d.commits = append(d.commits, cur)
			i = parseCommit(lines, i+1, cur)
			continue
		}
		if isDiffStart(ln) {
			f, next, ok := parseFile(lines, i)
			if !ok {
				return d, false
			}
			add(part{file: f})
			i = next
			continue
		}
		add(part{raw: ln})
		i++
	}
	return d, len(d.commits) > 0
}

// parseCommit reads the header block and the indented message, returning
// the index of the first line after the message.
func parseCommit(lines []string, i int, c *commit) int {
	for ; i < len(lines) && lines[i] != ""; i++ {
		if commitRe.MatchString(lines[i]) {
			return i // an empty commit block
		}
		m := headerRe.FindStringSubmatch(lines[i])
		if m == nil {
			// Not a "Key: value" header (gpg: lines of --show-signature,
			// …): kept verbatim under the commit line.
			c.other = append(c.other, lines[i])
			continue
		}
		switch m[1] {
		case "Merge":
			c.parents = strings.Fields(m[2])
		case "Author":
			c.author = m[2]
		case "Date", "AuthorDate":
			c.date = m[2]
		default:
			c.other = append(c.other, lines[i])
		}
	}
	if i < len(lines) && lines[i] == "" {
		i++
	}
	// Message: 4-space indented lines, blank lines between paragraphs.
	j := i
	for j < len(lines) && (strings.HasPrefix(lines[j], "    ") || lines[j] == "") {
		j++
	}
	// Trailing blank lines belong to the separator, not the message.
	end := j
	for end > i && lines[end-1] == "" {
		end--
	}
	for _, ln := range lines[i:end] {
		c.body = append(c.body, strings.TrimPrefix(ln, "    "))
	}
	return end
}

// ---- rendering ---------------------------------------------------------------

// gitDiagRe matches git's own diagnostics (die(), error(), warning() and
// BUG() messages).
var gitDiagRe = lazyre.New(`^(?:fatal|error|warning|BUG): `)

// logBudget: longer logs are cut at a commit boundary with an exact count,
// below the engine budget (which would cut mid-commit).
const logBudget = 7000

// listReserve: with -p/--stat, up to this many tokens of the budget are kept
// for listing the commits whose changes do not fit, one line each.
const listReserve = 1000

// renderLog renders commits in full while they fit the budget. With -p,
// --stat and the like, the commits after that are listed by their header
// line (and kept body lines) without their changes, under a marker, while
// they fit; any rest is counted exactly. git's own diagnostics among the
// commits not rendered in full are always printed.
func renderLog(d logDoc, show bool) []string {
	abbrev := abbrevLen(d.commits)
	var out []string
	for _, p := range d.pre {
		out = append(out, renderPart(p)...)
	}
	heads := make([][]string, len(d.commits))
	detailed := false
	for i, c := range d.commits {
		heads[i] = renderCommitHead(c, abbrev, show)
		for _, p := range c.parts {
			// The blank line between commits is a part too; changes
			// (stat rows, diffs, --name-only paths) are not blank.
			detailed = detailed || p.file != nil || p.raw != ""
		}
	}
	reserve := 0
	if detailed {
		for _, h := range heads[min(1, len(heads)):] {
			if reserve += countTokens(h); reserve >= listReserve {
				reserve = listReserve
				break
			}
		}
	}
	full := logBudget - reserve
	used := countTokens(out)
	for i, c := range d.commits {
		head := heads[i]
		body := renderCommitParts(c.parts, max(full-used-countTokens(head), 1500))
		cost := countTokens(head) + countTokens(body)
		if i == 0 || used+cost <= full {
			used += cost
			out = append(out, head...)
			out = append(out, body...)
			continue
		}
		j := i
		if detailed {
			var list []string
			for ; j < len(d.commits); j++ {
				hc := countTokens(heads[j])
				if used+hc > logBudget-60 {
					break
				}
				used += hc
				list = append(list, heads[j]...)
			}
			if j > i {
				out = append(out, fmt.Sprintf("[… the next %s without their changes (output budget):]", plural(j-i, "commit")))
				out = append(out, list...)
			}
		}
		if j < len(d.commits) {
			out = append(out, fmt.Sprintf("[… +%s not shown (%d total); oldest shown: %s]",
				plural(len(d.commits)-j, "more commit"), len(d.commits), short(d.commits[j-1].sha, abbrev)))
		}
		// git's own diagnostics after a commit (a warning, a trailing
		// "fatal: …") are never cut with the changes around them. Other
		// error-looking lines there are data (stat rows, file names).
		var diag []string
		for _, rc := range d.commits[i:] {
			for _, p := range rc.parts {
				if p.file == nil && gitDiagRe.MatchString(p.raw) {
					diag = append(diag, p.raw)
				}
			}
		}
		out = append(out, capItems(diag, 20)...)
		break
	}
	return out
}

func renderPart(p part) []string {
	if p.file != nil {
		return renderFile(p.file, lvlBase, 0)
	}
	return []string{p.raw}
}

// renderCommitParts renders what follows a message: diffstat rows folded,
// diffs through the diff renderer, anything else verbatim. Blank separator
// lines are dropped.
func renderCommitParts(parts []part, budget int) []string {
	var out []string
	var rows []statRow
	var files []part
	flushRows := func() {
		if len(rows) > 0 {
			// git's own one-space indent: message lines have four.
			out = append(out, renderGitStat(rows, " ")...)
			rows = nil
		}
	}
	flushFiles := func() {
		if len(files) > 0 {
			out = append(out, renderDiffParts(files, budget)...)
			files = nil
		}
	}
	for _, p := range parts {
		if p.file != nil {
			flushRows()
			files = append(files, p)
			continue
		}
		ln := p.raw
		if r, ok := parseStatRow(ln); ok {
			flushFiles()
			rows = append(rows, r)
			continue
		}
		if ln == "" {
			continue
		}
		if statSumRe.MatchString(ln) {
			resolveStat(rows, ln)
			flushRows()
			flushFiles()
			out = append(out, ln)
			continue
		}
		flushRows()
		flushFiles()
		out = append(out, ln)
	}
	flushRows()
	flushFiles()
	return out
}

// keepBodyRe matches message lines worth keeping in a one-line-per-commit
// log: breaking changes, issue links, reverts, security and deprecations.
var keepBodyRe = lazyre.New(`(?i)\bbreaking\b|` +
	`\b(?:fix(?:e[sd])?|close[sd]?|resolve[sd]?|refs?|references|see|related(?: to)?)\b:?\s+(?:[\w.-]+/[\w.-]+)?#\d+|` +
	`\b(?:fix(?:e[sd])?|close[sd]?|resolve[sd]?|refs?)\b:?\s+https?://\S+/(?:issues|pull)/\d+|` +
	`^revert\b|\bthis reverts commit\b|\bsecurity\b|\bcve-\d|\bghsa-|\bdeprecat|\bvulnerab`)

// keepBodyRe's alternatives each need either a standalone stem
// (keepBodyStems) or an issue keyword together with "#" or "http". Checking
// that first skips the regexp — a case-insensitive alternation, slow in Go's
// backtracker — for almost every body line, including "(#1234)" subjects.
var (
	keepBodyStems = []string{"breaking", "revert", "security", "cve-", "ghsa-", "deprecat", "vulnerab"}
	issueWords    = []string{"fix", "close", "resolve", "ref", "see", "related"}
)

// keepBody is keepBodyRe.MatchString with an exact prefilter.
func keepBody(ln string) bool {
	if !strings.ContainsAny(ln, "\u017f\u212a") { // runes that case-fold to ASCII
		low := strings.ToLower(ln)
		found := false
		for _, st := range keepBodyStems {
			if strings.Contains(low, st) {
				found = true
				break
			}
		}
		if !found && (strings.Contains(low, "#") || strings.Contains(low, "http")) {
			for _, w := range issueWords {
				if strings.Contains(low, w) {
					found = true
					break
				}
			}
		}
		if !found {
			return false
		}
	}
	return keepBodyRe.MatchString(ln)
}

// maxKept caps the body lines kept per commit in a log; the rest count
// as omitted body lines.
const maxKept = 12

// bulletRe matches the start of a list item ("- x", "* x", "1. x").
var bulletRe = lazyre.New(`^\s*(?:[-*+]|\d+[.)])\s`)

func indentOf(s string) int { return len(s) - len(strings.TrimLeft(s, " \t")) }

// trailerRe matches Signed-off-by:, Co-authored-by: and similar trailers.
var trailerRe = lazyre.New(`^[A-Z][\w-]*-[Bb]y: `)

func renderCommitHead(c *commit, abbrev int, show bool) []string {
	subject, rest := splitMessage(c.body)
	var b strings.Builder
	b.WriteString(short(c.sha, abbrev))
	if c.decor != "" {
		b.WriteString(" (" + c.decor + ")")
	}
	if d := shortDate(c.date); d != "" {
		b.WriteString(" " + d)
	}
	author := c.author
	if !show {
		author = authorName(author)
	}
	if author != "" {
		b.WriteString(" [" + author + "]")
	}
	if subject != "" {
		b.WriteString(" " + subject)
	}
	if len(c.parents) > 0 {
		b.WriteString(" [merge " + strings.Join(c.parents, " ") + "]")
	}
	var kept []string
	dropped := 0
	if show {
		// The one commit asked about: its whole message, blank-line runs
		// collapsed, capped at maxMsg lines.
		const maxMsg = 40
		blank := true
		for _, ln := range rest {
			if strings.TrimSpace(ln) == "" {
				if !blank && len(kept) < maxMsg {
					kept = append(kept, "")
				}
				blank = true
				continue
			}
			blank = false
			if len(kept) >= maxMsg {
				dropped++
				continue
			}
			kept = append(kept, "    "+ln)
		}
		for len(kept) > 0 && kept[len(kept)-1] == "" {
			kept = kept[:len(kept)-1]
		}
		if dropped > 0 {
			kept = append(kept, fmt.Sprintf("    [… +%d more message lines not shown]", dropped))
		}
	} else {
		// A merge commit's first body line is usually the merged pull
		// request's title: the only line that says what was merged.
		firstMergeLine := len(c.parents) > 1
		for i := 0; i < len(rest); i++ {
			ln := rest[i]
			switch {
			case strings.TrimSpace(ln) == "":
			case firstMergeLine && !trailerRe.MatchString(ln):
				firstMergeLine = false
				kept = append(kept, "    "+ln)
			case len(kept) < maxKept && !trailerRe.MatchString(ln) && keepBody(ln):
				kept = append(kept, "    "+ln)
				// A kept list item keeps its wrapped continuation lines.
				if bulletRe.MatchString(ln) {
					ind := indentOf(ln)
					for k := 0; k < 3 && i+1 < len(rest) && strings.TrimSpace(rest[i+1]) != "" &&
						indentOf(rest[i+1]) > ind && !bulletRe.MatchString(rest[i+1]); k++ {
						i++
						kept = append(kept, "    "+rest[i])
					}
				}
			default:
				dropped++
			}
		}
		if dropped > 0 {
			fmt.Fprintf(&b, " [+%s]", plural(dropped, "body line"))
		}
	}
	out := []string{b.String()}
	for _, o := range c.other {
		out = append(out, "    "+o)
	}
	return append(out, kept...)
}

// splitMessage returns git's %s (the first paragraph joined by spaces) and
// the remaining message lines.
func splitMessage(body []string) (string, []string) {
	i := 0
	for i < len(body) && strings.TrimSpace(body[i]) == "" {
		i++
	}
	j := i
	for j < len(body) && strings.TrimSpace(body[j]) != "" {
		j++
	}
	parts := make([]string, 0, j-i)
	for _, ln := range body[i:j] {
		parts = append(parts, strings.TrimSpace(ln))
	}
	return strings.Join(parts, " "), body[j:]
}

func authorName(a string) string {
	if i := strings.Index(a, " <"); i > 0 && strings.HasSuffix(a, ">") {
		return a[:i]
	}
	return a
}

var dateLayouts = []string{
	"Mon Jan 2 15:04:05 2006 -0700",  // default, --date=local drops the zone
	"Mon Jan 2 15:04:05 2006",        // --date=local
	"Mon, 2 Jan 2006 15:04:05 -0700", // --date=rfc
	"2006-01-02 15:04:05 -0700",      // --date=iso
	"2006-01-02T15:04:05-07:00",      // --date=iso-strict
	"2006-01-02",                     // --date=short
}

// shortDate turns git's date into YYYY-MM-DD in the date's own zone; dates
// it cannot parse (relative, raw, custom) are kept as they are.
func shortDate(d string) string {
	d = strings.TrimSpace(d)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, d); err == nil {
			return t.Format("2006-01-02")
		}
	}
	return d
}

func short(sha string, n int) string {
	if len(sha) > n {
		return sha[:n]
	}
	return sha
}

// abbrevLen picks the abbreviation length: git's own (seen on Merge: lines)
// or 7, extended until every commit shown is unique.
func abbrevLen(cs []*commit) int {
	n := 7
	for _, c := range cs {
		for _, p := range c.parents {
			n = max(n, len(p))
		}
	}
	for ; n < 40; n++ {
		seen := make(map[string]string, len(cs))
		dup := false
		for _, c := range cs {
			k := short(c.sha, n)
			if full, ok := seen[k]; ok && full != c.sha {
				dup = true
				break
			}
			seen[k] = c.sha
		}
		if !dup {
			break
		}
	}
	return n
}
