package agentctx

import (
	"bytes"
	"strconv"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/lazyre"
)

var receiptRe = lazyre.New(`\[lx: [^\]"\\]{0,400}?(?:full output|output so far): [^\s"\]\\]+ show (\d+)[^\]"\\]{0,300}\]`)

var (
	receiptMark = []byte("[lx: ")
	persisted   = []byte("persisted-output")
	cutMark     = []byte(" characters truncated] ...")
)

type outcome struct {
	ids     []int
	failed  bool
	partial bool
	uuid    string
}

type claudeParser struct {
	s          *Snapshot
	sidechains bool
	probe      bool
	results    map[string]outcome
	tools      map[string]bool
	files      map[string]bool
	msg        string
	turn       int
	started    bool
	compacted  bool
	preserved  map[string]bool
	usage      bool
	prompt     bool
}

type centry struct {
	typ, subtype, uuid, ts   string
	sidechain, meta, summary bool
	message, compact         []byte
	lastPrompt, aiTitle      []byte
}

func (e *centry) set(k, v []byte) bool {
	switch string(k) {
	case "type":
		e.typ = str(v)
	case "subtype":
		e.subtype = str(v)
	case "uuid":
		e.uuid = str(v)
	case "timestamp":
		e.ts = str(v)
	case "isSidechain":
		e.sidechain = string(v) == "true"
	case "isMeta":
		e.meta = string(v) == "true"
	case "isCompactSummary":
		e.summary = string(v) == "true"
	case "message":
		e.message = v
	case "compactMetadata":
		e.compact = v
	case "lastPrompt":
		e.lastPrompt = v
	case "aiTitle":
		e.aiTitle = v
	}
	return true
}

func (e *centry) skip(k []byte) bool { return e.message != nil && string(k) == "toolUseResult" }

func parseClaude(buf []byte, s *Snapshot, sidechains, probe bool) {
	p := &claudeParser{s: s, sidechains: sidechains, probe: probe, results: map[string]outcome{}, tools: map[string]bool{}, files: map[string]bool{}}
	eachLineReverse(buf, func(line []byte) bool {
		p.line(line)
		return !p.done()
	})
	if p.started {
		s.Turns = p.turn + 1
	}
}

func (p *claudeParser) done() bool {
	s := p.s
	if p.probe && p.turn > 0 {
		return true
	}
	return p.usage && p.prompt && p.compacted && s.Title != "" &&
		len(s.Assistant) >= maxTexts && len(s.Files) >= maxFiles && len(s.Runs) >= maxRuns
}

func (p *claudeParser) line(line []byte) {
	var e centry
	if !fieldsUntil(line, e.skip, e.set) {
		p.s.BadLines++
		return
	}
	if e.sidechain && !p.sidechains {
		return
	}
	switch e.typ {
	case "assistant":
		p.assistant(&e)
	case "user":
		p.user(&e)
	case "last-prompt":
		if !p.prompt {
			p.setPrompt(str(e.lastPrompt))
		}
	case "ai-title":
		if p.s.Title == "" {
			p.s.Title = strings.TrimSpace(str(e.aiTitle))
		}
	case "system":
		if e.subtype == "compact_boundary" {
			p.boundary(&e)
		}
	}
}

func (p *claudeParser) assistant(e *centry) {
	var id, model string
	var usage, content []byte
	ok := fields(e.message, func(k, v []byte) bool {
		switch string(k) {
		case "id":
			id = str(v)
		case "model":
			model = str(v)
		case "usage":
			usage = v
		case "content":
			content = v
		}
		return true
	})
	if !ok {
		p.s.BadLines++
		return
	}
	if id == "" {
		id = e.uuid
	}
	if model == "<synthetic>" {
		return
	}
	if p.started && id != p.msg {
		p.turn++
	}
	p.started, p.msg = true, id
	if model != "" {
		if p.s.Model == "" {
			p.s.Model = model
		}
		if u := usedTokens(usage); u > 0 {
			if !p.usage {
				p.s.Pressure.Used, p.usage = u, true
			}
			p.s.evidence = max(p.s.evidence, u)
		}
	}
	var blocks [][]byte
	elems(content, func(v []byte) { blocks = append(blocks, v) })
	for i := len(blocks) - 1; i >= 0; i-- {
		p.block(blocks[i], e.uuid)
	}
}

func usedTokens(usage []byte) int {
	n := 0
	fields(usage, func(k, v []byte) bool {
		switch string(k) {
		case "input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens":
			if x := num(v); x > 0 && x < 1e9 {
				n += x
			}
		}
		return true
	})
	return n
}

func (p *claudeParser) block(b []byte, uuid string) {
	var typ, id, name string
	var text, input []byte
	if !fields(b, func(k, v []byte) bool {
		switch string(k) {
		case "type":
			typ = str(v)
		case "id":
			id = str(v)
		case "name":
			name = str(v)
		case "text":
			text = v
		case "input":
			input = v
		}
		return true
	}) {
		return
	}
	switch typ {
	case "text":
		if len(p.s.Assistant) < maxTexts {
			if t := strings.TrimSpace(str(text)); t != "" {
				p.s.Assistant = append(p.s.Assistant, clipTail(t))
				p.s.textAge = append(p.s.textAge, p.turn)
			}
		}
	case "tool_use":
		if id != "" {
			if p.tools[id] {
				return
			}
			p.tools[id] = true
		}
		p.toolUse(id, name, input, uuid)
	}
}

