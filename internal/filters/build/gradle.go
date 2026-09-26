package build

import (
	"regexp"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// gradleFilter renders gradle / gradlew output:
//
//   - "> Task :x UP-TO-DATE|NO-SOURCE|FROM-CACHE|SKIPPED" lines and task
//     headers that printed nothing are counted; "> Task :x FAILED" and a
//     header followed by the task's output (compiler errors, warnings) are
//     kept;
//   - download lines, daemon/configuration-cache chatter, the "* Try:"
//     section, "* Get more help at" and the deprecation help sentences are
//     counted;
//   - "FAILURE: …", the whole "* What went wrong:" section (and "*
//     Exception is:" with its folded stack trace), test failure lines
//     ("CalcTest > adds() FAILED" and their indented detail), "N tests
//     completed, M failed", "BUILD SUCCESSFUL/FAILED in …" and "N actionable
//     tasks: …" are kept, as is every unknown line (generic reducer).
type gradleFilter struct{}

func (gradleFilter) Name() string { return "gradle" }

// GuardsErrors: counted lines are status/progress lines that can hold error
// words in task or file names ("> Task :errorprone UP-TO-DATE", test classes
// named ErrorHandlerTest > …() PASSED). The filter guards every other line.
func (gradleFilter) GuardsErrors() bool { return true }

func (gradleFilter) Match(c *engine.Context) bool {
	n := baseName(c.Name())
	if n != "gradle" && n != "gradlew" {
		return false
	}
	// Task listings, dependency reports, help and version print data;
	// --console=rich/verbose changes the shape. Continuous builds and
	// application runs are matched so that Stream can claim them.
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

// Stream: a continuous build (--continuous, -t) and the tasks that run the
// application or a dev server (run, bootRun, quarkusDev, appRun, jettyRun …)
// do not finish on their own; buffering them would hang the caller.
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
	gradleTaskRe    = regexp.MustCompile(`^> (?:Task|Configure project) (:\S*)(?: (UP-TO-DATE|NO-SOURCE|FROM-CACHE|SKIPPED|FAILED))?$`)
	gradleNoiseRe   = regexp.MustCompile(`^(?:Download(?:ing)? https?://\S+.*|Starting a Gradle Daemon.*|Reusing configuration cache\.|Configuration cache entry (?:stored|reused)\.?.*|Calculating task graph as .*|<-*> \d+% .*|Daemon will be stopped at the end of the build.*)$`)
	gradleTestRe    = regexp.MustCompile(`^\S.* > .+ (PASSED|FAILED|SKIPPED)$`)
	gradleDeprHelp  = regexp.MustCompile(`^(?:You can use '--warning-mode all' to show the individual deprecation warnings.*|For more on this, please refer to https://docs\.gradle\.org/\S+ in the Gradle documentation\.)$`)
	gradleVerdictRe = regexp.MustCompile(`^(?:BUILD (?:SUCCESSFUL|FAILED) in |\d+ actionable tasks?: |FAILURE: |\* What went wrong:|\* Exception is:|\d+ tests? completed, )`)
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
	// nextOutput reports whether a task header is followed by output of its
	// own before the next header / verdict.
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
			// An error-class line in one of these shapes ("Download
			// https://… failed: 403") is a report, not chatter: it stays.
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
			// Generic advice (--stacktrace, --info, --scan, help link).
			j := i + 1
			for j < len(lines) && strings.HasPrefix(lines[j], "> ") && !engine.IsError(lines[j]) {
				j++ // an error-class line is a report, not advice: it ends the block
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
		return "", false // killed mid-build: the generic reducer keeps the tail
	}
	return selfGuard(lines, func(i int) bool { return exempt[i] }, view), true
}
