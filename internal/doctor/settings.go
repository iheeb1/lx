package doctor

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// settingsFile is one Claude Code settings file, read (never written).
type settingsFile struct {
	Path  string
	Label string // "user", "user local", "project", "project local", "managed"
	Scope string // "user", "project" or "managed"
	Local bool   // settings.local.json

	Exists   bool
	ReadErr  error  // exists but could not be read
	ParseErr error  // not a JSON object
	Shape    string // a structural problem with "hooks" (the file is still usable)
	Linked   string // Path, or its directory, when that is a symlink
	Symlink  string // …and where it points
	Dangling bool   // the symlink points at nothing

	Hooks           []hookEntry
	Allow           []string // permissions.allow strings, verbatim
	Ask, Deny       []string
	Env             []envVar // the "env" object, sorted by name
	DisableAllHooks bool
	// AllowManagedHooksOnly (honored in managed settings only): Claude Code
	// runs no user or project hooks.
	AllowManagedHooksOnly bool
}

type envVar struct{ Name, Value string }

// hookEntry is one hooks.PreToolUse[*].hooks[*] object.
type hookEntry struct {
	File       *settingsFile
	Matcher    string
	HasMatcher bool
	Type       string
	Command    string
}

// coversBash reports whether the group's matcher selects the Bash tool.
// Claude Code matches tool names exactly or as a regular expression; an
// empty or missing matcher and "*" match every tool.
func (h hookEntry) coversBash() bool {
	m := h.Matcher
	if !h.HasMatcher || m == "" || m == "*" || m == "Bash" {
		return true
	}
	return matchesBash(m)
}

// settingsFiles lists the files Claude Code (and lx's hook) read, in the
// same set as hook.LoadClaudeRules, deduplicated: when the project walk
// reaches the user's own config directory, it is read once, as "user".
func (e *Env) settingsFiles() []*settingsFile {
	var out []*settingsFile
	add := func(path, label, scope string, local bool) {
		if path == "" {
			return
		}
		path = filepath.Clean(path)
		for _, f := range out {
			if f.Path == path || realPath(f.Path) == realPath(path) || sameFile(f.Path, path) {
				return
			}
		}
		out = append(out, &settingsFile{Path: path, Label: label, Scope: scope, Local: local})
	}
	if e.ConfigDir != "" {
		add(filepath.Join(e.ConfigDir, "settings.json"), "user", "user", false)
		add(filepath.Join(e.ConfigDir, "settings.local.json"), "user local", "user", true)
	}
	if e.ProjectDir != "" {
		add(filepath.Join(e.ProjectDir, ".claude", "settings.json"), "project", "project", false)
		add(filepath.Join(e.ProjectDir, ".claude", "settings.local.json"), "project local", "project", true)
	}
	add(e.ManagedPath, "managed", "managed", false)
	for _, f := range out {
		f.load()
	}
	return out
}

// maxSettingsBytes bounds what doctor reads from one settings file.
const maxSettingsBytes = 8 << 20

func (f *settingsFile) load() {
	fi, err := os.Lstat(f.Path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			f.Exists, f.ReadErr = true, err
		}
		return
	}
	f.Exists = true
	if fi.Mode()&fs.ModeSymlink != 0 {
		f.Linked, f.Symlink = f.Path, linkTarget(f.Path)
	} else if dir := filepath.Dir(f.Path); isSymlink(dir) {
		f.Linked, f.Symlink = dir, linkTarget(dir)
	}
	st, err := os.Stat(f.Path)
	if err != nil {
		f.ReadErr = err
		f.Dangling = f.Linked != "" && errors.Is(err, fs.ErrNotExist)
		return
	}
	if !st.Mode().IsRegular() {
		f.ReadErr = errors.New("not a regular file")
		return
	}
	if st.Size() > maxSettingsBytes {
		f.ReadErr = errors.New("larger than 8 MB")
		return
	}
	data, err := os.ReadFile(f.Path)
	if err != nil {
		f.ReadErr = err
		return
	}
	f.parse(data)
}

