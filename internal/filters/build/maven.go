package build

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// mavenFilter renders mvn / mvnw output:
//
//   - [INFO] chatter (separators, "--- plugin:ver:goal (id) @ module ---"
//     banners, "Building …", resource/compiler progress, per-class
//     "Running X" / passing "Tests run: …, Time elapsed … -- in X", Total
//     time / Finished at, the reactor build order, SUCCESS rows of the
//     Reactor Summary) and "Downloading/Downloaded from" lines are counted;
//   - every other [INFO] line (BUILD SUCCESS/FAILURE, the final "Tests run:"
//     totals, "N errors", plugin messages lx does not know) is kept;
//   - every [WARNING] line is kept (identical repeats as one line + [×N]),
//     except that a javac warning ("[WARNING] /x/A.java:[12,8] msg" and its
//     indented detail lines) repeating the message and details of an
//     earlier one adds its location to that one's "[lx: same warning at N
//     more locations: …]" list; error-class warnings are never merged;
//   - every [ERROR] line is kept, except Maven's constant help footer
//     ("-> [Help 1]", "To see the full stack trace…", "Re-run Maven using
//     the -X switch…", the [Help 1] link), which says nothing about the
//     failure, library stack frames printed under the [ERROR] prefix
//     (folded with engine.FoldStacks, application frames kept), and a run
//     of 3+ [ERROR] lines repeating line for line a run shown above
//     (surefire prints a fork crash twice), replaced by a count; "After
//     correcting the problems, you can resume the build with … -rf :module"
//     is kept;
//   - lines without a level (exceptions, javac "symbol:"/"location:"
//     lines, test output) are kept with stack traces folded, except the
//     stdout of test classes that passed, which is counted.
type mavenFilter struct{}

func (mavenFilter) Name() string { return "maven" }

// GuardsErrors: the help footer lines are error-class ("…the errors…")
// and dropped on purpose; so are reactor rows of modules named like
// "error-handling ... SUCCESS" and library frames under the [ERROR] prefix
// (error-class by the level word only). The filter guards every other line.
func (mavenFilter) GuardsErrors() bool { return true }

func (mavenFilter) Match(c *engine.Context) bool {
	n := baseName(c.Name())
	if n != "mvn" && n != "mvnw" && n != "mvnd" {
		return false
	}
	// Help, version and the dependency/help plugin goals print data.
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

// Stream: goals that start the application or a dev server (spring-boot:run,
// quarkus:dev, jetty:run …) do not finish on their own; buffering them would
// hang the caller.
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
		seg               []string // unlevelled lines pending
		info, downloads   int
		help, passOut     int
		warnCount         = map[string]int{} // warning line → occurrences
		inClass           bool               // between "Running X" and its "Tests run:" line
		classStart        int                // index in out2 where the class's output starts
		classLines        []int
		reactorHdr        string // the Reactor Summary header line, if any
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
		groups   = map[string]*javacGroup{} // javac warning message → its first occurrence
		groupSeq []*javacGroup
		shownErr = map[string]int{}  // [ERROR] line → index of its first occurrence
		errCache = map[string]bool{} // engine.IsError of javac warning lines
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
				// Skipped tests but no failure: the line stays, the
				// class's output goes.
				out2, passOut = endPassingClass(lines, exempt, out2, classStart, classLines, passOut)
				inClass = false
			}
			// The position cannot change how a javac line classifies (it
			// sits between ".java:" and the message), so one call covers
			// every occurrence of a message in a file.
			if jm := mvnJavacRe.FindStringSubmatch(msg); jm != nil && !isErrCached(errCache, "[WARNING] "+jm[1]+":[1,1] "+jm[3]) {
				// A javac warning and its indented detail lines.
				end := javacDetailsEnd(lines, i)
				details := lines[i+1 : end]
				g := groups[jm[3]]
				switch {
				case g != nil && equalLines(details, g.details):
					if warnCount[ln] > 0 {
						warnCount[ln]++ // an exact repeat: counted on its first line
					} else {
						g.locs = append(g.locs, jm[1]+":"+jm[2])
					}
					// Not error-class (checked above), and its detail lines
					// are the shown ones: nothing for the guard to look at.
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
					// Same message, other details: shown in full.
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
				continue // counted on its first occurrence
			}
			out2 = append(out2, ln)
		case "ERROR":
			if mvnHelpFooter.MatchString(msg) {
				// Maven's constant help footer: identical in every failed
				// build, nothing about this failure.
				help++
				exempt[i] = true
				continue
			}
			if mvnFrameRe.MatchString(msg) {
				// "[ERROR] \tat org.apache.maven…" — a stack trace Maven
				// prints under its own level prefix (surefire fork crashes,
				// plugin exceptions): library frames are folded as the
				// generic reducer folds unprefixed ones.
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
				// Surefire prints a fork crash twice (the message, then the
				// exception wrapping it): a run of 3+ [ERROR] lines repeating,
				// line for line, a run shown above is replaced by a count.
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
				inClass = false // a failing class keeps its output
			}
			out2 = append(out2, ln)
		default: // DEBUG
			out2 = append(out2, ln)
		}
	}
	flush()
	// Indexes into out2 shift when a passing class's output is dropped, so
	// the reactor header, warning counts and javac location lists are
	// resolved by text.
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
		return "", false // killed mid-build: the generic reducer keeps the tail
	}
	return selfGuard(lines, func(i int) bool { return exempt[i] }, res), true
}

