// Package tee stores the full output of commands whose view lx condensed,
// so nothing is ever lost: `lx show <id>` prints it back byte-for-byte.
//
// Runs live in <cache>/lx/runs, mode 0600 in a 0700 directory:
//
//	<id>.json      metadata; created first (O_EXCL), which reserves the id
//	<id>.log       the finished run's output, renamed into place complete
//	<id>.log.part  output so far of a run that is still going (a spool),
//	               flock'ed by the lx writing it for as long as it lives
//	.seq           the highest id handed out, so ids never repeat even
//	               after every run has been pruned
//
// A run whose metadata still says running is "running" only while the lx
// storing it holds the spool's lock (or, where there are no locks, while
// its pid is alive); otherwise it is "incomplete". The lock, unlike the
// pid, cannot be mistaken for a live lx after its pid is reused.
//
// IDs are small integers so the receipt stays short. Retention: at most
// Keep runs, none older than MaxAge, at most MaxBytes of output; runs
// younger than MinAge are pruned only to get under MaxBytes, and a run
// whose lx is still alive is never pruned.
package tee

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	Keep     = 1000
	MaxAge   = 7 * 24 * time.Hour
	MaxBytes = 256 << 20 // over all .log and .log.part files
	// MinAge protects recent runs from the Keep limit (not from MaxBytes).
	MinAge = 24 * time.Hour
	// MaxPart bounds one spool; past it the spool stops (the finished run
	// is still stored in full from the capture).
	MaxPart = 64 << 20
)

// maxID bounds run ids: a larger id in a file name or in .seq is corrupt
// (or planted), and ids near the int limit would overflow into negatives.
const maxID = 1 << 30

// staleReservation: a run's metadata says running from its reservation
// until Finish. Without a spool (Save) that lasts milliseconds, so such a
// reservation older than this was left by an lx that died.
const staleReservation = time.Minute

// Run states (Meta.State).
const (
	StateDone       = ""
	StateRunning    = "running"    // lx is still running the command
	StateIncomplete = "incomplete" // lx stopped before the command finished
)

// Knobs tests turn down.
var (
	keep     = Keep
	maxBytes = int64(MaxBytes)
	minAge   = MinAge
	maxPart  = int64(MaxPart)
)

// Meta describes a stored run.
type Meta struct {
	ID     int       `json:"id"`
	Argv   []string  `json:"argv"`
	Cwd    string    `json:"cwd"`
	Exit   int       `json:"exit"` // -1 while the run is not finished
	Filter string    `json:"filter"`
	Time   time.Time `json:"time"`
	Bytes  int       `json:"bytes"`
	State  string    `json:"state,omitempty"` // StateDone, StateRunning or StateIncomplete
	PID    int       `json:"pid,omitempty"`   // the lx process storing a running run
}

// StatusNote is a short note for a run that is not finished ("" when it is).
func (m Meta) StatusNote() string {
	switch m.State {
	case StateDone:
		return ""
	case StateRunning:
		if m.Time.IsZero() {
			return "still running"
		}
		return "still running (started " + ago(time.Since(m.Time)) + " ago)"
	}
	return "incomplete: lx stopped before the command finished; this is the output up to then"
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return strconv.Itoa(max(int(d/time.Second), 0)) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + "h"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + "d"
}

// Dir returns the run store directory ($LX_TEE_DIR overrides).
func Dir() string {
	if d := os.Getenv("LX_TEE_DIR"); d != "" {
		return d
	}
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "lx", "runs")
}

// Enabled reports whether runs are stored (LX_TEE=0 disables it).
func Enabled() bool { return os.Getenv("LX_TEE") != "0" }

var errDisabled = errors.New("tee disabled")

// Save stores output and returns its id. Disabled with LX_TEE=0.
func Save(m Meta, output string) (int, error) {
	s, err := reserve(m, false)
	if err != nil {
		return 0, err
	}
	if err := s.Finish(m, output); err != nil {
		s.discard()
		return 0, err
	}
	return s.id, nil
}

// Spool is a run reserved while its command is still going. Output written
// to it lands in <id>.log.part, so `lx show <id>` (and a later lx, if this
// one is killed) can read what the command printed so far.
type Spool struct {
	dir      string
	id       int
	time     time.Time
	reserved time.Time
	ids      []int // the store's runs when this one was reserved

	mu       sync.Mutex
	f        *os.File
	n        int64
	err      error // latched: the first write error stops the spool
	finished bool
}

var errNoSpool = errors.New("tee: no spool")

// Reserve allocates an id for a run that is still going: <id>.json says it
// is running (with this process's pid) and <id>.log.part is opened for
// Write. Finish stores the final output.
func Reserve(m Meta) (*Spool, error) { return reserve(m, true) }

