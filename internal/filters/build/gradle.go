package build

import (
	"github.com/iheeb1/lx/internal/lazyre"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type gradleFilter struct{}

func (gradleFilter) Name() string { return "gradle" }

func (gradleFilter) GuardsErrors() bool { return true }

func (gradleFilter) Match(c *engine.Context) bool {
	n := baseName(c.Name())
	if n != "gradle" && n != "gradlew" {
		return false
	}

	for _, a := range c.Args() {
		switch a {
		case "tasks", "dependencies", "dependencyInsight", "properties", "projects", "help", "--help", "-h",
			"--version", "-v", "--scan", "--debug", "-d":
			return false
		}
		if strings.HasSuffix(a, ":dependencies") || strings.HasSuffix(a, ":tasks") || a == "--console=rich" || a == "--console=verbose" {
			return false
		}
	}
	return true
}

func (gradleFilter) Stream(c *engine.Context) bool {
	for _, a := range c.Args() {
		if a == "--" {
			break
		}
		if a == "--continuous" || a == "-t" {
			return true
		}
		task := a[strings.LastIndexByte(a, ':')+1:]
		switch task {
		case "run", "bootRun", "quarkusDev", "appRun", "appStart", "jettyRun", "tomcatRun", "runServer", "runClient":
			return true
		}
	}
	return false
}

var (
	gradleTaskRe    = lazyre.New(`^> (?:Task|Configure project) (:\S*)(?: (UP-TO-DATE|NO-SOURCE|FROM-CACHE|SKIPPED|FAILED))?$`)
	gradleNoiseRe   = lazyre.New(`^(?:Download(?:ing)? https?://\S+.*|Starting a Gradle Daemon.*|Reusing configuration cache\.|Configuration cache entry (?:stored|reused)\.?.*|Calculating task graph as .*|<-*> \d+% .*|Daemon will be stopped at the end of the build.*)$`)
	gradleTestRe    = lazyre.New(`^\S.* > .+ (PASSED|FAILED|SKIPPED)$`)
	gradleDeprHelp  = lazyre.New(`^(?:You can use '--warning-mode all' to show the individual deprecation warnings.*|For more on this, please refer to https://docs\.gradle\.org/\S+ in the Gradle documentation\.)$`)
	gradleVerdictRe = lazyre.New(`^(?:BUILD (?:SUCCESSFUL|FAILED) in |\d+ actionable tasks?: |FAILURE: |\* What went wrong:|\* Exception is:|\d+ tests? completed, )`)
)

func (gradleFilter) Apply(c *engine.Context, out string) (string, bool) {
	lines := splitLines(out)
	if len(lines) == 0 {
		return "", false
	}
	recognized := false
	for _, ln := range lines {
		if gradleTaskRe.MatchString(ln) || gradleVerdictRe.MatchString(ln) {
			recognized = true
			break
		}
	}
	if !recognized {
		return "", false
	}
	exempt := make([]bool, len(lines))
	var (
		res                         []string
		seg                         []string
		tasks, noise, passed, tries int
		status                      counter
	)
	flush := func() {
		if len(seg) == 0 {
			return
		}
		res = append(res, genericLines(c, seg)...)
		seg = nil
	}

	nextOutput := func(i int) bool {
		for j := i + 1; j < len(lines); j++ {
			ln := lines[j]
			switch {
			case strings.TrimSpace(ln) == "":
				continue
			case gradleTaskRe.MatchString(ln), gradleVerdictRe.MatchString(ln), gradleNoiseRe.MatchString(ln):
				return false
			}
			return true
		}
		return false
	}
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		if m := gradleTaskRe.FindStringSubmatch(ln); m != nil {
			if m[2] == "FAILED" || nextOutput(i) {
				flush()
				res = append(res, ln)
				continue
			}
			tasks++
			if m[2] != "" {
				status.add(m[2], 1)
			} else {
				status.add("executed", 1)
			}
			exempt[i] = true
			continue
		}
		if gradleNoiseRe.MatchString(ln) && !engine.IsError(ln) {

			noise++
			exempt[i] = true
			continue
		}
		if gradleDeprHelp.MatchString(ln) {
			tries++
			exempt[i] = true
			continue
		}
		if m := gradleTestRe.FindStringSubmatch(ln); m != nil && m[1] == "PASSED" {
			passed++
			exempt[i] = true
			continue
		}
		if ln == "* Try:" {

			j := i + 1
			for j < len(lines) && strings.HasPrefix(lines[j], "> ") && !engine.IsError(lines[j]) {
				j++
			}
			for k := i; k < j; k++ {
				exempt[k] = true
			}
			tries += j - i
			i = j - 1
			continue
		}
		if strings.HasPrefix(ln, "* Get more help at ") {
			tries++
			exempt[i] = true
			continue
		}
		seg = append(seg, ln)
	}
	flush()
	var parts []string
	if tasks > 0 {
		parts = append(parts, engine.Plural(tasks, "task line", "task lines")+" ("+status.String()+")")
	}
	parts = append(parts,
		countPart(passed, "passing test", "passing tests"),
		countPart(noise, "download/daemon line", "download/daemon lines"),
		countPart(tries, "line of Gradle's help text (* Try:, --warning-mode)", "lines of Gradle's help text (* Try:, --warning-mode)"),
	)
	res = collapseBlank(res)
	if note := hiddenNote(parts...); note != "" {
		res = append([]string{note}, res...)
	}
	view := strings.Join(res, "\n")
	if c.Failed() && !hasErrorLine(view) {
		return "", false
	}
	return selfGuard(lines, func(i int) bool { return exempt[i] }, view), true
}
