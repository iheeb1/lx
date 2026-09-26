package build

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

// cargoFilter renders cargo build|check|clippy|install|fetch|update:
//
//   - status lines (Compiling, Checking, Downloaded, Fresh, Updating the
//     index, Locking, Adding …) are counted in one "[lx: hidden: …]" line;
//     "Finished", "Installed", "Replaced" and cargo update's version changes
//     are kept;
//   - rustc diagnostics: every error block ("error[E0308]: …", " --> file",
//     the code lines, "= help:"/"= note:") is kept whole; warnings are
//     grouped per lint (message with `names` masked): the first two in full,
//     further ones as header + location, repeats of an identical warning as
//     a count or a list of locations; "= note: `#[warn(…)]` on by default"
//     is dropped;
//   - "warning: `x` (lib) generated N warnings", "error: could not compile
//     …" and every other line are kept (unknown lines through the generic
//     reducer).
type cargoFilter struct{}

func (cargoFilter) Name() string { return "cargo" }

// GuardsErrors: hidden status lines are error-class when a crate name holds
// an error word ("Compiling quick-error v2.0.1", "Checking failure v0.1.8");
// the filter guards every other line itself.
func (cargoFilter) GuardsErrors() bool { return true }

func (cargoFilter) Match(c *engine.Context) bool {
	sub, args := cargoSub(c)
	switch sub {
	case "build", "b", "check", "c", "clippy", "install", "fetch", "update":
		return !cargoMachine(args)
	}
	return false
}

func (cargoFilter) Apply(c *engine.Context, out string) (string, bool) {
	sub, _ := cargoSub(c)
	return applyCargo(c, out, sub)
}

// cargoTest renders cargo test: the build part as cargoFilter does; passing
// tests ("test x ... ok") and "running N tests" are counted; FAILED and
// ignored tests, the failures: section (each "---- name stdout ----" block
// with its panic message and its backtrace, standard-library and runtime
// frames folded, application frames kept), the failure list, every
// non-empty "test result:" line (verbatim) and "error: test failed …" are
// kept; test binaries that ran no test are counted (unless no test ran at
// all: then their result lines are the report and stay); with more than
// one result line an exact total is added, except on a failed run whose
// total would read "0 failed" (a binary crashed before its result line).
type cargoTest struct{}

func (cargoTest) Name() string { return "cargo-test" }

func (cargoTest) GuardsErrors() bool { return true }

func (cargoTest) Match(c *engine.Context) bool {
	sub, args := cargoSub(c)
	return (sub == "test" || sub == "t") && !cargoMachine(args) && !anyArg(args, "--list", "--no-run")
}

// anyArg is hasArg that also looks past "--" (libtest's own flags).
func anyArg(args []string, names ...string) bool {
	for _, a := range args {
		for _, n := range names {
			if a == n {
				return true
			}
		}
	}
	return false
}

func (cargoTest) Apply(c *engine.Context, out string) (string, bool) {
	return applyCargo(c, out, "test")
}

// cargoSub returns cargo's subcommand and the arguments after it.
func cargoSub(c *engine.Context) (string, []string) {
	if baseName(c.Name()) != "cargo" {
		return "", nil
	}
	args := c.Args()
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case strings.HasPrefix(a, "+"): // +nightly
		case a == "--color" || a == "--config" || a == "-Z" || a == "-C":
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a, args[i+1:]
		}
	}
	return "", nil
}

// cargoMachine reports output meant for programs: --message-format (JSON),
// libtest's --format json/junit/terse after "--".
func cargoMachine(args []string) bool {
	for i, a := range args {
		switch {
		case strings.HasPrefix(a, "--message-format"),
			a == "--format" && i+1 < len(args) && args[i+1] != "pretty",
			strings.HasPrefix(a, "--format=") && a != "--format=pretty":
			return true
		}
	}
	return false
}

