// Package track records how many tokens lx saved, locally, as one JSON line
// per command. Only the command name and first subcommand are stored (never
// arguments or output), so the log is safe to keep. Disable with LX_TRACK=0.
package track

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Record is one wrapped command.
type Record struct {
	Time   int64  `json:"t"`              // unix seconds
	Cmd    string `json:"cmd"`            // "git status", "go test", "pytest"
	Filter string `json:"filter"`         // filter that produced the view
	Raw    int    `json:"raw"`            // tokens the agent would have read
	Out    int    `json:"out"`            // tokens it did read
	Ms     int64  `json:"ms"`             // wall time of the command
	Exit   int    `json:"exit"`           // child exit status
	Lossy  bool   `json:"lossy,omitzero"` // a full copy was stored for lx show
}

// Path returns the history file ($LX_DATA_DIR overrides; else
// $XDG_DATA_HOME/lx or the OS config dir).
func Path() string {
	if d := os.Getenv("LX_DATA_DIR"); d != "" {
		return filepath.Join(d, "history.jsonl")
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "lx", "history.jsonl")
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".local", "share")); err == nil {
			return filepath.Join(home, ".local", "share", "lx", "history.jsonl")
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "lx", "history.jsonl")
}

// Add appends a record. Errors are ignored by callers: tracking must never
// break a command.
func Add(r Record) error {
	if os.Getenv("LX_TRACK") == "0" {
		return nil
	}
	if r.Time == 0 {
		r.Time = time.Now().Unix()
	}
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Load reads every record newer than since (zero = all).
func Load(since time.Time) ([]Record, error) {
	f, err := os.Open(Path())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if !since.IsZero() && r.Time < since.Unix() {
			continue
		}
		out = append(out, r)
	}
	return out, sc.Err()
}
