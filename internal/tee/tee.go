// Package tee stores full outputs for lx show.
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
	MaxBytes = 256 << 20

	MinAge = 24 * time.Hour

	MaxPart = 64 << 20
)

const maxID = 1 << 30

const staleReservation = time.Minute

const (
	StateDone       = ""
	StateRunning    = "running"
	StateIncomplete = "incomplete"
)

var (
	keep     = Keep
	maxBytes = int64(MaxBytes)
	minAge   = MinAge
	maxPart  = int64(MaxPart)
)

type Meta struct {
	ID     int       `json:"id"`
	Argv   []string  `json:"argv"`
	Cwd    string    `json:"cwd"`
	Exit   int       `json:"exit"`
	Filter string    `json:"filter"`
	Time   time.Time `json:"time"`
	Bytes  int       `json:"bytes"`
	State  string    `json:"state,omitempty"`
	PID    int       `json:"pid,omitempty"`
}

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

func Enabled() bool { return os.Getenv("LX_TEE") != "0" }

var errDisabled = errors.New("tee disabled")

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

type Spool struct {
	dir      string
	id       int
	time     time.Time
	reserved time.Time
	ids      []int

	mu       sync.Mutex
	f        *os.File
	n        int64
	err      error
	finished bool
}

var errNoSpool = errors.New("tee: no spool")

func Reserve(m Meta) (*Spool, error) { return reserve(m, true) }

func (s *Spool) ID() int {
	if s == nil {
		return 0
	}
	return s.id
}

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

		_ = writeAtomic(base+".json", b)
	}
	_ = os.Remove(base + ".log.part")
	ids := s.ids
	if time.Since(s.reserved) > time.Second {
		ids = nil
	}
	prune(s.dir, s.id, len(output), ids)
	return nil
}

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

func claim(dir string, next int, m Meta) (int, error) {
	for attempt, id := 0, next; attempt < 50 && id < maxID; attempt, id = attempt+1, id+1 {
		base := filepath.Join(dir, strconv.Itoa(id))
		f, err := os.OpenFile(base+".json", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {

			id = max(id, readSeq(dir))
			continue
		}
		if err != nil {
			return 0, err
		}
		if _, err := os.Lstat(base + ".log"); err == nil {

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
				continue
			}
			m.State, m.Bytes = st, len(b)
			return string(b), m, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", Meta{}, err
		}

	}
	return "", Meta{}, fmt.Errorf("no stored output with id %d (runs are kept up to %d days; the newest %d and at most %d MiB)",
		id, int(MaxAge.Hours()/24), Keep, MaxBytes>>20)
}

func liveState(base string, pid int) string {
	part := base + ".log.part"
	if exists(part) {
		switch partHeld(part) {
		case alive:
			return StateRunning
		case dead:
			return StateIncomplete
		}

		if pid > 0 && pidAlive(pid) == alive {
			return StateRunning
		}
		return StateIncomplete
	}

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
		return 0
	}
	return n
}

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

func createFresh(path string) (*os.File, error) {
	_ = os.Remove(path)
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
}

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

type runStat struct {
	mod  time.Time
	size int64
	live bool
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
			continue
		}
		r := statRun(dir, id)
		age := now.Sub(r.mod)
		if age < minAge || remaining <= keep && age <= MaxAge {
			break
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
