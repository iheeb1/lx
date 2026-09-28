package infra

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type kubectlGet struct{}

func (kubectlGet) Name() string { return "kubectl-get" }

func (kubectlGet) Match(c *engine.Context) bool {
	k := kubectlCmd(c)
	return len(k) > 0 && (k[0] == "get" || k[0] == "top") && !engine.MachineReadable(c)
}

func (kubectlGet) Stream(c *engine.Context) bool {
	for _, a := range c.Args() {
		if a == "-w" || a == "--watch" || a == "--watch-only" || strings.HasPrefix(a, "--watch=") && a != "--watch=false" {
			return true
		}
	}
	return false
}

var healthyStatus = set("Running", "Completed", "Succeeded", "Ready", "Bound", "Active", "Complete", "Available", "Healthy", "True", "Established")

var (
	readyRe = lazyre.New(`^(\d+)/(\d+)$`)

	recentRestartRe = lazyre.New(`\((?:\d+s|\d+m(?:\d+s)?) ago\)$`)
)

func unhealthy(t *table, r int) bool {
	st, rd := t.col("STATUS"), t.col("READY")
	if rs := t.col("RESTARTS"); rs >= 0 && recentRestartRe.MatchString(t.cells[r][rs]) {
		return true
	}
	notReady := false
	if rd >= 0 {
		if m := readyRe.FindStringSubmatch(t.cells[r][rd]); m != nil && m[1] != m[2] {
			notReady = true
		}
	}
	if st >= 0 {
		s := t.cells[r][st]
		if !healthyStatus[s] {
			return true
		}
		return s == "Running" && notReady
	}
	return notReady
}

func (kubectlGet) Apply(c *engine.Context, out string) (string, bool) {
	lines := strings.Split(out, "\n")
	var res []string
	changed, tables := false, 0
	for bi, blk := range splitBlocks(lines) {
		if bi > 0 {
			res = append(res, "")
		}

		h := 0
		for h < len(blk) {
			if _, ok := parseHeader(blk[h]); ok {
				break
			}
			h++
		}
		if h == len(blk) {
			res = append(res, blk...)
			continue
		}
		res = append(res, blk[:h]...)
		blk = blk[h:]
		t, ok := parseTable(blk)
		if !ok {
			res = append(res, blk...)
			continue
		}
		tables++

		var trailer []string
		for n := len(t.cells); n > 0 && isMessageRow(t, n-1); n-- {
			trailer = append([]string{t.lines[n-1]}, trailer...)
			t.lines, t.cells = t.lines[:n-1], t.cells[:n-1]
		}
		if len(t.cells) <= maxTableRows {
			res = append(res, blk...)
			continue
		}
		changed = true
		res = append(res, t.header)
		res = append(res, t.lines[:keepTableRows]...)
		var bad []int
		problems := 0
		for r := keepTableRows; r < len(t.cells); r++ {
			if unhealthy(t, r) || engine.IsError(t.lines[r]) {
				problems++
				if len(bad) < maxProblemRows {
					bad = append(bad, r)
				}
			}
		}
		what := "the other row follows"
		switch {
		case problems > len(bad):
			what = fmt.Sprintf("the first %d of the %d other rows follow", len(bad), problems)
		case problems == 0:
			what = "no other row"
		case problems != 1:
			what = fmt.Sprintf("the %d other rows follow", problems)
		}
		res = append(res, fmt.Sprintf("[lx: rows %d-%d: %d healthy rows (running and ready with no restart in the last hour, or completed) not shown; %s]",
			keepTableRows+1, len(t.cells), len(t.cells)-keepTableRows-problems, what))
		for _, r := range bad {
			res = append(res, t.lines[r])
		}
		all := make([]int, len(t.cells))
		for i := range all {
			all[i] = i
		}
		if st := t.col("STATUS"); st >= 0 {
			res = append(res, fmt.Sprintf("[lx: %d rows by STATUS: %s]", len(t.cells), countBy(all, func(r int) string { return t.cells[r][st] })))
		} else if t.col("READY") >= 0 {
			res = append(res, fmt.Sprintf("[lx: %d rows: %s]", len(t.cells), countBy(all, func(r int) string {
				if unhealthy(t, r) {
					return "not ready"
				}
				return "ready"
			})))
		}
		res = append(res, trailer...)
	}
	if tables == 0 {
		return "", false
	}
	if !changed {
		return out, true
	}
	return strings.Join(res, "\n"), true
}

func isMessageRow(t *table, r int) bool {
	for _, c := range t.cells[r][1:] {
		if c != "" {
			return false
		}
	}
	return engine.Classify(t.lines[r]) != engine.Normal
}

type kubectlEvents struct{}

func (kubectlEvents) Name() string       { return "kubectl-events" }
func (kubectlEvents) GuardsErrors() bool { return true }

func (kubectlEvents) Match(c *engine.Context) bool {
	k := kubectlCmd(c)
	if len(k) == 0 || engine.MachineReadable(c) {
		return false
	}
	if k[0] == "events" {
		return true
	}
	return k[0] == "get" && len(k) > 1 && (k[1] == "events" || k[1] == "event" || k[1] == "ev" || strings.HasPrefix(k[1], "events."))
}

