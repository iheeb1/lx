package agentctx

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/iheeb1/lx/internal/lazyre"
)

var (
	jsExecRe = lazyre.New("exec_command\\(\\s*\\{\\s*[\"']?cmd[\"']?\\s*:\\s*(\"(?:[^\"\\\\\\n]|\\\\.)*\"|'(?:[^'\\\\\\n]|\\\\.)*'|`(?:[^`\\\\]|\\\\.)*`)")
	exitRe   = lazyre.New(`(?:Process exited with code |exit_code\\?"?:\s*)(\d+)`)
)

type codexParser struct {
	s         *Snapshot
	results   map[string]outcome
	files     map[string]bool
	responses int
	compacted bool
	usage     bool
	prompt    bool
}

var (
	codexWarnCut = []byte("Warning: truncated output")
	codexCut     = []byte(" tokens truncated")
)

func parseCodex(buf []byte, s *Snapshot) {
	p := &codexParser{s: s, results: map[string]outcome{}, files: map[string]bool{}}
	eachLineReverse(buf, func(line []byte) bool {
		p.line(line)
		return true
	})
	p.normalize()
}

func (p *codexParser) line(line []byte) {
	var typ, ts string
	var payload []byte
	if !fields(line, func(k, v []byte) bool {
		switch string(k) {
		case "type":
			typ = str(v)
		case "timestamp":
			ts = str(v)
		case "payload":
			payload = v
		}
		return true
	}) {
		p.s.BadLines++
		return
	}
	switch typ {
	case "event_msg":
		p.event(payload)
	case "response_item":
		p.item(payload)
	case "turn_context":
		if p.s.Model == "" {
			p.s.Model = strField(payload, "model")
		}
	case "compacted":
		p.s.Compactions++
		p.usage = true
		if !p.compacted {
			p.compacted = true
			if t, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				p.s.CompactedAt = t
			}
		}
	}
}

func (p *codexParser) event(b []byte) {
	switch strField(b, "type") {
	case "token_count":
		p.responses++
		info := field(b, "info")
		if p.usage || len(info) == 0 || info[0] != '{' {
			return
		}
		used := num(field(field(info, "last_token_usage"), "input_tokens"))
		if used > 0 {
			p.s.Pressure.Used, p.usage = used, true
			p.s.evidence = max(p.s.evidence, used)
			p.s.window = num(field(info, "model_context_window"))
		}
	case "user_message":
		if !p.prompt {
			if t := strings.TrimSpace(strField(b, "message")); t != "" {
				p.s.Prompt, p.prompt = clipHead(t), true
			}
		}
	case "patch_apply_end":
		fields(field(b, "changes"), func(k, v []byte) bool {
			op := "edit"
			switch strField(v, "type") {
			case "add":
				op = "write"
			case "delete":
				op = "delete"
			}
			p.file(key(k), op)
			return true
		})
	}
}

func (p *codexParser) item(b []byte) {
	var typ, name, role, callID string
	var args, input, output, content, action []byte
	fields(b, func(k, v []byte) bool {
		switch string(k) {
		case "type":
			typ = str(v)
		case "name":
			name = str(v)
		case "role":
			role = str(v)
		case "call_id":
			callID = str(v)
		case "arguments":
			args = v
		case "input":
			input = v
		case "output":
			output = v
		case "content":
			content = v
		case "action":
			action = v
		}
		return true
	})
	switch typ {
	case "message":
		if role != "assistant" || len(p.s.Assistant) >= maxTexts {
			return
		}
		var parts []string
		elems(content, func(c []byte) {
			if t := strField(c, "text"); t != "" {
				parts = append(parts, t)
			}
		})
		if t := strings.TrimSpace(strings.Join(parts, "\n")); t != "" {
			p.s.Assistant = append(p.s.Assistant, clipTail(t))
			p.s.textAge = append(p.s.textAge, p.responses)
		}
	case "function_call":
		switch name {
		case "exec_command", "shell", "shell_command", "container.exec":
			p.run(callID, shellCommand([]byte(str(args))))
		}
	case "local_shell_call":
		p.run(callID, shellCommand(action))
	case "custom_tool_call":
		if name == "exec" {
			p.run(callID, jsCommands(str(input))...)
		}
	case "function_call_output", "custom_tool_call_output", "local_shell_call_output":
		if callID != "" {
			o := outcome{ids: receipts(output, typ == "custom_tool_call_output" || len(output) > 0 && output[0] == '['),
				partial: bytes.Contains(output, codexWarnCut) || bytes.Contains(output, codexCut)}
			if m := exitRe.Get().FindAllSubmatch(tail(output, receiptScan), -1); len(m) == 1 {
				o.failed = string(m[0][1]) != "0"
			}
			p.results[callID] = o
		}
	}
}

