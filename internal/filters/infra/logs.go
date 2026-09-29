package infra

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type logsFilter struct{}

func (logsFilter) Name() string { return "logs" }

func (logsFilter) GuardsErrors() bool { return true }

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
	if t, ok := engine.TemplateLogsFor(c, tagged); ok {
		for i, ln := range t {
			if klog > 0 && len(ln) > 4 && ln[0] == '[' && ln[2] == ']' && ln[3] == ' ' && klogRe.MatchString(ln[4:]) {
				ln = ln[4:]
			}
			if j := strings.Index(ln, "] ["); klog > 0 && strings.HasPrefix(ln, "[×") && j > 0 && len(ln) > j+6 && ln[j+4] == ']' && ln[j+5] == ' ' && strings.IndexByte("IWEF", ln[j+3]) >= 0 {
				ln = ln[:j+2] + ln[j+6:]
			}
			if engine.Classify(ln) == engine.Normal {
				ln = engine.ShortenLine(ln, 400)
			}
			t[i] = ln
		}
		return strings.Join(t, "\n"), true
	}
	folded := engine.CollapseRuns(lines)
	folded = engine.FoldStacks(c, folded)
	folded = engine.CollapseSimilar(folded)
	folded = engine.JudgeChunks(c, folded)
	if len(folded) == len(lines) {
		return out, true
	}
	note := fmt.Sprintf("[lx: %d log lines; repeated lines and library stack frames folded]", len(lines))
	res, _ := engine.Guard(out, note+"\n"+strings.TrimRight(strings.Join(folded, "\n"), "\n"))
	return res, true
}
