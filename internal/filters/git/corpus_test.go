package git

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/lazyre"
	"github.com/iheeb1/lx/internal/tokens"
)

type corpusCase struct {
	name    string
	local   bool
	filter  string
	bail    bool
	process string
	why     string

	drops func(t *testing.T, clean, got string, missing []string)
}

const (
	small   = "engine: at most SmallOutput tokens after normalization, only normalized"
	notWith = "engine never-worse gate: the filtered view saves under 10%"
)

var corpusCases = []corpusCase{
	{name: "git-blame", filter: "git-blame", drops: blameDrops},
	{name: "git-branch-a", filter: "git-branch", drops: branchDrops},
	{name: "git-clone-default", filter: "git-sync", process: "passthrough", why: small},
	{name: "git-clone-progress", filter: "git-sync", process: "normalize", why: small + " (the \\r progress frames were the bulk)"},
	{name: "git-diff-cached", filter: "git-diff"},
	{name: "git-diff-color", filter: "git-diff"},
	{name: "git-diff-during-merge", filter: "git-diff", process: "passthrough", why: notWith + ": a combined diff of conflict hunks is almost all hunk content, which is kept verbatim"},
	{name: "git-diff-package-lock", filter: "git-diff", drops: lockfileDrops},
	{name: "git-diff-stat", filter: "git-diff", process: "passthrough", why: "--stat is kept as git prints it"},
	{name: "git-diff-unstaged-go-sum", filter: "git-diff"},
	{name: "git-fetch-all", filter: "git-sync", bail: true, process: "passthrough", why: "empty output"},
	{name: "git-fetch-unshallow-progress", filter: "git-sync"},
	{name: "git-log-60", filter: "git-log", drops: logDrops},
	{name: "git-log-full-history", filter: "git-log", drops: logDrops},
	{name: "git-log-oneline-color", filter: "git-log", process: "normalize", why: "--oneline is kept as git prints it (ANSI removed)"},
	{name: "git-log-oneline-graph", filter: "git-log", process: "passthrough", why: "--graph is kept as git prints it"},
	{name: "git-log-stat", filter: "git-log", drops: logDrops},
	{name: "git-merge-conflict", filter: "git-merge", process: "passthrough", why: notWith + ": every CONFLICT line is kept, only 7 Auto-merging lines fold"},
	{name: "git-pull-fast-forward", filter: "git-sync"},
	{name: "git-pull-rebase", filter: "git-sync", process: "normalize", why: small},
	{name: "git-push-new-branch", filter: "git-sync", process: "passthrough", why: small},
	{name: "git-push-progress-new-branch", filter: "git-sync"},
	{name: "git-push-rejected", filter: "git-sync", process: "passthrough", why: small + " (every line is kept anyway)"},
	{name: "git-show-big-commit", filter: "git-show"},
	{name: "git-status-clean", filter: "git-status", process: "passthrough", why: small},
	{name: "git-status-dirty-color", filter: "git-status", drops: statusDrops},
	{name: "git-status-dirty", filter: "git-status", drops: statusDrops},
	{name: "git-status-during-merge", filter: "git-status", drops: statusDrops},
	{name: "git-status-short-dirty", filter: "", process: "-", why: "-s is machine-readable: the CLI passes it through before any filter runs"},

	{local: true, name: "git-blame-range", filter: "git-blame", drops: blameDrops},
	{local: true, name: "git-branch-plain", filter: "git-branch", process: "passthrough", why: small},
	{local: true, name: "git-branch-r", filter: "git-branch", drops: branchDrops},
	{local: true, name: "git-clone-local", filter: "git-sync", process: "passthrough", why: small},
	{local: true, name: "git-commit-amend-noop", filter: "git-commit", bail: true, process: "passthrough", why: small + "; a status text, not commit output, so the filter bails"},
	{local: true, name: "git-commit-root", filter: "git-commit", process: "passthrough", why: small},
	{local: true, name: "git-diff-cached-binary-mode-rename", filter: "git-diff"},
	{local: true, name: "git-fetch-new-branches", filter: "git-sync"},
	{local: true, name: "git-fetch-prune", filter: "git-sync", process: "passthrough", why: small},
	{local: true, name: "git-log-decorate-merges", filter: "git-log", drops: logDrops},
	{local: true, name: "git-log-graph-default", filter: "git-log", process: "passthrough", why: "--graph is kept as git prints it"},
	{local: true, name: "git-log-local", filter: "git-log"},
	{local: true, name: "git-log-p-3", filter: "git-log", drops: logDrops},
	{local: true, name: "git-log-p-pnpm-lock", filter: "git-log", drops: logDrops},
	{local: true, name: "git-log-p-yarn-lock", filter: "git-log", drops: logDrops},
	{local: true, name: "git-log-pretty-fuller", filter: "git-log", process: "passthrough", why: "--pretty=fuller is kept as git prints it"},
	{local: true, name: "git-merge-conflict-small", filter: "git-merge", process: "passthrough", why: small},
	{local: true, name: "git-merge-ff", filter: "git-merge", process: "passthrough", why: small},
	{local: true, name: "git-pull-merge", filter: "git-sync", process: "passthrough", why: small},
	{local: true, name: "git-push-ok", filter: "git-sync", process: "passthrough", why: small},
	{local: true, name: "git-push-rejected-nonff", filter: "git-sync", process: "passthrough", why: small},
	{local: true, name: "git-remote-v", filter: "git-remote", process: "passthrough", why: small},
	{local: true, name: "git-show-binary", filter: "git-show"},
	{local: true, name: "git-show-merge", filter: "git-show", process: "passthrough", why: small},
	{local: true, name: "git-show-package-lock", filter: "git-show"},
	{local: true, name: "git-show-stat", filter: "git-show", process: "passthrough", why: notWith + ": git show keeps the whole message"},
	{local: true, name: "git-stash-show", filter: "git-diff", process: "passthrough", why: small},
	{local: true, name: "git-stash-show-p", filter: "git-diff", process: "passthrough", why: small},
	{local: true, name: "git-tag-list", filter: "git-tag"},

	{local: true, name: "git-status-error-names", filter: "git-status", drops: statusDrops},
	{local: true, name: "git-diff-cached-copies", filter: "git-diff"},
	{local: true, name: "git-diff-lockfile-conflict-markers", filter: "git-diff", drops: lockfileDrops},
	{local: true, name: "git-log-p-12", filter: "git-log", drops: logDrops},
	{local: true, name: "git-status-sb-numbered", filter: "", process: "passthrough", why: "-sb is machine-readable (git's short format): passed through before any filter runs"},
	{local: true, name: "git-reflog-amends", filter: "git-list", process: "passthrough", why: "reflog is kept as git prints it (the generic reducer would fold consecutive amend entries)"},
	{local: true, name: "git-branch-avv", filter: "git-list", process: "passthrough", why: "verbose branch listings are kept as git prints them"},
	{local: true, name: "git-clean-dry-run", filter: "git-list", process: "passthrough", why: "every file a dry run would delete is kept (the generic reducer factored them into test<N>.out ×24)"},
}

