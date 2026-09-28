package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tee"
	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
	"github.com/iheeb1/lx/internal/track"
)

const (
	showListed        = 20
	showArgvWidth     = 80
	showListArgv      = 120
	showErrorsContext = 3
)

type showEnv struct {
	stdout, stderr io.Writer
	now            time.Time
	cwd, home      string
	limit, cap     int
	piped          bool
}

func cmdShow(args []string) int {
	cwd, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	limit, capc := hostLimits()
	return runShow(args, showEnv{stdout: os.Stdout, stderr: os.Stderr, now: time.Now(),
		cwd: cwd, home: home, limit: limit, cap: capc, piped: showStdoutPiped(os.Stdout, os.Stderr)})
}

func showStdoutPiped(stdout, stderr *os.File) bool {
	fo, err := stdout.Stat()
	if err != nil {
		return false
	}
	switch m := fo.Mode(); {
	case m&(os.ModeNamedPipe|os.ModeSocket) != 0:
		return true
	case m.IsRegular():
		fe, err := stderr.Stat()
		return err == nil && fe.Mode().IsRegular() && !os.SameFile(fo, fe)
	}
	return false
}

type showSel struct {
	errors     bool
	grep       string
	re         *regexp.Regexp
	ctx        int
	ctxSet     bool
	lo, hi     int
	linesSet   bool
	head, tail int
}

func (s showSel) numbered() bool {
	return s.errors || s.re != nil || s.linesSet || s.head > 0 || s.tail > 0
}

func (s showSel) mode() string {
	switch {
	case s.errors:
		return "errors"
	case s.re != nil:
		return "grep"
	case s.linesSet:
		return "lines"
	case s.head > 0:
		return "head"
	case s.tail > 0:
		return "tail"
	}
	return "full"
}

func runShow(args []string, env showEnv) int {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	fs.SetOutput(env.stderr)
	fs.Usage = func() {
		fmt.Fprintln(env.stderr, "usage: lx show [ID|last|last~N] [--errors] [--grep RE] [-C N] [--lines A-B] [--head N] [--tail N] [--full] [--raw]\n       lx show [--all]")
		fs.PrintDefaults()
	}
	var s showSel
	fs.BoolVar(&s.errors, "errors", false, "only error and warning lines, with -C context (default 3)")
	fs.StringVar(&s.grep, "grep", "", "only lines matching this regexp")
	ctx := fs.Int("C", -1, "context lines around --errors/--grep matches")
	fs.IntVar(ctx, "context", -1, "same as -C")
	lines := fs.String("lines", "", "only lines A-B (1-based, inclusive; A- to the end)")
	fs.IntVar(&s.head, "head", 0, "keep the first N selected lines")
	fs.IntVar(&s.tail, "tail", 0, "keep the last N selected lines")
	full := fs.Bool("full", false, "don't fit the output to the agent's output limit")
	raw := fs.Bool("raw", false, "print the stored bytes exactly (ANSI included; implies --full, header on stderr)")
	all := fs.Bool("all", false, "list runs from every directory")
	origArgs := args
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		args = fs.Args()
		if len(args) > 0 {
			pos = append(pos, args[0])
			args = args[1:]
		}
	}
	bad := func(format string, a ...any) int {
		fmt.Fprintf(env.stderr, "lx show: "+format+"\n", a...)
		return 2
	}
	if len(pos) > 1 {
		return bad("one run at a time (got %s)", strings.Join(pos, " "))
	}
	if *ctx < -1 || s.head < 0 || s.tail < 0 {
		return bad("-C, --head and --tail take a count")
	}
	if *ctx >= 0 {
		s.ctx, s.ctxSet = *ctx, true
	} else if s.errors {
		s.ctx = showErrorsContext
	}
	if *lines != "" {
		lo, hi, ok := parseShowLines(*lines)
		if !ok {
			return bad("bad --lines %q (want A-B, A- or A)", *lines)
		}
		s.lo, s.hi, s.linesSet = lo, hi, true
	}
	if s.grep != "" {
		re, err := regexp.Compile(s.grep)
		if err != nil {
			return bad("bad --grep: %v", err)
		}
		s.re = re
	}

	proj := showFindProject(env.cwd)
	if len(pos) == 0 {
		if s.numbered() || *ctx >= 0 || *full || *raw {

			q := make([]string, len(origArgs))
			for i, a := range origArgs {
				q[i] = showQuote(a)
			}
			return bad("a selection needs a run: lx show last %s (lx show ID …; lx show alone lists runs)", strings.Join(q, " "))
		}
		return showList(env, tee.Recent(math.MaxInt), proj, *all)
	}
	runs := &showRuns{}
	id, err := showResolve(pos[0], runs, proj)
	if err != nil {
		fmt.Fprintln(env.stderr, "lx show:", err)
		if errors.Is(err, showErrBadID) {
			return 2
		}
		return 1
	}
	out, meta, err := tee.Load(id)
	if err != nil {
		fmt.Fprintln(env.stderr, "lx show:", err)
		return 1
	}
	r := &showRun{env: env, id: id, meta: meta}
	if *raw {
		r.text = out
	} else {
		r.text = textutil.Clean(out)
	}
	r.lines = showSplit(r.text)
	r.notes = r.provenance(runs, proj)

	var printed string
	switch {
	case *raw:
		printed = r.printRaw(s)
	case env.piped:
		printed = r.printPiped(s)
	default:
		printed = r.print(s, *full || env.cap <= 0)
	}

	cmd := cmdKey(meta.Argv)
	if cmd == "" {
		cmd = "(unknown)"
	}
	_ = track.Add(track.Record{Kind: track.KindShow, Cmd: cmd, Of: id, Mode: s.mode(), Out: tokens.Count(printed)})

	tuneRecordShow(id, meta, s, *full || *raw, env.piped, env.now)
	return 0
}