// ID is the reserved run id.
func (s *Spool) ID() int {
	if s == nil {
		return 0
	}
	return s.id
}

// Write appends to the run's partial output. Errors latch: after one, every
// Write fails. Past MaxPart the spool ends with a note and stops.
func (s *Spool) Write(p []byte) (int, error) {
	if s == nil {
		return 0, errNoSpool
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 0, s.err
	}
	if s.f == nil {
		s.err = errNoSpool
		return 0, s.err
	}
	if s.n+int64(len(p)) > maxPart {
		// Keep what fits, then say where the saved output stops.
		k, _ := s.f.Write(p[:max(maxPart-s.n, 0)])
		s.n += int64(k)
		_, _ = s.f.WriteString("\n[lx: the saved output stops here (over " + strconv.FormatInt(maxPart>>20, 10) + " MiB)]\n")
		s.err = errors.New("tee: spool full")
		return k, s.err
	}
	n, err := s.f.Write(p)
	s.n += int64(n)
	if err != nil {
		s.err = err
	}
	return n, err
}

// Finish stores the run's final output (atomically: a reader sees either
// the partial output or all of it) and final metadata, and drops the spool.
// It fails only when the output could not be stored.
func (s *Spool) Finish(m Meta, output string) error {
	if s == nil {
		return errNoSpool
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return errors.New("tee: run already finished")
	}
	if s.err == nil {
		s.err = errors.New("tee: run finished")
	}
	base := filepath.Join(s.dir, strconv.Itoa(s.id))
	err := writeAtomic(base+".log", []byte(output))
	// The spool (and its lock) stays open until the whole output is in
	// place, so a reader never takes a finishing run for an abandoned one.
	if s.f != nil {
		_ = s.f.Close()
		s.f = nil
	}
	if err != nil {
		return err
	}
	s.finished = true
	m.ID, m.Bytes, m.State, m.PID = s.id, len(output), StateDone, 0
	if m.Time.IsZero() {
		m.Time = s.time
	}
	if b, err := json.Marshal(m); err == nil {
		// Best effort: with the .log in place the run reads as finished
		// even if its metadata still says running.
		_ = writeAtomic(base+".json", b)
	}
	_ = os.Remove(base + ".log.part")
	ids := s.ids
	if time.Since(s.reserved) > time.Second {
		ids = nil // a long run: the listing is stale
	}
	prune(s.dir, s.id, len(output), ids)
	return nil
}

// discard removes a reservation that never got its output.
func (s *Spool) discard() {
	base := filepath.Join(s.dir, strconv.Itoa(s.id))
	_ = os.Remove(base + ".log.part")
	_ = os.Remove(base + ".json")
}

func reserve(m Meta, spool bool) (*Spool, error) {
	if !Enabled() {
		return nil, errDisabled
	}
	dir := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	next := readSeq(dir)
	ids := list(dir)
	if len(ids) > 0 {
		next = max(next, ids[len(ids)-1])
	}
	next++
	if next+50 >= maxID {
		return nil, errors.New("tee: run ids exhausted (remove " + filepath.Join(dir, seqFile) + ")")
	}
	if m.Time.IsZero() {
		m.Time = time.Now()
	}
	m.Exit, m.Bytes, m.State, m.PID = -1, 0, StateRunning, os.Getpid()
	id, err := claim(dir, next, m)
	if err != nil {
		return nil, err
	}
	s := &Spool{dir: dir, id: id, time: m.Time, reserved: time.Now(), ids: ids}
	if spool {
		base := filepath.Join(dir, strconv.Itoa(id))
		s.f, err = createFresh(base + ".log.part")
		if err != nil {
			_ = os.Remove(base + ".json")
			return nil, err
		}
		lockPart(s.f)
	}
	writeSeq(dir, id)
	return s, nil
}

// claim reserves the first free id from next on: creating <id>.json
// exclusively is what makes the id this run's, and m (with its ID set) is
// written into it.
func claim(dir string, next int, m Meta) (int, error) {
	for attempt, id := 0, next; attempt < 50 && id < maxID; attempt, id = attempt+1, id+1 {
		base := filepath.Join(dir, strconv.Itoa(id))
		f, err := os.OpenFile(base+".json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			// A concurrent lx took this id. Skip past every id handed out
			// since our listing: under load, a writer descheduled after
			// listing can fall far behind (never 50 collisions in a row).
			id = max(id, readSeq(dir))
			continue
		}
		if err != nil {
			return 0, err
		}
		if _, err := os.Lstat(base + ".log"); err == nil {
			// Output without metadata, left by an older lx: not ours.
			_ = f.Close()
			_ = os.Remove(base + ".json")
			continue
		}
		m.ID = id
		b, _ := json.Marshal(m)
		_, werr := f.Write(b)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(base + ".json")
			return 0, werr
		}
		return id, nil
	}
	return 0, errors.New("could not allocate a run id")
}