func (kubectlEvents) Stream(c *engine.Context) bool { return kubectlGet{}.Stream(c) }

func eventKey(t *table, r int) string {
	var parts []string

	for _, name := range []string{"NAMESPACE", "TYPE", "REASON", "OBJECT", "SUBOBJECT", "SOURCE", "MESSAGE", "NAME"} {
		if k := t.col(name); k >= 0 {
			parts = append(parts, t.cells[r][k])
		}
	}
	return strings.Join(parts, "\x00")
}

const (
	maxEventRows  = 60
	keepNormalEvs = 20
)

func (kubectlEvents) Apply(c *engine.Context, out string) (string, bool) {
	lines := strings.Split(out, "\n")
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	t, ok := parseTable(lines[start:end])
	if !ok || t.col("TYPE") < 0 || t.col("REASON") < 0 || t.col("MESSAGE") < 0 {
		return "", false
	}
	typ, reason := t.col("TYPE"), t.col("REASON")
	rows, others := mergeLatest(t, func(r int) string { return eventKey(t, r) })
	count := func(r int) int { return len(others[r]) + 1 }
	distinct := len(rows)

	var hiddenNormal []int
	if len(rows) > maxEventRows {
		var normal []int
		for _, r := range rows {
			if t.cells[r][typ] == "Normal" {
				normal = append(normal, r)
			}
		}
		if len(normal) > keepNormalEvs {
			hiddenNormal = normal[:len(normal)-keepNormalEvs]
			hide := map[int]bool{}
			for _, r := range hiddenNormal {
				hide[r] = true
			}
			var kept []int
			for _, r := range rows {
				if !hide[r] {
					kept = append(kept, r)
				}
			}
			rows = kept
		}
	}
	if len(rows) == len(t.cells) {
		return out, true
	}
	res := append([]string(nil), lines[:start]...)
	res = append(res, t.header)
	for _, r := range rows {
		res = append(res, t.lines[r]+mergedNote(t, others[r]))
	}
	if len(hiddenNormal) > 0 {
		n := 0
		for _, r := range hiddenNormal {
			n += count(r)
		}
		res = append(res, fmt.Sprintf("[lx: %s of earlier-listed Normal events not shown, by reason: %s]", engine.Plural(n, "row", "rows"),
			countByWeighted(hiddenNormal, count, func(r int) string { return t.cells[r][reason] })))
	}
	merged := len(t.cells) - distinct
	if merged > 0 {
		res = append(res, fmt.Sprintf("[lx: %d repeated event rows (same type, reason, object and message) merged into the last such row, marked ×N with the other rows' ages]", merged))
	}
	return strings.Join(res, "\n"), true
}

func countByWeighted(rows []int, weight func(int) int, key func(int) string) string {
	var expanded []int
	for _, r := range rows {
		for range weight(r) {
			expanded = append(expanded, r)
		}
	}
	return countBy(expanded, key)
}

const maxListedAges = 4

func mergedNote(t *table, others []int) string {
	if len(others) == 0 {
		return ""
	}
	age := t.col("LAST SEEN")
	if age < 0 {
		age = t.col("AGE")
	}
	if age < 0 {
		return fmt.Sprintf(" [×%d rows]", len(others)+1)
	}
	ages := make([]string, 0, len(others))
	for _, r := range others {
		a := t.cells[r][age]
		if a == "" {
			a = "?"
		}
		ages = append(ages, a)
	}
	if len(ages) > maxListedAges {
		ages = append(ages[:2:2], "…", ages[len(ages)-1])
	}
	return fmt.Sprintf(" [×%d rows; others: %s]", len(others)+1, strings.Join(ages, ", "))
}

type kubectlDescribe struct{}

func (kubectlDescribe) Name() string       { return "kubectl-describe" }
func (kubectlDescribe) GuardsErrors() bool { return true }

func (kubectlDescribe) Match(c *engine.Context) bool {
	k := kubectlCmd(c)
	return len(k) > 0 && k[0] == "describe"
}

var (
	describeDrop = set("Volumes", "Tolerations", "QoS Class", "Node-Selectors", "Container ID", "Image ID",
		"Mounts", "Host Port", "Host Ports", "managedFields")
	describeKeyRe = lazyre.New(`^(\s*)([A-Za-z][\w .()/-]*?):(?:\s|$)`)
	lastAppliedRe = lazyre.New(`kubectl\.kubernetes\.io/last-applied-configuration:.*$`)
)

func indentOf(s string) int { return len(s) - len(strings.TrimLeft(s, " ")) }

