package git

import (
	"fmt"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/lazyre"
)

func init() {
	engine.Register(syncFilter{})
	engine.Register(mergeFilter{})
}

type syncFilter struct{}

func (syncFilter) Name() string { return "git-sync" }

func (syncFilter) Match(c *engine.Context) bool {
	if !isGit(c) || engine.MachineReadable(c) {
		return false
	}
	switch c.Sub() {
	case "push", "pull", "fetch", "clone":
		return true
	}
	return false
}

func (syncFilter) Apply(c *engine.Context, out string) (string, bool) {
	return renderSync(c, out)
}

type mergeFilter struct{}

func (mergeFilter) Name() string { return "git-merge" }

func (mergeFilter) Match(c *engine.Context) bool {
	return isGit(c) && c.Sub() == "merge" && !engine.MachineReadable(c)
}

func (mergeFilter) Apply(c *engine.Context, out string) (string, bool) {
	return renderSync(c, out)
}

var (
	packStatsRe = lazyre.New(`^(?:remote: )?Total \d+ \(delta \d+\), reused \d+ \(delta \d+\)`)

	syncNoiseRe = lazyre.New(`^(?:remote: )?(?:Unpacking objects|Checking out files|Filtering content|Checking connectivity|Updating files)[:.]|` +
		`^Delta compression using up to \d+ threads\.?$|^remote:$`)

	refLineRe  = lazyre.New(`^ ([ +\-t*!=]) (\[[^\]]+\]|[0-9a-f]{4,}\.\.\.?[0-9a-f]{4,}) +(\S+) +-> +(\S+)(?: \(([^)]*)\))?$`)
	modeLineRe = lazyre.New(`^ (create|delete) mode (\d{6}) (.+)$`)
)

func matchRefLine(ln string) []string {
	if len(ln) < 8 || ln[0] != ' ' || !strings.Contains(ln, "->") {
		return nil
	}
	return refLineRe.FindStringSubmatch(ln)
}

func isModeLine(ln string) bool {
	return (strings.HasPrefix(ln, " create mode ") || strings.HasPrefix(ln, " delete mode ")) && modeLineRe.MatchString(ln)
}

const minRefGroup = 4

const maxNames = 400

type refLine struct {
	raw                     string
	flag, summary, src, dst string
	reason                  string
}

func (r refLine) groupKey() (key, item string, ok bool) {
	if r.reason != "" || r.flag == "!" || engine.IsError(r.raw) {
		return "", "", false
	}
	switch {
	case r.src == r.dst:
		return r.flag + r.summary + "\x00same", r.src, true
	case r.src == "(none)":
		return r.flag + r.summary + "\x00none", r.dst, true
	case strings.HasSuffix(r.dst, "/"+r.src):
		prefix := strings.TrimSuffix(r.dst, r.src)
		return r.flag + r.summary + "\x00" + prefix, r.src, true
	}
	return "", "", false
}

