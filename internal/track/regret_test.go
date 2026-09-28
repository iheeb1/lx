package track

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var tuneNow = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) int64 { return tuneNow.Add(-d).Unix() }

const dayDur = 24 * time.Hour

func tuneWorld(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("LX_DATA_DIR", dir)
	t.Setenv("LX_TRACK", "")
	t.Setenv("LX_TUNE", "")
	return dir
}

func entryAt(times ...int64) TuneEntry {
	e := TuneEntry{Project: strings.Repeat("a", hashHexLen), Cmd: "go test"}
	for i, tm := range times {
		e.Regrets = append(e.Regrets, Regret{Time: tm, Run: i + 1, Signal: SignalShow})
	}
	return e
}

func TestTuneLevels(t *testing.T) {
	L := ago(6 * dayDur)
	cases := []struct {
		name        string
		times       []int64
		level, base int
		count, week int
		until       time.Time
	}{
		{"none", nil, LevelNormal, LevelNormal, 0, 0, time.Time{}},
		{"one", []int64{ago(time.Hour)}, LevelNormal, LevelNormal, 1, 1, time.Time{}},
		{"two in a week", []int64{ago(3 * dayDur), ago(time.Hour)}, LevelLoosen, LevelLoosen, 2, 2,
			tuneNow.Add(-3*dayDur + PolicyWindow)},
		{"two, one older than a week", []int64{ago(8 * dayDur), ago(time.Hour)}, LevelNormal, LevelNormal, 1, 1, time.Time{}},
		{"exactly a week old drops out", []int64{ago(PolicyWindow), ago(time.Hour)}, LevelNormal, LevelNormal, 1, 1, time.Time{}},
		{"three", []int64{ago(5 * dayDur), ago(2 * dayDur), ago(time.Hour)}, LevelLoosen, LevelLoosen, 3, 3,
			tuneNow.Add(-2*dayDur + PolicyWindow)},
		{"four", []int64{ago(5 * dayDur), ago(4 * dayDur), ago(2 * dayDur), ago(time.Hour)}, LevelRaw, LevelRaw, 4, 4,
			tuneNow.Add(-time.Hour + PolicyWindow)},

		{"raw holds until a week after the latest", []int64{L - 3*86400, L - 2*86400, L - 86400, L}, LevelRaw, LevelRaw, 4, 1,
			time.Unix(L, 0).Add(PolicyWindow)},
		{"raw ends a week after the latest", []int64{ago(PolicyWindow + 3*dayDur), ago(PolicyWindow + 2*dayDur), ago(PolicyWindow + dayDur), ago(PolicyWindow)},
			LevelNormal, LevelNormal, 0, 0, time.Time{}},
		{"four spread over two weeks", []int64{ago(13 * dayDur), ago(9 * dayDur), ago(2 * dayDur), ago(time.Hour)}, LevelLoosen, LevelLoosen, 2, 2,
			tuneNow.Add(-2*dayDur + PolicyWindow)},
		{"a future regret (clock skew) counts", []int64{ago(time.Hour), tuneNow.Add(time.Hour).Unix()}, LevelLoosen, LevelLoosen, 2, 2,
			tuneNow.Add(-time.Hour + PolicyWindow)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lv := entryAt(c.times...).Evaluate(tuneNow)
			if lv.Level != c.level || lv.Base != c.base || lv.Count != c.count || lv.Week != c.week || !lv.Until.Equal(c.until) {
				t.Fatalf("level %d base %d count %d week %d until %v; want %d %d %d %d %v",
					lv.Level, lv.Base, lv.Count, lv.Week, lv.Until, c.level, c.base, c.count, c.week, c.until)
			}
			if lv.Total != len(c.times) || lv.Full+lv.Raw != lv.Count {
				t.Fatalf("total %d full %d raw %d", lv.Total, lv.Full, lv.Raw)
			}
		})
	}
}

func TestTuneSignalsCounted(t *testing.T) {
	e := entryAt(ago(3*time.Hour), ago(2*time.Hour), ago(time.Hour))
	e.Regrets[1].Signal = SignalRaw
	e.Regrets[2].Signal = SignalFull
	lv := e.Evaluate(tuneNow)
	if lv.Full != 2 || lv.Raw != 1 || SignalCounts(lv.Full, lv.Raw) != "2 full recalls, 1 raw re-run" {
		t.Fatalf("%+v: %q", lv, SignalCounts(lv.Full, lv.Raw))
	}
	for _, c := range []struct {
		full, raw int
		want      string
	}{{0, 0, "0 full recalls"}, {1, 0, "1 full recall"}, {0, 2, "2 raw re-runs"}, {3, 0, "3 full recalls"}} {
		if got := SignalCounts(c.full, c.raw); got != c.want {
			t.Errorf("SignalCounts(%d, %d) = %q, want %q", c.full, c.raw, got, c.want)
		}
	}
	if LevelName(LevelNormal) != "normal" || LevelName(LevelLoosen) != "loosened" || LevelName(LevelRaw) != "raw" {
		t.Error("level names")
	}
}