// passingClass reports surefire's per-class line for a class without
// failures or errors.
func passingClass(msg string) bool {
	if !strings.HasPrefix(msg, "Tests run: ") {
		return false
	}
	m := mvnClassEndRe.FindStringSubmatch(msg)
	return m != nil && m[1] == "0" && m[2] == "0"
}

// endPassingClass drops what a passing test class printed (error-like lines
// stay) and marks those lines exempt from the guard.
func endPassingClass(lines []string, exempt []bool, out []string, from int, classLines []int, counted int) ([]string, int) {
	out, counted = dropPassingOutput(out, from, counted)
	for _, k := range classLines {
		if !engine.IsError(lines[k]) {
			exempt[k] = true
		}
	}
	return out, counted
}

// dropPassingOutput removes the unlevelled lines a passing test class
// printed (out[from:]), keeping error-like ones.
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
	// mvnJavacRe: a javac diagnostic as the compiler plugin prints it after
	// the level: "/x/A.java:[12,8] [unchecked] unchecked conversion".
	mvnJavacRe = lazyre.New(`^(\S.*?):(\[\d+,\d+\]) (.+)$`)
	// mvnFrameRe: a stack frame (or "... N more") printed after a level.
	mvnFrameRe = lazyre.New(`^\s*(?:at \S|\.\.\. \d+ (?:more|common frames omitted)$)`)
)

// javacGroup is the first occurrence of a javac warning message; later
// occurrences with the same detail lines only add their location.
type javacGroup struct {
	id      int
	details []string
	locs    []string
}

// javacSentinel starts the placeholder line that becomes a group's
// "[lx: same warning at N more locations: …]" marker (or nothing).
const javacSentinel = "\x00lx-javac:"

func (g *javacGroup) sentinel() string { return javacSentinel + strconv.Itoa(g.id) }

// javacDetailsEnd returns the end of the indented, unlevelled detail lines
// ("  symbol:   method helper(int)", "  missing type arguments …") that
// follow a javac diagnostic at lines[i].
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

// relLocs renders "path:[l,c]" locations with paths under the working
// directory made relative, and a path equal to the previous one left out.
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

// foldPrefixedFrames folds the library frames of a stack trace printed
// under Maven's "[ERROR] " prefix with engine.FoldStacks, keeping the prefix
// on every line it keeps (the fold marker included), and marks the frames
// it folded exempt from the self-guard: the level word makes every frame
// error-class, but a library frame is not an error report.
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

// plainError reports an [ERROR] line that is shown as it is: not empty, not
// Maven's help footer, not a stack frame.
func plainError(ln string) bool {
	m := mvnLevelRe.FindStringSubmatch(ln)
	return m != nil && m[1] == "ERROR" && !mvnHelpFooter.MatchString(m[2]) && !mvnFrameRe.MatchString(m[2])
}

// isErrCached is engine.IsError memoized in cache (by line text).
func isErrCached(cache map[string]bool, ln string) bool {
	v, ok := cache[ln]
	if !ok {
		v = isErrorLine(ln)
		cache[ln] = v
	}
	return v
}