func parseShowLines(v string) (lo, hi int, ok bool) {
	a, b, dash := strings.Cut(v, "-")
	lo, err := strconv.Atoi(a)
	if err != nil || lo < 1 {
		return 0, 0, false
	}
	switch {
	case !dash:
		hi = lo
	case b == "":
		hi = 0
	default:
		if hi, err = strconv.Atoi(b); err != nil || hi < lo {
			return 0, 0, false
		}
	}
	return lo, hi, true
}

var showErrBadID = errors.New("bad id")

type showRuns struct {
	metas []tee.Meta
	all   bool
}

func (r *showRuns) newest(n int) []tee.Meta {
	if !r.all && len(r.metas) < n {
		r.metas = tee.Recent(n)
		r.all = len(r.metas) < n
	}
	return r.metas
}

func (r *showRuns) each(f func(tee.Meta) bool) {
	below := math.MaxInt
	for n := 16; ; n *= 4 {
		for _, m := range r.newest(n) {
			if m.ID >= below {
				continue
			}
			below = m.ID
			if !f(m) {
				return
			}
		}
		if r.all {
			return
		}
	}
}

func showResolve(spec string, runs *showRuns, proj showProject) (int, error) {
	if rest, ok := strings.CutPrefix(spec, "last"); ok {
		back := 0
		if rest != "" {
			n, err := strconv.Atoi(strings.TrimPrefix(rest, "~"))
			if !strings.HasPrefix(rest, "~") || err != nil || n < 0 {
				return 0, fmt.Errorf("%w %q (want N, #N, last or last~N)", showErrBadID, spec)
			}
			back = n
		}
		id, seen := 0, 0
		runs.each(func(m tee.Meta) bool {
			if proj.contains(m.Cwd) {
				if seen == back {
					id = m.ID
				}
				seen++
			}
			return id == 0
		})
		if id > 0 {
			return id, nil
		}
		where := "in this project"
		if seen == 0 {
			all := runs.newest(back + 1)
			if back < len(all) {
				return all[back].ID, nil
			}
			seen, where = len(all), "anywhere"
		}
		if seen == 0 {
			return 0, errors.New("no stored runs yet (they're stored when lx condenses a view)")
		}
		return 0, fmt.Errorf("%s: only %d stored %s %s (lx show --all lists them)", spec, seen, showPlural(seen, "run", "runs"), where)
	}
	id, err := strconv.Atoi(strings.TrimPrefix(spec, "#"))
	if err != nil || id < 1 {
		return 0, fmt.Errorf("%w %q (want N, #N, last or last~N)", showErrBadID, spec)
	}
	return id, nil
}

type showProject struct {
	root, real string

	resolved map[string]string
}