func TestTuneDecay(t *testing.T) {
	loose := entryAt(ago(2*time.Hour), ago(time.Hour))
	raw := entryAt(ago(4*time.Hour), ago(3*time.Hour), ago(2*time.Hour), ago(time.Hour))
	for _, c := range []struct {
		e     TuneEntry
		clean int
		want  int
	}{
		{loose, 0, LevelLoosen}, {loose, DecayRuns - 1, LevelLoosen}, {loose, DecayRuns, LevelNormal}, {loose, 3 * DecayRuns, LevelNormal},
		{raw, DecayRuns - 1, LevelRaw}, {raw, DecayRuns, LevelLoosen}, {raw, 2*DecayRuns - 1, LevelLoosen}, {raw, 2 * DecayRuns, LevelNormal},
		{raw, 1000, LevelNormal}, {raw, -5, LevelRaw},
	} {
		c.e.Clean = c.clean
		lv := c.e.Evaluate(tuneNow)
		if lv.Level != c.want || lv.Clean > maxClean || lv.Clean < 0 {
			t.Errorf("base %d, %d clean runs: level %d (clean %d), want %d", lv.Base, c.clean, lv.Level, lv.Clean, c.want)
		}
	}

	s := &TuneState{Salt: strings.Repeat("0", 2*saltBytes)}
	p := s.Hash("project", "/p")
	s.AddRegret(p, "go test", Regret{Time: ago(2 * time.Hour), Run: 1, Signal: SignalShow})
	s.AddRegret(p, "go test", Regret{Time: ago(time.Hour), Run: 2, Signal: SignalShow})
	s.Entry(p, "go test").Clean = DecayRuns
	if lv := s.Entry(p, "go test").Evaluate(tuneNow); lv.Level != LevelNormal {
		t.Fatalf("decayed level %d", lv.Level)
	}
	if !s.AddRegret(p, "go test", Regret{Time: ago(time.Minute), Run: 3, Signal: SignalFull}) {
		t.Fatal("a new run's regret was refused")
	}
	if e := s.Entry(p, "go test"); e.Clean != 0 || e.Evaluate(tuneNow).Level != LevelLoosen {
		t.Fatalf("a regret must restart the clean count: %+v", e)
	}
}

func TestAddRegretCountsARunOnce(t *testing.T) {
	s := &TuneState{Salt: strings.Repeat("1", 2*saltBytes)}
	p := s.Hash("project", "/p")
	if !s.AddRegret(p, "pytest", Regret{Time: ago(time.Hour), Run: 7, Signal: SignalShow}) {
		t.Fatal("first regret refused")
	}
	if s.AddRegret(p, "pytest", Regret{Time: ago(time.Minute), Run: 7, Signal: SignalFull}) {
		t.Fatal("run 7 counted twice")
	}

	if !s.AddRegret(p, "pytest", Regret{Time: ago(time.Minute), Signal: SignalRaw}) ||
		!s.AddRegret(p, "pytest", Regret{Time: ago(time.Second), Signal: SignalRaw}) {
		t.Fatal("unstored runs refused")
	}
	for _, bad := range []struct {
		p, k string
		r    Regret
	}{
		{"", "pytest", Regret{Time: 1, Signal: SignalShow}},
		{p, "", Regret{Time: 1, Signal: SignalShow}},
		{p, "pytest", Regret{Time: 1, Signal: "nope"}},
		{p, "pytest", Regret{Time: 0, Signal: SignalShow}},
	} {
		if s.AddRegret(bad.p, bad.k, bad.r) {
			t.Errorf("accepted %+v", bad)
		}
	}
	if n := len(s.Entry(p, "pytest").Regrets); n != 3 {
		t.Fatalf("%d regrets", n)
	}
}

