package build

import (
	"fmt"
	"github.com/iheeb1/lx/internal/lazyre"
	"strconv"
	"strings"

	"github.com/iheeb1/lx/internal/engine"
)

type nativeKind uint8

const (
	nkOther nativeKind = iota
	nkKeep
	nkQuiet
	nkCommand
	nkStep
	nkScript
	nkDir
	nkDiag
	nkSummary
	nkDelegate
)

type nitem struct {
	kind       nativeKind
	start, end int
	label      string
	d          *cdiag
	errTrigger bool
	hidden     bool
	count      int
	extra      []string
	make       bool
	dirKey     string
	dirLine    int
	inner      *engine.Context
	bodyEnd    int

	selfContained bool
}

var (
	makeLineRe  = lazyre.New(`^(?:\S*/)?(?:g?make|mingw32-make|bmake|gnumake)(?:\[(\d+)\])?: (.*)$`)
	makeDirRe   = lazyre.New("^(Entering|Leaving) directory [`'](.*)'$")
	makeQuietRe = lazyre.New("^(?:Nothing to be done for [`'].*'|[`'].*' is up to date)\\.$")

	makeFileErrRe = lazyre.New(`^[^\s:]+:\d+: \*\*\* |^\*\*\* Error code \d+|^Stop\.$`)
	makeErrNRe    = lazyre.New(`\*\*\* .*\bError (\d+)`)

	ninjaStepRe  = lazyre.New(`^\[\d+/\d+\] \S`)
	ninjaDirRe   = lazyre.New("^ninja: (Entering|Leaving) directory [`'](.*)'$")
	ninjaFailRe  = lazyre.New(`^FAILED: `)
	cmakeStepRe  = lazyre.New(`^\[\s*\d{1,3}%\] \S|^(?:Scanning dependencies|Consolidate compiler generated dependencies) of target \S+$`)
	silentStepRe = lazyre.New(`^  (?:CC|CXX|CPP|AS|CCAS|LD|AR|CCLD|CXXLD|OBJCLD|GEN|HOSTCC|HOSTCXX|HOSTLD|MKDIR|INSTALL|LINK|COPY|STRIP|OBJCOPY|SYMLINK|MOC|UIC|RCC|YACC|LEX|SED|CHK|UPD|DEP|RANLIB|DTC|LDS|MODPOST|BTF|ZSTD|GZIP|XZ|FC|F77)(?: \[M\])?\s+\S+$|^Making (?:all|install|check|clean|distclean|install-exec|install-data) in \S+$`)

	linkerRe = lazyre.New(`^(?:\S*/)?(?:ld|ld\.\w+|ld64(?:\.lld)?|lld(?:-link)?|collect2|[\w.+-]+-ld)(?:\.exe)?: ` +
		`|^\S+:\(\.[\w.$]+(?:\+0x[0-9a-f]+)?\): `)
	undefSymsRe = lazyre.New(`^Undefined symbols for architecture \S+:$`)

	ccMiscRe = lazyre.New(`^[\w.+-]+: (?:all|some) warnings being treated as errors$|^compilation terminated\.$`)

	libtoolEchoRe = lazyre.New(`^libtool: (?:compile|link|install|finish|relink|uninstall|clean|execute): `)

	ccNameRe   = lazyre.New(`^(?:[\w.]+-)*(?:cc|c\+\+|gcc|g\+\+|clang|clang\+\+|icc|icpc|icx|icpx|tcc|nvcc|emcc|em\+\+|gfortran|cpp)(?:-\d+(?:\.\d+)*)?$`)
	makeNameRe = lazyre.New(`^(?:g?make|mingw32-make|bmake|gnumake)$`)
)

func isMakeFileErr(ln string) bool {
	return (ln == "Stop." || strings.Contains(ln, "*** ")) && makeFileErrRe.MatchString(ln)
}

func isCCMisc(ln string) bool {
	return (strings.HasSuffix(ln, " treated as errors") || ln == "compilation terminated.") && ccMiscRe.MatchString(ln)
}

var buildTools = map[string]bool{
	"ar": true, "ranlib": true, "ld": true, "lld": true, "ld.lld": true, "ld.gold": true, "strip": true,
	"libtool": true, "install_name_tool": true, "objcopy": true, "as": true, "nasm": true, "yasm": true,
	"windres": true, "ln": true, "mkdir": true, "rm": true, "cp": true, "mv": true, "install": true,
	"touch": true, "chmod": true, "lipo": true, "dsymutil": true, "codesign": true,
}

