package discover

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/iheeb1/lx/internal/agentctx"
	"github.com/iheeb1/lx/internal/engine"
)

const (
	sessTexts = 3
	sessFiles = 20
	sessText  = 16 << 10
	sessLine  = 256 << 10
	sessCalm  = 100000
)

var (
	markUsage   = []byte(`"usage"`)
	markUser    = []byte(`"type":"user"`)
	markCompact = []byte(`compact_boundary`)
)

func contextOn() bool { return !agentctx.Disabled(os.Getenv) }

type session struct {
	path       string
	sidechains bool
	window     int
	windowRead bool

	used     int
	turns    int
	msg      string
	texts    []string
	textTurn int
	prompt   string
	files    []agentctx.File

	version  int
	focus    *engine.Focus
	focusFor int
}

type sessionPoint struct {
	s       *session
	version int
	used    int
	texts   []string
	recent  bool
	prompt  string
	files   []agentctx.File
}

type ctxBlock struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Text  string `json:"text"`
	Input struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	} `json:"input"`
}

type ctxLine struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	Sidechain bool   `json:"isSidechain"`
	Meta      bool   `json:"isMeta"`
	Summary   bool   `json:"isCompactSummary"`
	Message   struct {
		ID      string     `json:"id"`
		Model   string     `json:"model"`
		Usage   usage      `json:"usage"`
		Content []ctxBlock `json:"content"`
	} `json:"message"`
	Compact struct {
		PostTokens int `json:"postTokens"`
	} `json:"compactMetadata"`
}

func newSession(path string) *session {
	return &session{path: path, sidechains: strings.Contains(filepath.ToSlash(path), "/subagents/"), focusFor: -1}
}

func (s *session) wants(line []byte) bool {
	return len(line) <= sessLine && (bytes.Contains(line, markUsage) || bytes.Contains(line, markUser) || bytes.Contains(line, markCompact))
}

func (s *session) scan(line []byte) {
	var c ctxLine
	var te *json.UnmarshalTypeError
	if err := json.Unmarshal(line, &c); err != nil && !errors.As(err, &te) {
		return
	}
	if !s.header(c.Type, c.Subtype, c.Sidechain, c.Meta || c.Summary, c.Message.ID, c.Message.Model, c.Message.Usage, c.Compact.PostTokens) {
		return
	}
	if c.Type == "user" && c.Message.Content == nil {
		var m struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &m) == nil {
			s.setPrompt(m.Message.Content)
		}
		return
	}
	for _, b := range c.Message.Content {
		s.block(c.Type, b.Type, b.Name, b.Text, b.Input.FilePath, b.Input.NotebookPath)
	}
}

func (s *session) begin(rec *record) bool {
	if !s.header(rec.Type, rec.Subtype, rec.Sidechain, rec.Meta || rec.Summary, rec.Message.ID, rec.Message.Model, rec.Message.Usage, rec.Compact.PostTokens) {
		return false
	}
	var text string
	if c := rec.Message.Content; rec.Type == "user" && len(c) > 0 && c[0] == '"' && json.Unmarshal(c, &text) == nil {
		s.setPrompt(text)
		return false
	}
	return true
}

func (s *session) header(typ, subtype string, sidechain, meta bool, id, model string, u usage, post int) bool {
	if sidechain && !s.sidechains {
		return false
	}
	switch typ {
	case "assistant":
		if model == "<synthetic>" {
			return false
		}
		if id == "" || id != s.msg {
			s.turns++
			s.msg = id
		}
		if n := u.Input + u.CacheCreation + u.CacheRead; n > 0 && n < 1e9 {
			s.used = n
		}
		return true
	case "user":
		return !meta
	case "system":
		if subtype == "compact_boundary" {
			s.used = post
		}
	}
	return false
}

func (s *session) setPrompt(t string) {
	t = strings.TrimSpace(t)
	if t == "" || t[0] == '<' || strings.HasPrefix(t, "[Request interrupted") {
		return
	}
	if len(t) > sessText {
		t = t[:sessText]
	}
	s.prompt = t
	s.version++
}

func (s *session) recordBlock(typ string, b block) {
	if b.Type == "tool_use" && fileTool(b.Name) {
		var in struct {
			FilePath     string `json:"file_path"`
			NotebookPath string `json:"notebook_path"`
		}
		if json.Unmarshal(b.Input, &in) == nil {
			s.block(typ, b.Type, b.Name, "", in.FilePath, in.NotebookPath)
		}
		return
	}
	s.block(typ, b.Type, b.Name, b.Text, "", "")
}

func fileTool(name string) bool {
	switch name {
	case "Read", "Edit", "Write", "MultiEdit", "NotebookEdit":
		return true
	}
	return false
}

func (s *session) block(typ, kind, name, text, path, notebook string) {
	switch {
	case typ == "assistant" && kind == "text":
		t := strings.TrimSpace(text)
		if t == "" {
			return
		}
		if len(t) > sessText {
			t = t[len(t)-sessText:]
		}
		s.texts = append([]string{t}, s.texts[:min(len(s.texts), sessTexts-1)]...)
		s.textTurn = s.turns
		s.version++
	case typ == "assistant" && kind == "tool_use" && fileTool(name):
		if path == "" {
			path = notebook
		}
		if path == "" {
			return
		}
		files := []agentctx.File{{Path: path, Op: strings.ToLower(name)}}
		for _, f := range s.files {
			if f.Path != path && len(files) < sessFiles {
				files = append(files, f)
			}
		}
		s.files = files
		s.version++
	case typ == "user" && kind == "text":
		s.setPrompt(text)
	}
}

func (s *session) point() *sessionPoint {
	if s == nil {
		return nil
	}
	return &sessionPoint{s: s, version: s.version, used: s.used, texts: s.texts,
		recent: len(s.texts) > 0 && s.turns-s.textTurn <= 1, prompt: s.prompt, files: s.files}
}

func (p *sessionPoint) options(o engine.Options, command string, c *engine.Context) engine.Options {
	if p == nil {
		return o
	}
	o.Focus = p.focus()
	if p.recent && !strings.Contains(command, "LX_MODE=") {
		if m := (&agentctx.Snapshot{Assistant: p.texts[:1]}).InferMode(); m == engine.ModeError || m == engine.ModeVerify && verdict(c) {
			o.Mode = m
		}
	}
	if p.used > sessCalm {
		if w := p.s.contextWindow(); w > 0 {
			o.Pressure = engine.Pressure{Used: p.used, Window: w}
		}
	}
	return o
}

func (p *sessionPoint) focus() *engine.Focus {
	s := p.s
	if s.focusFor == p.version {
		return s.focus
	}
	f := (&agentctx.Snapshot{Assistant: p.texts, Prompt: p.prompt, Files: p.files}).Focus()
	s.focus, s.focusFor = f, p.version
	return f
}

func (s *session) contextWindow() int {
	if !s.windowRead {
		s.windowRead = true
		if snap, err := agentctx.Load(agentctx.Source{Agent: agentctx.ClaudeCode, Path: s.path}, agentctx.DefaultTail); err == nil {
			s.window = snap.Pressure.Window
		}
	}
	return s.window
}

func verdict(c *engine.Context) bool {
	if c.Failed() {
		return true
	}
	f, _ := engine.Resolve(c)
	if g, ok := f.(engine.Guarded); ok && g.GuardsErrors() {
		return true
	}
	_, ok := f.(engine.Identities)
	return ok
}