func loadCase(t testing.TB, cc corpusCase) fixture.Case {
	t.Helper()
	if !cc.local {
		return fixture.Load(t, "git", cc.name)
	}
	c, err := fixture.Read("testdata", "git", cc.name)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCorpusCoversEveryCapture(t *testing.T) {
	have := map[string]bool{}
	for _, cc := range corpusCases {
		have[cc.name] = true
	}
	all := fixture.All(t)
	local, err := fixture.ReadAll("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range append(all, local...) {
		if c.Category == "git" && !have[c.Name] {
			t.Errorf("capture git/%s has no corpus case", c.Name)
		}
	}
}

func TestCorpus(t *testing.T) {
	var table strings.Builder
	for _, cc := range corpusCases {
		t.Run(cc.name, func(t *testing.T) {
			fc := loadCase(t, cc)
			c := fc.Context()
			clean := fc.Clean()

			f := engine.Find(c)
			switch {
			case cc.filter == "" && f != nil:
				t.Fatalf("filter %s matched; want none", f.Name())
			case cc.filter != "" && (f == nil || f.Name() != cc.filter):
				t.Fatalf("filter = %v, want %s", f, cc.filter)
			}
			if cc.filter == "" {
				if !engine.MachineReadable(c) {
					t.Fatalf("no filter and not machine-readable: %s", cc.why)
				}
				return
			}

			got, ok := f.Apply(c, clean)
			if ok == cc.bail {
				t.Fatalf("Apply ok=%v, want %v", ok, !cc.bail)
			}
			if again, _ := f.Apply(c, clean); again != got {
				t.Fatal("Apply is not deterministic")
			}
			if ok {
				fixture.Golden(t, "git", cc.name, got)

				missing := fixture.ErrorLinesMissing(clean, got)
				if len(missing) > 0 {
					if cc.drops == nil {
						t.Errorf("%d error lines missing, e.g. %q", len(missing), missing[0])
					} else {
						cc.drops(t, clean, got, missing)
					}
				}
				if fc.Meta.ExitCode != 0 {
					if lm := fixture.LocationsMissing(clean, got); len(lm) > 0 {
						t.Errorf("failing run lost locations: %v", lm)
					}
				}
			}

			res := engine.Process(c, fc.Raw, engine.Options{})
			if res.GuardAdded != 0 {
				t.Errorf("guard re-added %d error lines:\n%s", res.GuardAdded, res.Output)
			}
			want := cc.process
			if want == "" {
				want = cc.filter
			}
			if res.Filter != want {
				t.Errorf("pipeline filter = %s, want %s (%s)", res.Filter, want, cc.why)
			}
			raw, out := tokens.Count(fc.Raw), res.OutTokens
			line := fmt.Sprintf("%-36s %8d → %6d tokens  %5.1f%%  %s", cc.name, raw, out, 100*(1-float64(out)/float64(max(raw, 1))), res.Filter)
			t.Log(line)
			table.WriteString(line + "\n")
		})
	}
	t.Log("\n" + table.String())
}

var (
	commitLineRe = regexp.MustCompile(`^commit (?:[<>-] )?([0-9a-f]{40})`)
	moreCommitRe = regexp.MustCompile(`\[… \+(\d+) more commits? not shown \((\d+) total\)`)
	listedRe     = regexp.MustCompile(`(?m)^\[… the next (\d+) commits? without their changes`)
)

func logDrops(t *testing.T, clean, got string, missing []string) {
	t.Helper()
	lines := strings.Split(clean, "\n")
	shown := -1
	if m := moreCommitRe.FindStringSubmatch(got); m != nil {
		more, _ := strconv.Atoi(m[1])
		total, _ := strconv.Atoi(m[2])
		shown = total - more
	}

	full := shown
	if m := listedRe.FindStringSubmatch(got); m != nil {
		listed, _ := strconv.Atoi(m[1])
		if full < 0 {
			full = 0
			for _, ln := range lines {
				if commitLineRe.MatchString(ln) {
					full++
				}
			}
		}
		full -= listed
	}

	inShown := make([]bool, len(lines))
	inFull := make([]bool, len(lines))
	isMsg := make([]bool, len(lines))
	inLock := lockfileLines(lines)
	commits, inMsg := 0, false
	for i, ln := range lines {
		if commitLineRe.MatchString(ln) {
			commits++
			sha := commitLineRe.FindStringSubmatch(ln)[1]
			if (shown < 0 || commits <= shown) && !strings.Contains(got, sha[:7]) {
				t.Errorf("commit %s not shown", sha[:7])
			}
			inMsg = false
		}
		inShown[i] = shown < 0 || commits <= shown
		inFull[i] = full < 0 || commits <= full
		if strings.HasPrefix(ln, "Date:") {
			inMsg = true
		} else if inMsg && ln != "" && !strings.HasPrefix(ln, "    ") {
			inMsg = false
		}
		isMsg[i] = inMsg && strings.HasPrefix(ln, "    ")
		if inShown[i] && isMsg[i] && keepBodyRe.MatchString(ln) && !trailerRe.MatchString(strings.TrimSpace(ln)) &&
			!strings.Contains(got, strings.TrimSpace(ln)) {
			t.Errorf("meaningful body line dropped: %q", ln)
		}
		if inFull[i] && statSumRe.MatchString(ln) && !strings.Contains(got, ln) {
			t.Errorf("stat summary dropped: %q", ln)
		}
	}
	if shown >= 0 && commits != shownTotal(got) {
		t.Errorf("cut marker total %d, input has %d commits", shownTotal(got), commits)
	}
	allowed := map[string]bool{}
	for i, ln := range lines {
		if !inFull[i] || isMsg[i] || inLock[i] {
			allowed[strings.TrimSpace(ln)] = true
		}
	}
	for _, m := range missing {
		if !allowed[m] {
			t.Errorf("error line dropped outside a commit message: %q", m)
		}
	}
}

func shownTotal(got string) int {
	m := moreCommitRe.FindStringSubmatch(got)
	if m == nil {
		return -1
	}
	n, _ := strconv.Atoi(m[2])
	return n
}

func lockfileLines(lines []string) []bool {
	in := make([]bool, len(lines))
	lock := false
	for i, ln := range lines {
		if isDiffStart(ln) {
			lock = isLockfile(headNewPath(strings.TrimPrefix(ln, "diff --git ")))
		} else if commitLineRe.MatchString(ln) {
			lock = false
		}
		in[i] = lock
	}
	return in
}

func lockfileDrops(t *testing.T, clean, got string, missing []string) {
	t.Helper()
	lines := strings.Split(clean, "\n")
	inLock := lockfileLines(lines)
	allowed := map[string]bool{}
	for i, ln := range lines {
		if inLock[i] {
			allowed[strings.TrimSpace(ln)] = true
		}
	}
	for _, m := range missing {
		if !allowed[m] {
			t.Errorf("error line dropped outside a lockfile: %q", m)
		}
	}
	if !strings.Contains(got, "[lockfile: ") {
		t.Error("no lockfile summary")
	}
}

var blameOutRe = regexp.MustCompile(`^(?:\^?[0-9a-f]{7,40}(?: \(.*? \d{4}-\d{2}-\d{2}\))? )? *(\d+\).*)$`)

var blameMarkerRe = regexp.MustCompile(`\(lines (\d+)-(\d+)\); add -L`)

func blameDrops(t *testing.T, clean, got string, missing []string) {
	t.Helper()
	cutFrom, cutTo := 1<<30, 0
	if m := blameMarkerRe.FindStringSubmatch(got); m != nil {
		cutFrom, _ = strconv.Atoi(m[1])
		cutTo, _ = strconv.Atoi(m[2])
	}
	have := map[string]bool{}
	for _, ln := range strings.Split(got, "\n") {
		if m := blameOutRe.FindStringSubmatch(ln); m != nil {
			have[m[1]] = true
		}
	}
	for _, ln := range strings.Split(clean, "\n") {
		m := blameRe.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[5])
		if n >= cutFrom && n <= cutTo {
			continue
		}
		want := m[5] + ")"
		if m[6] != "" {
			want += " " + m[6]
		}
		if !have[want] {
			t.Errorf("blame line %d lost: %q", n, want)
		}
	}
}