var (
	// cargo's right-aligned status verbs.
	cargoStatusRe = regexp.MustCompile(`^ {0,12}([A-Z][a-z]+(?:-[a-z]+)?) (\S.*)$`)
	rustHeadRe    = regexp.MustCompile(`^(error|warning)(\[[\w:]+\])?: (.+)$`)
	rustSubRe     = regexp.MustCompile(`^(?:note|help|suggestion)(?:\[[\w:]+\])?: |^(?:note|help)$`)
	// Body lines: locations, gutter/code lines, "= note:" lines, elisions
	// and diff-style suggestions ("18 -     return total;", "18 +     total").
	rustBodyRe    = regexp.MustCompile(`^\s*(?:--> |::: |= |\d*\s*\||\.\.\.$|\d+\s+[-+~](?:\s|$))`)
	emptyGutterRe = regexp.MustCompile(`^\s*\|$`)
	rustLocRe     = regexp.MustCompile(`^\s*--> (\S+)`)
	warnNoteRe    = regexp.MustCompile("^\\s*= note: `#\\[warn\\([\\w:]+\\)\\]`(?: \\(part of `#\\[warn\\([\\w:]+\\)\\]`\\))? on by default$")
	generatedRe   = regexp.MustCompile(`generated \d+ warnings?(?: \(|$|;)|could not compile|build failed, waiting|aborting due to`)
	downloadedSum = regexp.MustCompile(`^Downloaded \d+ crates? \(`)

	testLineRe    = regexp.MustCompile(`^test (.+?) \.\.\. (ok|FAILED|ignored(?:, .*)?|bench: .*)$`)
	runningNRe    = regexp.MustCompile(`^running (\d+) tests?$`)
	testResultRe  = regexp.MustCompile(`^test result: (\w+)\. (\d+) passed; (\d+) failed; (\d+) ignored; (\d+) measured; (\d+) filtered out(?:; finished in .*)?$`)
	testRunningRe = regexp.MustCompile("^\\s+(?:Running [^`\\s]|Doc-tests \\S)")
)

// hiddenStatus are the status verbs counted instead of shown (per
// subcommand exceptions in cargoHides).
var hiddenStatus = map[string]bool{
	"Compiling": true, "Checking": true, "Downloaded": true, "Downloading": true, "Fresh": true,
	"Documenting": true, "Blocking": true, "Updating": true, "Locking": true, "Adding": true,
	"Unpacking": true, "Dirty": true,
}

func cargoHides(sub, verb, rest string) bool {
	if !hiddenStatus[verb] {
		// cargo -v: "Running `rustc --crate-name …`" per crate.
		return verb == "Running" && strings.HasPrefix(rest, "`")
	}
	switch {
	case verb == "Downloaded" && downloadedSum.MatchString(verb+" "+rest):
		return false // the download summary
	case sub == "update" && verb != "Updating":
		return false // the version changes are the content
	case verb == "Updating" && sub == "update":
		// "Updating crates.io index" is chatter; "Updating a v1 -> v2" is content.
		return !strings.Contains(rest, " -> ")
	}
	return true
}

type ckind uint8

const (
	ckOther ckind = iota
	ckKeep
	ckStatus // hidden, counted by verb
	ckDiag   // rustc diagnostic block
	ckTestOK
	ckRunningN
	ckSection // "Running …" / "Doc-tests …"
	ckResult
	ckFailures // the failures: section
	ckTestKeep // FAILED / ignored / bench lines
)

type citem struct {
	kind       ckind
	start, end int
	verb       string
	hidden     bool
	count      int
	extra      []string
	headerOnly bool
	locLine    int // " --> " line of a diagnostic, -1 if none
	section    *csection
}

type csection struct {
	head, result *citem
	empty        bool
}

func applyCargo(c *engine.Context, out, sub string) (string, bool) {
	lines := splitLines(out)
	if len(lines) == 0 {
		return "", false
	}
	items, recognized := parseCargo(lines, sub)
	if !recognized {
		return "", false
	}
	r := &cargoRender{c: c, lines: lines, items: items, exempt: make([]bool, len(lines))}
	res := r.render()
	if c.Failed() && !hasErrorLine(res) {
		// Failed without an error line in view (killed mid-build): the
		// generic reducer keeps the tail.
		return "", false
	}
	return selfGuard(lines, func(i int) bool { return r.exempt[i] }, res), true
}