func (kubectlDescribe) Apply(c *engine.Context, out string) (string, bool) {
	lines := strings.Split(out, "\n")
	named := false
	for _, ln := range lines {
		if strings.HasPrefix(ln, "Name:") {
			named = true
			break
		}
	}
	if !named {
		return "", false
	}

	drop := describeDrop
	if needed := describeNeeded(out); len(needed) > 0 {
		drop = map[string]bool{}
		for k := range describeDrop {
			if !needed[k] {
				drop[k] = true
			}
		}
	}
	var res []string
	dropped := map[string]int{}
	var dropOrder []string
	merged := 0
	note := func(k string) {
		if dropped[k] == 0 {
			dropOrder = append(dropOrder, k)
		}
		dropped[k]++
	}
	for i := 0; i < len(lines); {
		ln := lines[i]
		ind := indentOf(ln)

		j := i + 1
		for j < len(lines) && strings.TrimSpace(lines[j]) != "" && indentOf(lines[j]) > ind {
			j++
		}
		if m := describeKeyRe.FindStringSubmatch(ln); m != nil && drop[m[2]] && !hasErrorLine(lines[i:j]) {
			note(m[2])
			i = j
			continue
		}
		if loc := lastAppliedRe.FindStringIndex(ln); loc != nil && !hasErrorLine(lines[i:j]) {

			res = append(res, ln[:loc[0]]+"kubectl.kubernetes.io/last-applied-configuration: [lx: not shown]")
			note("last-applied-configuration")
			i = j
			continue
		}
		if m := describeKeyRe.FindStringSubmatch(ln); m != nil && m[2] == "Events" && j > i+1 {
			ev, n := mergeEvents(lines[i+1 : j])
			res = append(append(res, ln), ev...)
			merged += n
			i = j
			continue
		}
		res = append(res, ln)
		i++
	}
	if len(dropOrder) > 0 {
		parts := make([]string, len(dropOrder))
		for i, k := range dropOrder {
			parts[i] = k
			if n := dropped[k]; n > 1 {
				parts[i] = fmt.Sprintf("%s ×%d", k, n)
			}
		}
		res = append(res, "[lx: not shown: "+strings.Join(parts, ", ")+"]")
	}
	if merged > 0 {
		res = append(res, fmt.Sprintf("[lx: %s (same type, reason, source and message) merged into the last such row, marked ×N with the other rows' ages]",
			engine.Plural(merged, "repeated event row", "repeated event rows")))
	}
	return strings.TrimRight(strings.Join(res, "\n"), "\n"), true
}

func describeNeeded(out string) map[string]bool {
	needed := map[string]bool{}
	for _, r := range []struct {
		words  []string
		blocks []string
	}{
		{[]string{"FailedMount", "FailedAttachVolume", "MountVolume", "VolumeMount"}, []string{"Volumes", "Mounts"}},
		{[]string{"FailedScheduling", "untolerated taint", "node affinity/selector", "didn't match Pod's node"}, []string{"Tolerations", "Node-Selectors"}},
		{[]string{"Evicted", "OOMKilled"}, []string{"QoS Class"}},
	} {
		for _, w := range r.words {
			if strings.Contains(out, w) {
				for _, b := range r.blocks {
					needed[b] = true
				}
				break
			}
		}
	}
	return needed
}

func mergeEvents(block []string) ([]string, int) {
	if len(block) < 3 {
		return block, 0
	}
	ind := indentOf(block[0])
	trimmed := make([]string, 0, len(block))
	var sep []int
	for i, ln := range block {
		if strings.Trim(ln, " -") == "" && strings.Contains(ln, "--") {
			sep = append(sep, i)
			continue
		}
		if indentOf(ln) < ind {
			return block, 0
		}
		trimmed = append(trimmed, ln[ind:])
	}
	if len(trimmed) < 2 {
		return block, 0
	}

	upper := append([]string{strings.ToUpper(trimmed[0])}, trimmed[1:]...)
	t, ok := parseTable(upper)
	if !ok || t.col("TYPE") < 0 || t.col("REASON") < 0 || t.col("MESSAGE") < 0 {
		return block, 0
	}
	rows, others := mergeLatest(t, func(r int) string { return eventKey(t, r) + "\x00" + cellOf(t, r, "FROM") })
	if len(rows) == len(t.cells) {
		return block, 0
	}
	pad := strings.Repeat(" ", ind)
	out := []string{pad + trimmed[0]}
	for _, s := range sep {
		out = append(out, block[s])
	}
	for _, r := range rows {
		out = append(out, pad+t.lines[r]+mergedNote(t, others[r]))
	}
	return out, len(t.cells) - len(rows)
}

func mergeLatest(t *table, key func(int) string) ([]int, map[int][]int) {
	keys := make([]string, len(t.cells))
	last := map[string]int{}
	for r := range t.cells {
		keys[r] = key(r)
		last[keys[r]] = r
	}
	others := map[int][]int{}
	var rows []int
	for r := range t.cells {
		l := last[keys[r]]
		if l == r {
			rows = append(rows, r)
		} else {
			others[l] = append(others[l], r)
		}
	}
	return rows, others
}

func cellOf(t *table, r int, name string) string {
	if k := t.col(name); k >= 0 {
		return t.cells[r][k]
	}
	return ""
}
