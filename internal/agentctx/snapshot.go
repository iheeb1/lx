package agentctx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/engine"
)

const (
	DefaultTail = 1 << 20
	probeTail   = 64 << 10

	maxTexts   = 3
	maxFiles   = 20
	maxRuns    = 30
	maxText    = 16 << 10
	maxProbes  = 16
	probeAge   = 5 * time.Minute
	staleAfter = time.Hour
	staleDir   = 24 * time.Hour
	maxSubDirs = 256
	maxEntries = 1024
)

type Snapshot struct {
	Agent       string
	SessionID   string
	Subagent    string
	Caller      bool
	Path        string
	Model       string
	Pressure    engine.Pressure
	WindowFrom  string
	Compactions int
	CompactedAt time.Time
	Title       string
	Prompt      string   `json:"-"`
	Assistant   []string `json:"-"`
	Files       []File
	Runs        []Run
	Turns       int
	Bytes       int
	BadLines    int

	textAge  []int
	pending  []string
	evidence int
	window   int
}

type File struct {
	Path     string
	Op       string
	TurnsAgo int
}

type Run struct {
	Command   string `json:"-"`
	TurnsAgo  int
	LxID      int
	InContext bool
	Failed    bool
	Pending   bool
}

func Current(env func(string) string, cwd string, argv []string) *Snapshot {
	src, ok := Find(env, cwd)
	if !ok {
		return nil
	}
	src.Argv = argv
	s, err := Load(src, DefaultTail)
	if err != nil || !s.Caller && time.Since(modTime(s.Path)) > staleAfter {
		return nil
	}
	return s
}

func Load(src Source, maxBytes int) (s *Snapshot, err error) {
	defer func() {
		if r := recover(); r != nil {
			s, err = nil, fmt.Errorf("agentctx: %v", r)
		}
	}()
	if maxBytes <= 0 {
		maxBytes = DefaultTail
	}
	switch src.Agent {
	case ClaudeCode:
		if s, err = loadCaller(src, maxBytes); err != nil {
			return nil, err
		}
	case Codex:
		buf, err := readTail(src.Path, maxBytes)
		if err != nil {
			return nil, err
		}
		s = &Snapshot{Path: src.Path, Bytes: len(buf)}
		parseCodex(buf, s)
		s.Caller = !src.Nested || s.issued(src.Argv)
	default:
		return nil, errors.New("agentctx: unknown agent " + src.Agent)
	}
	if !s.Caller {
		for i := range s.Runs {
			s.Runs[i].InContext = false
		}
	}
	s.Agent, s.SessionID = src.Agent, src.SessionID
	s.setWindow(src.Window)
	return s, nil
}

func loadClaude(path string, maxBytes int, sidechains, probe bool) (*Snapshot, error) {
	buf, err := readTail(path, maxBytes)
	if err != nil {
		return nil, err
	}
	s := &Snapshot{Path: path, Bytes: len(buf)}
	parseClaude(buf, s, sidechains, probe)
	return s, nil
}