var ccWrappers = map[string]bool{"ccache": true, "sccache": true, "distcc": true, "icecc": true}

var delegateTools = map[string]bool{
	"go": true, "cargo": true, "npm": true, "npx": true, "pnpm": true, "yarn": true, "bun": true,
	"bunx": true, "node": true, "jest": true, "vitest": true, "mocha": true, "tsc": true, "eslint": true,
	"pytest": true, "python": true, "python3": true, "ruff": true, "mypy": true, "pip": true, "pip3": true,
	"uv": true, "golangci-lint": true, "mvn": true, "mvnw": true, "gradle": true, "gradlew": true,
	"docker": true, "git": true,
}

func commandLabel(ln string) (string, bool) {
	if ln == "" || ln[0] == ' ' || ln[0] == '\t' || ln[0] == '[' {
		return "", false
	}
	if libtoolEchoRe.MatchString(ln) {

		return "libtool", true
	}
	sp := strings.IndexByte(ln, ' ')
	if sp < 0 {
		return "", false
	}
	name := baseName(ln[:sp])
	if !buildTools[name] && !ccWrappers[name] && !ccNameRe.MatchString(name) && !makeNameRe.MatchString(name) {
		return "", false
	}
	f := strings.Fields(ln)
	if len(f) < 2 {
		return "", false
	}
	args := f[1:]
	if ccWrappers[name] {
		name, args = baseName(args[0]), args[1:]
		if !ccNameRe.MatchString(name) {
			return "", false
		}
	}
	for _, a := range args {
		if strings.HasPrefix(a, "-") || strings.ContainsAny(a, "./") {
			return name, true
		}
	}
	return "", false
}

func isStep(ln string) bool {
	if ln == "" {
		return false
	}
	switch ln[0] {
	case '[':
		return ninjaStepRe.MatchString(ln) || cmakeStepRe.MatchString(ln)
	case ' ', 'M':
		return silentStepRe.MatchString(ln)
	case 'S', 'C':
		return cmakeStepRe.MatchString(ln)
	}
	return false
}

func scriptEnd(lines []string, i int) int {
	ln := lines[i]
	if ln == "" || ln[0] == ' ' || ln[0] == '\t' || !strings.HasSuffix(ln, "\\") {
		return i
	}
	j := i + 1
	for j < len(lines) && lines[j] != "" && (lines[j][0] == ' ' || lines[j][0] == '\t') {
		j++
		if !strings.HasSuffix(lines[j-1], "\\") {

			if engine.IsError(lines[j-1]) {
				return i
			}
			break
		}
	}
	if j-i < 2 {
		return i
	}
	return j
}

type nativeParser struct {
	c       *engine.Context
	lines   []string
	baseDir string

	dirStack  []int
	lastLeave int
	exits     []int

	noDiagThrough int
}

func (p *nativeParser) dirKey() (string, int) {
	if len(p.dirStack) == 0 {
		return "", p.lastLeave
	}
	top := p.dirStack[len(p.dirStack)-1]
	return p.dirOf(top), top
}

func (p *nativeParser) dirOf(i int) string {
	ln := p.lines[i]
	var m []string
	if mm := makeLineRe.FindStringSubmatch(ln); mm != nil {
		m = makeDirRe.FindStringSubmatch(mm[2])
	} else {
		m = ninjaDirRe.FindStringSubmatch(ln)
		if m != nil {
			m = m[1:]
		}
	}
	if m == nil {
		return ""
	}
	d := strings.TrimRight(m[len(m)-1], "/")
	if d == p.baseDir {
		return ""
	}
	return d
}

func (p *nativeParser) parse() []*nitem {
	lines := p.lines
	p.lastLeave = -1
	p.noDiagThrough = -1
	var items []*nitem
	for i := 0; i < len(lines); {
		it := &nitem{start: i, end: i + 1, count: 1, dirLine: -1}
		p.classify(it)
		if it.kind == nkDir {
			p.moveDir(it)
		}
		it.dirKey, it.dirLine = p.dirKey()
		if it.kind == nkDir {
			it.dirKey, it.dirLine = "", -1
		}
		items = append(items, it)
		i = it.end
	}
	return items
}