func (p *claudeParser) toolUse(id, name string, input []byte, uuid string) {
	switch name {
	case "Bash":
		if p.turn > 0 && len(p.s.Runs) >= maxRuns {
			return
		}
		cmd := strField(input, "command")
		if strings.TrimSpace(cmd) == "" {
			return
		}
		o, done := p.results[id]
		pending := !done && p.turn == 0
		if pending {
			p.s.pending = append(p.s.pending, cmd)
		}
		if len(p.s.Runs) >= maxRuns {
			return
		}
		kept := !p.compacted || p.preserved[uuid] && p.preserved[o.uuid]
		r := Run{Command: cmd, TurnsAgo: p.turn, InContext: kept && !o.partial, Failed: o.failed, Pending: pending}
		if len(o.ids) > 0 {
			r.LxID = o.ids[len(o.ids)-1]
		}
		p.s.Runs = append(p.s.Runs, r)
	case "Read", "Edit", "Write", "MultiEdit", "NotebookEdit":
		path := strField(input, "file_path")
		if path == "" {
			path = strField(input, "notebook_path")
		}
		p.file(path, strings.ToLower(name))
	}
}

func (p *claudeParser) file(path, op string) {
	if path == "" || p.files[path] || len(p.s.Files) >= maxFiles {
		return
	}
	p.files[path] = true
	p.s.Files = append(p.s.Files, File{Path: path, Op: op, TurnsAgo: p.turn})
}

func (p *claudeParser) user(e *centry) {
	content := field(e.message, "content")
	if len(content) == 0 {
		return
	}
	if content[0] == '"' {
		if !e.meta && !e.summary {
			p.setPrompt(str(content))
		}
		return
	}
	var texts []string
	elems(content, func(b []byte) {
		var typ, id string
		var body, text []byte
		failed := false
		fields(b, func(k, v []byte) bool {
			switch string(k) {
			case "type":
				typ = str(v)
			case "tool_use_id":
				id = str(v)
			case "content":
				body = v
			case "text":
				text = v
			case "is_error":
				failed = string(v) == "true"
			}
			return true
		})
		switch typ {
		case "tool_result":
			if id != "" {
				p.results[id] = outcome{ids: receipts(body, false), failed: failed, uuid: e.uuid,
					partial: bytes.Contains(body[:min(len(body), 64)], persisted) || bytes.Contains(body, cutMark)}
			}
		case "text":
			if !p.prompt {
				texts = append(texts, str(text))
			}
		}
	})
	if !e.meta && !e.summary {
		for i := len(texts) - 1; i >= 0; i-- {
			p.setPrompt(texts[i])
		}
	}
}

func (p *claudeParser) setPrompt(t string) {
	if p.prompt {
		return
	}
	t = strings.TrimSpace(t)
	if t == "" || t[0] == '<' || strings.HasPrefix(t, "[Request interrupted") {
		return
	}
	p.s.Prompt, p.prompt = clipHead(t), true
}

func (p *claudeParser) boundary(e *centry) {
	p.s.Compactions++
	var uuids, all []string
	fields(e.compact, func(k, v []byte) bool {
		switch string(k) {
		case "preTokens":
			if n := num(v); n < 1e9 {
				p.s.evidence = max(p.s.evidence, n)
			}
		case "postTokens":
			if n := num(v); !p.usage && n > 0 && n < 1e9 {
				p.s.Pressure.Used = n
			}
		case "preservedMessages":
			fields(v, func(k, v []byte) bool {
				switch string(k) {
				case "uuids":
					elems(v, func(u []byte) { uuids = append(uuids, str(u)) })
				case "allUuids":
					elems(v, func(u []byte) { all = append(all, str(u)) })
				}
				return true
			})
		}
		return true
	})
	if uuids == nil {
		uuids = all
	}
	p.usage = true
	if p.compacted {
		return
	}
	p.compacted = true
	if t, err := time.Parse(time.RFC3339Nano, e.ts); err == nil {
		p.s.CompactedAt = t
	}
	p.preserved = make(map[string]bool, len(uuids))
	for _, u := range uuids {
		if u != "" {
			p.preserved[u] = true
		}
	}
}

const receiptScan = 8 << 10

func receipts(raw []byte, nested bool) []int {
	if len(raw) <= receiptScan {
		return receiptIDs(raw, 0, len(raw), nested)
	}
	ids := receiptIDs(raw, 0, 2048, nested)
	return append(ids, receiptIDs(raw, len(raw)-receiptScan, len(raw), nested)...)
}

func receiptIDs(raw []byte, lo, hi int, nested bool) []int {
	w := raw[lo:hi]
	if !bytes.Contains(w, receiptMark) {
		return nil
	}
	var ids []int
	for _, m := range receiptRe.Get().FindAllSubmatchIndex(w, -1) {
		if !startsLine(raw, lo+m[0], nested) || !endsLine(raw, lo+m[1], nested) {
			continue
		}
		if n, err := strconv.Atoi(string(w[m[2]:m[3]])); err == nil && n > 0 {
			ids = append(ids, n)
		}
	}
	return ids
}

// raw is JSON-escaped (twice when nested): backslash counts tell real line breaks from escaped ones.
func startsLine(raw []byte, i int, nested bool) bool {
	if i < 1 {
		return false
	}
	c := 0
	for k := i - 2; k >= 0 && raw[k] == '\\'; k-- {
		c++
	}
	switch raw[i-1] {
	case 'n', 'r':
		return c%2 == 1 || nested && c%4 == 2
	case '"':
		return c%2 == 0 || nested && c%4 == 1
	}
	return false
}

func endsLine(raw []byte, j int, nested bool) bool {
	c := 0
	for j+c < len(raw) && raw[j+c] == '\\' {
		c++
	}
	if j+c >= len(raw) {
		return false
	}
	switch raw[j+c] {
	case 'n', 'r':
		return c == 1 || nested && c == 2
	case '"':
		return c == 0 || nested && c == 1
	}
	return false
}