func statusDrops(t *testing.T, clean, got string, missing []string) {
	t.Helper()
	for _, m := range missing {
		if !statusConverted(m, got) {
			t.Errorf("error line dropped by git-status: %q", m)
		}
	}
}

func statusConverted(ln, got string) bool {
	head, _, _ := strings.Cut(got, "\n")
	if !strings.HasPrefix(head, "## ") {
		return false
	}
	for _, re := range []*lazyre.Regexp{uptodateRe, aheadRe, behindRe, divergeRe, goneRe} {
		if m := re.FindStringSubmatch(ln); m != nil {
			return strings.Contains(head, "..."+m[1])
		}
	}
	switch {
	case strings.HasPrefix(ln, "On branch "):
		b := "## " + strings.TrimPrefix(ln, "On branch ")
		return head == b || strings.HasPrefix(head, b+"...") || strings.HasPrefix(head, b+" [")
	case strings.HasPrefix(ln, "HEAD detached "):
		return strings.HasPrefix(head, "## HEAD ("+strings.TrimPrefix(ln, "HEAD ")+")")
	}

	if m := entryRe.FindStringSubmatch("\t" + ln); m != nil {
		for _, g := range strings.Split(got, "\n") {
			if len(g) > 3 && g[2] == ' ' && g[3:] == m[2] {
				return true
			}
		}
	}
	return false
}

