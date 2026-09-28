package build

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type mavenFilter struct{}

func (mavenFilter) Name() string { return "maven" }

func (mavenFilter) GuardsErrors() bool { return true }

func (mavenFilter) Match(c *engine.Context) bool {
	n := baseName(c.Name())
	if n != "mvn" && n != "mvnw" && n != "mvnd" {
		return false
	}

	if hasArg(c.Args(), "-v", "--version", "-h", "--help", "-X", "--debug") {
		return false
	}
	for _, a := range c.Args() {
		if strings.HasPrefix(a, "dependency:tree") || strings.HasPrefix(a, "dependency:list") ||
			strings.HasPrefix(a, "help:") || strings.HasPrefix(a, "versions:display") {
			return false
		}
	}
	return true
}

func (mavenFilter) Stream(c *engine.Context) bool {
	for _, a := range c.Args() {
		switch {
		case a == "--":
			return false
		case a == "spring-boot:run", a == "quarkus:dev", a == "jetty:run", a == "jetty:run-war", a == "tomcat7:run",
			a == "tomcat:run", a == "liberty:dev", a == "liberty:run", a == "wildfly:run", a == "micronaut:run",
			strings.HasSuffix(a, ":spring-boot-maven-plugin:run"):
			return true
		}
	}
	return false
}

var (
	mvnLevelRe = lazyre.New(`^\[(INFO|WARNING|WARN|ERROR|DEBUG)\] ?(.*)$`)
	mvnNoiseRe = lazyre.New(`^(?:$|-{3,}.*|--- \S+ \(.*\) @ \S+ ---|Scanning for projects\.\.\.|Building \S.*|  from \S+` +
		`|skip non existing resourceDirectory .*|Copying \d+ resources?.*|Using '.*' encoding to copy filtered .*` +
		`|Changes detected - recompiling the module!.*|Compiling \d+ source files? .*|Nothing to compile.*|No sources to compile` +
		`|Recompiling the module because of .*|Using auto detected provider .*| T E S T S|Running [\w.$]+|Results:` +
		`|Total time: .*|Finished at: .*|Reactor Build Order:|\S+ +\[\w+\]|No tests to run\.|Installing \S+ to \S+` +
		`|Tests run: \d+, Failures: 0, Errors: 0, Skipped: \d+, Time elapsed: .*)$`)
	mvnDownloadRe  = lazyre.New(`^(?:\[INFO\] )?Download(?:ing|ed) from \S+: |^Progress \(\d+\): `)
	mvnReactorHdr  = lazyre.New(`^Reactor Summary(?: for .*)?:$`)
	mvnReactorRow  = lazyre.New(`^\S.*? \.{2,} ?(SUCCESS|FAILURE|SKIPPED)(?: \[.*\])?$`)
	mvnClassEndRe  = lazyre.New(`^Tests run: \d+, Failures: (\d+), Errors: (\d+), Skipped: \d+, Time elapsed: .*(?:--)? in \S+$`)
	mvnRunningRe   = lazyre.New(`^Running [\w.$]+$`)
	mvnHelpFooter  = lazyre.New(`^(?:-> \[Help \d+\]|To see the full stack trace of the errors, re-run Maven with the -e switch\.|Re-run Maven using the -X switch to enable full debug logging\.|For more information about the errors and possible solutions, please read the following articles:|\[Help \d+\] https?://\S+|Please refer to dump files \(if any exist\) .*|)$`)
	mvnTestClassRe = lazyre.New(`^Tests run: `)
)