func TestTuneMatchingRecentRuns(t *testing.T) {
	s := &TuneState{Salt: strings.Repeat("2", 2*saltBytes)}
	a := s.Hash("argv", "/w", "go", "test")
	if s.HasRecent(tuneNow) || s.LatestRun(a, tuneNow) != nil || s.RecentRun(1) != nil {
		t.Fatal("empty state matched")
	}
	s.AddRecent(TuneRun{Time: ago(20 * time.Minute), Run: 1, Project: s.Hash("project", "/w"), Cmd: "go test", Argv: a})
	if s.HasRecent(tuneNow) || s.LatestRun(a, tuneNow) != nil {
		t.Fatal("a run older than RegretWindow matched")
	}
	s.AddRecent(TuneRun{Time: ago(10 * time.Minute), Run: 2, Project: s.Hash("project", "/w"), Cmd: "go test", Argv: a})
	s.AddRecent(TuneRun{Time: ago(5 * time.Minute), Run: 3, Project: s.Hash("project", "/w"), Cmd: "go test", Argv: a})
	s.AddRecent(TuneRun{Time: ago(time.Minute), Run: 4, Project: s.Hash("project", "/w"), Cmd: "go vet", Argv: s.Hash("argv", "/w", "go", "vet")})
	if r := s.LatestRun(a, tuneNow); r == nil || r.Run != 3 {
		t.Fatalf("latest run of go test: %+v", r)
	}
	if r := s.RecentRun(2); r == nil || r.Cmd != "go test" {
		t.Fatalf("run 2: %+v", r)
	}
	if s.RecentRun(0) != nil || s.LatestRun("", tuneNow) != nil {
		t.Fatal("empty keys matched")
	}
}

func TestTuneHash(t *testing.T) {
	a := &TuneState{Salt: strings.Repeat("3", 2*saltBytes)}
	b := &TuneState{Salt: strings.Repeat("4", 2*saltBytes)}
	h := a.Hash("project", "/home/me/src/app")
	if !isHex(h, hashHexLen) || h == b.Hash("project", "/home/me/src/app") {
		t.Fatalf("hash %q not salted", h)
	}
	if h == a.Hash("argv", "/home/me/src/app") || a.Hash("argv", "a", "bc") == a.Hash("argv", "ab", "c") {
		t.Fatal("hash parts are ambiguous")
	}
	if (&TuneState{}).Hash("project", "/x") != "" || (*TuneState)(nil).Hash("project", "/x") != "" {
		t.Fatal("no salt must hash to nothing")
	}
}

func TestTuneResetScopes(t *testing.T) {
	s := &TuneState{Salt: strings.Repeat("5", 2*saltBytes)}
	p, q := s.Hash("project", "/p"), s.Hash("project", "/q")
	for _, pk := range [][2]string{{p, "go test"}, {p, "npm test"}, {q, "go test"}} {
		s.AddRegret(pk[0], pk[1], Regret{Time: ago(time.Hour), Signal: SignalShow})
	}
	if n := s.Reset(p, "go test"); n != 1 || s.Entry(p, "go test") != nil || s.Entry(q, "go test") == nil {
		t.Fatalf("reset one command in one project: %d", n)
	}
	if n := s.Reset(p, ""); n != 1 || s.Entry(p, "npm test") != nil || len(s.Entries) != 1 {
		t.Fatalf("reset a project: %d", n)
	}
	s.AddRegret(p, "go test", Regret{Time: ago(time.Hour), Signal: SignalShow})
	if n := s.Reset("", ""); n != 2 || len(s.Entries) != 0 {
		t.Fatalf("reset all: %d", n)
	}
}

func TestTunePruneBounds(t *testing.T) {
	s := &TuneState{Salt: strings.Repeat("6", 2*saltBytes)}
	p := s.Hash("project", "/p")
	s.AddRegret(p, "old", Regret{Time: ago(TuneRetention + time.Hour), Signal: SignalShow})
	for i := 0; i < maxRegrets+5; i++ {
		s.AddRegret(p, "many", Regret{Time: ago(time.Duration(maxRegrets+5-i) * time.Hour), Run: i + 1, Signal: SignalShow})
	}
	for i := 0; i < maxEntries+10; i++ {
		s.AddRegret(s.Hash("project", fmt.Sprint("/x", i)), "ls", Regret{Time: ago(time.Duration(i+100) * time.Hour), Signal: SignalShow})
	}
	for i := 0; i < maxRecent+10; i++ {
		s.AddRecent(TuneRun{Time: ago(time.Duration(maxRecent+10-i) * time.Second), Run: i + 1, Project: p, Cmd: "ls", Argv: p})
	}
	s.AddRecent(TuneRun{Time: ago(RegretWindow + time.Second), Run: 999, Project: p, Cmd: "ls", Argv: p})
	s.Prune(tuneNow)
	if s.Entry(p, "old") != nil {
		t.Error("a regret older than TuneRetention survived")
	}
	e := s.Entry(p, "many")
	if e == nil || len(e.Regrets) != maxRegrets || e.Regrets[len(e.Regrets)-1].Run != maxRegrets+5 || e.Regrets[0].Run != 6 {
		t.Fatalf("many: %+v", e)
	}
	if len(s.Entries) != maxEntries {
		t.Fatalf("%d entries, want %d", len(s.Entries), maxEntries)
	}
	if s.Entries[0].Cmd != "many" {
		t.Errorf("the entry with the latest regret must be kept first: %+v", s.Entries[0])
	}
	if len(s.Recent) != maxRecent || s.RecentRun(999) != nil || s.Recent[len(s.Recent)-1].Run != maxRecent+10 {
		t.Fatalf("recent: %d, newest %+v", len(s.Recent), s.Recent[len(s.Recent)-1])
	}
}

