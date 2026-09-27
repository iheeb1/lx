package infra

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// logsFilter condenses docker logs, docker compose logs, kubectl logs and
// journalctl with engine.TemplateLogs: repeated lines become one template
// with a count and a summary of their variable parts, while every record
// holding an error-class line is kept verbatim (stack traces folded by
// engine.FoldStacks), each distinct record once with its count.
//
// Output that is not log-shaped (fewer than 40 lines, or too few lines with
// a timestamp or level) is only folded: identical consecutive lines are
// collapsed with a count, stack traces are folded and runs of lines that
// differ only in numbers are collapsed. lx never adds --tail or --since,
// and followers (-f) are streamed, never buffered.
type logsFilter struct{}

func (logsFilter) Name() string { return "logs" }

func (logsFilter) Match(c *engine.Context) bool {
	if engine.MachineReadable(c) {
		return false
	}
	if dockerSub(c, "logs", "container logs", "compose logs", "service logs") {
		return true
	}
	if k := kubectlCmd(c); len(k) > 0 && k[0] == "logs" {
		return true
	}
	return c.Name() == "journalctl" && !journalMachine(c)
}

// journalMachine: -o json/json-pretty/json-sse/export/cat-less formats that
// are data, not text logs.
func journalMachine(c *engine.Context) bool {
	args := c.Args()
	for i, a := range args {
		v := ""
		switch {
		case (a == "-o" || a == "--output") && i+1 < len(args):
			v = args[i+1]
		case strings.HasPrefix(a, "--output="):
			v = strings.TrimPrefix(a, "--output=")
		case strings.HasPrefix(a, "-o") && len(a) > 2:
			v = strings.TrimPrefix(a[2:], "=")
		}
		if strings.HasPrefix(v, "json") || v == "export" {
			return true
		}
	}
	return false
}

func (logsFilter) Stream(c *engine.Context) bool {
	if c.Name() == "journalctl" {
		return isFollow(c.Args(), "unptoSUDMgFbic")
	}
	if k := kubectlCmd(c); len(k) > 0 && k[0] == "logs" {
		return isFollow(argsAfter(c.Args(), "logs"), "clLnp")
	}
	if dockerSub(c, "logs", "container logs", "compose logs", "service logs") {
		return isFollow(argsAfter(c.Args(), "logs"), "n")
	}
	return false
}

// klogRe matches the header of klog lines ("I0926 10:00:00.000000 1
// file.go:141] msg"), the format of Kubernetes components and many Go
// services. engine.TemplateLogs does not recognize it as a log line (no
// ISO timestamp, no level word), so such lines get a temporary "[I] "
// level tag for templating, removed again from every output line.
var klogRe = lazyre.New(`^([IWEF])\d{4} \d{2}:\d{2}:\d{2}\.\d{6}\s+\d+ \S+:\d+\] `)

func (logsFilter) Apply(c *engine.Context, out string) (string, bool) {
	lines := strings.Split(out, "\n")
	tagged := lines
	klog := 0
	for _, ln := range lines {
		if klogRe.MatchString(ln) {
			klog++
		}
	}
	if klog*2 > len(lines) {
		tagged = make([]string, len(lines))
		for i, ln := range lines {
			tagged[i] = ln
			if m := klogRe.FindStringSubmatch(ln); m != nil {
				tagged[i] = "[" + m[1] + "] " + ln
			}
		}
	}
	if t, ok := engine.TemplateLogs(tagged); ok {
		for i, ln := range t {
			if klog > 0 && len(ln) > 4 && ln[0] == '[' && ln[2] == ']' && ln[3] == ' ' && klogRe.MatchString(ln[4:]) {
				ln = ln[4:]
			}
			if engine.Classify(ln) == engine.Normal { // error and warning lines stay whole
				ln = engine.ShortenLine(ln, 400)
			}
			t[i] = ln
		}
		return strings.Join(t, "\n"), true
	}
	folded := engine.CollapseRuns(lines)
	folded = engine.FoldStacks(c, folded)
	folded = engine.CollapseSimilar(folded)
	if len(folded) == len(lines) {
		return out, true
	}
	note := fmt.Sprintf("[lx: %d log lines; repeated lines and library stack frames folded]", len(lines))
	return note + "\n" + strings.TrimRight(strings.Join(folded, "\n"), "\n"), true
}
