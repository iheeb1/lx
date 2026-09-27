package cli

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/tee"
	"github.com/iheeb1/lx/internal/testenv"
	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
	"github.com/iheeb1/lx/internal/track"
)

// showWorld is an isolated store, history, home and project for lx show.
type showWorld struct {
	t       *testing.T
	now     time.Time
	home    string
	project string // a directory holding .git
	cwd     string
}

func newShowWorld(t *testing.T) *showWorld {
	t.Helper()
	hostcapEnv(t, "")
	base := t.TempDir()
	t.Setenv("LX_TEE_DIR", filepath.Join(base, "runs"))
	t.Setenv("LX_DATA_DIR", filepath.Join(base, "data"))
	t.Setenv("LX_TEE", "")
	t.Setenv("LX_TRACK", "")
	w := &showWorld{t: t, now: time.Now(), home: filepath.Join(base, "home")}
	w.project = filepath.Join(w.home, "src", "app")
	if err := os.MkdirAll(filepath.Join(w.project, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(w.project, "pkg", "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	w.cwd = w.project
	return w
}

// save stores a run that finished ago before now.
func (w *showWorld) save(argv string, cwd string, exit int, ago time.Duration, out string) int {
	w.t.Helper()
	id, err := tee.Save(tee.Meta{Argv: strings.Fields(argv), Cwd: cwd, Exit: exit, Filter: "generic", Time: w.now.Add(-ago)}, out)
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

// show runs lx show with the world's cwd and the host limits of the
// current environment.
func (w *showWorld) show(args ...string) (stdout, stderr string, code int) {
	w.t.Helper()
	var o, e bytes.Buffer
	limit, capc := hostLimits()
	code = runShow(args, showEnv{stdout: &o, stderr: &e, now: w.now, cwd: w.cwd, home: w.home, limit: limit, cap: capc})
	return o.String(), e.String(), code
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// numbered is a numbered output of n lines, some of them errors and
// warnings.
func numberedOutput(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		switch {
		case i%50 == 0:
			fmt.Fprintf(&b, "--- FAIL: TestCase%d (0.01s)\n", i)
		case i%120 == 7:
			fmt.Fprintf(&b, "warning: deprecated option in step %d\n", i)
		default:
			fmt.Fprintf(&b, "step %d ok\n", i)
		}
	}
	return b.String()
}

func TestShowHeader(t *testing.T) {
	w := newShowWorld(t)
	id := w.save("go test ./...", w.project, 1, 14*time.Minute, numberedOutput(307))
	out, _, code := w.show(strconv.Itoa(id))
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got, want := firstLine(out), "[lx show 1 · go test ./... · exit 1 · 14 min ago · 307 lines]"; got != want {
		t.Fatalf("header\n got %s\nwant %s", got, want)
	}
	// The body is the stored output, unnumbered.
	if !strings.HasPrefix(out, "[lx show 1 · go test ./... · exit 1 · 14 min ago · 307 lines]\nstep 1 ok\nstep 2 ok\n") ||
		!strings.HasSuffix(out, "step 307 ok\n") {
		t.Fatalf("body:\n%.300s", out)
	}
	out, _, _ = w.show("#1", "--errors")
	if got, want := firstLine(out), "[lx show 1 · go test ./... · exit 1 · 14 min ago · 307 lines · 6 error lines, 3 warnings]"; got != want {
		t.Fatalf("--errors header\n got %s\nwant %s", got, want)
	}

	// Long and multi-line argv: one line, cut to 80 characters.
	long := "sh -c " + strings.Repeat("x", 100)
	id = w.save(long, w.project, 0, 3*time.Hour, "a\nb\n")
	id2, err := tee.Save(tee.Meta{Argv: []string{"sh", "-c", "echo a\necho b"}, Cwd: w.project, Time: w.now.Add(-50 * time.Hour)}, "a\n")
	if err != nil {
		t.Fatal(err)
	}
	out, _, _ = w.show(strconv.Itoa(id))
	if got, want := firstLine(out), "[lx show 2 · sh -c "+strings.Repeat("x", 73)+"… · exit 0 · 3 h ago · 2 lines]"; got != want {
		t.Fatalf("long argv\n got %s\nwant %s", got, want)
	}
	out, _, _ = w.show(strconv.Itoa(id2))
	if got, want := firstLine(out), "[lx show 3 · sh -c echo a echo b · exit 0 · 2 days ago · 1 line]"; got != want {
		t.Fatalf("multi-line argv\n got %s\nwant %s", got, want)
	}

	// A run whose metadata is gone claims no exit status.
	id = w.save("make", w.project, 2, time.Minute, "x\ny\nz\n")
	os.Remove(filepath.Join(tee.Dir(), strconv.Itoa(id)+".json"))
	out, _, _ = w.show(strconv.Itoa(id))
	if got, want := firstLine(out), "[lx show 4 · (no metadata) · 3 lines]"; got != want {
		t.Fatalf("no metadata\n got %s\nwant %s", got, want)
	}
}

// A run still going (reserved by a live lx) has no exit status yet: the
// header says so instead of "exit -1".
func TestShowHeaderRunningRun(t *testing.T) {
	w := newShowWorld(t)
	sp, err := tee.Reserve(tee.Meta{Argv: []string{"npm", "test"}, Cwd: w.project, Time: w.now.Add(-2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	sp.Write([]byte("PASS a\nPASS b\n"))
	out, _, _ := w.show("last")
	h := firstLine(out)
	if strings.Contains(h, "exit") || !strings.Contains(h, "still running") || !strings.HasPrefix(h, "[lx show 1 · npm test · 2 min ago · 2 lines · ") {
		t.Fatalf("running header: %s", h)
	}
}

func TestShowLastScopedToProject(t *testing.T) {
	w := newShowWorld(t)
	other := filepath.Join(w.home, "projects", "other")
	a := w.save("go test ./...", w.project, 1, 50*time.Minute, "a\n")
	b := w.save("go build", other, 0, 40*time.Minute, "b\n")
	c := w.save("go vet ./...", filepath.Join(w.project, "pkg", "sub"), 1, 30*time.Minute, "c\n")
	d := w.save("ls -R", other, 0, 20*time.Minute, "d\n")
	w.cwd = filepath.Join(w.project, "pkg") // anywhere inside the project

	for spec, want := range map[string]int{"last": c, "last~0": c, "last~1": a} {
		out, _, code := w.show(spec)
		if code != 0 || !strings.HasPrefix(out, fmt.Sprintf("[lx show %d · ", want)) {
			t.Errorf("%s: exit %d, got %s", spec, code, firstLine(out))
		}
		if strings.Contains(out, "not in this project") {
			t.Errorf("%s: a project run got the cwd note", spec)
		}
	}
	if _, errOut, code := w.show("last~2"); code != 1 || !strings.Contains(errOut, "only 2 stored runs in this project") {
		t.Errorf("last~2 past the project's runs: exit %d %q", code, errOut)
	}

	// A directory with no runs of its own falls back to the newest anywhere,
	// and says where that run came from.
	w.cwd = filepath.Join(w.home, "empty")
	os.MkdirAll(w.cwd, 0o700)
	for spec, want := range map[string]int{"last": d, "last~1": c, "last~3": a} {
		out, _, _ := w.show(spec)
		if !strings.HasPrefix(out, fmt.Sprintf("[lx show %d · ", want)) {
			t.Errorf("fallback %s: got %s", spec, firstLine(out))
		}
	}
	out, _, _ := w.show("last")
	if !strings.Contains(out, "\n[lx: run 4 ran in ~/projects/other, not in this project]\n") {
		t.Errorf("cwd note missing:\n%s", out)
	}
	_ = b
}

func TestShowNewerRunNote(t *testing.T) {
	w := newShowWorld(t)
	other := filepath.Join(w.home, "projects", "other")
	old := w.save("go test ./...", w.project, 1, 30*time.Minute, "FAIL\n")
	w.save("go test ./pkg", w.project, 1, 20*time.Minute, "x\n")          // other argv
	w.save("go test ./...", other, 0, 10*time.Minute, "ok\n")             // other cwd
	newer := w.save("go test ./...", w.project, 0, 2*time.Minute, "ok\n") // the same command, again
	w.save("go test ./...", w.project+"x", 0, time.Minute, "ok\n")        // a sibling directory, not the same

	out, _, _ := w.show(strconv.Itoa(old))
	want := fmt.Sprintf("[lx: a newer run of this command exists: lx show %d (exit 0, 2 min ago)]", newer)
	if lines := strings.Split(out, "\n"); len(lines) < 2 || lines[1] != want {
		t.Fatalf("newer-run note:\n%s\nwant line 2: %s", out, want)
	}
	for _, id := range []int{2, 3, newer} {
		if out, _, _ := w.show(strconv.Itoa(id)); strings.Contains(out, "a newer run") {
			t.Errorf("run %d: no newer run has its argv and cwd:\n%s", id, out)
		}
	}
	// Run 3 ran elsewhere: it gets the cwd note, run 5's sibling too.
	if out, _, _ := w.show("3"); !strings.Contains(out, "[lx: run 3 ran in ~/projects/other, not in this project]") {
		t.Errorf("cwd note for run 3:\n%s", out)
	}
	if out, _, _ := w.show("5"); !strings.Contains(out, "[lx: run 5 ran in ~/src/appx, not in this project]") {
		t.Errorf("a sibling directory with a shared prefix is not in the project:\n%s", out)
	}
}

// --errors on every failing capture of the corpus selects every error line.
func TestShowErrorsCorpus(t *testing.T) {
	w := newShowWorld(t)
	matchRe := regexp.MustCompile(`^\s*(\d+): `)
	n := 0
	for _, c := range fixture.All(t) {
		if c.Meta.ExitCode == 0 {
			continue
		}
		n++
		id := w.save(strings.Join(c.Meta.Argv, " "), w.project, c.Meta.ExitCode, time.Minute, c.Raw)
		out, _, code := w.show(strconv.Itoa(id), "--errors")
		if code != 0 {
			t.Fatalf("%s: exit %d", c.Name, code)
		}
		got := map[int]bool{}
		for _, ln := range strings.Split(out, "\n") {
			if m := matchRe.FindStringSubmatch(ln); m != nil {
				k, _ := strconv.Atoi(m[1])
				got[k] = true
			}
		}
		nerr := 0
		for i, ln := range showSplit(textutil.Clean(c.Raw)) {
			if engine.Classify(ln) == engine.Err {
				nerr++
				if !got[i+1] {
					t.Errorf("%s/%s: error line %d not selected: %q", c.Category, c.Name, i+1, ln)
				}
			}
		}
		if !strings.Contains(firstLine(out), fmt.Sprintf(" · %s %s, ", showCommas(nerr), showPlural(nerr, "error line", "error lines"))) {
			t.Errorf("%s: header count: %s (want %d error lines)", c.Name, firstLine(out), nerr)
		}
	}
	if n < 30 {
		t.Fatalf("only %d failing captures", n)
	}
}

func TestShowGrepContextGolden(t *testing.T) {
	w := newShowWorld(t)
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		switch i {
		case 5, 7, 22:
			fmt.Fprintf(&b, "FAIL case %d: want 1, got 2\n", i)
		default:
			fmt.Fprintf(&b, "ok   case %d\n", i)
		}
	}
	id := w.save("pytest -q", w.project, 1, 5*time.Minute, b.String())
	out, _, _ := w.show(strconv.Itoa(id), "--grep", "^FAIL", "-C", "2")
	want := `[lx show 1 · pytest -q · exit 1 · 5 min ago · 30 lines · 3 lines match --grep]
     3- ok   case 3
     4- ok   case 4
     5: FAIL case 5: want 1, got 2
     6- ok   case 6
     7: FAIL case 7: want 1, got 2
     8- ok   case 8
     9- ok   case 9
     … lines 10-19 (10 lines) …
    20- ok   case 20
    21- ok   case 21
    22: FAIL case 22: want 1, got 2
    23- ok   case 23
    24- ok   case 24
`
	if out != want {
		t.Fatalf("--grep -C 2:\n%s\nwant:\n%s", out, want)
	}
	// No context: the plain numbered form.
	out, _, _ = w.show(strconv.Itoa(id), "--grep", "case 2[12]$")
	want = `[lx show 1 · pytest -q · exit 1 · 5 min ago · 30 lines · 1 line matches --grep]
    21  ok   case 21
`
	if out != want {
		t.Fatalf("--grep:\n%s\nwant:\n%s", out, want)
	}
	// Selection order: --lines, then --grep, then --head/--tail.
	out, _, _ = w.show(strconv.Itoa(id), "--lines", "6-25", "--grep", "FAIL", "-C", "1", "--tail", "2")
	want = `[lx show 1 · pytest -q · exit 1 · 5 min ago · 30 lines · 2 lines match --grep]
    22: FAIL case 22: want 1, got 2
    23- ok   case 23
`
	if out != want {
		t.Fatalf("--lines --grep --tail:\n%s\nwant:\n%s", out, want)
	}
	out, _, _ = w.show(strconv.Itoa(id), "--head", "2", "--tail", "1")
	want = `[lx show 1 · pytest -q · exit 1 · 5 min ago · 30 lines]
     1  ok   case 1
     2  ok   case 2
     … lines 3-29 (27 lines) …
    30  ok   case 30
`
	if out != want {
		t.Fatalf("--head --tail:\n%s\nwant:\n%s", out, want)
	}
	out, _, _ = w.show(strconv.Itoa(id), "--errors", "--lines", "8-20")
	want = `[lx show 1 · pytest -q · exit 1 · 5 min ago · 30 lines · 0 error lines, 0 warnings]
[lx: no error or warning lines in lines 8-20 of run 1; lx show 1 --tail 40 shows the end]
`
	if out != want {
		t.Fatalf("--errors, none:\n%s\nwant:\n%s", out, want)
	}
	out, _, _ = w.show(strconv.Itoa(id), "--lines", "40-50")
	if !strings.Contains(out, "[lx: run 1 has 30 lines; --lines 40-50 is past the end]") {
		t.Fatalf("--lines past the end:\n%s", out)
	}
}

// The spec's example: no errors at all in the run.
func TestShowErrorsNone(t *testing.T) {
	w := newShowWorld(t)
	w.save("git log", w.project, 0, time.Minute, "a\nb\n")
	out, _, _ := w.show("1", "--errors")
	if !strings.HasSuffix(out, "\n[lx: no error or warning lines in run 1; lx show 1 --tail 40 shows the end]\n") {
		t.Fatalf("got:\n%s", out)
	}
}

var nextRe = regexp.MustCompile(`next: (lx show \d+(?: --errors)? --lines (\d+)-(\d+))`)

// The 343 KB git log capture, read back under a 27,000-character limit:
// the view and the next part it suggests both fit, and the next part picks
// up exactly where the view stopped.
func TestShowCapGitLog(t *testing.T) {
	w := newShowWorld(t)
	t.Setenv("LX_MAX_CHARS", "27000")
	c := fixture.Load(t, "git", "git-log-full-history")
	if len(c.Raw) < 300_000 {
		t.Fatalf("capture is %d bytes", len(c.Raw))
	}
	id := w.save(strings.Join(c.Meta.Argv, " "), w.project, 0, time.Minute, c.Raw)
	total := len(showSplit(textutil.Clean(c.Raw)))

	out, _, _ := w.show(strconv.Itoa(id))
	if len(out) > 27000 || len(out) > hostCharCap() {
		t.Fatalf("capped view is %d bytes", len(out))
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	tr := lines[len(lines)-1]
	shown := len(lines) - 2 // header and closing line
	wantPrefix := fmt.Sprintf("… [lines %s-%s not shown: over this agent's 27,000-character output limit · next: lx show %d --lines %d-",
		showCommas(shown+1), showCommas(total), id, shown+1)
	if !strings.HasPrefix(tr, wantPrefix) || !strings.HasSuffix(tr, " · or --errors / --grep RE]") {
		t.Fatalf("closing line:\n%s\nwant prefix:\n%s", tr, wantPrefix)
	}
	// Every line before it is whole and in order.
	clean := showSplit(textutil.Clean(c.Raw))
	for i := 1; i <= shown; i++ {
		if lines[i] != clean[i-1] {
			t.Fatalf("line %d differs", i)
		}
	}

	// Walk the whole run through the suggested commands.
	next := shown + 1
	for steps := 0; next <= total; steps++ {
		m := nextRe.FindStringSubmatch(tr)
		if m == nil {
			t.Fatalf("no next command in %q", tr)
		}
		lo, _ := strconv.Atoi(m[2])
		hi, _ := strconv.Atoi(m[3])
		if lo != next || hi < lo {
			t.Fatalf("next range %d-%d, want it to start at %d", lo, hi, next)
		}
		out, _, _ = w.show(strings.Fields(m[1])[2:]...)
		if len(out) > hostCharCap() || strings.Contains(out, "not shown") {
			t.Fatalf("suggested %q does not fit: %d bytes", m[1], len(out))
		}
		got := strings.Split(strings.TrimSuffix(out, "\n"), "\n")[1:]
		if len(got) != hi-lo+1 || !strings.HasPrefix(got[0], fmt.Sprintf("%6d  ", lo)) {
			t.Fatalf("suggested %q printed %d lines", m[1], len(got))
		}
		// The largest end that fits: one more line would not.
		if hi < total {
			more, _, _ := w.show(strconv.Itoa(id), "--lines", fmt.Sprintf("%d-%d", lo, hi+1))
			if !strings.Contains(more, "not shown") {
				t.Fatalf("--lines %d-%d fits too; %d was not the largest end", lo, hi+1, hi)
			}
		}
		next = hi + 1
		if next > total {
			break
		}
		// The capped --lines view of the rest gives the step after.
		out, _, _ = w.show(strconv.Itoa(id), "--lines", fmt.Sprintf("%d-", next))
		ls := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		tr = ls[len(ls)-1]
		if len(out) > hostCharCap() {
			t.Fatalf("--lines %d- is %d bytes", next, len(out))
		}
		first, _ := strconv.Atoi(strings.TrimSpace(ls[len(ls)-2][:6]))
		next = first + 1
		if steps > 40 {
			t.Fatal("too many steps")
		}
	}

	// --full and --raw bypass the cap; --raw is byte-exact on stdout.
	out, _, _ = w.show(strconv.Itoa(id), "--full")
	if len(out) < 300_000 || strings.Contains(out, "not shown") || !strings.HasSuffix(out, clean[len(clean)-1]+"\n") {
		t.Fatalf("--full: %d bytes", len(out))
	}
	out, errOut, _ := w.show(strconv.Itoa(id), "--raw")
	if out != c.Raw {
		t.Fatalf("--raw stdout is not the stored bytes (%d vs %d)", len(out), len(c.Raw))
	}
	if !strings.HasPrefix(errOut, fmt.Sprintf("[lx show %d · git log", id)) {
		t.Fatalf("--raw header goes to stderr: %q", firstLine(errOut))
	}
}

// Capped --errors keeps its selection in the suggested command, and the
// closing line doesn't offer --errors again.
func TestShowCapErrors(t *testing.T) {
	w := newShowWorld(t)
	t.Setenv("LX_MAX_CHARS", "3000")
	id := w.save("go test ./...", w.project, 1, time.Minute, numberedOutput(5000))
	out, _, _ := w.show(strconv.Itoa(id), "--errors")
	if len(out) > hostCharCap() {
		t.Fatalf("%d bytes", len(out))
	}
	m := nextRe.FindStringSubmatch(out)
	if m == nil || !strings.Contains(m[1], " --errors --lines ") || strings.Contains(out, "or --errors") {
		t.Fatalf("closing line:\n%s", out)
	}
	next, _, _ := w.show(strings.Fields(m[1])[2:]...)
	if len(next) > hostCharCap() || strings.Contains(next, "not shown") || !strings.Contains(next, ": --- FAIL") {
		t.Fatalf("suggested %q:\n%s", m[1], next)
	}
}

// One line longer than the whole limit can't be cut to fit: say so and
// point at --full.
func TestShowCapHugeLine(t *testing.T) {
	w := newShowWorld(t)
	t.Setenv("LX_MAX_CHARS", "2000")
	id := w.save("cat bundle.min.js", w.project, 0, time.Minute, "head\n"+strings.Repeat("x", 5000)+"\ntail\n")
	out, _, _ := w.show(strconv.Itoa(id))
	if len(out) > hostCharCap() {
		t.Fatalf("%d bytes", len(out))
	}
	if !strings.Contains(out, "\nhead\n… [lines 2-3 not shown: over this agent's 2,000-character output limit · line 2 alone is longer than that: lx show 1 --lines 2-2 --full prints it") {
		t.Fatalf("got:\n%s", out)
	}
}

// Nothing is capped without a host limit, and LX_MAX_CHARS=0 turns the
// cap off inside Claude Code.
func TestShowNoCap(t *testing.T) {
	w := newShowWorld(t)
	id := w.save("seq 5000", w.project, 0, time.Minute, numberedOutput(5000))
	out, _, _ := w.show(strconv.Itoa(id))
	if strings.Contains(out, "not shown") || len(out) < 40000 {
		t.Fatalf("uncapped: %d bytes", len(out))
	}
	t.Setenv("CLAUDECODE", "1")
	if out, _, _ = w.show(strconv.Itoa(id)); len(out) > 26800 || !strings.Contains(out, "30,000-character output limit") {
		t.Fatalf("in Claude Code: %d bytes", len(out))
	}
	t.Setenv("LX_MAX_CHARS", "0")
	if out, _, _ = w.show(strconv.Itoa(id)); strings.Contains(out, "not shown") {
		t.Fatalf("LX_MAX_CHARS=0 must disable the cap")
	}
}

func TestShowRecallRecords(t *testing.T) {
	w := newShowWorld(t)
	id := w.save("go test ./...", w.project, 1, time.Minute, numberedOutput(300))
	w.show()      // listing: no record
	w.show("99")  // missing run: no record
	w.show("abc") // bad id: no record
	out, _, _ := w.show(strconv.Itoa(id), "--errors")
	out2, _, _ := w.show("last", "--tail", "5")
	recs, err := track.Load(time.Time{})
	if err != nil || len(recs) != 2 {
		t.Fatalf("history: %v, %d records: %+v", err, len(recs), recs)
	}
	for i, want := range []struct {
		mode, out string
	}{{"errors", out}, {"tail", out2}} {
		r := recs[i]
		if r.Kind != track.KindShow || r.Of != id || r.Mode != want.mode || r.Cmd != "go test" || r.Out != tokens.Count(want.out) || r.Raw != 0 {
			t.Errorf("record %d: %+v", i, r)
		}
	}
	for _, c := range []struct {
		args []string
		mode string
	}{
		{nil, "full"}, {[]string{"--grep", "x"}, "grep"}, {[]string{"--lines", "3-4"}, "lines"}, {[]string{"--head", "3"}, "head"},
	} {
		s := showSel{}
		for i := 0; i < len(c.args); i += 2 {
			switch c.args[i] {
			case "--grep":
				s.re = regexp.MustCompile(c.args[i+1])
			case "--lines":
				s.linesSet = true
			case "--head":
				s.head = 3
			}
		}
		if s.mode() != c.mode {
			t.Errorf("%v: mode %s", c.args, s.mode())
		}
	}
	// LX_TRACK=0 records nothing.
	t.Setenv("LX_TRACK", "0")
	w.show(strconv.Itoa(id))
	if recs, _ := track.Load(time.Time{}); len(recs) != 2 {
		t.Fatalf("LX_TRACK=0 still recorded: %d", len(recs))
	}
}

func TestShowListing(t *testing.T) {
	w := newShowWorld(t)
	if out, _, _ := w.show(); !strings.Contains(out, "no stored outputs yet") {
		t.Fatalf("empty store: %s", out)
	}
	other := filepath.Join(w.home, "projects", "other")
	for i := 0; i < 31; i++ {
		w.save(fmt.Sprintf("other-cmd %d --secret-flag", i), other, 0, time.Hour, "x\n")
	}
	for i := 0; i < 25; i++ {
		w.save(fmt.Sprintf("go test ./pkg%d", i), w.project, i%2, time.Minute, "x\n")
	}
	out, _, _ := w.show()
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 21 || strings.Contains(out, "other-cmd") {
		t.Fatalf("project listing (%d lines):\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "   56  ") || !strings.Contains(lines[0], "exit 0   generic      go test ./pkg24") {
		t.Fatalf("newest first, today's format: %q", lines[0])
	}
	if lines[20] != "(+5 older runs here, +31 runs from other directories: lx show --all)" {
		t.Fatalf("closing line: %q", lines[20])
	}
	out, _, _ = w.show("--all")
	if n := strings.Count(out, "\n"); n != 56 || !strings.Contains(out, "other-cmd 0 --secret-flag") {
		t.Fatalf("--all lists every run: %d lines", n)
	}
	// Capped: whole rows, then a count of the rest.
	t.Setenv("LX_MAX_CHARS", "1500")
	out, _, _ = w.show("--all")
	if len(out) > hostCharCap() || !strings.Contains(out, "more runs not shown: over this agent's 1,500-character output limit]") {
		t.Fatalf("capped listing (%d bytes):\n%s", len(out), out)
	}
}

func TestShowBadInput(t *testing.T) {
	w := newShowWorld(t)
	w.save("go test", w.project, 1, time.Minute, "x\n")
	for _, c := range []struct {
		args []string
		code int
		msg  string
	}{
		{[]string{"abc"}, 2, "bad id"},
		{[]string{"0"}, 2, "bad id"},
		{[]string{"last~x"}, 2, "bad id"},
		{[]string{"last~-1"}, 2, "bad id"},
		{[]string{"lastly"}, 2, "bad id"},
		{[]string{"1", "2"}, 2, "one run at a time"},
		{[]string{"7"}, 1, "no stored output with id 7"},
		{[]string{"1", "--lines", "5-2"}, 2, "bad --lines"},
		{[]string{"1", "--lines", "x"}, 2, "bad --lines"},
		{[]string{"1", "--grep", "("}, 2, "bad --grep"},
		{[]string{"1", "--head", "-3"}, 2, "take a count"},
		{[]string{"1", "--nope"}, 2, "not defined"},
	} {
		out, errOut, code := w.show(c.args...)
		if code != c.code || !strings.Contains(errOut, c.msg) || out != "" {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", c.args, code, out, errOut)
		}
	}
	// A run that printed nothing.
	w.save("true", w.project, 0, time.Minute, "")
	if out, _, _ := w.show("2", "--errors"); !strings.Contains(out, "[lx: run 2 printed nothing]") {
		t.Errorf("empty run: %s", out)
	}
}

func TestShowQuote(t *testing.T) {
	for in, want := range map[string]string{
		"FAIL":        "FAIL",
		"^FAIL":       "'^FAIL'",
		"a b":         "'a b'",
		"it's":        `'it'\''s'`,
		"":            "''",
		"src/x.go:12": "src/x.go:12",
	} {
		if got := showQuote(in); got != want {
			t.Errorf("showQuote(%q) = %s, want %s", in, got, want)
		}
	}
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1100: "1,100", 4210: "4,210", 1234567: "1,234,567", -1200: "-1,200"} {
		if got := showCommas(n); got != want {
			t.Errorf("showCommas(%d) = %s", n, got)
		}
	}
}

// Metadata is read newest first in growing batches: answers that need
// older runs must still find them.
func TestShowManyRuns(t *testing.T) {
	w := newShowWorld(t)
	other := filepath.Join(w.home, "projects", "other")
	w.save("go test ./...", w.project, 1, time.Hour, "FAIL\n") // 1
	w.save("go vet ./...", w.project, 0, time.Hour, "ok\n")    // 2
	for i := 0; i < 100; i++ {                                 // 3..102
		w.save(fmt.Sprintf("npm run x%d", i), other, 0, time.Hour, "x\n")
	}
	w.save("go test ./...", w.project, 0, time.Minute, "ok\n") // 103
	for i := 0; i < 70; i++ {                                  // 104..173
		w.save(fmt.Sprintf("npm run y%d", i), other, 0, time.Hour, "y\n")
	}
	for spec, want := range map[string]int{"last": 103, "last~1": 2, "last~2": 1} {
		if out, _, _ := w.show(spec); !strings.HasPrefix(out, fmt.Sprintf("[lx show %d · ", want)) {
			t.Errorf("%s: %s", spec, firstLine(out))
		}
	}
	if out, _, _ := w.show("1"); !strings.Contains(out, "[lx: a newer run of this command exists: lx show 103 (exit 0, 1 min ago)]") {
		t.Errorf("newer run 102 ids later:\n%s", out)
	}
	w.cwd = filepath.Join(w.home, "elsewhere")
	if out, _, _ := w.show("last~150"); !strings.HasPrefix(out, "[lx show 23 · ") {
		t.Errorf("fallback last~150: %s", firstLine(out))
	}
	if _, errOut, code := w.show("last~173"); code != 1 || !strings.Contains(errOut, "only 173 stored runs anywhere") {
		t.Errorf("last~173: %d %s", code, errOut)
	}
}

// Property: whatever the run and the selection, a capped view fits the
// cap, and its suggested next command fits too and is not itself cut.
func TestShowCapProperty(t *testing.T) {
	w := newShowWorld(t)
	r := rand.New(rand.NewSource(11))
	iters := 40
	if testenv.Race {
		iters = 10
	}
	for iter := 0; iter < iters; iter++ {
		var b strings.Builder
		n := 1 + r.Intn(3000)
		for i := 0; i < n; i++ {
			switch k := r.Intn(30); {
			case k == 0:
				b.WriteString(strings.Repeat("é€x", 50+r.Intn(1200)))
			case k == 1:
				fmt.Fprintf(&b, "FAIL: case %d broke", i)
			case k == 2:
			case k == 3:
				b.WriteString("warning: " + strings.Repeat("w", r.Intn(300)))
			default:
				b.WriteString(strings.Repeat("line ", 1+r.Intn(30)) + strconv.Itoa(i))
			}
			b.WriteByte('\n')
		}
		id := w.save("./job --flag", w.project, r.Intn(2), time.Minute, b.String())
		limit := 1000 + r.Intn(20000)
		t.Setenv("LX_MAX_CHARS", strconv.Itoa(limit))
		capc := hostCharCap()
		sel := [][]string{{}, {"--errors"}, {"--grep", "FAIL", "-C", strconv.Itoa(r.Intn(4))}, {"--tail", strconv.Itoa(1 + r.Intn(500))}, {"--lines", fmt.Sprintf("%d-", 1+r.Intn(n))}}[r.Intn(5)]
		args := append([]string{strconv.Itoa(id)}, sel...)
		for step := 0; step < 3; step++ {
			out, errOut, code := w.show(args...)
			if code != 0 {
				t.Fatalf("iter %d %v: exit %d %s", iter, args, code, errOut)
			}
			if len(out) > capc {
				t.Fatalf("iter %d %v: %d bytes over the %d cap", iter, args, len(out), capc)
			}
			if !strings.Contains(out, " not shown: ") {
				break
			}
			ls := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
			tr := ls[len(ls)-1]
			if strings.Contains(tr, "alone is longer than that") {
				m := regexp.MustCompile(`line ([\d,]+) alone`).FindStringSubmatch(tr)
				k, _ := strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
				full := showSplit(textutil.Clean(b.String()))[k-1]
				if len(full)+8 < capc-300 {
					t.Fatalf("iter %d: line %d (%d bytes) said to be over a %d cap", iter, k, len(full), capc)
				}
				break
			}
			m := regexp.MustCompile(`next: lx show (.*?)(?: · or --errors / --grep RE)?\]$`).FindStringSubmatch(tr)
			if m == nil {
				t.Fatalf("iter %d: closing line %q", iter, tr)
			}
			args = strings.Fields(strings.ReplaceAll(m[1], "'", ""))
			next, _, _ := w.show(args...)
			if len(next) > capc || strings.Contains(next, " not shown: ") {
				t.Fatalf("iter %d: suggested %v does not fit (%d bytes, cap %d)", iter, args, len(next), capc)
			}
			// Continue from the end of that range.
			_, hi, _ := strings.Cut(args[len(args)-1], "-")
			h, _ := strconv.Atoi(hi)
			args[len(args)-1] = strconv.Itoa(h+1) + "-"
		}
	}
}

// A context count too large to add to a line index must not wrap around:
// with -C 9223372036854775807 the view once selected nothing and said "no
// error or warning lines" under a header counting them.
func TestShowContextOverflow(t *testing.T) {
	w := newShowWorld(t)
	id := w.save("go test ./...", w.project, 1, time.Minute, numberedOutput(300))
	for _, c := range []string{"9223372036854775807", "9223372036854775000", "100000000"} {
		out, _, code := w.show(strconv.Itoa(id), "--errors", "-C", c)
		if code != 0 || strings.Contains(out, "no error or warning lines") {
			t.Fatalf("-C %s: exit %d\n%.400s", c, code, out)
		}
		for i := 50; i <= 300; i += 50 {
			if !strings.Contains(out, fmt.Sprintf("%6d: --- FAIL: TestCase%d", i, i)) {
				t.Fatalf("-C %s: error line %d missing", c, i)
			}
		}
		if n := strings.Count(out, "\n"); n != 301 {
			t.Fatalf("-C %s: every line is within context: %d lines", c, n)
		}
	}
	// Huge --head and --tail don't wrap either.
	out, _, _ := w.show(strconv.Itoa(id), "--head", "9223372036854775807", "--tail", "9223372036854775807")
	if strings.Count(out, "\n") != 301 {
		t.Fatalf("huge --head/--tail: %d lines", strings.Count(out, "\n"))
	}
}

// pickNaive is the obvious selection, for checking pick's one-pass form.
func pickNaive(r *showRun, s showSel) (idx []int, match []bool) {
	n := len(r.lines)
	lo, hi := 0, n-1
	if s.linesSet {
		lo = s.lo - 1
		if s.hi > 0 {
			hi = min(hi, s.hi-1)
		}
	}
	if lo > hi {
		return nil, nil
	}
	isMatch := make([]bool, n)
	keep := make([]bool, n)
	for i := lo; i <= hi; i++ {
		m := !s.errors && s.re == nil
		if s.errors {
			if l := engine.Classify(r.lines[i]); l == engine.Err || l == engine.Warn {
				m = true
			}
		}
		if s.re != nil && s.re.MatchString(r.lines[i]) {
			m = true
		}
		if m {
			isMatch[i] = true
			for j := max(lo, i-s.ctx); j <= min(hi, i+s.ctx); j++ {
				keep[j] = true
			}
		}
	}
	for i := lo; i <= hi; i++ {
		if keep[i] {
			idx = append(idx, i)
			match = append(match, isMatch[i])
		}
	}
	if (s.head > 0 || s.tail > 0) && s.head+s.tail < len(idx) {
		var ni []int
		var nm []bool
		for k := range idx {
			if k < s.head || k >= len(idx)-s.tail {
				ni, nm = append(ni, idx[k]), append(nm, match[k])
			}
		}
		idx, match = ni, nm
	}
	return idx, match
}

// pick (one pass, cached classification) selects exactly what the obvious
// definition does, and size is exactly what render prints.
func TestShowPickAndSizeProperty(t *testing.T) {
	r := rand.New(rand.NewSource(23))
	re := regexp.MustCompile(`x[0-9]`)
	for iter := 0; iter < 300; iter++ {
		var lines []string
		for i := r.Intn(200); i > 0; i-- {
			switch r.Intn(8) {
			case 0:
				lines = append(lines, "error: broke "+strconv.Itoa(i))
			case 1:
				lines = append(lines, "warning: odd")
			case 2:
				lines = append(lines, "has x"+strconv.Itoa(r.Intn(10)))
			case 3:
				lines = append(lines, "")
			default:
				lines = append(lines, strings.Repeat("ok ", r.Intn(5)))
			}
		}
		run := &showRun{id: 3, meta: tee.Meta{Argv: []string{"job"}, Exit: 1}, lines: lines}
		s := showSel{errors: r.Intn(2) == 0, ctx: r.Intn(5), head: r.Intn(3) * r.Intn(20), tail: r.Intn(3) * r.Intn(20)}
		if r.Intn(2) == 0 {
			s.re = re
		}
		if r.Intn(2) == 0 {
			s.linesSet, s.lo = true, 1+r.Intn(len(lines)+2)
			if r.Intn(2) == 0 {
				s.hi = s.lo + r.Intn(100)
			}
		}
		if !s.errors && s.re == nil {
			s.ctx = 0
		}
		gotIdx, gotMatch, _, _, _ := run.pick(s)
		wantIdx, wantMatch := pickNaive(run, s)
		if !slices.Equal(gotIdx, wantIdx) || !slices.Equal(gotMatch, wantMatch) {
			t.Fatalf("iter %d %+v:\n got %v %v\nwant %v %v", iter, s, gotIdx, gotMatch, wantIdx, wantMatch)
		}
		head, body := run.render(s)
		n := showSize(head)
		for _, l := range body {
			n += len(l.text) + 1
		}
		if sz := run.size(s); sz != n {
			t.Fatalf("iter %d %+v: size %d, render prints %d", iter, s, sz, n)
		}
	}
}

// Piped into a program or redirected to a file, lx show prints the whole
// selection with nothing cut, and the header and notes go to stderr:
// `lx show 7 | grep FAIL` must search all of run 7, not its first 27 KB.
func TestShowPiped(t *testing.T) {
	w := newShowWorld(t)
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("LX_MAX_CHARS", "2000")
	other := filepath.Join(w.home, "projects", "other")
	body := numberedOutput(3000)
	id := w.save("go test ./...", other, 1, time.Minute, body)
	pipedShow := func(args ...string) (string, string, int) {
		var o, e bytes.Buffer
		limit, capc := hostLimits()
		code := runShow(args, showEnv{stdout: &o, stderr: &e, now: w.now, cwd: w.cwd, home: w.home, limit: limit, cap: capc, piped: true})
		return o.String(), e.String(), code
	}
	out, errOut, code := pipedShow(strconv.Itoa(id))
	if code != 0 || out != body {
		t.Fatalf("piped: stdout must be the whole run, exactly (%d bytes of %d)", len(out), len(body))
	}
	wantErr := "[lx show 1 · go test ./... · exit 1 · 1 min ago · 3,000 lines]\n[lx: run 1 ran in ~/projects/other, not in this project]\n"
	if errOut != wantErr {
		t.Fatalf("piped: stderr\n%q\nwant\n%q", errOut, wantErr)
	}
	out, errOut, _ = pipedShow(strconv.Itoa(id), "--errors", "-C", "0")
	if strings.Count(out, "--- FAIL") != 60 || strings.Contains(out, "not shown") || !strings.HasPrefix(errOut, "[lx show 1 · ") {
		t.Fatalf("piped --errors: %d FAIL lines\n%s", strings.Count(out, "--- FAIL"), errOut)
	}
	// Straight to the agent the same command is cut to the cap.
	if out, _, _ := w.show(strconv.Itoa(id)); len(out) > hostCharCap() || !strings.Contains(out, "not shown") {
		t.Fatalf("to the agent: %d bytes", len(out))
	}
	// The listing isn't cut when piped either.
	for i := 0; i < 40; i++ {
		w.save(fmt.Sprintf("go vet ./pkg%d", i), w.project, 0, time.Minute, "x\n")
	}
	var o bytes.Buffer
	limit, capc := hostLimits()
	runShow([]string{"--all"}, showEnv{stdout: &o, stderr: io.Discard, now: w.now, cwd: w.cwd, home: w.home, limit: limit, cap: capc, piped: true})
	if strings.Contains(o.String(), "not shown") || strings.Count(o.String(), "\n") != 41 {
		t.Fatalf("piped listing:\n%s", o.String())
	}
}

// showStdoutPiped on real descriptors: Claude Code's Bash tool gives a
// command one regular file for both stdout and stderr.
func TestShowStdoutPiped(t *testing.T) {
	dir := t.TempDir()
	open := func(name string) *os.File {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { f.Close() })
		return f
	}
	capture, capture2, other := open("capture"), open("capture"), open("other")
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer devnull.Close()
	for _, c := range []struct {
		name           string
		stdout, stderr *os.File
		piped          bool
	}{
		{"the host's capture file (same descriptor)", capture, capture, false},
		{"the host's capture file (two descriptors)", capture, capture2, false},
		{"> out.log", other, capture, true},
		{"| grep", pw, capture, true},
		{"2>&1 | grep", pw, pw, true},
		{"| grep, 2>/dev/null", pw, devnull, true},
		{"2>/dev/null (stdout still the host's file)", capture, devnull, false},
		{"stderr piped elsewhere", capture, pw, false},
		{"a character device", devnull, capture, false},
	} {
		if got := showStdoutPiped(c.stdout, c.stderr); got != c.piped {
			t.Errorf("%s: piped=%v, want %v", c.name, got, c.piped)
		}
	}
}

// Selection flags need a run: printing the listing would silently ignore
// them.
func TestShowSelectionNeedsRun(t *testing.T) {
	w := newShowWorld(t)
	w.save("go test", w.project, 1, time.Minute, "x\n")
	for _, args := range [][]string{{"--errors"}, {"--grep", "x"}, {"--lines", "1-2"}, {"--tail", "3"}, {"--head", "3"}, {"-C", "2"}, {"--full"}, {"--raw"}} {
		out, errOut, code := w.show(args...)
		if code != 2 || out != "" || !strings.Contains(errOut, "a selection needs a run: lx show last "+strings.Join(args, " ")) {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, out, errOut)
		}
	}
	if _, errOut, _ := w.show("--grep", "a b"); !strings.Contains(errOut, "lx show last --grep 'a b' (") {
		t.Errorf("the suggested command is quoted: %q", errOut)
	}
	if out, _, code := w.show("--all"); code != 0 || !strings.Contains(out, "go test") {
		t.Errorf("--all still lists: %d %q", code, out)
	}
}

// A finished run whose final metadata was never written still holds its
// reservation's exit -1: that is not an exit status.
func TestShowExitUnknown(t *testing.T) {
	w := newShowWorld(t)
	sp, err := tee.Reserve(tee.Meta{Argv: []string{"make", "test"}, Cwd: w.project, Time: w.now.Add(-3 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tee.Dir(), strconv.Itoa(sp.ID())+".log"), []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, _ := w.show(strconv.Itoa(sp.ID()))
	if h := firstLine(out); h != "[lx show 1 · make test · exit unknown · 3 min ago · 1 line]" {
		t.Fatalf("header: %s", h)
	}
	if out, _, _ := w.show(); !strings.Contains(out, " exit ?   ") || strings.Contains(out, "-1") {
		t.Fatalf("listing: %s", out)
	}
	w.save("make test", w.project, 0, time.Minute, "ok\n")
	w.save("make test", w.project, 0, time.Minute, "ok\n")
	// The newer-run note names the newest identical run.
	if out, _, _ := w.show("1"); !strings.Contains(out, "[lx: a newer run of this command exists: lx show 3 (exit 0, 1 min ago)]") {
		t.Fatalf("note:\n%s", out)
	}
}

// A run recorded through a symlinked path to the project is in the project.
func TestShowProjectThroughSymlink(t *testing.T) {
	w := newShowWorld(t)
	link := filepath.Join(w.home, "link-to-app")
	if err := os.Symlink(w.project, link); err != nil {
		t.Skip("no symlinks:", err)
	}
	id := w.save("go test ./...", filepath.Join(link, "pkg"), 1, time.Minute, "FAIL\n")
	w.save("ls", filepath.Join(w.home, "projects", "other"), 0, 0, "x\n")
	out, _, _ := w.show("last")
	if !strings.HasPrefix(out, fmt.Sprintf("[lx show %d · ", id)) || strings.Contains(out, "not in this project") {
		t.Fatalf("symlinked run dir:\n%s", out)
	}
	if out, _, _ := w.show(); !strings.Contains(out, "go test ./...") || !strings.Contains(out, "+1 run from other directories") {
		t.Fatalf("listing:\n%s", out)
	}
}

// The closing line's search measures sizes instead of formatting the rest
// of the run again at every step: capped, a huge run costs about what
// printing it in full does.
func TestShowCapHugeRunCost(t *testing.T) {
	if testenv.Race || testing.Short() {
		t.Skip("timing")
	}
	w := newShowWorld(t)
	var b strings.Builder
	for i := 0; i < 400_000; i++ {
		fmt.Fprintf(&b, "line of output number %d\n", i)
	}
	id := w.save("./gen", w.project, 0, time.Minute, b.String())
	t.Setenv("LX_MAX_CHARS", "30000")
	timeOf := func(args ...string) time.Duration {
		best := time.Hour
		for k := 0; k < 3; k++ {
			start := time.Now()
			w.show(args...)
			best = min(best, time.Since(start))
		}
		return best
	}
	full := timeOf(strconv.Itoa(id), "--full")
	for _, args := range [][]string{{strconv.Itoa(id)}, {strconv.Itoa(id), "--lines", "200000-"}, {strconv.Itoa(id), "--grep", "number 9"}} {
		d := timeOf(args...)
		t.Logf("%v: %v (full: %v)", args, d, full)
		if d > 3*full+100*time.Millisecond {
			t.Errorf("%v: %v, printing all of it takes %v", args, d, full)
		}
	}
}

// A recall of a run without metadata is recorded under "(unknown)", not a
// blank command name.
func TestShowRecallUnknownCommand(t *testing.T) {
	w := newShowWorld(t)
	id := w.save("make", w.project, 2, time.Minute, "x\n")
	os.Remove(filepath.Join(tee.Dir(), strconv.Itoa(id)+".json"))
	w.show(strconv.Itoa(id))
	recs, err := track.Load(time.Time{})
	if err != nil || len(recs) != 1 || recs[0].Cmd != "(unknown)" || recs[0].Kind != track.KindShow {
		t.Fatalf("%v %+v", err, recs)
	}
}