func TestTuneStorePrivateAndBounded(t *testing.T) {
	dir := tuneWorld(t)
	project := "/Users/someone/src/secret-project"
	argv := []string{"go", "test", "./internal/secret/...", "-run", "TestPassword"}
	err := UpdateTune(tuneNow, func(s *TuneState) bool {
		p := s.Hash("project", project)
		s.AddRecent(TuneRun{Time: tuneNow.Unix(), Run: 12, Project: p, Cmd: "go test", Argv: s.Hash("argv", append([]string{project + "/internal"}, argv...)...)})
		s.AddRegret(p, "go test", Regret{Time: tuneNow.Unix(), Run: 12, Signal: SignalShow})
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "tune.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"Users", "someone", "secret", "internal", "Password", "-run", "./"} {
		if strings.Contains(string(b), leak) {
			t.Errorf("tune.json contains %q:\n%s", leak, b)
		}
	}
	if !strings.Contains(string(b), `"k":"go test"`) {
		t.Errorf("the command key is missing:\n%s", b)
	}
	if runtime.GOOS != "windows" {
		for _, f := range []string{"tune.json", "tune.lock"} {
			if st, err := os.Stat(filepath.Join(dir, f)); err != nil || st.Mode().Perm() != 0o600 {
				t.Errorf("%s: mode %v, %v", f, st.Mode(), err)
			}
		}
	}

	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("left %s", e.Name())
		}
	}
	s := LoadTune()
	if s.Salt == "" || s.Entry(s.Hash("project", project), "go test") == nil || s.RecentRun(12) == nil {
		t.Fatalf("round trip: %+v", s)
	}

	salt := s.Salt
	_ = UpdateTune(tuneNow, func(s *TuneState) bool { return true })
	if LoadTune().Salt != salt {
		t.Fatal("the salt changed between writes")
	}
}

