// Package agentctx reads the tail of the calling agent's session transcript.
package agentctx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	ClaudeCode = "claude-code"
	Codex      = "codex"
)

type Source struct {
	Agent     string
	SessionID string
	Path      string
	Window    int
	Argv      []string
	Nested    bool
}

var (
	ErrDisabled = errors.New("context awareness is off (LX_CONTEXT=0)")
	ErrNoAgent  = errors.New("not running under a coding agent (neither CLAUDE_CODE_SESSION_ID nor CODEX_THREAD_ID is set)")
	ErrNoID     = errors.New("CLAUDECODE=1 but CLAUDE_CODE_SESSION_ID is not set, so the session transcript can't be located")
)

type NotFoundError struct {
	Agent   string
	Session string
	Dir     string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("no transcript for %s session %s under %s", e.Agent, e.Session, e.Dir)
}

const (
	maxProjects = 512
	maxDayDirs  = 45
)

func Find(env func(string) string, cwd string) (Source, bool) {
	s, err := Locate(env, cwd)
	return s, err == nil
}

func Locate(env func(string) string, cwd string) (Source, error) {
	if Disabled(env) {
		return Source{}, ErrDisabled
	}
	var found []Source
	var first error
	try := func(s Source, err error) {
		if err == nil {
			found = append(found, s)
		} else if first == nil {
			first = err
		}
	}
	claudeID := strings.TrimSpace(env("CLAUDE_CODE_SESSION_ID"))
	codexID := strings.TrimSpace(env("CODEX_THREAD_ID"))
	if claudeID != "" {
		try(locateClaude(env, cwd, claudeID))
	}
	if codexID != "" {
		try(locateCodex(env, codexID))
	}
	switch {
	case len(found) == 0 && first != nil:
		return Source{}, first
	case len(found) == 0 && env("CLAUDECODE") == "1":
		return Source{}, ErrNoID
	case len(found) == 0:
		return Source{}, ErrNoAgent
	}
	src := found[0]
	if claudeID != "" && codexID != "" {
		if len(found) == 2 && modTime(found[1].Path).After(modTime(src.Path)) {
			src = found[1]
		}
		src.Nested = true
	}
	src.Window = parseWindow(env("LX_CONTEXT_WINDOW"))
	return src, nil
}

func locateClaude(env func(string) string, cwd, id string) (Source, error) {
	if !validID(id) {
		return Source{}, errors.New("CLAUDE_CODE_SESSION_ID is not a session id")
	}
	projects := filepath.Join(claudeDir(env), "projects")
	p := findClaude(projects, cwd, id)
	if p == "" {
		return Source{}, &NotFoundError{ClaudeCode, id, projects}
	}
	return Source{Agent: ClaudeCode, SessionID: id, Path: p}, nil
}

func locateCodex(env func(string) string, id string) (Source, error) {
	if !validID(id) {
		return Source{}, errors.New("CODEX_THREAD_ID is not a session id")
	}
	sessions := filepath.Join(codexDir(env), "sessions")
	p := findCodex(sessions, id)
	if p == "" {
		return Source{}, &NotFoundError{Codex, id, sessions}
	}
	return Source{Agent: Codex, SessionID: id, Path: p}, nil
}

func modTime(p string) time.Time {
	st, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

func Disabled(env func(string) string) bool {
	switch strings.ToLower(strings.TrimSpace(env("LX_CONTEXT"))) {
	case "0", "off", "false", "no":
		return true
	}
	return false
}

func validID(id string) bool {
	if len(id) > 128 || strings.Trim(id, "-_") == "" {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func homeDir(env func(string) string) string {
	if h := env("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

func claudeDir(env func(string) string) string {
	if d := env("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(homeDir(env), ".claude")
}

func codexDir(env func(string) string) string {
	if d := env("CODEX_HOME"); d != "" {
		return d
	}
	return filepath.Join(homeDir(env), ".codex")
}

// Claude Code replaces each non-alphanumeric UTF-16 unit with '-'.
func Slug(dir string) string {
	var b strings.Builder
	b.Grow(len(dir))
	for _, c := range dir {
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9':
			b.WriteRune(c)
		case c > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

func findClaude(projects, cwd, id string) string {
	name := id + ".jsonl"
	if cwd != "" {
		d := filepath.Clean(cwd)
		for {
			if p := filepath.Join(projects, Slug(d), name); isFile(p) {
				return p
			}
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
			d = parent
		}
	}
	best, newest := "", time.Time{}
	for _, e := range readDirN(projects, maxProjects) {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(projects, e.Name(), name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && st.ModTime().After(newest) {
			best, newest = p, st.ModTime()
		}
	}
	return best
}

func findCodex(sessions, id string) string {
	suffix := "-" + id + ".jsonl"
	match := func(dir string) string {
		for _, e := range readDirN(dir, 4096) {
			if n := e.Name(); strings.HasPrefix(n, "rollout-") && strings.HasSuffix(n, suffix) && e.Type().IsRegular() {
				return filepath.Join(dir, n)
			}
		}
		return ""
	}
	if t, ok := uuidTime(id); ok {
		for _, d := range []time.Duration{0, -24 * time.Hour, 24 * time.Hour} {
			if p := match(dayDir(sessions, t.Add(d))); p != "" {
				return p
			}
		}
	}
	checked := 0
	for _, y := range sortedDirs(sessions) {
		for _, m := range sortedDirs(y) {
			for _, d := range sortedDirs(m) {
				if p := match(d); p != "" {
					return p
				}
				if checked++; checked >= maxDayDirs {
					return ""
				}
			}
		}
	}
	return ""
}

func dayDir(root string, t time.Time) string {
	t = t.Local()
	return filepath.Join(root, fmt.Sprintf("%04d", t.Year()), fmt.Sprintf("%02d", int(t.Month())), fmt.Sprintf("%02d", t.Day()))
}

func uuidTime(id string) (time.Time, bool) {
	h := strings.ReplaceAll(id, "-", "")
	if len(h) != 32 || h[12] != '7' {
		return time.Time{}, false
	}
	ms, err := strconv.ParseInt(h[:12], 16, 64)
	if err != nil || ms <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(ms), true
}

func sortedDirs(dir string) []string {
	var out []string
	for _, e := range readDirN(dir, 400) {
		if e.IsDir() {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

func readDirN(dir string, n int) []fs.DirEntry {
	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer f.Close()
	es, _ := f.ReadDir(n)
	return es
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func parseWindow(s string) int {
	s = strings.ToLower(strings.TrimSpace(s))
	mult := 1
	switch {
	case strings.HasSuffix(s, "k"):
		s, mult = s[:len(s)-1], 1000
	case strings.HasSuffix(s, "m"):
		s, mult = s[:len(s)-1], 1000000
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64)
	if err != nil || f <= 0 || f*float64(mult) > 1e10 {
		return 0
	}
	return int(f * float64(mult))
}