func (mavenFilter) Apply(c *engine.Context, out string) (string, bool) {
	lines := splitLines(out)
	if len(lines) == 0 {
		return "", false
	}
	levels := 0
	for _, ln := range lines {
		if strings.HasPrefix(ln, "[INFO]") || strings.HasPrefix(ln, "[ERROR]") || strings.HasPrefix(ln, "[WARNING]") {
			levels++
		}
	}
	if levels == 0 {
		return "", false
	}
	exempt := make([]bool, len(lines))
	var (
		out2              []string
		seg               []string
		info, downloads   int
		help, passOut     int
		warnCount         = map[string]int{}
		inClass           bool
		classStart        int
		classLines        []int
		reactorHdr        string
		reactorBad        bool
		reactorRowsHidden int
	)
	flush := func() {
		if len(seg) == 0 {
			return
		}
		out2 = append(out2, genericLines(c, seg)...)
		seg = nil
	}
	var (
		groups   = map[string]*javacGroup{}
		groupSeq []*javacGroup
		shownErr = map[string]int{}
		errCache = map[string]bool{}
	)
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		m := mvnLevelRe.FindStringSubmatch(ln)
		if m == nil {
			if mvnDownloadRe.MatchString(ln) {
				downloads++
				exempt[i] = true
				continue
			}
			seg = append(seg, ln)
			if inClass {
				classLines = append(classLines, i)
			}
			continue
		}
		flush()
		level, msg := m[1], m[2]
		switch level {
		case "INFO":
			switch {
			case strings.HasPrefix(msg, "Download") && mvnDownloadRe.MatchString(msg):
				downloads++
				exempt[i] = true
			case strings.HasPrefix(msg, "Running ") && mvnRunningRe.MatchString(msg):
				inClass, classStart, classLines = true, len(out2), nil
				info++
				exempt[i] = true
			case passingClass(msg):
				if inClass {
					out2, passOut = endPassingClass(lines, exempt, out2, classStart, classLines, passOut)
					inClass = false
				}
				info++
				exempt[i] = true
			case strings.HasPrefix(msg, "Reactor Summary") && mvnReactorHdr.MatchString(msg):
				reactorHdr = ln
				out2 = append(out2, ln)
			case strings.Contains(msg, " ..") && mvnReactorRow.MatchString(msg):
				row := mvnReactorRow.FindStringSubmatch(msg)
				if row[1] == "SUCCESS" {
					reactorRowsHidden++
					exempt[i] = true
					continue
				}
				reactorBad = true
				out2 = append(out2, ln)
			case mvnNoiseRe.MatchString(msg) && !engine.IsError(msg):
				info++
				exempt[i] = true
			default:
				out2 = append(out2, ln)
			}
		case "WARNING", "WARN":
			if msg == "" {
				continue
			}
			if inClass && passingClass(msg) {

				out2, passOut = endPassingClass(lines, exempt, out2, classStart, classLines, passOut)
				inClass = false
			}

			if jm := mvnJavacRe.FindStringSubmatch(msg); jm != nil && !isErrCached(errCache, "[WARNING] "+jm[1]+":[1,1] "+jm[3]) {

				end := javacDetailsEnd(lines, i)
				details := lines[i+1 : end]
				g := groups[jm[3]]
				switch {
				case g != nil && equalLines(details, g.details):
					if warnCount[ln] > 0 {
						warnCount[ln]++
					} else {
						g.locs = append(g.locs, jm[1]+":"+jm[2])
					}

					for k := i; k < end; k++ {
						exempt[k] = true
					}
				case g == nil:
					g = &javacGroup{id: len(groupSeq), details: details}
					groups[jm[3]] = g
					groupSeq = append(groupSeq, g)
					warnCount[ln]++
					out2 = append(out2, ln)
					out2 = append(out2, details...)
					out2 = append(out2, g.sentinel())
				default:

					warnCount[ln]++
					if warnCount[ln] == 1 {
						out2 = append(out2, ln)
						out2 = append(out2, details...)
					}
				}
				i = end - 1
				continue
			}
			warnCount[ln]++
			if warnCount[ln] > 1 {
				continue
			}
			out2 = append(out2, ln)
		case "ERROR":
			if mvnHelpFooter.MatchString(msg) {

				help++
				exempt[i] = true
				continue
			}
			if mvnFrameRe.MatchString(msg) {

				end := i
				for end < len(lines) {
					fm := mvnLevelRe.FindStringSubmatch(lines[end])
					if fm == nil || fm[1] != "ERROR" || !mvnFrameRe.MatchString(fm[2]) {
						break
					}
					end++
				}
				out2 = append(out2, foldPrefixedFrames(c, lines[i:end], exempt[i:end])...)
				i = end - 1
				continue
			}
			if f, seen := shownErr[ln]; seen {

				k := 0
				for i+k < len(lines) && f+k < i && lines[i+k] == lines[f+k] && plainError(lines[i+k]) {
					k++
				}
				if k >= 3 {
					out2 = append(out2, fmt.Sprintf("[lx: %d [ERROR] lines repeated verbatim from above]", k))
					i += k - 1
					continue
				}
			} else {
				shownErr[ln] = i
			}
			if mvnTestClassRe.MatchString(msg) {
				inClass = false
			}
			out2 = append(out2, ln)
		default:
			out2 = append(out2, ln)
		}
	}
	flush()

	kept := out2[:0]
	for _, ln := range out2 {
		if reactorHdr != "" && !reactorBad && ln == reactorHdr {
			reactorHdr = ""
			info++
			continue
		}
		if strings.HasPrefix(ln, javacSentinel) {
			id, _ := strconv.Atoi(ln[len(javacSentinel):])
			if id >= 0 && id < len(groupSeq) && len(groupSeq[id].locs) > 0 {
				kept = append(kept, alsoAt("warning", relLocs(c, groupSeq[id].locs)))
			}
			continue
		}
		if n := warnCount[ln]; n > 1 {
			warnCount[ln] = 0
			ln = withCount(ln, n)
		}
		kept = append(kept, ln)
	}
	out2 = kept
	note := hiddenNote(
		countPart(info, "[INFO] progress line", "[INFO] progress lines"),
		countPart(reactorRowsHidden, "SUCCESS row of the Reactor Summary", "SUCCESS rows of the Reactor Summary"),
		countPart(downloads, "download line", "download lines"),
		countPart(passOut, "line of passing-test output", "lines of passing-test output"),
		countPart(help, "line of Maven's help footer", "lines of Maven's help footer"),
	)
	if note != "" {
		out2 = append([]string{note}, out2...)
	}
	res := strings.Join(collapseBlank(out2), "\n")
	if c.Failed() && !hasErrorLine(res) {
		return "", false
	}
	return selfGuard(lines, func(i int) bool { return exempt[i] }, res), true
}