func TestTuneCorruptFileTolerated(t *testing.T) {
	good := `{"v":1,"salt":"` + strings.Repeat("ab", saltBytes) + `","entries":[{"p":"` + strings.Repeat("c", hashHexLen) + `","k":"go test","r":[{"t":1790000000,"id":3,"s":"show"}]}]}`
	cases := map[string]string{
		"garbage":        "not json at all\x00\xff",
		"truncated":      good[:len(good)/2],
		"empty":          "",
		"wrong version":  strings.Replace(good, `"v":1`, `"v":99`, 1),
		"no salt":        strings.Replace(good, strings.Repeat("ab", saltBytes), "", 1),
		"short salt":     strings.Replace(good, strings.Repeat("ab", saltBytes), "abcd", 1),
		"wrong types":    `{"v":1,"salt":"` + strings.Repeat("ab", saltBytes) + `","entries":"nope"}`,
		"array":          `[1,2,3]`,
		"oversized file": good + strings.Repeat(" ", maxTuneBytes),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := tuneWorld(t)
			p := filepath.Join(dir, "tune.json")
			if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			s := LoadTune()
			if len(s.Entries) != 0 || len(s.Recent) != 0 || s.Salt != "" {
				t.Fatalf("damaged file read as %+v", s)
			}
			if err := UpdateTune(tuneNow, func(s *TuneState) bool {
				s.AddRegret(s.Hash("project", "/p"), "ls", Regret{Time: tuneNow.Unix(), Signal: SignalShow})
				return true
			}); err != nil {
				t.Fatal(err)
			}
			s = LoadTune()
			if len(s.Entries) != 1 || s.Entries[0].Cmd != "ls" || !isHex(s.Salt, 2*saltBytes) {
				t.Fatalf("after a write: %+v", s)
			}
		})
	}

	dir := tuneWorld(t)
	bad := `{"v":1,"salt":"` + strings.Repeat("ab", saltBytes) + `","entries":[` +
		`{"p":"` + strings.Repeat("c", hashHexLen) + `","k":"go test","c":-4,"r":[{"t":1790000000,"id":3,"s":"show"},{"t":-1,"s":"show"},{"t":1790000001,"s":"bogus"},{"t":1790000002,"id":-2,"s":"raw"}]},` +
		`{"p":"/home/me/project","k":"ls","r":[{"t":1790000000,"s":"show"}]},` +
		`{"p":"` + strings.Repeat("d", hashHexLen) + `","k":"","r":[{"t":1790000000,"s":"show"}]},` +
		`{"p":"` + strings.Repeat("e", hashHexLen) + `","k":"npm test","c":999,"r":[]}],` +
		`"recent":[{"t":1790000000,"id":1,"p":"` + strings.Repeat("c", hashHexLen) + `","cmd":"go test","a":"` + strings.Repeat("f", hashHexLen) + `"},{"t":1790000000,"id":2,"p":"x","cmd":"go test","a":"y"}]}`
	if err := os.WriteFile(filepath.Join(dir, "tune.json"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	s := LoadTune()
	if len(s.Entries) != 1 || len(s.Entries[0].Regrets) != 1 || s.Entries[0].Clean != 0 || len(s.Recent) != 1 || s.Recent[0].Run != 1 {
		t.Fatalf("sanitized: %+v", s)
	}
}

func TestTuneConcurrentWriters(t *testing.T) {
	tuneWorld(t)
	const writers, each = 12, 6
	var wg sync.WaitGroup
	stop := make(chan struct{})
	readerErr := make(chan error, 1)
	go func() {
		salt := ""
		for {
			select {
			case <-stop:
				readerErr <- nil
				return
			default:
			}
			s := LoadTune()
			if s.Salt != "" {
				if salt != "" && s.Salt != salt {
					readerErr <- fmt.Errorf("salt changed from %s to %s", salt, s.Salt)
					return
				}
				salt = s.Salt
			} else if salt != "" {
				readerErr <- errors.New("the state read as empty after it was written")
				return
			}
		}
	}()
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				err := UpdateTune(tuneNow, func(s *TuneState) bool {
					return s.AddRegret(s.Hash("project", "/p"), fmt.Sprint("cmd", w), Regret{Time: ago(time.Duration(i) * time.Minute), Run: i + 1, Signal: SignalShow})
				})
				if err != nil {
					t.Error(err)
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	if err := <-readerErr; err != nil {
		t.Fatal(err)
	}
	s := LoadTune()
	if len(s.Entries) != writers {
		t.Fatalf("%d entries, want %d (lost updates)", len(s.Entries), writers)
	}
	for _, e := range s.Entries {
		if len(e.Regrets) != each {
			t.Fatalf("%s: %d regrets, want %d (lost updates)", e.Cmd, len(e.Regrets), each)
		}
	}
}

func TestTuneConcurrentProcesses(t *testing.T) {
	if os.Getenv("LX_TUNE_HELPER") != "" {
		return
	}
	dir := tuneWorld(t)
	const procs, each = 4, 8
	var cmds []*exec.Cmd
	for p := 0; p < procs; p++ {
		c := exec.Command(os.Args[0], "-test.run=^TestTuneHelperProcess$", "-test.count=1")
		c.Env = append(os.Environ(), "LX_TUNE_HELPER="+strconv.Itoa(p), "LX_DATA_DIR="+dir, "LX_TUNE_EACH="+strconv.Itoa(each))
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		cmds = append(cmds, c)
	}
	for _, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	s := LoadTune()
	if len(s.Entries) != procs {
		t.Fatalf("%d entries, want %d", len(s.Entries), procs)
	}
	for _, e := range s.Entries {
		if len(e.Regrets) != each {
			t.Fatalf("%s: %d regrets, want %d", e.Cmd, len(e.Regrets), each)
		}
	}
}

func TestTuneHelperProcess(t *testing.T) {
	id := os.Getenv("LX_TUNE_HELPER")
	if id == "" {
		t.Skip("helper for TestTuneConcurrentProcesses")
	}
	each, _ := strconv.Atoi(os.Getenv("LX_TUNE_EACH"))
	for i := 0; i < each; i++ {
		if err := UpdateTune(tuneNow, func(s *TuneState) bool {
			return s.AddRegret(s.Hash("project", "/p"), "cmd"+id, Regret{Time: ago(time.Duration(i) * time.Minute), Run: i + 1, Signal: SignalRaw})
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTuneLockGivesUp(t *testing.T) {
	dir := tuneWorld(t)
	defer func(w time.Duration) { lockWait = w }(lockWait)
	lockWait = 50 * time.Millisecond
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	unlock, err := lockTune(filepath.Join(dir, "tune.lock"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = UpdateTune(tuneNow, func(s *TuneState) bool { t.Error("ran without the lock"); return true })
	if !errors.Is(err, errTuneBusy) || time.Since(start) > 5*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
	unlock()
	if err := UpdateTune(tuneNow, func(s *TuneState) bool { return true }); err != nil {
		t.Fatalf("after unlock: %v", err)
	}

	excl := filepath.Join(dir, "excl.lock")
	if err := os.WriteFile(excl, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := lockTuneExcl(excl); !errors.Is(err, errTuneBusy) {
		t.Fatalf("a fresh lock file was not waited on: %v", err)
	}
	old := time.Now().Add(-2 * staleLockAfter)
	if err := os.Chtimes(excl, old, old); err != nil {
		t.Fatal(err)
	}
	un, err := lockTuneExcl(excl)
	if err != nil {
		t.Fatalf("a stale lock file was not taken over: %v", err)
	}
	un()
	if _, err := os.Stat(excl); !os.IsNotExist(err) {
		t.Fatalf("unlock left the lock file: %v", err)
	}
}

func TestTuneEnabled(t *testing.T) {
	for _, c := range []struct {
		tune, trackv string
		want         bool
	}{{"", "", true}, {"1", "", true}, {"0", "", false}, {"", "0", false}, {"1", "1", true}} {
		t.Setenv("LX_TUNE", c.tune)
		t.Setenv("LX_TRACK", c.trackv)
		if TuneEnabled() != c.want {
			t.Errorf("LX_TUNE=%q LX_TRACK=%q: %v", c.tune, c.trackv, !c.want)
		}
	}
}

func TestTuneFileStaysSmall(t *testing.T) {
	tuneWorld(t)
	_ = UpdateTune(tuneNow, func(s *TuneState) bool {
		for i := 0; i < 3*maxEntries; i++ {
			for j := 0; j < 2*maxRegrets; j++ {
				s.AddRegret(s.Hash("project", fmt.Sprint(i)), "some command", Regret{Time: ago(time.Duration(j) * time.Minute), Signal: SignalShow})
			}
		}
		for i := 0; i < 3*maxRecent; i++ {
			s.AddRecent(TuneRun{Time: tuneNow.Unix(), Run: i + 1, Project: s.Hash("p"), Cmd: "some command", Argv: s.Hash("a")})
		}
		return true
	})
	st, err := os.Stat(TunePath())
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() > 128<<10 {
		t.Fatalf("tune.json is %d bytes at its bounds", st.Size())
	}
	var s TuneState
	b, _ := os.ReadFile(TunePath())
	if json.Unmarshal(b, &s) != nil || len(s.Entries) != maxEntries || len(s.Recent) != maxRecent {
		t.Fatalf("%d entries, %d recent", len(s.Entries), len(s.Recent))
	}
}

func TestLoadTuneForSkipsOtherCommands(t *testing.T) {
	tuneWorld(t)
	if s := LoadTuneFor("go test"); s == nil || len(s.Entries) != 0 {
		t.Fatalf("no file: %+v", s)
	}
	_ = UpdateTune(tuneNow, func(s *TuneState) bool {
		p := s.Hash("project", "/p")
		s.AddRegret(p, `say "hi" <now>`, Regret{Time: tuneNow.Unix(), Signal: SignalShow})
		s.AddRecent(TuneRun{Time: tuneNow.Unix(), Run: 1, Project: p, Cmd: "go test", Argv: s.Hash("a")})
		return true
	})
	if s := LoadTuneFor("go test"); len(s.Entries) != 0 || s.Salt != "" {
		t.Fatalf("decoded for a command with no regrets: %+v", s)
	}
	s := LoadTuneFor(`say "hi" <now>`)
	if len(s.Entries) != 1 || !s.HasCmd(`say "hi" <now>`) || s.Salt == "" {
		t.Fatalf("a command with regrets (escaped in JSON): %+v", s)
	}
}
