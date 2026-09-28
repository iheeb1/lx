package track

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

const (
	SignalShow = "show"
	SignalFull = "full"
	SignalRaw  = "raw"
)

const (
	LevelNormal = 0
	LevelLoosen = 1
	LevelRaw    = 2
)

const (
	RegretWindow  = 15 * time.Minute
	PolicyWindow  = 7 * 24 * time.Hour
	TuneRetention = 30 * 24 * time.Hour
	LoosenAt      = 2
	RawAt         = 4
	DecayRuns     = 20
)

const (
	tuneVersion    = 1
	maxRegrets     = 16
	maxEntries     = 128
	maxRecent      = 64
	maxTuneBytes   = 1 << 20
	maxClean       = 2 * DecayRuns
	saltBytes      = 16
	hashHexLen     = 16
	staleLockAfter = 10 * time.Second
)

var lockWait = time.Second

type Regret struct {
	Time   int64  `json:"t"`
	Run    int    `json:"id,omitempty"`
	Signal string `json:"s"`
}

type TuneEntry struct {
	Project string   `json:"p"`
	Cmd     string   `json:"k"`
	Regrets []Regret `json:"r"`

	Clean int `json:"c,omitempty"`
}

type TuneRun struct {
	Time      int64  `json:"t"`
	Run       int    `json:"id,omitempty"`
	Project   string `json:"p"`
	Cmd       string `json:"cmd"`
	Argv      string `json:"a"`
	Regretted bool   `json:"x,omitempty"`
}

type TuneState struct {
	Version int         `json:"v"`
	Salt    string      `json:"salt"`
	Entries []TuneEntry `json:"entries,omitempty"`
	Recent  []TuneRun   `json:"recent,omitempty"`
}

func TuneEnabled() bool {
	return os.Getenv("LX_TUNE") != "0" && os.Getenv("LX_TRACK") != "0"
}

func TunePath() string { return filepath.Join(filepath.Dir(Path()), "tune.json") }

func LoadTune() *TuneState { return loadTuneFile(TunePath()) }

func LoadTuneFor(cmd string) *TuneState {
	b := readTuneFile(TunePath())
	if b == nil {
		return &TuneState{Version: tuneVersion}
	}
	k, err := json.Marshal(cmd)
	if err != nil || !bytes.Contains(b, append([]byte(`"k":`), k...)) {
		return &TuneState{Version: tuneVersion}
	}
	return decodeTune(b)
}

func loadTuneFile(path string) *TuneState { return decodeTune(readTuneFile(path)) }

func readTuneFile(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxTuneBytes+1))
	if err != nil || len(b) > maxTuneBytes {
		return nil
	}
	return b
}

func decodeTune(b []byte) *TuneState {
	empty := &TuneState{Version: tuneVersion}
	if b == nil {
		return empty
	}
	var s TuneState
	if json.Unmarshal(b, &s) != nil || s.Version != tuneVersion || !isHex(s.Salt, 2*saltBytes) {
		return empty
	}
	s.sanitize()
	return &s
}

func (s *TuneState) sanitize() {
	s.Entries = slices.DeleteFunc(s.Entries, func(e TuneEntry) bool {
		return !isHex(e.Project, hashHexLen) || e.Cmd == ""
	})
	for i := range s.Entries {
		e := &s.Entries[i]
		e.Regrets = slices.DeleteFunc(e.Regrets, func(r Regret) bool {
			return r.Time <= 0 || r.Run < 0 || !validSignal(r.Signal)
		})
		sort.SliceStable(e.Regrets, func(a, b int) bool { return e.Regrets[a].Time < e.Regrets[b].Time })
		e.Clean = min(max(e.Clean, 0), maxClean)
	}
	s.Entries = slices.DeleteFunc(s.Entries, func(e TuneEntry) bool { return len(e.Regrets) == 0 })
	s.Recent = slices.DeleteFunc(s.Recent, func(r TuneRun) bool {
		return r.Time <= 0 || r.Run < 0 || !isHex(r.Project, hashHexLen) || !isHex(r.Argv, hashHexLen) || r.Cmd == ""
	})
}