func parseCargo(lines []string, sub string) ([]*citem, bool) {
	var items []*citem
	recognized := false
	var sec *csection
	for i := 0; i < len(lines); {
		ln := lines[i]
		it := &citem{start: i, end: i + 1, count: 1, locLine: -1}
		switch {
		case ln == "":
		case testRunningRe.MatchString(ln):
			it.kind = ckSection
			sec = &csection{head: it}
			it.section = sec
			recognized = true
		case runningNRe.MatchString(ln):
			it.kind = ckRunningN
			recognized = true
		case testLineRe.MatchString(ln):
			m := testLineRe.FindStringSubmatch(ln)
			if m[2] == "ok" {
				it.kind = ckTestOK
			} else {
				it.kind = ckTestKeep
				it.verb = m[2]
			}
			recognized = true
		case ln == "failures:" || ln == "successes:":
			// Everything up to the result line: stdout blocks with panics,
			// then the list of failing tests.
			it.kind = ckFailures
			j := i + 1
			for j < len(lines) && !testResultRe.MatchString(lines[j]) && !testRunningRe.MatchString(lines[j]) {
				j++
			}
			it.end = j
			recognized = true
		case testResultRe.MatchString(ln):
			it.kind = ckResult
			if sec != nil {
				sec.result = it
				it.section = sec
				m := testResultRe.FindStringSubmatch(ln)
				sec.empty = m[2] == "0" && m[3] == "0" && m[4] == "0" && m[5] == "0"
				sec = nil
			}
			recognized = true
		case rustHeadRe.MatchString(ln):
			it.kind = ckDiag
			j := i + 1
			for j < len(lines) && lines[j] != "" && (rustBodyRe.MatchString(lines[j]) || rustSubRe.MatchString(lines[j])) {
				if it.locLine < 0 && rustLocRe.MatchString(lines[j]) {
					it.locLine = j
				}
				j++
			}
			it.end = j
			recognized = true
		default:
			m := cargoStatusRe.FindStringSubmatch(ln)
			if m == nil || !knownVerb(m[1]) {
				break // unknown: generic reducer
			}
			recognized = true
			if cargoHides(sub, m[1], m[2]) {
				it.kind, it.verb = ckStatus, m[1]
			} else {
				it.kind = ckKeep
			}
		}
		items = append(items, it)
		i = it.end
	}
	return items, recognized
}

func knownVerb(v string) bool {
	switch v {
	case "Compiling", "Checking", "Downloaded", "Downloading", "Fresh", "Documenting", "Blocking",
		"Updating", "Locking", "Adding", "Removing", "Unchanged", "Finished", "Running", "Doc-tests",
		"Installing", "Installed", "Replacing", "Replaced", "Ignored", "Executable", "Unpacking",
		"Upgrading", "Downgrading", "Summary", "Dirty", "Packaging", "Packaged", "Verifying", "Archiving",
		"Waiting", "Generated", "Skipping":
		return true
	}
	return false
}

type cargoRender struct {
	c      *engine.Context
	lines  []string
	items  []*citem
	exempt []bool

	status   counter
	passed   int
	empties  int
	ignored  int
	snippets int
}

const maxIgnoredShown = 10