func (p *nativeParser) moveDir(it *nitem) {
	ln := p.lines[it.start]
	if strings.Contains(ln, "Entering directory") {
		if p.dirOf(it.start) == "" && len(p.dirStack) == 0 {
			return
		}
		p.dirStack = append(p.dirStack, it.start)
		return
	}
	if len(p.dirStack) > 0 {
		p.dirStack = p.dirStack[:len(p.dirStack)-1]
		if len(p.dirStack) == 0 {
			p.lastLeave = it.start
		}
	}
}

func (p *nativeParser) classify(it *nitem) {
	lines := p.lines
	i := it.start
	ln := lines[i]
	if ln == "" {
		it.kind = nkOther
		return
	}

	if m := makeLineRe.FindStringSubmatch(ln); m != nil {
		it.make = true
		switch {
		case makeDirRe.MatchString(m[2]):
			it.kind = nkDir
		case m[1] != "" && makeQuietRe.MatchString(m[2]):
			it.kind = nkQuiet
		default:
			it.kind = nkKeep
			it.errTrigger = strings.HasPrefix(m[2], "*** ")
		}
		return
	}
	if isMakeFileErr(ln) {
		it.kind, it.make, it.errTrigger = nkKeep, true, true
		return
	}

	if strings.HasPrefix(ln, "ninja: ") {
		if ninjaDirRe.MatchString(ln) {
			it.kind = nkDir
			return
		}

		it.kind, it.make = nkKeep, true
		return
	}
	if ninjaFailRe.MatchString(ln) {

		it.kind, it.errTrigger, it.selfContained = nkKeep, true, true
		if i+1 < len(lines) {
			if _, _, _, _, hdr := parseCHeader(lines[i+1]); !hdr && lines[i+1] != "" {
				it.end = i + 2
			}
		}
		return
	}
	if isStep(ln) {
		it.kind = nkStep
		return
	}

	if i > p.noDiagThrough {
		d, stop, ok := parseCDiag(lines, i)
		if ok {
			it.kind, it.d, it.end = nkDiag, d, d.end()
			return
		}
		p.noDiagThrough = stop
	}
	if strings.HasSuffix(ln, " generated.") && tuSummaryRe.MatchString(ln) {
		it.kind = nkSummary
		return
	}
	if undefSymsRe.MatchString(ln) {
		it.kind, it.errTrigger = nkKeep, true
		j := i + 1
		for j < len(lines) && lines[j] != "" && (lines[j][0] == ' ' || lines[j][0] == '\t') {
			j++
		}
		it.end = j
		return
	}
	if linkerRe.MatchString(ln) {
		it.kind = nkKeep

		it.errTrigger = !strings.Contains(ln, "warning")
		return
	}
	if isCCMisc(ln) {
		it.kind = nkKeep
		it.errTrigger = engine.IsError(ln)
		return
	}
	if label, ok := commandLabel(ln); ok {
		it.kind, it.label = nkCommand, label
		if e := scriptEnd(lines, i); e > i {
			it.end = e
		}
		return
	}
	if e := scriptEnd(lines, i); e > i {
		it.kind, it.end = nkScript, e
		return
	}
	if inner := p.delegateStart(ln); inner != nil {
		it.kind, it.inner = nkDelegate, inner
		j := i + 1
		for j < len(lines) && !p.boundary(lines[j]) {
			j++
		}
		it.end, it.bodyEnd = j, j
		inner.Exit = p.exitAfter(j)
		return
	}
	it.kind = nkOther
}

func (p *nativeParser) boundary(ln string) bool {
	if ln == "" {
		return false
	}
	if makeLineRe.MatchString(ln) || isStep(ln) || strings.HasPrefix(ln, "ninja: ") || ninjaFailRe.MatchString(ln) ||
		isMakeFileErr(ln) || linkerRe.MatchString(ln) || undefSymsRe.MatchString(ln) || isCCMisc(ln) || nativeDiag(ln) {
		return true
	}
	if _, ok := commandLabel(ln); ok {
		return true
	}
	return p.delegateStart(ln) != nil
}

var cFamilyExt = map[string]bool{
	".c": true, ".h": true, ".cc": true, ".cpp": true, ".cxx": true, ".c++": true, ".hpp": true, ".hh": true,
	".hxx": true, ".ipp": true, ".tcc": true, ".inl": true, ".inc": true, ".def": true, ".m": true, ".mm": true,
	".s": true, ".S": true, ".asm": true, ".cu": true, ".cuh": true, ".f": true, ".f90": true, ".ld": true, ".lds": true,
}

