package doctor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/tee"
	"github.com/iheeb1/lx/internal/track"
)

func (s *state) checkStorage() {
	s.checkTee()
	s.checkHistory()
}

func (s *state) checkTee() {
	const id = "storage"
	dir := s.e.TeeDir
	if dir == "" {
		s.add(id, Warn, "the run store location is unknown", "")
		return
	}
	d := s.show(dir)
	fi, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		anc := existingAncestor(dir)
		if w, known := writable(anc); known && !w {
			s.add(id, Fail, "cannot create the run store "+d+": "+s.show(anc)+" is not writable, so condensed views cannot offer lx show",
				"export LX_TEE_DIR=<a writable directory>")
			return
		}
		s.add(id, OK, "runs: none stored yet ("+d+" is created on the first condensed run)", "")
		return
	case err != nil:
		s.add(id, Fail, "cannot read the run store "+d+": "+errText(err), "")
		return
	case !fi.IsDir():
		fix := "rm " + shellQuote(dir) + "  # lx recreates it as a directory"
		if s.e.Getenv("LX_TEE_DIR") != "" {
			fix = "export LX_TEE_DIR=<a directory>  # LX_TEE_DIR names a file"
		}
		s.add(id, Fail, "the run store "+d+" is not a directory", fix)
		return
	}

	f, err := os.CreateTemp(dir, ".lx-doctor-*.tmp")
	if err != nil {
		s.add(id, Fail, "cannot write to the run store "+d+" ("+errText(err)+"): condensed views cannot be stored, so their receipts cannot offer lx show",
			"chmod u+rwx "+shellQuote(dir)+"  # or: export LX_TEE_DIR=<a writable directory>")
		return
	}
	name := f.Name()
	f.Close()
	os.Remove(name)

	u := storeUsage(dir)
	msg := fmt.Sprintf("runs: %s, %s in %s", plural(u.runs, "stored run"), fmtBytes(u.bytes), d)
	if u.foreign > 0 {
		s.add(id, Warn, fmt.Sprintf("%s, which also holds %s lx did not write: the run store needs a directory of its own",
			msg, plural(u.foreign, "file")), "export LX_TEE_DIR=<an empty directory>  # in your shell's startup file")
		return
	}
	if u.bytes > bigStore {
		s.add(id, Warn, msg+" (over "+fmtBytes(bigStore)+"; lx normally prunes it to "+fmtBytes(tee.MaxBytes)+")",
			"find "+shellQuote(dir)+" -maxdepth 1 -type f -name '[0-9]*.*' -delete  # deletes every stored run: lx show <id> then has nothing to show")
		return
	}
	s.add(id, OK, msg, "")
}

type usage struct {
	runs    int
	bytes   int64
	foreign int
}

func runID(name string) int {
	head, ext, _ := strings.Cut(name, ".")
	switch ext {
	case "log", "json", "log.part", "log.tmp", "json.tmp":
	default:
		return -1
	}
	for i := 0; i < len(head); i++ {
		if head[i] < '0' || head[i] > '9' {
			return -1
		}
	}
	n, err := strconv.Atoi(head)
	if err != nil {
		return -1
	}
	return n
}

func storeUsage(dir string) (u usage) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return u
	}
	ids := map[int]bool{}
	for _, e := range ents {
		if !e.Type().IsRegular() {
			continue
		}
		name := e.Name()
		id := runID(name)
		switch {
		case id >= 0:
			ids[id] = true
		case name == ".seq":
		case strings.HasPrefix(name, "."):
			continue
		default:
			u.foreign++
			continue
		}
		if fi, err := e.Info(); err == nil {
			u.bytes += fi.Size()
		}
	}
	u.runs = len(ids)
	return u
}

