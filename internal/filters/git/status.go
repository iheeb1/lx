// Package git condenses git porcelain-for-humans output.
package git

import (
	"regexp"
	"sort"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

func init() { engine.Register(status{}) }

// status renders `git status` (long format) as git's own short format, which
// every model reads fluently: "## branch...upstream [ahead N]" then one
// "XY path" line per entry. Hints ("use git add…") are dropped unless the repo
// is mid-merge/rebase/cherry-pick, where they are the next step.
type status struct{}

func (status) Name() string { return "git-status" }

// Faithful: every entry, the branch/upstream state and any in-progress
// operation survive; only the "(use git …)" hints are dropped.
func (status) Faithful(*engine.Context) bool { return true }

// GuardsErrors: status rewrites entry and branch lines into short-format
// ones, so a path or branch that merely contains an error word
// ("\tmodified:   src/panic.go", "On branch fix-panic") would otherwise be
// re-added by the engine guard although it is shown. Every line the filter
// does not convert (repository state, warnings, fatal: lines, and hints
// that are error-class) is printed verbatim; TestStatusGuardsErrors proves
// that every error-class input line survives either verbatim or as its
// converted entry/branch line.
func (status) GuardsErrors() bool { return true }

// Match: exact -s/--short/--porcelain/-z are machine-readable and passed
// through before any filter; combined short flags (-sb, -bs, -suno) are
// not detected there, so this filter matches them and keeps git's short
// format as it is (see Apply).
func (status) Match(c *engine.Context) bool {
	// Short format (-s, -sb, --short) is machine-readable: the engine passes
	// it through before any filter runs.
	return isGit(c) && c.Sub() == "status" && !engine.MachineReadable(c)
}

// shortStatusRe matches a line of git's short format: "## branch…" or
// "XY path".
var shortStatusRe = regexp.MustCompile(`^(?:## .+|[ MTADRCU?!]{2} .+)$`)

// isShortStatus reports whether every non-blank line is short format.
func isShortStatus(lines []string) bool {
	n := 0
	for _, ln := range lines {
		if ln == "" {
			continue
		}
		if !shortStatusRe.MatchString(ln) {
			return false
		}
		n++
	}
	return n > 0
}

var (
	aheadRe    = regexp.MustCompile(`^Your branch is ahead of '([^']+)' by (\d+) commits?`)
	behindRe   = regexp.MustCompile(`^Your branch is behind '([^']+)' by (\d+) commits?`)
	divergedRe = regexp.MustCompile(`^and have (\d+) and (\d+) different commits each`)
	divergeRe  = regexp.MustCompile(`^Your branch and '([^']+)' have diverged,`)
	uptodateRe = regexp.MustCompile(`^Your branch is up[ -]to[ -]date with '([^']+)'`) // "up-to-date" before git 2.15
	goneRe     = regexp.MustCompile(`^Your branch is based on '([^']+)', but the upstream is gone`)
	entryRe    = regexp.MustCompile(`^\t(?:(new file|modified|deleted|renamed|copied|typechange|both modified|both added|both deleted|added by us|added by them|deleted by us|deleted by them):\s+)?(.+)$`)
)

var indexCode = map[string]byte{"new file": 'A', "modified": 'M', "deleted": 'D', "renamed": 'R', "copied": 'C', "typechange": 'T'}

var unmergedCode = map[string]string{
	"both modified": "UU", "both added": "AA", "both deleted": "DD",
	"added by us": "AU", "added by them": "UA", "deleted by us": "DU", "deleted by them": "UD",
}

type entry struct {
	x, y byte
	path string
}

func (status) Apply(c *engine.Context, out string) (string, bool) {
	lines := strings.Split(out, "\n")
	if isShortStatus(lines) || strings.Contains(out, "\ndiff --git ") {
		// Short format (-sb and other combined flags) is already the
		// target format; -v appends staged/unstaged diffs. Both are kept
		// as they are rather than left to the generic reducer, which
		// folds runs of look-alike lines ("?? fixture01.json" …).
		return out, true
	}
	var (
		branch, upstream, track string
		states, hints           []string
		entries                 = map[string]*entry{}
		renamedTo               = map[string]string{} // "b" → "a -> b" for staged renames/copies
		order                   []string
		section                 string
		trailers                []string
		recognized              bool
	)
	// Tracked entries are keyed by path, so a path staged and modified
	// again becomes one "MM" entry. Untracked and ignored entries get keys
	// of their own: a path can be both staged for deletion and untracked
	// (git rm --cached), and git's short format lists it twice
	// ("D  x" and "?? x").
	get := func(key, p string) *entry {
		if e := entries[key]; e != nil {
			return e
		}
		e := &entry{x: ' ', y: ' ', path: p}
		entries[key] = e
		order = append(order, key)
		return e
	}
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		switch {
		case t == "":
			continue
		case strings.HasPrefix(ln, "On branch "):
			branch, recognized = strings.TrimPrefix(ln, "On branch "), true
		case strings.HasPrefix(ln, "HEAD detached "):
			branch, recognized = "HEAD ("+strings.TrimPrefix(ln, "HEAD ")+")", true
		case ln == "Not currently on any branch.":
			branch, recognized = "HEAD (no branch)", true
		case ln == "No commits yet":
			track = "no commits yet"
		case uptodateRe.MatchString(ln):
			upstream = uptodateRe.FindStringSubmatch(ln)[1]
		case aheadRe.MatchString(ln):
			m := aheadRe.FindStringSubmatch(ln)
			upstream, track = m[1], "ahead "+m[2]
		case behindRe.MatchString(ln):
			m := behindRe.FindStringSubmatch(ln)
			upstream, track = m[1], "behind "+m[2]
		case divergeRe.MatchString(ln):
			upstream = divergeRe.FindStringSubmatch(ln)[1]
		case divergedRe.MatchString(ln):
			m := divergedRe.FindStringSubmatch(ln)
			track = "ahead " + m[1] + ", behind " + m[2]
		case goneRe.MatchString(ln):
			upstream, track = goneRe.FindStringSubmatch(ln)[1], "gone"
		case strings.HasPrefix(ln, "  (") && strings.HasSuffix(ln, ")") && !engine.IsError(ln):
			hints = append(hints, ln)
		case ln == "Changes to be committed:":
			section = "staged"
		case ln == "Changes not staged for commit:":
			section = "unstaged"
		case ln == "Unmerged paths:":
			section = "unmerged"
		case ln == "Untracked files:":
			section = "untracked"
		case ln == "Ignored files:":
			section = "ignored"
		case strings.HasPrefix(ln, "\t") && section != "":
			m := entryRe.FindStringSubmatch(ln)
			if m == nil {
				return "", false
			}
			kind, p := m[1], m[2]
			switch section {
			case "staged":
				code, ok := indexCode[kind]
				if !ok {
					return "", false
				}
				get(p, p).x = code
				if code == 'R' || code == 'C' {
					if _, to, ok := strings.Cut(p, " -> "); ok {
						renamedTo[to] = p
					}
				}
			case "unstaged":
				code, ok := indexCode[kind]
				if !ok {
					return "", false
				}
				// A rename is staged as "a -> b"; unstaged edits name b.
				if k, ok := renamedTo[p]; ok {
					p = k
				}
				get(p, p).y = code
			case "unmerged":
				code, ok := unmergedCode[kind]
				if !ok {
					return "", false
				}
				e := get(p, p)
				e.x, e.y = code[0], code[1]
			case "untracked":
				e := get("?\x00"+p, p)
				e.x, e.y = '?', '?'
			case "ignored":
				e := get("!\x00"+p, p)
				e.x, e.y = '!', '!'
			}
		case strings.HasPrefix(ln, "nothing to commit"):
			trailers = append(trailers, ln)
		case (strings.HasPrefix(ln, "nothing added to commit") || strings.HasPrefix(ln, "no changes added to commit")) && !engine.IsError(ln):
			// "(use "git add" …)" advice: the entries above say it.
		default:
			// Repository state ("You have unmerged paths.", "interactive
			// rebase in progress; onto …", "Last command done:" …) is kept.
			states = append(states, ln)
		}
	}
	if !recognized {
		return "", false
	}

	var b strings.Builder
	b.WriteString("## ")
	b.WriteString(branch)
	if upstream != "" {
		b.WriteString("..." + upstream)
	}
	if track != "" {
		b.WriteString(" [" + track + "]")
	}
	b.WriteByte('\n')
	for _, s := range states {
		b.WriteString(s + "\n")
	}
	if len(states) > 0 {
		// Mid-operation: the hints are the way out, keep them.
		for _, h := range hints {
			b.WriteString(h + "\n")
		}
	}
	type sorted struct {
		key string
		idx int // input order breaks ties: the sort is stable
		e   *entry
	}
	list := make([]sorted, len(order))
	for i, k := range order {
		list[i] = sorted{sortKey(entries[k]), i, entries[k]}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].key != list[j].key {
			return list[i].key < list[j].key
		}
		return list[i].idx < list[j].idx
	})
	for _, it := range list {
		e := it.e
		b.WriteByte(e.x)
		b.WriteByte(e.y)
		b.WriteByte(' ')
		b.WriteString(e.path)
		b.WriteByte('\n')
	}
	for _, tr := range trailers {
		b.WriteString(tr + "\n")
	}
	return strings.TrimRight(b.String(), "\n"), true
}

// sortKey puts conflicts first, then tracked changes, then untracked, then
// ignored, each by path — the order an agent should act on them.
func sortKey(e *entry) string {
	rank := "1"
	switch {
	case e.x == 'U' || e.y == 'U' || e.x == 'A' && e.y == 'A' || e.x == 'D' && e.y == 'D':
		rank = "0"
	case e.x == '?':
		rank = "2"
	case e.x == '!':
		rank = "3"
	}
	return rank + e.path
}