func nativeDiag(ln string) bool {
	if ln == "" || ln[0] == ' ' || ln[0] == '\t' || !hasSeverityWord(ln) {
		return false
	}
	if m := cLocDiagRe.FindStringSubmatch(ln); m != nil {
		file := m[1]
		if k := strings.LastIndexByte(file, '.'); k >= 0 {
			return cFamilyExt[file[k:]]
		}
		return false
	}
	if m := cToolDiagRe.FindStringSubmatch(ln); m != nil {
		tool := m[1]
		return ccNameRe.MatchString(tool) || tool == "cc1" || tool == "cc1plus" || tool == "cc1obj" || tool == "collect2" || tool == "lto1"
	}
	return false
}

func (p *nativeParser) delegateStart(ln string) *engine.Context {
	if ln == "" || ln[0] == ' ' || ln[0] == '\t' {
		return nil
	}
	sp := strings.IndexByte(ln, ' ')
	first := ln
	if sp >= 0 {
		first = ln[:sp]
	}
	if !delegateTools[baseName(first)] {
		return nil
	}
	argv, ok := simpleArgv(ln)
	if !ok {
		return nil
	}
	inner := &engine.Context{Argv: argv, Cwd: p.c.Cwd, Home: p.c.Home}
	if engine.MachineReadable(inner) {
		return nil
	}
	f := engine.Find(inner)
	if f == nil || isNative(f.Name()) || isContent(f) {

		return nil
	}
	return inner
}

func isContent(f engine.Filter) bool {
	ct, ok := f.(engine.Content)
	return ok && ct.IsContent()
}

func (p *nativeParser) exitAfter(j int) int {
	if p.exits == nil {

		p.exits = make([]int, len(p.lines)+1)
		p.exits[len(p.lines)] = -1
		for k := len(p.lines) - 1; k >= 0; k-- {
			p.exits[k] = p.exits[k+1]
			ln := p.lines[k]
			if !strings.Contains(ln, "*** ") {
				continue
			}
			if m := makeLineRe.FindStringSubmatch(ln); m != nil && strings.HasPrefix(m[2], "*** ") {
				p.exits[k] = p.c.Exit
				if e := makeErrNRe.FindStringSubmatch(m[2]); e != nil {
					p.exits[k], _ = strconv.Atoi(e[1])
				}
			}
		}
	}
	if j >= len(p.lines) || p.exits[j] < 0 {
		return p.c.Exit
	}
	return p.exits[j]
}

func isNative(name string) bool {
	switch name {
	case "make", "cmake-build", "ninja", "cc":
		return true
	}
	return false
}

var goTestSigRe = lazyre.New(`^(?:=== RUN\s|--- (?:FAIL|PASS|SKIP): |ok  \t\S|FAIL\t\S|\?   \t\S)`)

func detectInner(c *engine.Context, seg []string, exit int) *engine.Context {
	verbose, found := false, false
	for _, ln := range seg {
		if len(ln) < 4 || !strings.ContainsAny(ln[:4], "=-oF?") {
			continue
		}
		if goTestSigRe.MatchString(ln) {
			found = true
			if strings.HasPrefix(ln, "=== RUN") {
				verbose = true
			}
		}
	}
	if !found {
		return nil
	}
	argv := []string{"go", "test"}
	if verbose {
		argv = append(argv, "-v")
	}
	inner := &engine.Context{Argv: argv, Exit: exit, Cwd: c.Cwd, Home: c.Home}
	if f := engine.Find(inner); f == nil || isNative(f.Name()) {
		return nil
	}
	return inner
}

func applyNative(c *engine.Context, out string) (string, bool) {
	lines := splitLines(out)
	if len(lines) == 0 {
		return "", false
	}
	p := &nativeParser{c: c, lines: lines, baseDir: strings.TrimRight(c.Cwd, "/")}
	items := p.parse()
	recognized := false
	for _, it := range items {
		if it.kind != nkOther {
			recognized = true
			break
		}
	}
	if !recognized {
		return "", false
	}
	r := &nativeRender{c: c, lines: lines, items: items, exempt: make([]bool, len(lines)), parser: p}
	r.group()
	res := r.render()
	if c.Failed() && !hasErrorLine(res) && !r.shownFailure {

		return "", false
	}
	return selfGuard(lines, func(i int) bool { return r.exempt[i] }, res), true
}