func readTail(path string, max int) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("agentctx: " + path + " is not a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	size := st.Size()
	start := max64(size-int64(max), 0)
	off := max64(start-1, 0)
	buf := make([]byte, size-off)
	n, _ := f.ReadAt(buf, off)
	buf = buf[:n]
	if start > 0 {
		i := bytes.IndexByte(buf, '\n')
		if i < 0 {
			return nil, nil
		}
		buf = buf[i+1:]
	}
	return buf, nil
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (s *Snapshot) issued(argv []string) bool {
	for _, c := range s.pending {
		if commandMatches(c, argv) {
			return true
		}
	}
	return false
}

func commandMatches(cmd string, argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	words := shellWords(cmd)
	name := filepath.Base(argv[0])
	for i := 0; i+len(argv) <= len(words); i++ {
		if filepath.Base(words[i]) != name {
			continue
		}
		k := 1
		for k < len(argv) && words[i+k] == argv[k] {
			k++
		}
		if k == len(argv) {
			return true
		}
	}
	return false
}

func shellWords(cmd string) []string {
	var out []string
	var b strings.Builder
	word := false
	end := func() {
		if word {
			out = append(out, b.String())
			b.Reset()
			word = false
		}
	}
	for i := 0; i < len(cmd); i++ {
		switch c := cmd[i]; c {
		case ' ', '\t', '\n', ';', '&', '|', '(', ')', '<', '>':
			end()
		case '\\':
			if i++; i < len(cmd) && cmd[i] != '\n' {
				b.WriteByte(cmd[i])
				word = true
			}
		case '\'':
			j := strings.IndexByte(cmd[i+1:], '\'')
			if j < 0 {
				j = len(cmd) - i - 1
			}
			b.WriteString(cmd[i+1 : i+1+j])
			i += j + 1
			word = true
		case '"':
			for i++; i < len(cmd) && cmd[i] != '"'; i++ {
				if cmd[i] == '\\' && i+1 < len(cmd) && strings.IndexByte("\"\\$`", cmd[i+1]) >= 0 {
					i++
				}
				b.WriteByte(cmd[i])
			}
			word = true
		default:
			b.WriteByte(c)
			word = true
		}
	}
	end()
	return out
}

type candidate struct {
	path string
	mod  time.Time
}

func loadCaller(src Source, maxBytes int) (*Snapshot, error) {
	main, err := loadClaude(src.Path, probeTail, false, true)
	if err != nil {
		return nil, err
	}
	var hits []string
	if main.issued(src.Argv) {
		hits = append(hits, src.Path)
	}
	subs, more := subagents(src.Path)
	for _, p := range subs {
		if probe, err := loadClaude(p, probeTail, true, true); err == nil && probe.issued(src.Argv) {
			hits = append(hits, p)
		}
	}
	path := src.Path
	if len(hits) > 0 {
		path = hits[0]
	}
	sub := path != src.Path
	s, err := loadClaude(path, maxBytes, sub, false)
	if err != nil {
		return nil, err
	}
	if sub {
		s.Subagent = strings.TrimPrefix(strings.TrimSuffix(filepath.Base(path), ".jsonl"), "agent-")
	}
	s.Caller = len(hits) == 1 && !more
	return s, nil
}

func subagents(main string) (paths []string, more bool) {
	now := time.Now()
	var files []candidate
	budget := maxSubDirs
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if budget <= 0 {
			return
		}
		var dirs []candidate
		for _, e := range readDirN(dir, maxEntries) {
			name, sub := e.Name(), e.IsDir()
			if !sub && !(strings.HasPrefix(name, "agent-") && strings.HasSuffix(name, ".jsonl") && e.Type().IsRegular()) {
				continue
			}
			if budget--; budget < 0 {
				more = true
				break
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			c := candidate{filepath.Join(dir, name), info.ModTime()}
			switch age := now.Sub(c.mod); {
			case sub && depth < 2 && age < staleDir:
				dirs = append(dirs, c)
			case !sub && age < probeAge:
				files = append(files, c)
			}
		}
		newestFirst(dirs)
		for _, d := range dirs {
			if budget <= 0 {
				more = true
				return
			}
			walk(d.path, depth+1)
		}
	}
	walk(filepath.Join(strings.TrimSuffix(main, ".jsonl"), "subagents"), 0)
	newestFirst(files)
	if len(files) > maxProbes {
		files, more = files[:maxProbes], true
	}
	for _, f := range files {
		paths = append(paths, f.path)
	}
	return paths, more
}

func newestFirst(cs []candidate) {
	sort.Slice(cs, func(i, j int) bool { return cs[i].mod.After(cs[j].mod) })
}

var windows = []struct {
	prefix string
	tokens int
}{
	{"claude-fable-5", 1000000},
	{"claude-mythos-5", 1000000},
	{"claude-opus-5", 1000000},
	{"claude-sonnet-5", 1000000},
	{"claude-opus-4-8", 1000000},
	{"claude-opus-4-7", 1000000},
	{"claude-opus-4-6", 1000000},
	{"claude-sonnet-4-6", 1000000},
	{"claude-haiku-4-5", 200000},
	{"claude-", 200000},
}

const (
	defaultWindow = 200000
	largeWindow   = 1000000
)

func modelWindow(model string) (int, bool) {
	m := strings.ToLower(model)
	if i := strings.Index(m, "claude-"); i > 0 {
		m = m[i:]
	}
	if strings.Contains(m, "[1m]") || strings.HasSuffix(m, "-1m") {
		return largeWindow, true
	}
	for _, w := range windows {
		if strings.HasPrefix(m, w.prefix) {
			return w.tokens, true
		}
	}
	return defaultWindow, false
}

func (s *Snapshot) setWindow(override int) {
	switch {
	case override > 0:
		s.Pressure.Window, s.WindowFrom = override, "LX_CONTEXT_WINDOW"
		return
	case s.window > 0:
		s.Pressure.Window, s.WindowFrom = s.window, "transcript"
	default:
		w, known := modelWindow(s.Model)
		s.Pressure.Window, s.WindowFrom = w, "model"
		if !known {
			s.WindowFrom = "default"
		}
	}
	if seen := max(s.evidence, s.Pressure.Used); seen > s.Pressure.Window && s.Pressure.Window < largeWindow {
		s.Pressure.Window = largeWindow
		if seen > largeWindow {
			s.Pressure.Window = seen
		}
		s.WindowFrom = "usage"
	}
}

func clipTail(s string) string {
	if len(s) <= maxText {
		return s
	}
	s = s[len(s)-maxText:]
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

func clipHead(s string) string {
	if len(s) <= maxText {
		return s
	}
	s = s[:maxText]
	for i := 0; i < 3; i++ {
		if r, n := utf8.DecodeLastRuneInString(s); r != utf8.RuneError || n != 1 {
			break
		}
		s = s[:len(s)-1]
	}
	return s
}
