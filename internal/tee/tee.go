// Package tee stores the full output of commands whose view lx condensed,
// so nothing is ever lost: `lx show <id>` prints it back byte-for-byte.
//
// Runs live in <cache>/lx/runs as <id>.log (raw output) and <id>.json
// (metadata), mode 0600 in a 0700 directory. IDs are small integers so the
// receipt stays short. The newest Keep runs are retained, none older than
// MaxAge.
package tee

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	Keep   = 200
	MaxAge = 7 * 24 * time.Hour
)

// Meta describes a stored run.
type Meta struct {
	ID     int       `json:"id"`
	Argv   []string  `json:"argv"`
	Cwd    string    `json:"cwd"`
	Exit   int       `json:"exit"`
	Filter string    `json:"filter"`
	Time   time.Time `json:"time"`
	Bytes  int       `json:"bytes"`
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

// Save stores output and returns its id. Disabled with LX_TEE=0.
func Save(m Meta, output string) (int, error) {
	if os.Getenv("LX_TEE") == "0" {
		return 0, errors.New("tee disabled")
	}
	dir := Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	ids := list(dir)
	next := 1
	if len(ids) > 0 {
		next = ids[len(ids)-1] + 1
	}
	for attempt := 0; attempt < 50; attempt++ {
		id := next + attempt
		f, err := os.OpenFile(filepath.Join(dir, strconv.Itoa(id)+".log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue // a concurrent lx took this id
		}
		if err != nil {
			return 0, err
		}
		_, werr := f.WriteString(output)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			return 0, errors.Join(werr, cerr)
		}
		m.ID, m.Bytes = id, len(output)
		if m.Time.IsZero() {
			m.Time = time.Now()
		}
		if b, err := json.Marshal(m); err == nil {
			_ = os.WriteFile(filepath.Join(dir, strconv.Itoa(id)+".json"), b, 0o600)
		}
		prune(dir, append(ids, id))
		return id, nil
	}
	return 0, errors.New("could not allocate a run id")
}

// Load returns a stored run's output and metadata.
func Load(id int) (string, Meta, error) {
	dir := Dir()
	b, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(id)+".log"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", Meta{}, fmt.Errorf("no stored output with id %d (runs are kept %d days, newest %d)", id, int(MaxAge.Hours()/24), Keep)
		}
		return "", Meta{}, err
	}
	var m Meta
	if mb, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(id)+".json")); err == nil {
		_ = json.Unmarshal(mb, &m)
	}
	m.ID = id
	return string(b), m, nil
}

// Recent returns metadata of the newest n runs, newest first.
func Recent(n int) []Meta {
	dir := Dir()
	ids := list(dir)
	var out []Meta
	for i := len(ids) - 1; i >= 0 && len(out) < n; i-- {
		var m Meta
		if mb, err := os.ReadFile(filepath.Join(dir, strconv.Itoa(ids[i])+".json")); err == nil {
			_ = json.Unmarshal(mb, &m)
		}
		m.ID = ids[i]
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
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".log") {
			continue
		}
		if id, err := strconv.Atoi(strings.TrimSuffix(name, ".log")); err == nil {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

func prune(dir string, ids []int) {
	sort.Ints(ids)
	cutoff := time.Now().Add(-MaxAge)
	for i, id := range ids {
		base := filepath.Join(dir, strconv.Itoa(id))
		old := len(ids)-i > Keep
		if !old {
			if st, err := os.Stat(base + ".log"); err == nil && st.ModTime().Before(cutoff) {
				old = true
			}
		}
		if old {
			_ = os.Remove(base + ".log")
			_ = os.Remove(base + ".json")
		}
	}
}