func hasErrorLine(s string) bool {
	for _, ln := range strings.Split(s, "\n") {
		if engine.IsError(ln) {
			return true
		}
	}
	return false
}

type nativeRender struct {
	c        *engine.Context
	lines    []string
	items    []*nitem
	exempt   []bool
	parser   *nativeParser
	errCache []int8

	shownFailure bool

	commands  counter
	steps     int
	dirs      int
	quiet     int
	excerpts  int
	notesHid  int
	chainsHid int
	sysShown  int
	sysHidden int
}

const (
	fullPerTemplate = 2
	maxPerTemplate  = 40
	maxAlsoAt       = 12
)

func (r *nativeRender) group() {
	lines := r.lines

	exact := map[string]*nitem{}
	byMsg := map[string]*nitem{}
	tmplN := map[string]int{}
	tmplLast := map[string]*nitem{}
	tmplOver := map[string]int{}
	var tmplOrder []string
	for _, it := range r.items {
		switch it.kind {
		case nkSummary:
			k := "S" + lines[it.start]
			if first := exact[k]; first != nil {
				it.hidden = true
				first.count++
				continue
			}
			exact[k] = it
		case nkKeep:
			if it.make || it.end-it.start > 1 || r.isErr(it.start) {
				continue
			}
			k := "K" + lines[it.start]
			if first := exact[k]; first != nil {
				it.hidden = true
				first.count++
				continue
			}
			exact[k] = it
		case nkDiag:
			d := it.d
			hdr := lines[d.head]
			if d.isError() {
				it.errTrigger = true
				if d.loc == "" {
					continue
				}
				k := "E" + squash(hdr)
				if first := exact[k]; first != nil {
					it.hidden = true
					first.count++
					continue
				}
				exact[k] = it
				continue
			}

			errClass := r.diagHasErr(d)
			k := "W" + d.sev + "\x00" + d.normLoc + "\x00" + d.msg
			if errClass {
				k = "E" + squash(hdr)
			}
			if first := exact[k]; first != nil {
				it.hidden = true
				first.count++
				continue
			}
			exact[k] = it
			if !errClass && d.loc != "" {
				mk := d.sev + "\x00" + d.msg
				if first := byMsg[mk]; first != nil {
					it.hidden = true
					first.extra = append(first.extra, d.loc)
					continue
				}
				byMsg[mk] = it
			}
			tk := d.sev + "\x00" + cTemplate(d.msg)
			if tmplN[tk] == 0 {
				tmplOrder = append(tmplOrder, tk)
			}
			tmplN[tk]++
			switch n := tmplN[tk]; {
			case n > maxPerTemplate && !errClass:
				it.hidden = true
				tmplOver[tk]++
			case n > fullPerTemplate:
				if len(d.body) > 0 || len(d.notes) > 0 || len(d.prefix) > 0 {
					r.excerpts++
				}
				d.headerOnly = true
				tmplLast[tk] = it
			default:
				tmplLast[tk] = it
			}
		}
	}

	for _, it := range r.items {
		if it.kind != nkDiag || len(it.extra) == 0 {
			continue
		}
		it.extra = []string{alsoAt(it.d.sev, it.extra)}
	}
	for _, tk := range tmplOrder {
		if n := tmplOver[tk]; n > 0 && tmplLast[tk] != nil {
			last := tmplLast[tk]
			last.extra = append(last.extra, fmt.Sprintf("[lx: +%d more %s like the above not shown]", n, plural(n, last.d.sev, last.d.sev+"s")))
		}
	}

	for k, it := range r.items {
		switch it.kind {
		case nkCommand, nkStep:
			it.hidden = true
		case nkDir, nkQuiet:
			it.hidden = true
		}
		if !it.errTrigger || it.hidden || it.selfContained {
			continue
		}
		for j := k - 1; j >= 0; j-- {
			prev := r.items[j]
			if prev.kind == nkCommand || prev.kind == nkStep {
				prev.hidden = false
				break
			}
			if prev.errTrigger && !prev.hidden {
				break
			}
		}
	}
	for _, it := range r.items {
		if it.kind == nkDiag && (it.hidden || it.d.headerOnly) {
			r.exemptDiag(it.d, it.hidden)
		}
		if !it.hidden {
			continue
		}
		switch it.kind {
		case nkCommand:
			r.commands.add(it.label, 1)
			r.markExempt(it.start, it.end)
		case nkStep:
			r.steps++
			r.markExempt(it.start, it.end)
		case nkDir:
			r.markExempt(it.start, it.end)
		case nkQuiet:
			r.quiet++

			r.markExempt(it.start, it.end)
		}
	}
}

