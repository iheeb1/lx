package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tee"
	"github.com/iheeb1/lx/internal/testenv"
	"github.com/iheeb1/lx/internal/textutil"
	"github.com/iheeb1/lx/internal/tokens"
	"github.com/iheeb1/lx/internal/track"
)

func tuneRegrets(dir, cmd string) []track.Regret {
	st := track.LoadTune()
	if e := st.Entry(st.Hash("project", tuneProjectRoot(dir)), cmd); e != nil {
		return e.Regrets
	}
	return nil
}

func tuneSeed(t *testing.T, dir, cmd, sig string, n int, now time.Time) {
	t.Helper()
	err := track.UpdateTune(now, func(st *track.TuneState) bool {
		p := st.Hash("project", tuneProjectRoot(dir))
		for i := 0; i < n; i++ {
			st.AddRegret(p, cmd, track.Regret{Time: now.Add(-time.Duration(n-i) * time.Minute).Unix(), Run: 1000 + i, Signal: sig})
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
}

func tuneOn(t *testing.T) {
	t.Helper()
	t.Setenv("LX_TUNE", "")
	t.Setenv("LX_TRACK", "")
}

func TestTuneShowSignals(t *testing.T) {
	w := newShowWorld(t)
	tuneOn(t)
	argv := []string{"go", "test", "./..."}

	condensed := func(ago time.Duration) int {
		t.Helper()
		id := w.save("go test ./...", w.project, 1, ago, numberedOutput(300))
		tuneLookup(argv, w.project, w.now.Add(-ago)).recordCondensed(strconv.Itoa(id), 1, w.now.Add(-ago))
		return id
	}
	count := func() int { return len(tuneRegrets(w.project, "go test")) }
	piped := func(args ...string) {
		t.Helper()
		var o, e bytes.Buffer
		runShow(args, showEnv{stdout: &o, stderr: &e, now: w.now, cwd: w.cwd, home: w.home, piped: true})
	}

	id1 := condensed(2 * time.Minute)
	for _, sel := range [][]string{{"--errors"}, {"--grep", "FAIL"}, {"--lines", "1-5"}, {"--head", "3"}, {"--tail", "3"}, {"--full", "--tail", "40"}, {"--raw", "--grep", "x"}} {
		w.show(append([]string{strconv.Itoa(id1)}, sel...)...)
	}
	piped(strconv.Itoa(id1))
	w.show()
	if n := count(); n != 0 {
		t.Fatalf("narrowed, piped and listing shows recorded %d regrets", n)
	}
	w.show(strconv.Itoa(id1))
	if r := tuneRegrets(w.project, "go test"); len(r) != 1 || r[0].Signal != track.SignalShow || r[0].Run != id1 || r[0].Time != w.now.Unix() {
		t.Fatalf("lx show %d within the window: %+v", id1, r)
	}
	w.show(strconv.Itoa(id1))
	w.show("last", "--full")
	if n := count(); n != 1 {
		t.Fatalf("one run counted %d times", n)
	}

	id2 := condensed(track.RegretWindow + time.Minute)
	w.show(strconv.Itoa(id2))
	if n := count(); n != 1 {
		t.Fatalf("a show %v after the run counted", track.RegretWindow+time.Minute)
	}
	w.show(strconv.Itoa(id2), "--full")
	if r := tuneRegrets(w.project, "go test"); len(r) != 2 || r[1].Signal != track.SignalFull || r[1].Run != id2 {
		t.Fatalf("--full: %+v", r)
	}

	id3 := w.save("go test ./pkg", w.project, 1, 3*time.Minute, numberedOutput(300))
	w.show(strconv.Itoa(id3), "--raw")
	if n := count(); n != 3 {
		t.Fatalf("an unremembered condensed run: %d regrets", n)
	}
	plain, err := tee.Save(tee.Meta{Argv: argv, Cwd: w.project, Filter: "passthrough", Time: w.now.Add(-time.Minute)}, numberedOutput(40))
	if err != nil {
		t.Fatal(err)
	}
	w.show(strconv.Itoa(plain), "--full")
	sp, err := tee.Reserve(tee.Meta{Argv: argv, Cwd: w.project})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = sp.Write([]byte(numberedOutput(40)))
	w.show(strconv.Itoa(sp.ID()), "--full")
	_ = sp.Finish(tee.Meta{Argv: argv, Cwd: w.project, Filter: "generic"}, numberedOutput(40))
	if n := count(); n != 3 {
		t.Fatalf("a plain-output run or a running one counted: %d", n)
	}

	other := filepath.Join(w.home, "src", "other")
	if err := os.MkdirAll(filepath.Join(other, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	idO := w.save("go test ./...", other, 1, time.Minute, numberedOutput(300))
	w.show(strconv.Itoa(idO))
	if n, m := count(), len(tuneRegrets(other, "go test")); n != 3 || m != 1 {
		t.Fatalf("regrets here %d, in the other project %d", n, m)
	}

	for _, env := range []string{"LX_TUNE", "LX_TRACK"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "0")
			id := condensed(time.Minute)
			w.show(strconv.Itoa(id), "--full")
			if n := count(); n != 3 {
				t.Fatalf("%s=0 recorded a regret", env)
			}
		})
	}

	if tn := tuneLookup(argv, filepath.Join(w.project, "pkg", "sub"), w.now); tn.level() != track.LevelLoosen || tn.lv.Count != 3 {
		t.Fatalf("level in a subdirectory: %+v", tn.lv)
	}
	if tn := tuneLookup(argv, other, w.now); tn.level() != track.LevelNormal {
		t.Fatalf("level in the other project: %+v", tn.lv)
	}
	if tn := tuneLookup([]string{"go", "vet"}, w.project, w.now); tn.level() != track.LevelNormal {
		t.Fatalf("level of another command: %+v", tn.lv)
	}
}

func TestTuneRawRerun(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LX_DATA_DIR", dir)
	tuneOn(t)
	proj := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	argv := []string{"pytest", "-x", "tests/"}

	tuneRecordRaw(argv, proj, now)
	if _, err := os.Stat(filepath.Join(dir, "tune.json")); !os.IsNotExist(err) {
		t.Fatalf("a raw run with nothing to regret wrote the state: %v", err)
	}

	tuneLookup(argv, proj, now).recordCondensed("5", 1, now)
	tuneRecordRaw([]string{"pytest", "-x"}, proj, now.Add(time.Minute))
	tuneRecordRaw(argv, filepath.Join(proj, "tests"), now.Add(time.Minute))
	tuneRecordRaw(argv, proj, now.Add(track.RegretWindow+time.Second))
	tuneRecordRaw(argv, proj, now.Add(-time.Hour))
	if r := tuneRegrets(proj, "pytest"); len(r) != 0 {
		t.Fatalf("unrelated raw runs counted: %+v", r)
	}
	tuneRecordRaw(argv, proj, now.Add(5*time.Minute))
	tuneRecordRaw(argv, proj, now.Add(6*time.Minute))
	r := tuneRegrets(proj, "pytest")
	if len(r) != 1 || r[0].Signal != track.SignalRaw || r[0].Run != 5 || r[0].Time != now.Add(5*time.Minute).Unix() {
		t.Fatalf("raw re-run: %+v", r)
	}

	t2 := now.Add(time.Hour)
	tuneLookup(argv, proj, t2).recordCondensed("", 1, t2)
	tuneRecordRaw(argv, proj, t2.Add(time.Minute))
	tuneRecordRaw(argv, proj, t2.Add(2*time.Minute))
	if r := tuneRegrets(proj, "pytest"); len(r) != 2 || r[1].Run != 0 {
		t.Fatalf("unstored run: %+v", r)
	}
	if tn := tuneLookup(argv, proj, t2.Add(3*time.Minute)); tn.level() != track.LevelLoosen || tn.note("lx", false) != "loosened after 2 raw re-runs: lx tune" {
		t.Fatalf("after two raw re-runs: %+v %q", tn.lv, tn.note("lx", false))
	}
}

func TestTuneDecayByCleanRuns(t *testing.T) {
	t.Setenv("LX_DATA_DIR", t.TempDir())
	tuneOn(t)
	proj := t.TempDir()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	argv := []string{"npm", "test"}
	tuneSeed(t, proj, "npm test", track.SignalShow, 2, now)
	for i := 0; i < 2*track.DecayRuns; i++ {
		tuneLookup(argv, proj, now).recordCondensed(strconv.Itoa(100+i), 1, now)
	}
	if tn := tuneLookup(argv, proj, now); tn.level() != track.LevelLoosen {
		t.Fatalf("failing runs lowered the level: %+v", tn.lv)
	}
	for i := 0; i < track.DecayRuns-1; i++ {
		tuneLookup(argv, proj, now).recordCondensed(strconv.Itoa(200+i), 0, now)
	}
	if tn := tuneLookup(argv, proj, now); tn.level() != track.LevelLoosen || tn.lv.Clean != track.DecayRuns-1 {
		t.Fatalf("after %d clean runs: %+v", track.DecayRuns-1, tn.lv)
	}
	tuneLookup(argv, proj, now).recordCondensed("300", 0, now)
	if tn := tuneLookup(argv, proj, now); tn.level() != track.LevelNormal {
		t.Fatalf("after %d clean runs: %+v", track.DecayRuns, tn.lv)
	}
}

const maxRegretsShown = 16

func TestTuneBudgetAndReceipt(t *testing.T) {
	at := func(level, full, raw int) tuneRun {
		return tuneRun{on: true, lv: track.TuneLevel{Level: level, Base: level, Full: full, Raw: raw}}
	}
	for _, c := range []struct {
		level, in, want int
	}{
		{track.LevelNormal, 0, 0}, {track.LevelNormal, 3000, 3000},
		{track.LevelLoosen, 0, 16000}, {track.LevelLoosen, 3000, 6000}, {track.LevelLoosen, 20000, 24000}, {track.LevelLoosen, 30000, 30000},
		{track.LevelRaw, 0, 24000}, {track.LevelRaw, 3000, 24000}, {track.LevelRaw, 50000, 50000},
	} {
		if got := at(c.level, 2, 0).budget(c.in); got != c.want {
			t.Errorf("level %d budget(%d) = %d, want %d", c.level, c.in, got, c.want)
		}
	}
	r := "[lx: 307→29 lines (−71%) · full output: lx show 1]"
	if got := at(track.LevelNormal, 0, 0).receipt(r); got != r {
		t.Errorf("normal receipt %q", got)
	}
	if got, want := at(track.LevelLoosen, 3, 0).receipt(r), "[lx: 307→29 lines (−71%) · full output: lx show 1 · loosened after 3 full recalls: lx tune]"; got != want {
		t.Errorf("loosened receipt\n got %q\nwant %q", got, want)
	}
	if got, want := at(track.LevelRaw, 2, 2).receipt(r), "[lx: 307→29 lines (−71%) · full output: lx show 1 · tuned to raw after 2 full recalls, 2 raw re-runs, but over the output limit: lx tune]"; got != want {
		t.Errorf("raw receipt\n got %q\nwant %q", got, want)
	}

	worst := "[lx: 9,999,999→9,999,999 lines (−100%) · full output: lx show 1073741823 · interrupted by SIGTERM]"
	long := at(track.LevelRaw, maxRegretsShown, maxRegretsShown).receipt(worst)
	if !strings.Contains(long, "tuned to raw after 16 full recalls, 16 raw re-runs, but over the output limit: lx tune]") || len(long) >= receiptRoom {
		t.Errorf("worst-case receipt (%d bytes): %s", len(long), long)
	}
	bin := filepath.Join(t.TempDir(), strings.Repeat("very-long-directory-name/", 3))
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	self := selfCommand()
	longSelf := strings.Replace(worst, "lx show ", self+" show ", 1)
	got := at(track.LevelRaw, maxRegretsShown, maxRegretsShown).receipt(longSelf)
	if len(got) >= receiptRoom && got != longSelf {
		t.Errorf("the note pushed the receipt to %d bytes: %s", len(got), got)
	}
	if short := at(track.LevelLoosen, 3, 0).receipt("[lx: 307→29 lines (−71%) · full output: " + self + " show 1]"); !strings.HasSuffix(short, " · loosened after 3 full recalls: "+self+" tune]") && !strings.HasSuffix(short, " · loosened: "+self+" tune]") && len(self) < 60 {
		t.Errorf("receipt with lx off PATH: %s", short)
	}

	t.Setenv("LX_DATA_DIR", t.TempDir())
	tuneOn(t)
	tuneRun{}.recordCondensed("3", 0, time.Now())
	if _, err := os.Stat(track.TunePath()); !os.IsNotExist(err) {
		t.Fatal("the zero tuneRun wrote the state")
	}
	t.Setenv("LX_TUNE", "0")
	if tn := tuneLookup([]string{"ls"}, t.TempDir(), time.Now()); tn.on {
		t.Fatal("LX_TUNE=0 left tuning on")
	}
}

func tuneLog(lines int) string {
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		switch {
		case i%97 == 0:
			fmt.Fprintf(&b, "ERROR worker-%d: request %d failed: connection reset by peer\n", i%7, i)
		case i%131 == 0:
			fmt.Fprintf(&b, "warning: retrying batch %d after timeout\n", i)
		default:
			fmt.Fprintf(&b, "INFO worker-%d processed batch %d of the nightly import in %dms\n", i%7, i, 10+i%90)
		}
	}
	return b.String()
}

func lineSet(s string) map[string]bool {
	m := map[string]bool{}
	for _, l := range strings.Split(s, "\n") {
		m[l] = true
	}
	return m
}

func tuneProse(lines int) string {
	words := strings.Fields("alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november " +
		"oscar papa quebec romeo sierra tango uniform victor whiskey xray yankee zulu parse render compile link " +
		"fetch merge rebase index cache vendor module package import export")
	r := rand.New(rand.NewSource(1))
	var b strings.Builder
	for i := 0; i < lines; i++ {
		for j, k := 0, 3+r.Intn(9); j < k; j++ {
			if j > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(words[r.Intn(len(words))])
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestTuneViewsShowMore(t *testing.T) {
	c := func() *engine.Context {
		return &engine.Context{Argv: []string{"importer", "--nightly"}, Exit: 1, Cwd: "/w"}
	}
	at := func(level int) tuneRun {
		return tuneRun{on: true, lv: track.TuneLevel{Level: level, Base: level, Full: 3}}
	}
	medium, prose, logs := tuneLog(200), tuneProse(2000), tuneLog(1000)
	if n := tokens.Count(medium); n <= engine.SmallOutput*4 || n > 16000 {
		t.Fatalf("medium output is %d tokens", n)
	}
	if n := tokens.Count(prose); n <= 16000+1000 {
		t.Fatalf("prose output is %d tokens", n)
	}

	v0 := at(track.LevelNormal).process(c(), medium, engine.Options{})
	v1 := at(track.LevelLoosen).process(c(), medium, engine.Options{})
	v2 := at(track.LevelRaw).process(c(), medium, engine.Options{})
	if !v0.Lossy || v1.Lossy || v2.Lossy || v1.Output != strings.TrimSuffix(medium, "\n") || v2.Output != v1.Output {
		t.Fatalf("medium: lossy %v/%v/%v, loosened view is the whole output: %v", v0.Lossy, v1.Lossy, v2.Lossy, v1.Output == strings.TrimSuffix(medium, "\n"))
	}

	p0 := at(track.LevelNormal).process(c(), prose, engine.Options{})
	p1 := at(track.LevelLoosen).process(c(), prose, engine.Options{})
	p2 := at(track.LevelRaw).process(c(), prose, engine.Options{})
	if !p0.Lossy || !p1.Lossy || p2.Lossy || p1.OutTokens < p0.OutTokens*3/2 || p1.OutTokens > 2*engine.DefaultBudget+200 {
		t.Fatalf("prose: lossy %v/%v/%v, tokens %d → %d", p0.Lossy, p1.Lossy, p2.Lossy, p0.OutTokens, p1.OutTokens)
	}

	l0 := at(track.LevelNormal).process(c(), logs, engine.Options{})
	l1 := at(track.LevelLoosen).process(c(), logs, engine.Options{})
	l2 := at(track.LevelRaw).process(c(), logs, engine.Options{})
	if !l0.Lossy || l1.OutTokens < l0.OutTokens || l2.Lossy {
		t.Fatalf("logs: lossy %v/%v/%v, tokens %d → %d", l0.Lossy, l1.Lossy, l2.Lossy, l0.OutTokens, l1.OutTokens)
	}

	caps := []int{2000, 26800}
	if testenv.Race {
		caps = caps[:1]
	}
	for _, capc := range caps {
		for _, out := range []string{medium, prose, logs} {
			o := engine.Options{MaxChars: capc}
			base := at(track.LevelNormal).process(c(), out, o)
			lines := lineSet(out)
			for _, level := range []int{track.LevelLoosen, track.LevelRaw} {
				v := at(level).process(c(), out, o)
				if len(v.Output) > max(capc, engine.MinMaxChars) {
					t.Errorf("cap %d level %d: %d bytes", capc, level, len(v.Output))
				}
				if v.OutTokens < base.OutTokens {
					t.Errorf("cap %d level %d: %d tokens, normal view %d", capc, level, v.OutTokens, base.OutTokens)
				}
				have := lineSet(v.Output)
				for _, l := range strings.Split(base.Output, "\n") {
					if lines[l] && engine.Classify(l) == engine.Err && !have[l] {
						t.Errorf("cap %d level %d lost %q", capc, level, l)
					}
				}
			}
		}
	}
}

func TestTuneCorpusShowsMore(t *testing.T) {
	n := 0
	defer func() {
		if !testenv.Race && n < 60 {
			t.Errorf("only %d captures checked", n)
		}
	}()
	files, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "corpus", "*", "*.txt"))
	if len(files) < 100 {
		t.Fatalf("corpus: %d captures", len(files))
	}
	for i, f := range files {
		if testenv.Race && i%20 != 0 {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) > 48<<10 {

			continue
		}
		var meta struct {
			Argv []string `json:"argv"`
			Exit int      `json:"exit_code"`
		}
		if b, err := os.ReadFile(strings.TrimSuffix(f, ".txt") + ".meta.json"); err == nil {
			_ = json.Unmarshal(b, &meta)
		}
		if len(meta.Argv) == 0 {
			meta.Argv = []string{"cmd"}
		}
		clean := lineSet(textutil.Clean(string(raw)))
		n++
		for _, capc := range []int{[]int{0, 8000}[i%2]} {
			o := engine.Options{MaxChars: capc}
			ctx := func() *engine.Context {
				return &engine.Context{Argv: meta.Argv, Exit: meta.Exit, Cwd: "/home/user/src"}
			}
			v0 := tuneRun{}.process(ctx(), string(raw), o)
			for _, level := range []int{track.LevelLoosen, track.LevelRaw} {
				v := tuneRun{on: true, lv: track.TuneLevel{Level: level, Base: level}}.process(ctx(), string(raw), o)
				name := fmt.Sprintf("%s cap %d level %d", filepath.Base(f), capc, level)
				if capc > 0 && len(v.Output) > capc {
					t.Errorf("%s: %d bytes over the cap", name, len(v.Output))
				}
				if v0.Lossy && v.OutTokens < v0.OutTokens {
					t.Errorf("%s: %d tokens, normal view %d", name, v.OutTokens, v0.OutTokens)
				}
				if !v0.Lossy && v.Output != v0.Output {
					t.Errorf("%s: the normal view was the whole output, the tuned one differs", name)
				}
				have := lineSet(v.Output)
				for _, l := range strings.Split(v0.Output, "\n") {
					if l != "" && clean[l] && !have[l] {
						t.Errorf("%s: lost output line %q", name, l)
						break
					}
				}
			}
		}
	}
}

func TestTuneCommand(t *testing.T) {
	t.Setenv("LX_DATA_DIR", t.TempDir())
	tuneOn(t)
	base := t.TempDir()
	home := filepath.Join(base, "home")
	proj := filepath.Join(home, "src", "app")
	other := filepath.Join(home, "src", "other")
	for _, d := range []string{proj, other} {
		if err := os.MkdirAll(filepath.Join(d, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	proj, _ = filepath.EvalSymlinks(proj)
	home, _ = filepath.EvalSymlinks(home)
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	run := func(cwd string, args ...string) (string, string, int) {
		var o, e bytes.Buffer
		code := runTune(args, tuneEnv{stdout: &o, stderr: &e, cwd: cwd, home: home, now: now})
		return o.String(), e.String(), code
	}

	out, _, code := run(proj)
	if code != 0 || !strings.HasPrefix(out, "lx tune: no regrets for anything in this project (~/src/app).\n") {
		t.Fatalf("empty (%d):\n%s", code, out)
	}

	tuneSeed(t, proj, "go test", track.SignalShow, 3, now)
	tuneSeed(t, proj, "npm test", track.SignalRaw, 4, now)
	tuneSeed(t, proj, "pytest", track.SignalFull, 1, now)
	tuneSeed(t, other, "cargo test", track.SignalShow, 2, now)
	_ = track.UpdateTune(now, func(st *track.TuneState) bool {
		st.Entry(st.Hash("project", tuneProjectRoot(proj)), "go test").Clean = 7
		return true
	})

	out, _, _ = run(filepath.Join(proj, ".git"))
	golden := "lx tune · this project (~/src/app)\n" +
		"\n" +
		"  npm test  raw       4 raw re-runs in 7 days · until Oct 3 11:59\n" +
		"  go test   loosened  3 full recalls in 7 days · 7 of 20 clean runs to lower it · until Oct 3 11:58\n" +
		"  pytest    normal    1 full recall in 7 days · loosens at 2\n" +
		"\n" +
		"  loosened: twice the token budget, and the whole output when it fits in it\n" +
		"  raw: the whole output, condensed only past the agent's output limit\n" +
		"  20 clean runs (exit 0, not read back) lower a level · lx tune --reset [CMD] forgets\n"
	if out != golden {
		t.Errorf("listing\n got:\n%s\nwant:\n%s", out, golden)
	}
	if out, _, _ := run(proj, "go", "test", "./..."); !strings.Contains(out, "go test") || strings.Contains(out, "npm test") {
		t.Errorf("filtered by command:\n%s", out)
	}
	out, _, _ = run(proj, "--all")
	if !strings.Contains(out, "  this project (~/src/app)\n") || !strings.Contains(out, "cargo test") || strings.Contains(out, other) {
		t.Errorf("--all:\n%s", out)
	}

	out, _, _ = run(proj, "--json")
	var js struct {
		Enabled  bool   `json:"enabled"`
		Project  string `json:"project"`
		Commands []struct {
			Cmd       string     `json:"cmd"`
			Level     string     `json:"level"`
			Regrets   int        `json:"regrets"`
			RawReruns int        `json:"raw_reruns"`
			Current   bool       `json:"current"`
			Until     *time.Time `json:"until"`
			CleanRuns int        `json:"clean_runs"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(out), &js); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if !js.Enabled || js.Project != proj || len(js.Commands) != 3 || js.Commands[0].Cmd != "npm test" || js.Commands[0].Level != "raw" ||
		js.Commands[0].RawReruns != 4 || !js.Commands[0].Current || js.Commands[0].Until == nil || js.Commands[1].CleanRuns != 7 || js.Commands[2].Until != nil {
		t.Errorf("json:\n%s", out)
	}

	if out, _, code := run(proj, "--reset", "go", "test"); code != 0 || out != "lx tune: forgot go test in this project; its views are back to normal\n" {
		t.Errorf("reset go test (%d): %q", code, out)
	}
	if tuneRegrets(proj, "go test") != nil || tuneRegrets(proj, "npm test") == nil {
		t.Fatal("reset go test")
	}
	if out, _, _ := run(proj, "--reset", "go", "test"); out != "lx tune: nothing to forget for go test in this project\n" {
		t.Errorf("second reset: %q", out)
	}
	if out, _, _ := run(proj, "--reset"); out != "lx tune: forgot 2 commands in this project; their views are back to normal\n" {
		t.Errorf("reset project: %q", out)
	}
	if tuneRegrets(other, "cargo test") == nil {
		t.Fatal("reset reached another project")
	}
	if out, _, _ := run(proj, "--reset", "--all", "--json"); out != "{\"reset\": 1}\n" {
		t.Errorf("reset all: %q", out)
	}
	if st := track.LoadTune(); len(st.Entries) != 0 {
		t.Fatalf("after reset --all: %+v", st.Entries)
	}

	if _, e, code := run(proj, "--bogus"); code != 2 || !strings.Contains(e, "usage: lx tune") {
		t.Errorf("bad flag: %d %q", code, e)
	}
	t.Setenv("LX_TUNE", "0")
	if out, _, _ := run(proj); !strings.Contains(out, "off (LX_TUNE=0 or LX_TRACK=0)") {
		t.Errorf("off:\n%s", out)
	}
}

func TestTuneLookupIsCheap(t *testing.T) {
	t.Setenv("LX_DATA_DIR", t.TempDir())
	tuneOn(t)
	proj := t.TempDir()
	now := time.Now()
	_ = track.UpdateTune(now, func(st *track.TuneState) bool {
		for i := 0; i < 100; i++ {
			st.AddRegret(st.Hash("project", fmt.Sprint("/p", i)), fmt.Sprint("tool", i%10), track.Regret{Time: now.Unix() - int64(i), Signal: track.SignalShow})
		}
		for i := 0; i < 60; i++ {
			st.AddRecent(track.TuneRun{Time: now.Unix(), Run: i + 1, Project: st.Hash("p"), Cmd: "tool1", Argv: st.Hash("a", fmt.Sprint(i))})
		}
		return true
	})

	per := func(argv ...string) time.Duration {
		best := time.Duration(math.MaxInt64)
		for round := 0; round < 5; round++ {
			const n = 100
			start := time.Now()
			for i := 0; i < n; i++ {
				tuneLookup(argv, proj, now)
			}
			best = min(best, time.Since(start)/n)
		}
		return best
	}
	fast, slow := per("git", "status"), per("tool3", "x")
	t.Logf("tuneLookup with a busy state: %v (no regrets for the command), %v (regrets elsewhere)", fast, slow)
	if limit := testenv.Scale(250 * time.Microsecond); fast > limit {
		t.Errorf("tuneLookup takes %v for a command with no regrets, limit %v", fast, limit)
	}
	if limit := testenv.Scale(3 * time.Millisecond); slow > limit {
		t.Errorf("tuneLookup takes %v for a command with regrets, limit %v", slow, limit)
	}
}

func TestTuneRunPathSurvivesFailures(t *testing.T) {
	t.Setenv("LX_DATA_DIR", t.TempDir())
	tuneOn(t)
	proj := t.TempDir()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	argv := []string{"npm", "test"}
	tuneSeed(t, proj, "npm test", track.SignalShow, 2, now)
	if tn := tuneLookup(argv, proj, now); tn.level() != track.LevelLoosen {
		t.Fatalf("seeded level: %+v", tn.lv)
	}
	defer func(l, lf, u any) {
		tuneLoad, tuneLoadFor = l.(func() *track.TuneState), lf.(func(string) *track.TuneState)
		tuneUpdate = u.(func(time.Time, func(*track.TuneState) bool) error)
	}(tuneLoad, tuneLoadFor, tuneUpdate)
	tuneLoad = func() *track.TuneState { panic("test: tune state") }
	tuneLoadFor = func(string) *track.TuneState { panic("test: tune state") }
	tuneUpdate = func(time.Time, func(*track.TuneState) bool) error { panic("test: tune state") }

	tn := tuneLookup(argv, proj, now)
	if tn.on || tn.level() != track.LevelNormal {
		t.Fatalf("a failed lookup left tuning on: %+v", tn)
	}
	out := tuneLog(200)
	c := func() *engine.Context { return &engine.Context{Argv: argv, Exit: 1, Cwd: proj} }
	if got, want := tn.process(c(), out, engine.Options{}), processView(c(), out, engine.Options{}); got.Output != want.Output || got.Lossy != want.Lossy {
		t.Fatal("the view after a failed lookup is not the normal one")
	}

	loose := tuneRun{on: true, argv: argv, cwd: proj, key: "npm test", lv: track.TuneLevel{Level: track.LevelLoosen, Base: track.LevelLoosen, Full: 2}}
	loose.recordCondensed("7", 0, now)
	tuneRecordRaw(argv, proj, now)
	tuneRecordShow(7, tee.Meta{Argv: argv, Cwd: proj, Filter: "generic"}, showSel{}, true, false, now)
}

func TestTuneCommandFlagsAfterCmd(t *testing.T) {
	t.Setenv("LX_DATA_DIR", t.TempDir())
	tuneOn(t)
	proj := t.TempDir()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	tuneSeed(t, proj, "go test", track.SignalShow, 2, now)
	tuneSeed(t, proj, "npm test", track.SignalShow, 2, now)
	run := func(args ...string) (string, string, int) {
		var o, e bytes.Buffer
		code := runTune(args, tuneEnv{stdout: &o, stderr: &e, cwd: proj, home: t.TempDir(), now: now})
		return o.String(), e.String(), code
	}
	out, errOut, code := run("go", "test", "-race", "./...", "--json")
	var js struct {
		Commands []struct {
			Cmd string `json:"cmd"`
		} `json:"commands"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &js) != nil || len(js.Commands) != 1 || js.Commands[0].Cmd != "go test" {
		t.Fatalf("lx tune go test -race ./... --json (%d): %s%s", code, out, errOut)
	}
	if out, _, code := run("--", "npm", "test", "--json"); code != 0 || !strings.Contains(out, "npm test") || strings.Contains(out, "{") {
		t.Fatalf("after --, --json belongs to CMD (%d):\n%s", code, out)
	}
	if out, _, code := run("go", "test", "--reset", "-count=1"); code != 0 || out != "lx tune: forgot go test in this project; its views are back to normal\n" {
		t.Fatalf("lx tune go test --reset (%d): %q", code, out)
	}
	if tuneRegrets(proj, "go test") != nil || tuneRegrets(proj, "npm test") == nil {
		t.Fatal("the reset reached the wrong command")
	}
	if _, errOut, code := run("npm", "test", "--bogus=1", "--json=maybe"); code != 2 || !strings.Contains(errOut, "usage: lx tune") {
		t.Fatalf("a bad value of lx tune's own flag (%d): %q", code, errOut)
	}
	if _, _, code := run("npm", "--help"); code != 0 {
		t.Fatalf("--help after CMD: %d", code)
	}
}