func validSignal(s string) bool { return s == SignalShow || s == SignalFull || s == SignalRaw }

func isHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *TuneState) Hash(kind string, parts ...string) string {
	if s == nil || s.Salt == "" {
		return ""
	}
	h := sha256.New()
	h.Write([]byte(s.Salt))
	h.Write([]byte{0})
	h.Write([]byte(kind))
	for _, p := range parts {
		h.Write([]byte{0})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:hashHexLen]
}

func (s *TuneState) HasCmd(cmd string) bool {
	for _, e := range s.Entries {
		if e.Cmd == cmd {
			return true
		}
	}
	return false
}

func (s *TuneState) Entry(project, cmd string) *TuneEntry {
	if project == "" {
		return nil
	}
	for i := range s.Entries {
		if s.Entries[i].Project == project && s.Entries[i].Cmd == cmd {
			return &s.Entries[i]
		}
	}
	return nil
}

func (s *TuneState) AddRegret(project, cmd string, r Regret) bool {
	if project == "" || cmd == "" || !validSignal(r.Signal) || r.Time <= 0 {
		return false
	}
	e := s.Entry(project, cmd)
	if e == nil {
		s.Entries = append(s.Entries, TuneEntry{Project: project, Cmd: cmd})
		e = &s.Entries[len(s.Entries)-1]
	}
	if r.Run > 0 && slices.ContainsFunc(e.Regrets, func(x Regret) bool { return x.Run == r.Run }) {
		return false
	}
	e.Regrets = append(e.Regrets, r)
	sort.SliceStable(e.Regrets, func(a, b int) bool { return e.Regrets[a].Time < e.Regrets[b].Time })
	e.Clean = 0
	return true
}

func (s *TuneState) AddRecent(r TuneRun) {
	s.Recent = append(s.Recent, r)
}

func (s *TuneState) RecentRun(id int) *TuneRun {
	if id <= 0 {
		return nil
	}
	for i := len(s.Recent) - 1; i >= 0; i-- {
		if s.Recent[i].Run == id {
			return &s.Recent[i]
		}
	}
	return nil
}

func (s *TuneState) LatestRun(a string, now time.Time) *TuneRun {
	if a == "" {
		return nil
	}
	since, until := now.Add(-RegretWindow).Unix(), now.Unix()
	var best *TuneRun
	for i := range s.Recent {
		r := &s.Recent[i]
		if r.Argv == a && r.Time >= since && r.Time <= until && (best == nil || r.Time >= best.Time) {
			best = r
		}
	}
	return best
}

func (s *TuneState) HasRecent(now time.Time) bool {
	since := now.Add(-RegretWindow).Unix()
	for _, r := range s.Recent {
		if r.Time >= since {
			return true
		}
	}
	return false
}

func (s *TuneState) Reset(project, cmd string) int {
	n := len(s.Entries)
	s.Entries = slices.DeleteFunc(s.Entries, func(e TuneEntry) bool {
		return (project == "" || e.Project == project) && (cmd == "" || e.Cmd == cmd)
	})
	return n - len(s.Entries)
}

func (s *TuneState) Prune(now time.Time) {
	cut := now.Add(-TuneRetention).Unix()
	for i := range s.Entries {
		e := &s.Entries[i]
		e.Regrets = slices.DeleteFunc(e.Regrets, func(r Regret) bool { return r.Time < cut })
		if len(e.Regrets) > maxRegrets {
			e.Regrets = slices.Clone(e.Regrets[len(e.Regrets)-maxRegrets:])
		}
		e.Clean = min(max(e.Clean, 0), maxClean)
	}
	s.Entries = slices.DeleteFunc(s.Entries, func(e TuneEntry) bool { return len(e.Regrets) == 0 })
	sort.SliceStable(s.Entries, func(a, b int) bool {
		la, lb := s.Entries[a].latest(), s.Entries[b].latest()
		if la != lb {
			return la > lb
		}
		if s.Entries[a].Project != s.Entries[b].Project {
			return s.Entries[a].Project < s.Entries[b].Project
		}
		return s.Entries[a].Cmd < s.Entries[b].Cmd
	})
	if len(s.Entries) > maxEntries {
		s.Entries = s.Entries[:maxEntries]
	}

	since := now.Add(-RegretWindow).Unix()
	s.Recent = slices.DeleteFunc(s.Recent, func(r TuneRun) bool { return r.Time < since })
	sort.SliceStable(s.Recent, func(a, b int) bool { return s.Recent[a].Time < s.Recent[b].Time })
	if len(s.Recent) > maxRecent {
		s.Recent = slices.Clone(s.Recent[len(s.Recent)-maxRecent:])
	}
}