func passingClass(msg string) bool {
	if !strings.HasPrefix(msg, "Tests run: ") {
		return false
	}
	m := mvnClassEndRe.FindStringSubmatch(msg)
	return m != nil && m[1] == "0" && m[2] == "0"
}

func endPassingClass(lines []string, exempt []bool, out []string, from int, classLines []int, counted int) ([]string, int) {
	out, counted = dropPassingOutput(out, from, counted)
	for _, k := range classLines {
		if !engine.IsError(lines[k]) {
			exempt[k] = true
		}
	}
	return out, counted
}

func dropPassingOutput(out []string, from, counted int) ([]string, int) {
	if from > len(out) {
		return out, counted
	}
	kept := out[:from]
	for _, ln := range out[from:] {
		if engine.IsError(ln) || mvnLevelRe.MatchString(ln) || strings.HasPrefix(ln, javacSentinel) {
			kept = append(kept, ln)
			continue
		}
		if strings.TrimSpace(ln) != "" {
			counted++
		}
	}
	return kept, counted
}

var (
	mvnJavacRe = lazyre.New(`^(\S.*?):(\[\d+,\d+\]) (.+)$`)

	mvnFrameRe = lazyre.New(`^\s*(?:at \S|\.\.\. \d+ (?:more|common frames omitted)$)`)
)

type javacGroup struct {
	id      int
	details []string
	locs    []string
}

const javacSentinel = "\x00lx-javac:"

func (g *javacGroup) sentinel() string { return javacSentinel + strconv.Itoa(g.id) }

func javacDetailsEnd(lines []string, i int) int {
	j := i + 1
	for j < len(lines) && lines[j] != "" && (lines[j][0] == ' ' || lines[j][0] == '\t') && !mvnLevelRe.MatchString(lines[j]) {
		j++
	}
	return j
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func relLocs(c *engine.Context, locs []string) []string {
	out := make([]string, len(locs))
	prev := ""
	for k, loc := range locs {
		i := strings.LastIndex(loc, ":[")
		path, pos := loc[:i], loc[i+1:]
		if path == prev {
			out[k] = pos
			continue
		}
		prev = path
		out[k] = engine.Relativize(c, path) + ":" + pos
	}
	return out
}

func foldPrefixedFrames(c *engine.Context, lines []string, exempt []bool) []string {
	frames := make([]string, len(lines))
	prefix := make([]string, len(lines))
	for k, ln := range lines {
		m := mvnLevelRe.FindStringSubmatchIndex(ln)
		prefix[k], frames[k] = ln[:m[4]], ln[m[4]:]
	}
	folded := engine.FoldStacks(genericCtx(c), frames)
	if len(folded) == len(frames) {
		return lines
	}
	shown := make(map[string]bool, len(folded))
	for _, f := range folded {
		shown[f] = true
	}
	for k, f := range frames {
		if !shown[f] {
			exempt[k] = true
		}
	}
	out := make([]string, len(folded))
	for k, f := range folded {
		out[k] = prefix[0] + f
	}
	return out
}

func plainError(ln string) bool {
	m := mvnLevelRe.FindStringSubmatch(ln)
	return m != nil && m[1] == "ERROR" && !mvnHelpFooter.MatchString(m[2]) && !mvnFrameRe.MatchString(m[2])
}

func isErrCached(cache map[string]bool, ln string) bool {
	v, ok := cache[ln]
	if !ok {
		v = isErrorLine(ln)
		cache[ln] = v
	}
	return v
}