func existingAncestor(p string) string {
	for {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

type historyInfo struct {
	exists  bool
	err     error
	size    int64
	records int
	lastRun time.Time
}

func (s *state) history() *historyInfo {
	if s.hist != nil {
		return s.hist
	}
	h := &historyInfo{}
	s.hist = h
	if s.e.HistoryPath == "" {
		return h
	}
	f, err := os.Open(s.e.HistoryPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			h.exists, h.err = true, err
		}
		return h
	}
	defer f.Close()
	h.exists = true
	fi, err := f.Stat()
	if err != nil {
		h.err = err
		return h
	}
	h.size = fi.Size()

	buf := make([]byte, 64<<10)
	var last byte = '\n'
	for {
		n, err := f.Read(buf)
		for _, b := range buf[:n] {
			if b == '\n' {
				h.records++
			}
		}
		if n > 0 {
			last = buf[n-1]
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			h.err = err
			return h
		}
	}
	if last != '\n' {
		h.records++
	}

	const tail = 256 << 10
	off := max(h.size-tail, 0)
	chunk := make([]byte, h.size-off)
	if _, err := f.ReadAt(chunk, off); err != nil && err != io.EOF {
		return h
	}
	lines := strings.Split(string(chunk), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var r struct {
			T    int64  `json:"t"`
			Kind string `json:"kind"`
		}
		if json.Unmarshal([]byte(lines[i]), &r) != nil || r.T <= 0 || r.Kind == track.KindShow {
			continue
		}
		h.lastRun = time.Unix(r.T, 0)
		break
	}
	return h
}

func (s *state) checkHistory() {
	const id = "storage"
	p := s.e.HistoryPath
	if p == "" {
		return
	}
	h := s.history()
	switch {
	case !h.exists:
		if anc := existingAncestor(filepath.Dir(p)); anc != "" {
			if w, known := writable(anc); known && !w {
				s.add(id, Warn, "history: cannot create "+s.show(p)+" ("+s.show(anc)+" is not writable), so lx gain has nothing to report",
					"export LX_DATA_DIR=<a writable directory>")
				return
			}
		}
		s.add(id, OK, "history: nothing recorded yet ("+s.show(p)+")", "")
	case h.err != nil:
		s.add(id, Warn, "history: cannot read "+s.show(p)+": "+errText(h.err), "")
	default:
		msg := fmt.Sprintf("history: %s, %s in %s", plural(h.records, "record"), fmtBytes(h.size), s.show(p))
		if w, known := writable(p); known && !w {
			s.add(id, Warn, msg+", not writable", "chmod u+w "+shellQuote(p))
			return
		}
		s.add(id, OK, msg, "")
	}
}

func (s *state) checkActivity() {
	const id = "activity"
	if len(s.hooks) == 0 {
		s.add(id, Skip, "no lx hook installed", "")
		return
	}
	if v := s.e.Getenv("LX_TRACK"); v == "0" {
		s.add(id, Skip, "LX_TRACK=0: lx keeps no history to compare with", "")
		return
	}
	if f, _ := s.settingsEnv("LX_TRACK", is0); f != nil {
		s.add(id, Skip, "LX_TRACK=0 in the env of "+s.show(f.Path)+": lx keeps no history to compare with", "")
		return
	}
	last := s.history().lastRun
	quiet := last.IsZero() || s.e.Now.Sub(last) > quietFor
	lastText := "no lx run recorded yet"
	if !last.IsZero() {
		lastText = "last lx run " + fmtAgo(s.e.Now.Sub(last))
	}
	if !quiet {
		s.add(id, OK, lastText, "")
		return
	}
	if s.e.ConfigDir == "" || !recentFile(filepath.Join(s.e.ConfigDir, "projects"), s.e.Now.Add(-activeWithin)) {
		s.add(id, OK, lastText+"; no Claude Code session in the last day", "")
		return
	}
	msg := "Claude Code ran today but lx recorded nothing for 3 days: is the hook running (another config dir)?"
	if last.IsZero() {
		msg = "Claude Code ran today but lx has never recorded a run: is the hook running (another config dir)?"
	}
	s.add(id, Warn, msg, "restart Claude Code after lx init, and check that /hooks lists "+s.hooks[0].entry.Command)
}

func recentFile(dir string, since time.Time) bool {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	found := false
	visited := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if visited++; visited > maxWalkedDirs {
			return fs.SkipAll
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().After(since) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}