// Load returns a stored run's output and metadata. A run that is still
// going (or whose lx died) returns its partial output with Meta.State set.
func Load(id int) (string, Meta, error) {
	dir := Dir()
	base := filepath.Join(dir, strconv.Itoa(id))
	var m Meta
	if mb, err := os.ReadFile(base + ".json"); err == nil {
		_ = json.Unmarshal(mb, &m)
	}
	m.ID = id
	for try := 0; try < 3; try++ {
		b, err := os.ReadFile(base + ".log")
		if err == nil {
			if m.State != StateDone {
				// The .log is renamed into place before the final
				// metadata: re-read it once.
				if mb, err := os.ReadFile(base + ".json"); err == nil {
					var fm Meta
					if json.Unmarshal(mb, &fm) == nil && fm.State == StateDone {
						m = fm
						m.ID = id
					}
				}
				m.State, m.PID = StateDone, 0
			}
			return string(b), m, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", Meta{}, err
		}
		b, err = os.ReadFile(base + ".log.part")
		if err == nil {
			st := liveState(base, m.PID)
			if st == StateIncomplete && try < 2 && exists(base+".log") {
				continue // it finished between the two reads: read the .log
			}
			m.State, m.Bytes = st, len(b)
			return string(b), m, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", Meta{}, err
		}
		// Neither: gone, or finished between the two reads. Look again.
	}
	return "", Meta{}, fmt.Errorf("no stored output with id %d (runs are kept up to %d days; the newest %d and at most %d MiB)",
		id, int(MaxAge.Hours()/24), Keep, MaxBytes>>20)
}