func (r *cargoRender) group() {
	lines := r.lines
	exact := map[string]*citem{}
	byMsg := map[string]*citem{}
	tmplN := map[string]int{}
	tmplLast := map[string]*citem{}
	tmplOver := map[string]int{}
	var tmplOrder []string
	ignored := 0
	// When no test ran anywhere (a name filter that matched nothing), the
	// empty binaries' result lines ("0 passed; …; 16 filtered out") are the
	// report itself and stay.
	ranAny := false
	for _, it := range r.items {
		if it.kind == ckSection && it.section != nil && it.section.result != nil && !it.section.empty ||
			it.kind == ckTestOK || it.kind == ckTestKeep {
			ranAny = true
			break
		}
	}
	for _, it := range r.items {
		switch it.kind {
		case ckStatus:
			it.hidden = true
			r.status.add(it.verb, 1)
			r.markExempt(it.start, it.end)
		case ckTestOK:
			it.hidden = true
			r.passed++
			r.markExempt(it.start, it.end)
		case ckRunningN:
			it.hidden = true
		case ckTestKeep:
			if strings.HasPrefix(it.verb, "ignored") {
				ignored++
				if ignored > maxIgnoredShown {
					it.hidden = true
					r.ignored++
				}
			}
		case ckSection:
			if s := it.section; s != nil && s.result != nil && s.empty && ranAny {
				it.hidden, s.result.hidden = true, true
				r.empties++
				// "Running tests/errors.rs (…)" is error-class by name only.
				r.markExempt(it.start, it.end)
			}
		case ckDiag:
			hdr := lines[it.start]
			loc := ""
			if it.locLine >= 0 {
				loc = strings.TrimSpace(lines[it.locLine])
			}
			key := hdr + "\x00" + loc
			if first := exact[key]; first != nil {
				it.hidden = true
				first.count++
				r.exemptBody(it)
				continue
			}
			exact[key] = it
			m := rustHeadRe.FindStringSubmatch(hdr)
			if m[1] == "error" || generatedRe.MatchString(hdr) || isErrorLine(hdr) || loc == "" || r.errNote(it) {
				continue
			}
			if first := byMsg[hdr]; first != nil {
				it.hidden = true
				first.extra = append(first.extra, strings.TrimPrefix(loc, "--> "))
				r.exemptBody(it)
				continue
			}
			byMsg[hdr] = it
			tk := cTemplate(m[3])
			if tmplN[tk] == 0 {
				tmplOrder = append(tmplOrder, tk)
			}
			tmplN[tk]++
			switch n := tmplN[tk]; {
			case n > maxPerTemplate:
				it.hidden = true
				tmplOver[tk]++
				r.exemptBody(it)
			case n > fullPerTemplate:
				it.headerOnly = true
				r.snippets++
				r.exemptBody(it)
				tmplLast[tk] = it
			default:
				tmplLast[tk] = it
			}
		}
	}
	for _, it := range r.items {
		if it.kind != ckDiag || len(it.extra) == 0 {
			continue
		}
		it.extra = []string{alsoAt("warning", it.extra)}
	}
	for _, tk := range tmplOrder {
		if n := tmplOver[tk]; n > 0 && tmplLast[tk] != nil {
			tmplLast[tk].extra = append(tmplLast[tk].extra, fmt.Sprintf("[lx: +%d more %s like the above not shown]", n, plural(n, "warning", "warnings")))
		}
	}
}

// errNote reports an error-class line in a diagnostic's body that is not
// quoted code ("= note: … could not …"): such a warning is never condensed.
func (r *cargoRender) errNote(it *citem) bool {
	for i := it.start + 1; i < it.end; i++ {
		ln := r.lines[i]
		if !codeLineRe.MatchString(ln) && engine.IsError(ln) {
			return true
		}
	}
	return false
}

// codeLineRe: rustc's quoted source lines (gutter, diff-style suggestion).
var codeLineRe = regexp.MustCompile(`^\s*\d*\s*\||^\s*\d+\s+[-+~](?:\s|$)`)

// exemptBody marks a condensed warning's code lines: source excerpts quote
// code ("if err { fail() }"), not error reports; its header is exempt only
// when not error-class (error-class headers are hidden only as duplicates).
func (r *cargoRender) exemptBody(it *citem) {
	for i := it.start + 1; i < it.end; i++ {
		r.exempt[i] = true
	}
	if it.hidden && !isErrorLine(r.lines[it.start]) {
		r.exempt[it.start] = true
	}
}

func (r *cargoRender) markExempt(start, end int) {
	for i := start; i < end; i++ {
		r.exempt[i] = true
	}
}