func branchDrops(t *testing.T, clean, got string, missing []string) {
	t.Helper()
	words := map[string]bool{}
	for _, w := range strings.Fields(got) {
		words[w] = true
	}
	for _, ln := range strings.Split(clean, "\n") {
		name := strings.TrimSpace(strings.TrimPrefix(ln, "*"))
		if name == "" || strings.Contains(name, " -> ") {
			continue
		}

		rel := strings.TrimPrefix(name, "remotes/")
		_, rel, _ = strings.Cut(rel, "/")
		if !words[name] && !words[rel] {
			t.Errorf("branch %q not listed", name)
		}
	}
}

func TestParsersConsumeRealDiffs(t *testing.T) {
	allowed := regexp.MustCompile(`^\* Unmerged path `)
	for _, cc := range corpusCases {
		if cc.filter != "git-diff" && cc.filter != "git-show" && cc.filter != "git-log" {
			continue
		}
		fc := loadCase(t, cc)
		lines := strings.Split(fc.Clean(), "\n")
		var parts []part
		if d, ok := parseLog(lines); ok {
			parts = d.pre
			for _, c := range d.commits {
				parts = append(parts, c.parts...)
			}
		} else if d, ok := parseDiffDoc(lines); ok {
			parts = d.parts
		}
		for _, p := range parts {
			if p.file != nil || p.raw == "" || statSumRe.MatchString(p.raw) || allowed.MatchString(p.raw) {
				continue
			}
			if _, ok := parseStatRow(p.raw); ok {
				continue
			}
			if cc.process == "passthrough" || cc.process == "normalize" {
				continue
			}
			t.Errorf("%s: stray line %q", cc.name, p.raw)
		}
	}
}