func renderSync(c *engine.Context, out string) (string, bool) {
	lines := strings.Split(out, "\n")
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	var res []string
	var rows []statRow
	var refs []refLine
	var modes []string
	var autos []string
	autoAt := -1
	recognized := false
	noise := 0

	flushRows := func() {
		if len(rows) > 0 {
			res = append(res, renderGitStat(rows, " ")...)
			rows = nil
		}
	}
	flushRefs := func() {
		res = append(res, groupRefs(refs)...)
		refs = nil
	}
	flushModes := func() {
		res = append(res, groupModes(modes)...)
		modes = nil
	}
	for i, ln := range lines {
		if m := matchRefLine(ln); m != nil {
			flushRows()
			flushModes()
			refs = append(refs, refLine{raw: ln, flag: m[1], summary: m[2], src: m[3], dst: m[4], reason: m[5]})
			recognized = true
			continue
		}
		flushRefs()
		if r, ok := parseStatRow(ln); ok && !engine.IsError(ln) {
			rows = append(rows, r)
			continue
		}
		if strings.Contains(ln, " changed") && statSumRe.MatchString(ln) {
			resolveStat(rows, ln)
		}
		flushRows()
		if isModeLine(ln) && !engine.IsError(ln) {
			modes = append(modes, ln)
			continue
		}
		flushModes()
		switch {
		case strings.TrimSpace(ln) == "":
		case len(ln) > len("Auto-merging ") && strings.HasPrefix(ln, "Auto-merging ") &&
			!strings.Contains(ln[len("Auto-merging "):], " ") && !engine.IsError(ln):
			if autoAt < 0 {
				autoAt = len(res)
				res = append(res, "")
			}
			autos = append(autos, ln[len("Auto-merging "):])
		case engine.IsProgress(ln), strings.Contains(ln, "Total ") && packStatsRe.MatchString(ln), syncNoiseRe.MatchString(ln):
			recognized = true
			noise++
			if c.Failed() && i == last {
				res = append(res, ln)
			}
		default:
			if isSyncLine(ln) {
				recognized = true
			}
			res = append(res, ln)
		}
	}
	flushRefs()
	flushRows()
	flushModes()
	if autoAt >= 0 {
		if len(autos) == 1 {
			res[autoAt] = "Auto-merging " + autos[0]
		} else {
			res[autoAt] = fmt.Sprintf("Auto-merging (%d): %s", len(autos), strings.Join(capItems(autos, maxNames), " "))
		}
		recognized = true
	}
	if !recognized {
		return "", false
	}
	if out := join(res); out != "" {
		return out, true
	}

	return fmt.Sprintf("[%s of transfer progress hidden]", engine.Plural(noise, "line", "lines")), true
}

func isSyncLine(ln string) bool {
	for _, p := range []string{"To ", "From ", "Cloning into ", "Updating ", "Fast-forward", "Merge made by",
		"Already up to date", "Everything up-to-date", "CONFLICT ", "Automatic merge failed", "branch '",
		"Successfully rebased", "remote: ", "hint: ", "error: ", "fatal: ", "Fetching ", " * branch ",
		"Already up-to-date", "Your branch "} {
		if strings.HasPrefix(ln, p) {
			return true
		}
	}
	return statSumRe.MatchString(ln)
}

func groupRefs(refs []refLine) []string {
	if len(refs) == 0 {
		return nil
	}

	keys := make([]string, len(refs))
	names := make([]string, len(refs))
	oks := make([]bool, len(refs))
	for i, r := range refs {
		keys[i], names[i], oks[i] = r.groupKey()
	}
	var out []string
	for i := 0; i < len(refs); {
		key, ok := keys[i], oks[i]
		j := i + 1
		for ok && j < len(refs) && oks[j] && keys[j] == key {
			j++
		}
		if !ok || j-i < minRefGroup {
			for ; i < j; i++ {
				out = append(out, refs[i].raw)
			}
			continue
		}
		r := refs[i]
		items := append([]string(nil), names[i:j]...)
		var label string
		_, kind, _ := strings.Cut(key, "\x00")
		switch kind {
		case "same":
			label = fmt.Sprintf(" %s %s (%d):", r.flag, r.summary, len(items))
		case "none":
			label = fmt.Sprintf(" %s %s (%d):", r.flag, r.summary, len(items))
		default:
			label = fmt.Sprintf(" %s %s (%d, each -> %s<name>):", r.flag, r.summary, len(items), kind)
		}
		items = capItems(items, maxNames)
		if kind == "same" {
			items = braceRuns(items)
		}
		wrapped := wrapItems("    ", items, " ", statWidth)
		out = append(out, label)
		out = append(out, wrapped...)
		i = j
	}
	return out
}

func groupModes(modes []string) []string {
	var out []string
	for i := 0; i < len(modes); {
		mi := modeLineRe.FindStringSubmatch(modes[i])
		j := i + 1
		var paths []string
		paths = append(paths, mi[3])
		for j < len(modes) {
			mj := modeLineRe.FindStringSubmatch(modes[j])
			if mj[1] != mi[1] || mj[2] != mi[2] {
				break
			}
			paths = append(paths, mj[3])
			j++
		}
		if j-i < minRefGroup {
			out = append(out, modes[i:j]...)
		} else {
			out = append(out, fmt.Sprintf(" %s mode %s (%d):", mi[1], mi[2], j-i))
			out = append(out, wrapItems("    ", capItems(paths, maxNames), "  ", statWidth)...)
		}
		i = j
	}
	return out
}