// liveState is the state of run base, whose metadata says running and
// which has no .log: StateRunning while the lx storing it is alive, else
// StateIncomplete.
func liveState(base string, pid int) string {
	part := base + ".log.part"
	if exists(part) {
		switch partHeld(part) {
		case alive:
			return StateRunning
		case dead:
			return StateIncomplete // even if pid now names another process
		}
		// No locks here: the pid decides.
		if pid > 0 && pidAlive(pid) == alive {
			return StateRunning
		}
		return StateIncomplete
	}
	// No spool: Save between its reservation and its output (milliseconds),
	// or Reserve just before it opens the spool.
	if pid > 0 && pidAlive(pid) == alive {
		if st, err := os.Stat(base + ".json"); err == nil && time.Since(st.ModTime()) < staleReservation {
			return StateRunning
		}
	}
	return StateIncomplete
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// Recent returns metadata of the newest n runs, newest first.
func Recent(n int) []Meta {
	dir := Dir()
	ids := list(dir)
	var out []Meta
	for i := len(ids) - 1; i >= 0 && len(out) < n; i-- {
		base := filepath.Join(dir, strconv.Itoa(ids[i]))
		var m Meta
		if mb, err := os.ReadFile(base + ".json"); err == nil {
			if len(mb) == 0 || json.Unmarshal(mb, &m) != nil {
				if !exists(base + ".log") {
					// Being reserved right now (a reader can catch the
					// metadata half-written), or garbage without output:
					// never a finished "exit 0" run.
					continue
				}
				m = Meta{}
			}
		}
		m.ID = ids[i]
		if m.State != StateDone {
			if exists(base + ".log") {
				m.State, m.PID = StateDone, 0
			} else {
				m.State = liveState(base, m.PID)
			}
		}
		out = append(out, m)
	}
	return out
}

// list returns the ids in dir, oldest first. A run is known by any of its
// files, so stores written by older versions (.log + .json) keep working.
func list(dir string) []int {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var ids []int
	seen := make(map[int]bool, len(ents)/2)
	for _, e := range ents {
		if id, ok := runID(e.Name()); ok && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

// runID parses "<id>.json", "<id>.log" and "<id>.log.part"; temporary files
// (.tmp) and anything else in the directory are not runs.
func runID(name string) (int, bool) {
	var stem string
	switch {
	case strings.HasSuffix(name, ".json"):
		stem = strings.TrimSuffix(name, ".json")
	case strings.HasSuffix(name, ".log"):
		stem = strings.TrimSuffix(name, ".log")
	case strings.HasSuffix(name, ".log.part"):
		stem = strings.TrimSuffix(name, ".log.part")
	default:
		return 0, false
	}
	if stem == "" || stem[0] < '1' || stem[0] > '9' {
		return 0, false
	}
	id, err := strconv.Atoi(stem)
	return id, err == nil && id > 0 && id < maxID
}

const seqFile = ".seq"

func readSeq(dir string) int {
	b, err := os.ReadFile(filepath.Join(dir, seqFile))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 0 || n >= maxID {
		return 0 // corrupt: the listing alone decides
	}
	return n
}

// writeSeq records id as the highest id handed out (best effort: the O_EXCL
// reservation alone keeps ids unique; .seq keeps them from restarting).
func writeSeq(dir string, id int) {
	if readSeq(dir) >= id {
		return
	}
	f, err := os.CreateTemp(dir, ".seq-*.tmp")
	if err != nil {
		return
	}
	_, werr := f.WriteString(strconv.Itoa(id) + "\n")
	if cerr := f.Close(); werr == nil && cerr == nil {
		if os.Rename(f.Name(), filepath.Join(dir, seqFile)) == nil {
			return
		}
	}
	_ = os.Remove(f.Name())
}

// createFresh creates path (mode 0600) for writing. Only the lx holding a
// run's id writes that run's files, so a file already at path is a crashed
// writer's leftover, or a link planted in a shared directory: it is
// removed and path created exclusively, never written through.
func createFresh(path string) (*os.File, error) {
	_ = os.Remove(path)
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
}

// writeAtomic writes path through path.tmp and a rename, so a reader never
// sees a half-written file.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := createFresh(tmp)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		_ = os.Remove(tmp)
	}
	return werr
}

// runStat describes one stored run for pruning.
type runStat struct {
	mod  time.Time
	size int64
	live bool // a running run whose lx is alive: never pruned
}

func statRun(dir string, id int) runStat {
	base := filepath.Join(dir, strconv.Itoa(id))
	if st, err := os.Stat(base + ".log"); err == nil {
		return runStat{mod: st.ModTime(), size: st.Size()}
	}
	var r runStat
	js, jerr := os.Stat(base + ".json")
	if st, err := os.Stat(base + ".log.part"); err == nil {
		r.mod, r.size = st.ModTime(), st.Size()
	} else if jerr == nil {
		r.mod = js.ModTime()
	}
	if jerr != nil {
		return r
	}
	mb, err := os.ReadFile(base + ".json")
	if err != nil {
		return r
	}
	if len(mb) == 0 {
		// Being reserved right now; an lx that died in between leaves it
		// empty for good, and that must not be kept forever.
		r.live = time.Since(js.ModTime()) < staleReservation
		return r
	}
	var m Meta
	if json.Unmarshal(mb, &m) == nil && m.State == StateRunning {
		r.live = liveState(base, m.PID) == StateRunning
	}
	return r
}

func removeRun(dir string, id int) {
	base := filepath.Join(dir, strconv.Itoa(id))
	for _, ext := range []string{".log", ".log.part", ".log.tmp", ".json.tmp", ".json"} {
		_ = os.Remove(base + ext)
	}
}

// prune applies retention after run just (of size bytes) was stored; ids
// is the store's listing from just before (nil: read it again). Oldest runs
// go first. The count and age limits stop at the first run younger than
// minAge, so they cost a stat or two; the byte limit, which needs every
// run's size, is checked on every 8th run and after any run of 1 MiB or
// more.
func prune(dir string, just, size int, ids []int) {
	now := time.Now()
	if ids == nil {
		ids = list(dir)
	} else if !slices.Contains(ids, just) {
		ids = append(slices.Clone(ids), just)
		sort.Ints(ids)
	}
	remaining := len(ids)
	for _, id := range ids {
		if id == just {
			continue // never the run whose id was just handed out
		}
		r := statRun(dir, id)
		age := now.Sub(r.mod)
		if age < minAge || remaining <= keep && age <= MaxAge {
			break // every newer run is younger
		}
		if r.live {
			continue
		}
		removeRun(dir, id)
		remaining--
	}
	if size >= 1<<20 || just%8 == 0 {
		pruneBytes(dir, just, now)
	}
}

func pruneBytes(dir string, just int, now time.Time) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	sizes := map[int]int64{}
	var total int64
	for _, e := range ents {
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") {
			// Left by a crashed writer (a live one renames within ms).
			if st, err := e.Info(); err == nil && now.Sub(st.ModTime()) > time.Hour {
				_ = os.Remove(filepath.Join(dir, name))
			}
			continue
		}
		if !strings.HasSuffix(name, ".log") && !strings.HasSuffix(name, ".log.part") {
			continue
		}
		id, ok := runID(name)
		if !ok {
			continue
		}
		if st, err := e.Info(); err == nil {
			sizes[id] += st.Size()
			total += st.Size()
		}
	}
	if total <= maxBytes {
		return
	}
	ids := make([]int, 0, len(sizes))
	for id := range sizes {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		if total <= maxBytes {
			return
		}
		if id == just || statRun(dir, id).live {
			continue
		}
		removeRun(dir, id)
		total -= sizes[id]
	}
}