func (e TuneEntry) latest() int64 {
	if len(e.Regrets) == 0 {
		return 0
	}
	return e.Regrets[len(e.Regrets)-1].Time
}

type TuneLevel struct {
	Level  int
	Base   int
	Count  int
	Full   int
	Raw    int
	Week   int
	Total  int
	Latest time.Time
	Until  time.Time
	Clean  int
}

func (e TuneEntry) Evaluate(now time.Time) TuneLevel {
	var lv TuneLevel
	rs := e.Regrets
	lv.Total, lv.Clean = len(rs), min(max(e.Clean, 0), maxClean)
	if len(rs) == 0 {
		return lv
	}
	win := int64(PolicyWindow / time.Second)
	nowU := now.Unix()
	L := rs[len(rs)-1].Time
	lv.Latest = time.Unix(L, 0)
	var week, peak []Regret
	for _, r := range rs {
		if r.Time > nowU-win {
			week = append(week, r)
		}
		if r.Time > L-win {
			peak = append(peak, r)
		}
	}
	lv.Week = len(week)
	decided := week
	switch {
	case len(peak) >= RawAt && nowU < L+win:
		lv.Base, decided, lv.Until = LevelRaw, peak, time.Unix(L+win, 0)
	case len(week) >= LoosenAt:

		lv.Base, lv.Until = LevelLoosen, time.Unix(week[len(week)-LoosenAt].Time+win, 0)
	}
	lv.Count = len(decided)
	for _, r := range decided {
		if r.Signal == SignalRaw {
			lv.Raw++
		} else {
			lv.Full++
		}
	}
	lv.Level = max(LevelNormal, lv.Base-lv.Clean/DecayRuns)
	return lv
}

func UpdateTune(now time.Time, fn func(s *TuneState) bool) error {
	p := TunePath()
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	unlock, err := lockTune(filepath.Join(dir, "tune.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	s := loadTuneFile(p)
	if s.Salt == "" {
		salt, err := newSalt()
		if err != nil {
			return err
		}
		s = &TuneState{Version: tuneVersion, Salt: salt}
	}
	if !fn(s) {
		return nil
	}
	s.Prune(now)
	return writeTune(p, s)
}

func newSalt() (string, error) {
	b := make([]byte, saltBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func writeTune(path string, s *TuneState) error {
	s.Version = tuneVersion
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tune-*.tmp")
	if err != nil {
		return err
	}
	_, werr := f.Write(append(b, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(f.Name(), 0o600)
	}
	if werr == nil {
		werr = os.Rename(f.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(f.Name())
	}
	return werr
}

var errTuneBusy = errors.New("tune: state is locked by another lx")

func lockTuneExcl(path string) (func(), error) {
	deadline := time.Now().Add(lockWait)
	for wait := time.Millisecond; ; wait = min(2*wait, 20*time.Millisecond) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) > staleLockAfter {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, errTuneBusy
		}
		time.Sleep(wait)
	}
}

func LevelName(level int) string {
	switch level {
	case LevelLoosen:
		return "loosened"
	case LevelRaw:
		return "raw"
	}
	return "normal"
}

func SignalCounts(full, raw int) string {
	var parts []string
	if full > 0 || raw == 0 {
		parts = append(parts, plural(full, "full recall"))
	}
	if raw > 0 {
		parts = append(parts, plural(raw, "raw re-run"))
	}
	return strings.Join(parts, ", ")
}
