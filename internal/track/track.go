// Package track records token savings.
package track

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Record struct {
	Time   int64  `json:"t"`
	Cmd    string `json:"cmd"`
	Filter string `json:"filter"`
	Raw    int    `json:"raw"`
	Out    int    `json:"out"`
	Ms     int64  `json:"ms"`
	Exit   int    `json:"exit"`
	Lossy  bool   `json:"lossy,omitzero"`

	Kind string `json:"kind,omitempty"`
	Of   int    `json:"of,omitempty"`
	Mode string `json:"mode,omitempty"`
}

const KindShow = "show"

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