func tail(b []byte, n int) []byte {
	if len(b) > n {
		return b[len(b)-n:]
	}
	return b
}

func shellCommand(b []byte) string {
	if c := strField(b, "cmd"); c != "" {
		return c
	}
	v := field(b, "command")
	if len(v) > 0 && v[0] == '"' {
		return str(v)
	}
	var argv []string
	elems(v, func(a []byte) { argv = append(argv, str(a)) })
	if n := len(argv); n >= 3 && (argv[n-2] == "-lc" || argv[n-2] == "-c") {
		return argv[n-1]
	}
	return strings.Join(argv, " ")
}

func jsCommands(src string) []string {
	var out []string
	for _, m := range jsExecRe.FindAllStringSubmatch(src, -1) {
		if c := jsString(m[1]); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func jsString(lit string) string {
	if len(lit) < 2 {
		return ""
	}
	body := lit[1 : len(lit)-1]
	switch lit[0] {
	case '`':
		return body
	case '\'':
		body = strings.NewReplacer(`\'`, `'`, `"`, `\"`).Replace(body)
	default:
		body = strings.ReplaceAll(body, `\'`, `'`)
	}
	var s string
	if json.Unmarshal([]byte(`"`+body+`"`), &s) != nil {
		return ""
	}
	return s
}

func (p *codexParser) run(callID string, cmds ...string) {
	o, done := p.results[callID]
	for i := len(cmds) - 1; i >= 0; i-- {
		cmd := cmds[i]
		if strings.TrimSpace(cmd) == "" {
			continue
		}
		if len(p.s.Runs) >= maxRuns {
			continue
		}
		r := Run{Command: cmd, TurnsAgo: p.responses, InContext: !p.compacted && !o.partial, Pending: !done, Failed: o.failed && len(cmds) == 1}
		switch {
		case len(o.ids) == len(cmds):
			r.LxID = o.ids[i]
		case i == len(cmds)-1 && len(o.ids) > 0:
			r.LxID = o.ids[len(o.ids)-1]
		}
		p.s.Runs = append(p.s.Runs, r)
	}
}

func (p *codexParser) file(path, op string) {
	if path == "" || p.files[path] || len(p.s.Files) >= maxFiles {
		return
	}
	p.files[path] = true
	p.s.Files = append(p.s.Files, File{Path: path, Op: op, TurnsAgo: p.responses})
}

func (p *codexParser) normalize() {
	s := p.s
	low := -1
	note := func(n int) {
		if low < 0 || n < low {
			low = n
		}
	}
	for _, r := range s.Runs {
		note(r.TurnsAgo)
	}
	for _, f := range s.Files {
		note(f.TurnsAgo)
	}
	for _, a := range s.textAge {
		note(a)
	}
	if low < 0 {
		low = 0
	}
	for i := range s.Runs {
		s.Runs[i].TurnsAgo -= low
		if s.Runs[i].Pending && s.Runs[i].TurnsAgo > 0 {
			s.Runs[i].Pending = false
		}
	}
	for i := range s.Files {
		s.Files[i].TurnsAgo -= low
	}
	for i := range s.textAge {
		s.textAge[i] -= low
	}
	if p.responses > 0 {
		s.Turns = p.responses - low
	}
	s.pending = s.pending[:0]
	for _, r := range s.Runs {
		if r.Pending {
			s.pending = append(s.pending, r.Command)
		}
	}
}