func (r *cargoRender) render() string {
	r.group()
	var out []string
	var seg []string
	flush := func() {
		if len(seg) == 0 {
			return
		}
		out = append(out, genericLines(r.c, seg)...)
		seg = nil
	}
	results := 0
	for _, it := range r.items {
		if it.kind == ckResult {
			results++
		}
		if it.hidden {
			continue
		}
		if it.kind == ckOther {
			seg = append(seg, r.lines[it.start:it.end]...)
			continue
		}
		flush()
		switch it.kind {
		case ckDiag:
			out = append(out, withCount(r.lines[it.start], it.count))
			for i := it.start + 1; i < it.end; i++ {
				ln := r.lines[i]
				if it.headerOnly && i != it.locLine {
					continue
				}
				if warnNoteRe.MatchString(ln) && strings.HasPrefix(r.lines[it.start], "warning") {
					// Drop the note and the empty gutter line it leaves behind.
					if n := len(out); n > 0 && emptyGutterRe.MatchString(out[n-1]) && (i+1 == it.end || !strings.HasPrefix(strings.TrimSpace(r.lines[i+1]), "=")) {
						out = out[:n-1]
					}
					continue
				}
				out = append(out, ln)
			}
		case ckFailures:
			block := foldRustBacktraces(r.lines[it.start:it.end], r.exempt[it.start:it.end])
			out = append(out, engine.FoldStacks(r.c, block)...)
		case ckResult:
			out = append(out, r.lines[it.start])
		default:
			out = append(out, withCount(r.lines[it.start], it.count))
			out = append(out, r.lines[it.start+1:it.end]...)
		}
		out = append(out, it.extra...)
	}
	flush()
	// Collapse the blank-line runs left by hidden lines, and the blank line
	// after a test binary's header whose "running N tests" line is hidden.
	out = collapseBlank(out)
	for k := len(out) - 2; k >= 0; k-- {
		if out[k+1] == "" && testRunningRe.MatchString(out[k]) {
			out = append(out[:k+1], out[k+2:]...)
		}
	}
	if results > 1 {
		// The total is lx's own line: on a failed run it is added only when
		// it reports failures. "0 failed" next to exit 101 (a test binary
		// that crashed or was killed before its result line) would read as
		// a pass; cargo's own lines above say what failed.
		if total, failed := totalResults(r.lines, r.items); failed > 0 || !r.c.Failed() {
			out = append(out, total)
		}
	}
	var parts []string
	if n := r.status.total(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d cargo status %s (%s)", n, plural(n, "line", "lines"), r.status.String()))
	}
	parts = append(parts,
		countPart(r.passed, "passing test", "passing tests"),
		countPart(r.ignored, "more ignored test", "more ignored tests"),
		countPart(r.empties, "test binary that ran no tests", "test binaries that ran no tests"),
		countPart(r.snippets, "code snippet of a repeated warning", "code snippets of repeated warnings"),
	)
	if note := hiddenNote(parts...); note != "" {
		out = append([]string{note}, out...)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// totalResults sums every "test result:" line (shown or not) and returns
// the marker line and the failed total.
func totalResults(lines []string, items []*citem) (string, int) {
	var sum [5]int
	n := 0
	for _, it := range items {
		if it.kind != ckResult {
			continue
		}
		m := testResultRe.FindStringSubmatch(lines[it.start])
		n++
		for k := 0; k < 5; k++ {
			v, _ := strconv.Atoi(m[k+2])
			sum[k] += v
		}
	}
	return fmt.Sprintf("[lx: total of %d test result lines: %d passed; %d failed; %d ignored; %d measured; %d filtered out]",
		n, sum[0], sum[1], sum[2], sum[3], sum[4]), sum[1]
}

// collapseBlank trims leading blank lines and squeezes blank runs.
func collapseBlank(lines []string) []string {
	out := lines[:0:0]
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" && (len(out) == 0 || strings.TrimSpace(out[len(out)-1]) == "") {
			continue
		}
		out = append(out, ln)
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

var (
	// A frame of a Rust backtrace: "   4: demo::parser::tests::parses_negative",
	// optionally followed by "             at ./src/parser.rs:141:9".
	rustFrameRe   = regexp.MustCompile(`^\s+\d+:\s+(\S.*)$`)
	rustFrameAtRe = regexp.MustCompile(`^\s+at \S`)
	// Library frames: the standard library and runtime (by symbol or by a
	// location under /rustc/<hash>/library), test harness, cargo registry
	// crates.
	rustLibSymRe = regexp.MustCompile(`^(?:<?(?:std|core|alloc|test|panic_unwind|panic_abort)::|rust_begin_unwind$|__rust|_start$|__libc_start|start_thread$|clone3?$|__pthread|thread_start$|<F as )`)
	rustLibAtRe  = regexp.MustCompile(`/rustc/[0-9a-f]+/library/|/\.cargo/registry/|/\.rustup/toolchains/`)
	rustStdAtRe  = regexp.MustCompile(`/rustc/[0-9a-f]+/library/(\w+)/`)
)

// foldRustBacktraces folds, in each "stack backtrace:" section of a test's
// failure output, every run of two or more library frames (standard
// library, runtime, test harness, registry crates) into one marker line and
// keeps every application frame. The panic line above the backtrace
// already names the failing location, so the runtime frames around it
// ("rust_begin_unwind", "core::panicking::panic_fmt", "FnOnce::call_once")
// add nothing. Folded lines are marked in exempt; a frame with an
// error-class line is never folded.
func foldRustBacktraces(lines []string, exempt []bool) []string {
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		if strings.TrimSpace(lines[i]) != "stack backtrace:" {
			out = append(out, lines[i])
			i++
			continue
		}
		out = append(out, lines[i])
		i++
		type frame struct {
			start, end int
			lib        bool
			root       string
		}
		var frames []frame
		for i < len(lines) {
			m := rustFrameRe.FindStringSubmatch(lines[i])
			if m == nil {
				break
			}
			f := frame{start: i, end: i + 1}
			if f.end < len(lines) && rustFrameAtRe.MatchString(lines[f.end]) && !rustFrameRe.MatchString(lines[f.end]) {
				f.end++
			}
			sym := m[1]
			if k := strings.Index(sym, " - "); k >= 0 && strings.HasPrefix(sym, "0x") {
				sym = sym[k+3:] // RUST_BACKTRACE=full: "0x1049c8f3c - std::…"
			}
			at := ""
			if f.end > f.start+1 {
				at = lines[f.start+1]
			}
			f.lib = rustLibSymRe.MatchString(sym) || rustLibAtRe.MatchString(at)
			for k := f.start; k < f.end; k++ {
				if engine.IsError(lines[k]) {
					f.lib = false
				}
			}
			f.root = rustRoot(sym, at)
			frames = append(frames, f)
			i = f.end
		}
		for k := 0; k < len(frames); {
			if !frames[k].lib {
				out = append(out, lines[frames[k].start:frames[k].end]...)
				k++
				continue
			}
			j := k
			var roots counter
			for j < len(frames) && frames[j].lib {
				roots.add(frames[j].root, 1)
				j++
			}
			if j-k < 2 {
				out = append(out, lines[frames[k].start:frames[k].end]...)
				k = j
				continue
			}
			indent := lines[frames[k].start][:len(lines[frames[k].start])-len(strings.TrimLeft(lines[frames[k].start], " "))]
			out = append(out, fmt.Sprintf("%s… %d library frames (%s)", indent, j-k, strings.Join(roots.order, ", ")))
			for f := k; f < j; f++ {
				for x := frames[f].start; x < frames[f].end; x++ {
					exempt[x] = true
				}
			}
			k = j
		}
	}
	return out
}

// rustRoot names a library frame's area for the fold marker: the crate of
// its symbol ("core", "std", "test") or "registry" for crates.io code.
func rustRoot(sym, at string) string {
	if strings.Contains(at, "/.cargo/registry/") {
		return "registry"
	}
	if m := rustStdAtRe.FindStringSubmatch(at); m != nil {
		return m[1]
	}
	sym = strings.TrimPrefix(strings.TrimPrefix(sym, "<"), "&")
	if k := strings.Index(sym, "::"); k > 0 {
		return sym[:k]
	}
	return "runtime"
}