func (f *settingsFile) parse(data []byte) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(data)) == 0 {
		return // an empty file is an empty object (as for lx init)
	}
	var top map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&top); err != nil {
		f.ParseErr = jsonErr(data, err)
		return
	}
	if top == nil {
		f.ParseErr = errors.New("not a JSON object")
		return
	}
	if _, err := dec.Token(); err == nil {
		f.ParseErr = errors.New("unexpected data after the top-level object")
		return
	}

	var perms struct {
		Allow, Ask, Deny []json.RawMessage
	}
	if raw, ok := top["permissions"]; ok {
		_ = json.Unmarshal(raw, &perms)
	}
	f.Allow = rawStrings(perms.Allow)
	f.Ask = rawStrings(perms.Ask)
	f.Deny = rawStrings(perms.Deny)

	if raw, ok := top["env"]; ok {
		var env map[string]json.RawMessage
		if json.Unmarshal(raw, &env) == nil {
			for k, v := range env {
				var s string
				if json.Unmarshal(v, &s) != nil {
					s = strings.TrimSpace(string(v)) // Claude Code stringifies numbers
				}
				f.Env = append(f.Env, envVar{k, s})
			}
			sort.Slice(f.Env, func(i, j int) bool { return f.Env[i].Name < f.Env[j].Name })
		}
	}
	if raw, ok := top["disableAllHooks"]; ok {
		var b bool
		f.DisableAllHooks = json.Unmarshal(raw, &b) == nil && b
	}
	if raw, ok := top["allowManagedHooksOnly"]; ok {
		var b bool
		f.AllowManagedHooksOnly = json.Unmarshal(raw, &b) == nil && b
	}

	raw, ok := top["hooks"]
	if !ok || string(raw) == "null" {
		return
	}
	var hooks map[string]json.RawMessage
	if json.Unmarshal(raw, &hooks) != nil || hooks == nil {
		f.Shape = `"hooks" is not an object`
		return
	}
	pre, ok := hooks["PreToolUse"]
	if !ok || string(pre) == "null" {
		return
	}
	var groups []json.RawMessage
	if json.Unmarshal(pre, &groups) != nil {
		f.Shape = `"hooks.PreToolUse" is not an array`
		return
	}
	for _, g := range groups {
		var group struct {
			Matcher *string           `json:"matcher"`
			Hooks   []json.RawMessage `json:"hooks"`
		}
		if json.Unmarshal(g, &group) != nil {
			var probe map[string]json.RawMessage
			if json.Unmarshal(g, &probe) == nil {
				// matcher or hooks of the wrong type: try hooks alone
				_ = json.Unmarshal(probe["hooks"], &group.Hooks)
			}
		}
		for _, hr := range group.Hooks {
			var h struct {
				Type    *string `json:"type"`
				Command *string `json:"command"`
			}
			if json.Unmarshal(hr, &h) != nil || h.Command == nil {
				continue
			}
			e := hookEntry{File: f, Command: *h.Command}
			if h.Type != nil {
				e.Type = *h.Type
			}
			if group.Matcher != nil {
				e.Matcher, e.HasMatcher = *group.Matcher, true
			}
			f.Hooks = append(f.Hooks, e)
		}
	}
}

func rawStrings(raws []json.RawMessage) []string {
	var out []string
	for _, r := range raws {
		var s string
		if json.Unmarshal(r, &s) == nil {
			out = append(out, s)
		}
	}
	return out
}

// jsonErr adds a line:column position to a JSON syntax error.
func jsonErr(data []byte, err error) error {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		line, col := position(data, se.Offset)
		return &posError{line: line, col: col, msg: se.Error()}
	}
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		return errors.New("not a JSON object")
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return errors.New("unexpected end of file")
	}
	return err
}

type posError struct {
	line, col int
	msg       string
}

func (p *posError) Error() string {
	return "line " + itoa(p.line) + ", column " + itoa(p.col) + ": " + p.msg
}

func position(data []byte, off int64) (line, col int) {
	if off > int64(len(data)) {
		off = int64(len(data))
	}
	line, col = 1, 1
	for _, b := range data[:off] {
		if b == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, max(col-1, 1)
}

func isSymlink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&fs.ModeSymlink != 0
}

func linkTarget(p string) string {
	if t, err := filepath.EvalSymlinks(p); err == nil {
		return t
	}
	if t, err := os.Readlink(p); err == nil {
		if !filepath.IsAbs(t) {
			t = filepath.Join(filepath.Dir(p), t)
		}
		return t
	}
	return "?"
}

func sameFile(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}