func showFindProject(cwd string) showProject {
	if cwd == "" {
		return showProject{}
	}
	cwd = filepath.Clean(cwd)
	p := showProject{root: cwd, resolved: map[string]string{}}
	for d := cwd; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			p.root = d
			break
		}
		up := filepath.Dir(d)
		if up == d {
			break
		}
		d = up
	}
	if real, err := filepath.EvalSymlinks(p.root); err == nil && real != p.root {
		p.real = real
	}
	return p
}

func (p showProject) contains(dir string) bool {
	if dir == "" || p.root == "" {
		return false
	}
	dir = filepath.Clean(dir)
	if p.under(dir) {
		return true
	}

	real, ok := p.resolved[dir]
	if !ok {
		real, _ = filepath.EvalSymlinks(dir)
		if p.resolved != nil {
			p.resolved[dir] = real
		}
	}
	return real != "" && real != dir && p.under(real)
}

func (p showProject) under(dir string) bool {
	sep := string(filepath.Separator)
	in := func(root string) bool {
		return root != "" && (dir == root || strings.HasPrefix(dir, strings.TrimSuffix(root, sep)+sep))
	}
	return in(p.root) || in(p.real)
}

type showRun struct {
	env   showEnv
	id    int
	meta  tee.Meta
	text  string
	lines []string
	notes []string

	level  []int8
	grepRe *regexp.Regexp
	grep   []int8
}

type showLine struct {
	text string
	n    int
}

