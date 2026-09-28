package tee

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func setKnobs(t *testing.T, k int, mb int64) {
	t.Helper()
	ok, omb, oma, omp := keep, maxBytes, minAge, maxPart
	keep, maxBytes = k, mb
	t.Cleanup(func() { keep, maxBytes, minAge, maxPart = ok, omb, oma, omp })
}

func age(t *testing.T, id int, d time.Duration) {
	t.Helper()
	when := time.Now().Add(-d)
	base := filepath.Join(Dir(), strconv.Itoa(id))
	for _, ext := range []string{".log", ".log.part", ".json"} {
		if err := os.Chtimes(base+ext, when, when); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	raw := "line 1\n\x1b[31mred\x1b[0m\nlast"
	id, err := Save(Meta{Argv: []string{"go", "test"}, Exit: 1}, raw)
	if err != nil {
		t.Fatal(err)
	}
	got, m, err := Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if got != raw {
		t.Fatalf("output not byte-identical: %q", got)
	}
	if m.Exit != 1 || m.Argv[1] != "test" || m.State != StateDone || m.StatusNote() != "" || m.Bytes != len(raw) {
		t.Fatalf("meta = %+v", m)
	}
	for _, name := range []string{"1.log", "1.json", ".seq"} {
		st, err := os.Stat(filepath.Join(Dir(), name))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, want 0600", name, st.Mode().Perm())
		}
	}
	if _, err := os.Stat(filepath.Join(Dir(), "1.log.part")); !os.IsNotExist(err) {
		t.Fatalf("Save left a .part file: %v", err)
	}
}

func TestIDsIncreaseAndPrune(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	setKnobs(t, 5, MaxBytes)
	var last int
	for i := 0; i < 12; i++ {
		id, err := Save(Meta{}, "x")
		if err != nil {
			t.Fatal(err)
		}
		if id <= last {
			t.Fatalf("id %d after %d", id, last)
		}
		last = id
	}

	if n := len(list(Dir())); n != 12 {
		t.Fatalf("kept %d young runs, want all 12", n)
	}
	for id := 1; id <= 10; id++ {
		age(t, id, 2*24*time.Hour)
	}
	if _, err := Save(Meta{}, "x"); err != nil {
		t.Fatal(err)
	}

	if got := list(Dir()); !slices.Equal(got, []int{9, 10, 11, 12, 13}) {
		t.Fatalf("after count prune: %v", got)
	}
	if _, _, err := Load(1); err == nil {
		t.Fatal("oldest run should have been pruned")
	}
	if r := Recent(3); len(r) != 3 || r[0].ID != 13 || r[2].ID != 11 {
		t.Fatalf("Recent = %+v", r)
	}

	age(t, 9, MaxAge+time.Hour)
	if _, err := Save(Meta{}, "x"); err != nil {
		t.Fatal(err)
	}
	if got := list(Dir()); got[0] != 10 {
		t.Fatalf("a run older than MaxAge survived: %v", got)
	}
}