func (r *nativeRender) exemptDiag(d *cdiag, hidden bool) {
	for _, i := range d.prefix {
		r.exempt[i] = true
	}
	for _, i := range d.body {
		r.exempt[i] = true
	}
	if hidden && !r.isErr(d.head) {
		r.exempt[d.head] = true
	}
	for _, n := range d.notes {
		r.exemptDiag(n, true)
	}
}

func (r *nativeRender) diagHasErr(d *cdiag) bool {
	if r.isErr(d.head) {
		return true
	}
	for _, n := range d.notes {
		if r.isErr(n.head) {
			return true
		}
	}
	return false
}

func (r *nativeRender) isErr(i int) bool {
	if r.errCache == nil {
		r.errCache = make([]int8, len(r.lines))
	}
	if r.errCache[i] == 0 {
		r.errCache[i] = 2
		if isErrorLine(r.lines[i]) {
			r.errCache[i] = 1
		}
	}
	return r.errCache[i] == 1
}

func (r *nativeRender) markExempt(start, end int) {
	for i := start; i < end; i++ {
		r.exempt[i] = true
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (r *nativeRender) render() string {
	var out []string
	shownChains := map[string]bool{}
	shownNotes := map[string]bool{}
	announced := ""
	shownDirs := map[int]bool{}
	announce := func(it *nitem) {
		if it.make || it.dirKey == announced {
			return
		}
		announced = it.dirKey
		if it.dirLine >= 0 {
			out = append(out, r.lines[it.dirLine])
			shownDirs[it.dirLine] = true
		}
	}
	var seg []int
	flush := func() {
		if len(seg) == 0 {
			return
		}
		out = append(out, r.segment(seg)...)
		seg = seg[:0]
	}
	for _, it := range r.items {
		if it.hidden {
			continue
		}
		if it.kind == nkOther {
			if len(seg) == 0 {
				announce(it)
			}
			for i := it.start; i < it.end; i++ {
				seg = append(seg, i)
			}
			continue
		}
		flush()
		announce(it)
		if it.errTrigger {
			r.shownFailure = true
		}
		switch it.kind {
		case nkDiag:
			out = append(out, r.renderDiag(it.d, it.count, shownChains, shownNotes)...)
		case nkScript:
			out = append(out, r.lines[it.start])
			if n := it.end - it.start - 1; n > 0 {
				out = append(out, fmt.Sprintf("[lx: %d more %s of this echoed recipe hidden]", n, plural(n, "line", "lines")))

				r.markExempt(it.start+1, it.end)
			}
		case nkDelegate:
			out = append(out, r.lines[it.start])
			out = append(out, r.delegate(it.inner, it.start+1, it.bodyEnd)...)
		default:
			out = append(out, withCount(r.lines[it.start], it.count))
			out = append(out, r.lines[it.start+1:it.end]...)
		}
		out = append(out, it.extra...)
	}
	flush()
	for _, it := range r.items {
		if it.kind == nkDir && !shownDirs[it.start] {
			r.dirs++
		}
	}
	var parts []string
	if n := r.commands.total(); n > 0 {
		parts = append(parts, fmt.Sprintf("%d recipe %s (%s)", n, plural(n, "command", "commands"), r.commands.String()))
	}
	parts = append(parts,
		countPart(r.steps, "build step", "build steps"),
		countPart(r.dirs, "directory line", "directory lines"),
		countPart(r.quiet, "\"Nothing to be done\" line", "\"Nothing to be done\" lines"),
		countPart(r.excerpts, "source excerpt of a repeated warning", "source excerpts of repeated warnings"),
		countPart(r.notesHid, "repeated note", "repeated notes"),
		countPart(r.chainsHid, "repeated include chain", "repeated include chains"),
		countPart(r.sysHidden, "source excerpt from a system header", "source excerpts from system headers"),
	)
	if note := hiddenNote(parts...); note != "" {
		out = append([]string{note}, out...)
	}
	return strings.Join(out, "\n")
}

const maxSysExcerpts = 3

func isSystemPath(loc string) bool {
	if loc == "" {
		return false
	}
	for _, p := range []string{"/usr/include/", "/usr/local/include/", "/usr/lib/", "/usr/lib64/", "/usr/local/lib/",
		"/usr/share/", "/opt/homebrew/", "/usr/local/Cellar/", "/Applications/Xcode", "/Library/Developer/", "/nix/store/",
		"C:/Program Files", `C:\Program Files`} {
		if strings.HasPrefix(loc, p) {
			return true
		}
	}
	return strings.Contains(loc, "/include/c++/") || strings.Contains(loc, ".sdk/usr/include/")
}

func withCount(line string, n int) string {
	if n > 1 {
		return fmt.Sprintf("%s [×%d]", line, n)
	}
	return line
}

func (r *nativeRender) renderDiag(d *cdiag, count int, chains, notes map[string]bool) []string {
	var out []string
	if !d.headerOnly {
		out = append(out, r.renderPrefix(d.prefix, chains)...)
	}
	out = append(out, withCount(r.lines[d.head], count))
	if d.headerOnly {

		for _, n := range d.notes {
			if r.isErr(n.head) {
				out = append(out, r.lines[n.head])
			}
		}
		return out
	}
	switch {
	case len(d.body) > 0 && isSystemPath(d.loc) && r.sysShown >= maxSysExcerpts:

		r.sysHidden++
		for _, i := range d.body {
			r.exempt[i] = true
		}
	default:
		if len(d.body) > 0 && isSystemPath(d.loc) {
			r.sysShown++
		}
		for _, i := range d.body {
			out = append(out, r.lines[i])
		}
	}
	for _, n := range d.notes {
		key := squash(r.lines[n.head])
		if !r.isErr(n.head) {
			key = n.normLoc + "\x00" + n.msg
		}
		if notes[key] {
			r.notesHid++
			r.exemptDiag(n, true)
			continue
		}
		notes[key] = true
		out = append(out, r.renderDiag(n, 1, chains, notes)...)
	}
	return out
}

func (r *nativeRender) renderPrefix(prefix []int, chains map[string]bool) []string {
	if len(prefix) == 0 {
		return nil
	}

	var incl, ctx []int
	for _, i := range prefix {
		ln := r.lines[i]
		if strings.HasPrefix(ln, "In file included from ") || strings.HasPrefix(strings.TrimSpace(ln), "from ") && len(ctx) == 0 {
			incl = append(incl, i)
		} else {
			ctx = append(ctx, i)
		}
	}
	var out []string
	if len(incl) > 0 {
		var key strings.Builder
		for _, i := range incl {
			key.WriteString(r.lines[i])
			key.WriteByte('\n')
		}
		switch {
		case chains[key.String()]:
			r.chainsHid++
			for _, i := range incl {
				r.exempt[i] = true
			}
		case len(incl) > 3:
			chains[key.String()] = true
			out = append(out, r.lines[incl[0]],
				fmt.Sprintf("… %d more include lines …", len(incl)-2),
				r.lines[incl[len(incl)-1]])
			for _, i := range incl[1 : len(incl)-1] {
				r.exempt[i] = true
			}
		default:
			chains[key.String()] = true
			for _, i := range incl {
				out = append(out, r.lines[i])
			}
		}
	}
	for _, i := range ctx {
		out = append(out, r.lines[i])
	}
	return out
}

func (r *nativeRender) segment(idx []int) []string {
	seg := make([]string, len(idx))
	for k, i := range idx {
		seg[k] = r.lines[i]
	}

	contiguous := idx[len(idx)-1]-idx[0] == len(idx)-1
	if contiguous {
		if inner := detectInner(r.c, seg, r.parser.exitAfter(idx[len(idx)-1]+1)); inner != nil {
			return r.delegate(inner, idx[0], idx[len(idx)-1]+1)
		}
	}
	return genericLines(r.c, seg)
}

func (r *nativeRender) delegate(inner *engine.Context, start, end int) []string {
	body := r.lines[start:end]
	if len(body) == 0 {
		return nil
	}
	f := engine.Find(inner)
	if f != nil && !isNative(f.Name()) && !isContent(f) {
		if res, ok := safeApply(f, inner, strings.Join(body, "\n")); ok {
			if g, guarded := f.(engine.Guarded); guarded && g.GuardsErrors() {
				r.markExempt(start, end)
			}
			return splitLines(res)
		}
	}
	return genericLines(r.c, body)
}

func safeApply(f engine.Filter, c *engine.Context, in string) (out string, ok bool) {
	defer func() {
		if recover() != nil {
			out, ok = "", false
		}
	}()
	return f.Apply(c, in)
}