func showSplit(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func (r *showRun) provenance(runs *showRuns, proj showProject) []string {
	var notes []string
	if len(r.meta.Argv) > 0 {
		runs.each(func(m tee.Meta) bool {
			if m.ID <= r.id {
				return false
			}
			if m.Cwd != r.meta.Cwd || !slices.Equal(m.Argv, r.meta.Argv) {
				return true
			}
			what := showExit(m)
			if note := m.StatusNote(); note != "" {
				what = note
			}
			if ago := showAgo(r.env.now, m.Time); ago != "" {
				what += ", " + ago
			}
			notes = append(notes, fmt.Sprintf("[lx: a newer run of this command exists: lx show %d (%s)]", m.ID, what))
			return false
		})
	}
	if r.meta.Cwd != "" && !proj.contains(r.meta.Cwd) {
		notes = append(notes, fmt.Sprintf("[lx: run %d ran in %s, not in this project]", r.id, showCut(showTilde(r.meta.Cwd, r.env.home), showListArgv)))
	}
	return notes
}

func (r *showRun) header(extra ...string) string {
	m := r.meta
	parts := []string{fmt.Sprintf("lx show %d", r.id)}
	note := m.StatusNote()
	if len(m.Argv) == 0 && m.Time.IsZero() {
		parts = append(parts, "(no metadata)")
	} else {
		parts = append(parts, showArgv(m.Argv, showArgvWidth))
		if note == "" {
			parts = append(parts, showExit(m))
		}
		if ago := showAgo(r.env.now, m.Time); ago != "" {
			parts = append(parts, ago)
		}
	}
	parts = append(parts, showCommas(len(r.lines))+" "+showPlural(len(r.lines), "line", "lines"))
	parts = append(parts, extra...)
	if note != "" {
		parts = append(parts, note)
	}
	return "[" + strings.Join(parts, " · ") + "]"
}

func showExit(m tee.Meta) string {
	if m.Exit < 0 {
		return "exit unknown"
	}
	return "exit " + strconv.Itoa(m.Exit)
}

func (r *showRun) levelAt(i int) engine.Level {
	if r.level == nil {
		r.level = make([]int8, len(r.lines))
	}
	if r.level[i] == 0 {
		r.level[i] = int8(engine.Classify(r.lines[i])) + 1
	}
	return engine.Level(r.level[i] - 1)
}

func (r *showRun) grepAt(re *regexp.Regexp, i int) bool {
	if r.grepRe != re {
		r.grepRe, r.grep = re, make([]int8, len(r.lines))
	}
	if r.grep[i] == 0 {
		r.grep[i] = 1
		if re.MatchString(r.lines[i]) {
			r.grep[i] = 2
		}
	}
	return r.grep[i] == 2
}

func (r *showRun) pick(s showSel) (idx []int, match []bool, nerr, nwarn, ngrep int) {
	n := len(r.lines)
	lo, hi := 0, n-1
	if s.linesSet {
		lo = s.lo - 1
		if s.hi > 0 {
			hi = min(hi, s.hi-1)
		}
	}
	if lo > hi {
		return nil, nil, 0, 0, 0
	}
	if !s.errors && s.re == nil {
		idx = make([]int, 0, hi-lo+1)
		match = make([]bool, 0, hi-lo+1)
		for i := lo; i <= hi; i++ {
			idx = append(idx, i)
			match = append(match, true)
		}
	} else {

		ctx := min(max(s.ctx, 0), hi-lo)
		kept, until := lo-1, lo-1
		for i := lo; i <= hi; i++ {
			m := false
			if s.errors {
				switch r.levelAt(i) {
				case engine.Err:
					nerr++
					m = true
				case engine.Warn:
					nwarn++
					m = true
				}
			}
			if s.re != nil && r.grepAt(s.re, i) {
				ngrep++
				m = true
			}
			switch {
			case m:
				for j := max(lo, i-ctx, kept+1); j < i; j++ {
					idx = append(idx, j)
					match = append(match, false)
				}
				idx = append(idx, i)
				match = append(match, true)
				kept, until = i, i+ctx
			case i <= until:
				idx = append(idx, i)
				match = append(match, false)
				kept = i
			}
		}
	}

	if (s.head > 0 || s.tail > 0) && s.head < len(idx) && s.tail < len(idx) && s.head+s.tail < len(idx) {
		var ni []int
		var nm []bool
		for k := range idx {
			if k < s.head || k >= len(idx)-s.tail {
				ni = append(ni, idx[k])
				nm = append(nm, match[k])
			}
		}
		idx, match = ni, nm
	}
	return idx, match, nerr, nwarn, ngrep
}

func (r *showRun) render(s showSel) (head []string, body []showLine) {
	head, body, _ = r.build(s, true)
	return head, body
}

func (r *showRun) size(s showSel) int {
	_, _, n := r.build(s, false)
	return n
}

func (r *showRun) build(s showSel, keep bool) (head []string, body []showLine, size int) {
	idx, match, nerr, nwarn, ngrep := r.pick(s)
	var extra []string
	if s.errors {
		extra = append(extra, fmt.Sprintf("%s %s, %s %s", showCommas(nerr), showPlural(nerr, "error line", "error lines"),
			showCommas(nwarn), showPlural(nwarn, "warning", "warnings")))
	}
	if s.re != nil {
		extra = append(extra, fmt.Sprintf("%s %s --grep", showCommas(ngrep), showPlural(ngrep, "line matches", "lines match")))
	}
	head = append([]string{r.header(extra...)}, r.notes...)
	size = showSize(head)
	add := func(l showLine) {
		size += len(l.text) + 1
		if keep {
			body = append(body, l)
		}
	}

	switch {
	case len(idx) == 0:
		add(showLine{text: r.emptyNote(s)})
	case !s.numbered():
		for _, i := range idx {
			add(showLine{r.lines[i], i + 1})
		}
	default:
		ctxMode := s.ctx > 0 && (s.errors || s.re != nil)
		prev := -1
		for k, i := range idx {
			if prev >= 0 && i > prev+1 {
				add(showLine{text: showGap(prev+2, i)})
			}
			prev = i
			if !keep {

				size += max(6, showDigits(i+1)) + 2 + len(r.lines[i]) + 1
				continue
			}
			sep := "  "
			if ctxMode {
				sep = "- "
				if match[k] {
					sep = ": "
				}
			}
			add(showLine{fmt.Sprintf("%6d%s%s", i+1, sep, r.lines[i]), i + 1})
		}
	}
	return head, body, size
}

func showDigits(n int) int {
	d := 1
	for ; n >= 10; n /= 10 {
		d++
	}
	return d
}

func (r *showRun) emptyNote(s showSel) string {
	n := len(r.lines)
	switch {
	case n == 0:
		return fmt.Sprintf("[lx: run %d printed nothing]", r.id)
	case s.linesSet && s.lo > n:
		return fmt.Sprintf("[lx: run %d has %s %s; --lines %s is past the end]", r.id,
			showCommas(n), showPlural(n, "line", "lines"), showRange(s.lo, s.hi))
	}
	where := fmt.Sprintf("run %d", r.id)
	if s.linesSet {
		hi := n
		if s.hi > 0 {
			hi = min(s.hi, n)
		}
		where = fmt.Sprintf("lines %s of run %d", showRange(s.lo, hi), r.id)
	}
	switch {
	case s.errors && s.re != nil:
		return fmt.Sprintf("[lx: no error, warning or --grep lines in %s; lx show %d --tail 40 shows the end]", where, r.id)
	case s.errors:
		return fmt.Sprintf("[lx: no error or warning lines in %s; lx show %d --tail 40 shows the end]", where, r.id)
	case s.re != nil:
		return fmt.Sprintf("[lx: no line of %s matches --grep %s]", where, showQuote(s.grep))
	}
	return fmt.Sprintf("[lx: nothing selected in %s]", where)
}

func (r *showRun) print(s showSel, full bool) string {
	head, body := r.render(s)
	out := head
	if full {
		for _, l := range body {
			out = append(out, l.text)
		}
	} else {
		out = r.fit(s, head, body)
	}
	var b strings.Builder
	b.Grow(showSize(out))
	for _, ln := range out {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	io.WriteString(r.env.stdout, b.String())
	return b.String()
}

func (r *showRun) printPiped(s showSel) string {
	head, body := r.render(s)
	h := strings.Join(head, "\n") + "\n"
	io.WriteString(r.env.stderr, h)
	var b strings.Builder
	for _, l := range body {
		b.WriteString(l.text)
		b.WriteByte('\n')
	}
	io.WriteString(r.env.stdout, b.String())
	return h + b.String()
}

func (r *showRun) printRaw(s showSel) string {
	head, body := r.render(s)
	h := strings.Join(head, "\n") + "\n"
	io.WriteString(r.env.stderr, h)
	if !s.numbered() {
		io.WriteString(r.env.stdout, r.text)
		return h + r.text
	}
	var b strings.Builder
	for _, l := range body {
		b.WriteString(l.text)
		b.WriteByte('\n')
	}
	io.WriteString(r.env.stdout, b.String())
	return h + b.String()
}

func showSize(ls []string) int {
	n := 0
	for _, l := range ls {
		n += len(l) + 1
	}
	return n
}

func (r *showRun) fit(s showSel, head []string, body []showLine) []string {
	capc := r.env.cap
	hs := showSize(head)
	pre := make([]int, len(body)+1)
	for i, l := range body {
		pre[i+1] = pre[i] + len(l.text) + 1
	}
	out := func(k int, tail ...string) []string {
		res := append([]string(nil), head...)
		for _, l := range body[:k] {
			res = append(res, l.text)
		}
		return append(res, tail...)
	}
	if hs+pre[len(body)] <= capc {
		return out(len(body))
	}
	k := 0
	for k < len(body) && hs+pre[k+1] <= capc {
		k++
	}
	last := 0
	for i := len(body) - 1; i >= 0; i-- {
		if body[i].n > 0 {
			last = body[i].n
			break
		}
	}
	for ; k >= 0; k-- {
		for k > 0 && body[k-1].n == 0 {
			k--
		}
		first := 0
		for j := k; j < len(body); j++ {
			if body[j].n > 0 {
				first = body[j].n
				break
			}
		}
		if first == 0 {
			return out(k)
		}
		tr := r.trailer(s, first, last)
		if hs+pre[k]+len(tr)+1 <= capc || k == 0 {
			return out(k, tr)
		}
	}
	return out(0)
}

func (r *showRun) trailer(s showSel, first, last int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "… [%s not shown: over this agent's %s-character output limit", showLinesWord(first, last), showCommas(r.env.limit))
	next := s
	next.linesSet, next.lo, next.head, next.tail = true, first, 0, 0
	fits := func(x int) bool {
		next.hi = x
		return r.size(next) <= r.env.cap
	}
	if !fits(first) {
		next.hi = first
		fmt.Fprintf(&b, " · line %s alone is longer than that: %s --full prints it (the host may save it to a file)]",
			showCommas(first), r.command(next))
		return b.String()
	}
	lo, hi := first, last
	if !s.errors && s.re == nil {

		hi = min(hi, first+r.env.cap/9)
	}
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		if fits(mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	next.hi = lo
	b.WriteString(" · next: " + r.command(next))
	if !s.errors && s.re == nil {
		b.WriteString(" · or --errors / --grep RE")
	}
	b.WriteString("]")
	return b.String()
}

func (r *showRun) command(s showSel) string {
	c := "lx show " + strconv.Itoa(r.id)
	if s.errors {
		c += " --errors"
	}
	if s.re != nil {
		c += " --grep " + showQuote(s.grep)
	}
	if s.ctxSet {
		c += " -C " + strconv.Itoa(s.ctx)
	}
	if s.linesSet {
		c += " --lines " + strconv.Itoa(s.lo) + "-"
		if s.hi > 0 {
			c += strconv.Itoa(s.hi)
		}
	}
	return c
}

func showList(env showEnv, runs []tee.Meta, proj showProject, all bool) int {
	if len(runs) == 0 {
		fmt.Fprintln(env.stdout, "lx show: no stored outputs yet (they're stored when lx condenses a view)")
		return 0
	}
	var list []tee.Meta
	older, elsewhere := 0, 0
	for _, m := range runs {
		switch {
		case all:
			list = append(list, m)
		case !proj.contains(m.Cwd):
			elsewhere++
		case len(list) < showListed:
			list = append(list, m)
		default:
			older++
		}
	}
	var lines []string
	for _, m := range list {
		status := fmt.Sprintf("exit %-3d", m.Exit)
		if m.Exit < 0 {
			status = "exit ?  "
		}
		switch m.State {
		case tee.StateDone:
		case tee.StateRunning:
			status = "running "
		default:
			status = "stopped "
		}
		when := "--         "
		if !m.Time.IsZero() {
			when = m.Time.Local().Format("01-02 15:04")
		}
		lines = append(lines, fmt.Sprintf("%5d  %s  %s %-12s %s", m.ID, when, status, m.Filter, showArgv(m.Argv, showListArgv)))
	}
	if len(list) == 0 {
		lines = append(lines, "(no stored runs from this project)")
	}
	var more []string
	if older > 0 {
		more = append(more, fmt.Sprintf("+%d older %s here", older, showPlural(older, "run", "runs")))
	}
	if elsewhere > 0 {
		more = append(more, fmt.Sprintf("+%d %s from other directories", elsewhere, showPlural(elsewhere, "run", "runs")))
	}
	if len(more) > 0 {
		lines = append(lines, "("+strings.Join(more, ", ")+": lx show --all)")
	}
	if env.cap > 0 && !env.piped && showSize(lines) > env.cap {

		k, n := 0, 0
		for k < len(list) && n+len(lines[k])+1+120 <= env.cap {
			n += len(lines[k]) + 1
			k++
		}
		if rest := len(list) - k; rest > 0 {
			lines = append(lines[:k:k], fmt.Sprintf("… [%d more %s not shown: over this agent's %s-character output limit]",
				rest, showPlural(rest, "run", "runs"), showCommas(env.limit)))
		}
	}
	w := bufio.NewWriter(env.stdout)
	for _, l := range lines {
		w.WriteString(l)
		w.WriteByte('\n')
	}
	w.Flush()
	return 0
}

func showArgv(argv []string, width int) string {
	if len(argv) == 0 {
		return "(unknown command)"
	}
	return showCut(strings.Join(argv, " "), width)
}

func showCut(s string, width int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	if rs := []rune(s); len(rs) > width {
		s = string(rs[:width-1]) + "…"
	}
	return s
}

func showAgo(now, t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := max(now.Sub(t), 0)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d/time.Hour))
	}
	return fmt.Sprintf("%d days ago", int(d/(24*time.Hour)))
}

func showTilde(dir, home string) string {
	if home != "" && home != "/" && (dir == home || strings.HasPrefix(dir, home+"/")) {
		return "~" + dir[len(home):]
	}
	return dir
}

func showCommas(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + showCommas(-n)
	}
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func showPlural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func showGap(a, b int) string {
	if a == b {
		return fmt.Sprintf("     … line %s …", showCommas(a))
	}
	return fmt.Sprintf("     … lines %s-%s (%s lines) …", showCommas(a), showCommas(b), showCommas(b-a+1))
}

func showRange(lo, hi int) string {
	if hi == 0 {
		return strconv.Itoa(lo) + "-"
	}
	return strconv.Itoa(lo) + "-" + strconv.Itoa(hi)
}

func showLinesWord(a, b int) string {
	if a == b {
		return "line " + showCommas(a)
	}
	return "lines " + showCommas(a) + "-" + showCommas(b)
}

func showQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == ',' || r == '=' ||
			r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