func TestYoungRunsSurviveCountOverflow(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	setKnobs(t, 2, MaxBytes)
	for i := 0; i < 6; i++ {
		if _, err := Save(Meta{}, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if got := list(Dir()); len(got) != 6 {
		t.Fatalf("young runs were pruned by count: %v", got)
	}
}

func TestPruneByBytesOldestFirst(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	setKnobs(t, Keep, 10_000)

	sp, err := Reserve(Meta{Argv: []string{"sleep", "100"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sp.Write([]byte(strings.Repeat("s", 3000))); err != nil {
		t.Fatal(err)
	}
	out := strings.Repeat("y", 2999) + "\n"
	for i := 0; i < 7; i++ {
		if _, err := Save(Meta{}, out); err != nil {
			t.Fatal(err)
		}
	}

	if got := list(Dir()); !slices.Equal(got, []int{1, 7, 8}) {
		t.Fatalf("after byte prune: %v", got)
	}

	if err := sp.Finish(Meta{Argv: []string{"sleep", "100"}}, "done\n"); err != nil {
		t.Fatal(err)
	}
}

func TestBigRunTriggersBytePrune(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	setKnobs(t, Keep, 1<<20+10)
	if _, err := Save(Meta{}, "small"); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(Meta{}, strings.Repeat("z", 1<<20)); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(Meta{}, strings.Repeat("z", 1<<20)); err != nil {
		t.Fatal(err)
	}

	if got := list(Dir()); !slices.Equal(got, []int{3}) {
		t.Fatalf("after a big run: %v", got)
	}
}

func TestConcurrentSaveAndRead(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	const writers, perWriter = 16, 6
	payload := strings.Repeat("0123456789abcdef", 4096)
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		ids   = map[int]string{}
		stop  = make(chan struct{})
		errCh = make(chan error, 64)
	)
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, m := range Recent(20) {
					out, lm, err := Load(m.ID)
					if err != nil {
						continue
					}
					if lm.State == StateDone && (!strings.HasSuffix(out, "END\n") || !strings.Contains(out, payload)) {
						errCh <- fmt.Errorf("run %d read truncated: %d bytes", m.ID, len(out))
						return
					}
				}
			}
		}()
	}
	var ww sync.WaitGroup
	for w := 0; w < writers; w++ {
		ww.Add(1)
		go func() {
			defer ww.Done()
			last := 0
			for i := 0; i < perWriter; i++ {
				tag := fmt.Sprintf("w%d-%d", w, i)
				id, err := Save(Meta{Argv: []string{tag}}, tag+"\n"+payload+"\nEND\n")
				if err != nil {
					errCh <- err
					return
				}
				if id <= last {
					errCh <- fmt.Errorf("writer %d: id %d after %d", w, id, last)
				}
				last = id
				mu.Lock()
				if prev, dup := ids[id]; dup {
					errCh <- fmt.Errorf("id %d given to %s and %s", id, prev, tag)
				}
				ids[id] = tag
				mu.Unlock()
			}
		}()
	}
	ww.Wait()
	close(stop)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	if len(ids) != writers*perWriter {
		t.Fatalf("%d ids for %d runs", len(ids), writers*perWriter)
	}
	for id, tag := range ids {
		out, m, err := Load(id)
		if err != nil || !strings.HasPrefix(out, tag+"\n") || m.Argv[0] != tag {
			t.Fatalf("run %d: %v %q", id, err, m.Argv)
		}
	}
}

func TestIDsNeverRepeatAfterEmptying(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	var last int
	for i := 0; i < 3; i++ {
		last, _ = Save(Meta{}, "x")
	}
	for _, id := range list(Dir()) {
		removeRun(Dir(), id)
	}
	id, err := Save(Meta{}, "y")
	if err != nil {
		t.Fatal(err)
	}
	if id <= last {
		t.Fatalf("id %d reused after the store was emptied (old max %d)", id, last)
	}
}

func TestLeftoverTmpIgnored(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	dir := Dir()
	os.MkdirAll(dir, 0o700)
	for _, name := range []string{"7.log.tmp", "8.json.tmp", ".seq-123.tmp", "doctor-probe", "x.log", "0.log", "-3.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("junk"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if ids := list(dir); len(ids) != 0 {
		t.Fatalf("temporary files listed as runs: %v", ids)
	}
	if _, _, err := Load(7); err == nil {
		t.Fatal("a leftover .log.tmp was served as a run")
	}
	if id, err := Save(Meta{}, "x"); err != nil || id != 1 {
		t.Fatalf("Save = %d, %v", id, err)
	}
}

func TestSpoolIncompleteAfterDeadLx(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	sp, err := Reserve(Meta{Argv: []string{"sh", "-c", "seq 1 500; sleep 30"}, Cwd: "/w"})
	if err != nil {
		t.Fatal(err)
	}
	var want strings.Builder
	for i := 1; i <= 500; i++ {
		fmt.Fprintf(&want, "%d\n", i)
	}
	if _, err := sp.Write([]byte(want.String())); err != nil {
		t.Fatal(err)
	}

	out, m, err := Load(sp.ID())
	if err != nil || out != want.String() {
		t.Fatalf("live Load: %v, %d bytes", err, len(out))
	}
	if m.State != StateRunning || !strings.HasPrefix(m.StatusNote(), "still running (started ") || m.Exit != -1 {
		t.Fatalf("live meta = %+v, note %q", m, m.StatusNote())
	}
	if r := Recent(1); len(r) != 1 || r[0].State != StateRunning {
		t.Fatalf("Recent = %+v", r)
	}

	sp.f.Close()
	path := filepath.Join(Dir(), strconv.Itoa(sp.ID())+".json")
	b, _ := os.ReadFile(path)
	var raw map[string]any
	json.Unmarshal(b, &raw)
	raw["pid"] = 1 << 30
	b, _ = json.Marshal(raw)
	os.WriteFile(path, b, 0o600)

	out, m, err = Load(sp.ID())
	if err != nil || out != want.String() || strings.Count(out, "\n") != 500 {
		t.Fatalf("dead Load: %v, %d bytes", err, len(out))
	}
	if m.State != StateIncomplete || !strings.HasPrefix(m.StatusNote(), "incomplete: lx stopped before the command finished") {
		t.Fatalf("dead meta = %+v, note %q", m, m.StatusNote())
	}
	if r := Recent(1); r[0].State != StateIncomplete {
		t.Fatalf("Recent = %+v", r)
	}

	if statRun(Dir(), sp.ID()).live {
		t.Fatal("a dead spool counts as live")
	}
}

func TestSpoolFinish(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	sp, err := Reserve(Meta{Argv: []string{"make"}})
	if err != nil {
		t.Fatal(err)
	}
	sp.Write([]byte("partial\n"))
	st, err := os.Stat(filepath.Join(Dir(), "1.log.part"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("part: %v %v", err, st)
	}
	if err := sp.Finish(Meta{Argv: []string{"make"}, Exit: 2, Filter: "make"}, "partial\nand the rest\n"); err != nil {
		t.Fatal(err)
	}
	out, m, err := Load(1)
	if err != nil || out != "partial\nand the rest\n" || m.Exit != 2 || m.State != StateDone || m.PID != 0 {
		t.Fatalf("Load = %q %+v %v", out, m, err)
	}
	if _, err := os.Stat(filepath.Join(Dir(), "1.log.part")); !os.IsNotExist(err) {
		t.Fatal(".part not removed")
	}
	if _, err := sp.Write([]byte("late")); err == nil {
		t.Fatal("Write after Finish succeeded")
	}
	if err := sp.Finish(Meta{}, "again"); err == nil {
		t.Fatal("second Finish succeeded")
	}
	var nilSpool *Spool
	if _, err := nilSpool.Write([]byte("x")); err == nil || nilSpool.ID() != 0 {
		t.Fatal("nil spool must fail safely")
	}
}

func TestSpoolCap(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	setKnobs(t, Keep, MaxBytes)
	maxPart = 100
	sp, err := Reserve(Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := sp.Write([]byte(strings.Repeat("a", 60))); err != nil || n != 60 {
		t.Fatal(n, err)
	}
	if n, err := sp.Write([]byte(strings.Repeat("b", 60))); err == nil || n != 40 {
		t.Fatalf("write past the cap: %d, %v", n, err)
	}
	if _, err := sp.Write([]byte("c")); err == nil {
		t.Fatal("errors must latch")
	}
	out, _, _ := Load(1)
	want := strings.Repeat("a", 60) + strings.Repeat("b", 40) + "\n[lx: the saved output stops here (over 0 MiB)]\n"
	if out != want {
		t.Fatalf("capped part = %q", out)
	}
}

func TestOldStoreStillLoads(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	dir := Dir()
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "41.log"), []byte("old output\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "41.json"), []byte(`{"id":41,"argv":["git","log"],"cwd":"/r","exit":0,"filter":"git-log","time":"2026-09-20T10:00:00Z","bytes":11}`), 0o600)
	os.WriteFile(filepath.Join(dir, "42.log"), []byte("no meta\n"), 0o600)
	out, m, err := Load(41)
	if err != nil || out != "old output\n" || m.Filter != "git-log" || m.State != StateDone {
		t.Fatalf("Load(41) = %q %+v %v", out, m, err)
	}
	if out, _, err := Load(42); err != nil || out != "no meta\n" {
		t.Fatalf("Load(42) = %q %v", out, err)
	}
	if r := Recent(5); len(r) != 2 || r[0].ID != 42 || r[1].Argv[0] != "git" {
		t.Fatalf("Recent = %+v", r)
	}
	if id, err := Save(Meta{}, "new"); err != nil || id != 43 {
		t.Fatalf("Save = %d %v", id, err)
	}
}

func TestLoadMissing(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	_, _, err := Load(9)
	if err == nil || !strings.Contains(err.Error(), "no stored output with id 9") {
		t.Fatalf("err = %v", err)
	}
}

func TestDisabled(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	t.Setenv("LX_TEE", "0")
	if _, err := Save(Meta{}, "x"); err == nil {
		t.Fatal("expected error when disabled")
	}
	if _, err := Reserve(Meta{}); err == nil {
		t.Fatal("expected Reserve error when disabled")
	}
	if ents, _ := os.ReadDir(Dir()); len(ents) != 0 {
		t.Fatalf("disabled tee wrote %d files", len(ents))
	}
}

func TestStatusNote(t *testing.T) {
	now := time.Now()
	cases := []struct {
		m    Meta
		want string
	}{
		{Meta{}, ""},
		{Meta{State: StateRunning, Time: now.Add(-125 * time.Second)}, "still running (started 2m ago)"},
		{Meta{State: StateRunning, Time: now.Add(-5 * time.Second)}, "still running (started 5s ago)"},
		{Meta{State: StateIncomplete}, "incomplete: lx stopped before the command finished; this is the output up to then"},
		{Meta{State: "something new"}, "incomplete: lx stopped before the command finished; this is the output up to then"},
	}
	for _, c := range cases {
		if got := c.m.StatusNote(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.m, got, c.want)
		}
	}
}

func BenchmarkSaveFullStore(b *testing.B) {
	b.Setenv("LX_TEE_DIR", b.TempDir())
	for i := 0; i < 2000; i++ {
		if _, err := Save(Meta{Argv: []string{"x"}}, "some output\n"); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Save(Meta{Argv: []string{"x"}}, "some output\n"); err != nil {
			b.Fatal(err)
		}
	}
}

func lockable(t *testing.T, sp *Spool) bool {
	t.Helper()
	if !haveLocks {
		return false
	}
	if got := partHeld(filepath.Join(Dir(), strconv.Itoa(sp.ID())+".log.part")); got != alive {
		t.Fatalf("a live spool's lock reads as %d", got)
	}
	return true
}

func TestReusedPidIsNotRunning(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	sp, err := Reserve(Meta{Argv: []string{"go", "test"}})
	if err != nil {
		t.Fatal(err)
	}
	sp.Write([]byte("=== RUN TestA\n"))
	if !lockable(t, sp) {
		t.Skip("no flock on this platform: liveness is the pid")
	}

	sp.f.Close()
	out, m, err := Load(sp.ID())
	if err != nil || out != "=== RUN TestA\n" {
		t.Fatalf("Load: %v %q", err, out)
	}
	if m.State != StateIncomplete || !strings.HasPrefix(m.StatusNote(), "incomplete: ") {
		t.Fatalf("a dead lx with a reused pid reads as %q (%q)", m.State, m.StatusNote())
	}
	if r := Recent(1); r[0].State != StateIncomplete {
		t.Fatalf("Recent = %+v", r)
	}
	if statRun(Dir(), sp.ID()).live {
		t.Fatal("an abandoned spool is never pruned while its pid is reused")
	}
}

func TestLockedSpoolIsRunning(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	sp, err := Reserve(Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if !lockable(t, sp) {
		t.Skip("no flock on this platform")
	}
	path := filepath.Join(Dir(), strconv.Itoa(sp.ID())+".json")
	b, _ := os.ReadFile(path)
	var raw map[string]any
	json.Unmarshal(b, &raw)
	raw["pid"] = 1 << 30
	b, _ = json.Marshal(raw)
	os.WriteFile(path, b, 0o600)
	if _, m, _ := Load(sp.ID()); m.State != StateRunning {
		t.Fatalf("a locked spool reads as %q", m.State)
	}
	if !statRun(Dir(), sp.ID()).live {
		t.Fatal("a locked spool counts as abandoned")
	}
	sp.Finish(Meta{}, "done\n")
}

func TestFinishNeverReadsIncomplete(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	final := strings.Repeat("output line\n", 2000)
	for i := 0; i < 60; i++ {
		sp, err := Reserve(Meta{})
		if err != nil {
			t.Fatal(err)
		}
		sp.Write([]byte("output line\n"))
		stop := make(chan struct{})
		bad := make(chan string, 1)
		go func() {
			defer close(bad)
			for {
				out, m, err := Load(sp.ID())
				switch {
				case err != nil:
					bad <- err.Error()
					return
				case m.State == StateIncomplete:
					bad <- "incomplete while finishing"
					return
				case m.State == StateDone && out != final:
					bad <- fmt.Sprintf("done with %d bytes", len(out))
					return
				case m.State == StateDone:
					return
				}
				select {
				case <-stop:
					return
				default:
				}
			}
		}()
		if err := sp.Finish(Meta{Exit: 1}, final); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
		close(stop)
		if msg, ok := <-bad; ok {
			t.Fatalf("run %d: %s", sp.ID(), msg)
		}
	}
}

func TestStaleEmptyReservation(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	setKnobs(t, 1, MaxBytes)
	dir := Dir()
	os.MkdirAll(dir, 0o700)
	empty := filepath.Join(dir, "1.json")
	os.WriteFile(empty, nil, 0o600)
	if !statRun(dir, 1).live {
		t.Fatal("a reservation being written right now must not be pruned")
	}
	age(t, 1, 3*24*time.Hour)
	if statRun(dir, 1).live {
		t.Fatal("a days-old empty reservation counts as live forever")
	}
	for i := 0; i < 2; i++ {
		if _, err := Save(Meta{}, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if exists(empty) {
		t.Fatal("the stale reservation was never pruned")
	}

	os.WriteFile(filepath.Join(dir, "9.json"), []byte(`{"id":9,"exit":-1,"state":"running","pid":`+strconv.Itoa(os.Getpid())+`}`), 0o600)
	age(t, 9, 2*time.Minute)
	for _, m := range Recent(10) {
		if m.ID == 9 && m.State != StateIncomplete {
			t.Fatalf("a dead Save's reservation reads as %q", m.State)
		}
	}
}

func TestNoWriteThroughPlantedLinks(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	dir := Dir()
	os.MkdirAll(dir, 0o700)
	victim := filepath.Join(t.TempDir(), "victim")
	os.WriteFile(victim, []byte("precious\n"), 0o600)
	for _, name := range []string{"1.log.tmp", "1.json.tmp", "1.log.part.tmp"} {
		if err := os.Symlink(victim, filepath.Join(dir, name)); err != nil {
			t.Skip("no symlinks here:", err)
		}
	}
	id, err := Save(Meta{Argv: []string{"secret"}}, "command output\n")
	if err != nil || id != 1 {
		t.Fatalf("Save = %d, %v", id, err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "precious\n" {
		t.Fatalf("lx wrote through a planted link: %q", b)
	}
	if out, m, err := Load(1); err != nil || out != "command output\n" || m.Argv[0] != "secret" {
		t.Fatalf("Load = %q %+v %v", out, m, err)
	}

	part := filepath.Join(dir, "7.log.part")
	os.Symlink(victim, part)
	f, err := createFresh(part)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("spooled\n")
	f.Close()
	if b, _ := os.ReadFile(victim); string(b) != "precious\n" {
		t.Fatalf("the spool wrote through a planted link: %q", b)
	}
	if st, err := os.Lstat(part); err != nil || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm() != 0o600 {
		t.Fatalf("spool file %v %v", st, err)
	}
}

func TestCorruptIDsIgnored(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	dir := Dir()
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, ".seq"), []byte("9223372036854775807\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "9223372036854775807.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(dir, "99999999999.log"), []byte("x"), 0o600)
	id, err := Save(Meta{}, "x")
	if err != nil || id != 1 {
		t.Fatalf("Save = %d, %v; want id 1", id, err)
	}
}

func TestRecentSkipsHalfWrittenMeta(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	dir := Dir()
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "3.json"), []byte(`{"id":3,"argv":["go","te`), 0o600)
	os.WriteFile(filepath.Join(dir, "4.log"), []byte("old output\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "4.json"), []byte(`{"id":4,"argv":[`), 0o600)
	r := Recent(10)
	if len(r) != 1 || r[0].ID != 4 {
		t.Fatalf("Recent = %+v; want only run 4 (it has output)", r)
	}
}

func TestStaleWriterStillGetsAnID(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	dir := Dir()
	os.MkdirAll(dir, 0o700)

	for id := 1; id <= 200; id++ {
		os.WriteFile(filepath.Join(dir, strconv.Itoa(id)+".json"), []byte("{}"), 0o600)
	}
	os.WriteFile(filepath.Join(dir, ".seq"), []byte("200\n"), 0o600)
	id, err := claim(dir, 1, Meta{State: StateRunning})
	if err != nil || id != 201 {
		t.Fatalf("claim = %d, %v; want id 201", id, err)
	}
}
