package hook

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
)

const MaxHookInput = 1 << 20

const RewriteReason = "lx: condensed output (lx show <id> for full)"

func ClaudeHook(stdin io.Reader, stdout io.Writer, cwd string) error {
	return runHook(claudeHost, stdin, stdout, cwd, HookOptions{})
}

type HookOptions struct {
	ReadOnly bool

	Prefix string
}

func Hook(agent string, stdin io.Reader, stdout io.Writer, cwd string) error {
	return HookWith(agent, stdin, stdout, cwd, HookOptions{})
}

func HookWith(agent string, stdin io.Reader, stdout io.Writer, cwd string, o HookOptions) error {
	h, ok := hosts[agent]
	if !ok {
		return nil
	}
	return runHook(h, stdin, stdout, cwd, o)
}

func Agents() []string { return []string{"claude", "copilot", "cursor", "gemini"} }

type host struct {
	tools []string

	render func(p *payload, o outcome) []byte

	alwaysJSON bool

	parity bool
}

var (
	claudeHost = host{tools: []string{"Bash"}, render: renderClaude(true), parity: true}
	hosts      = map[string]host{
		"claude":  claudeHost,
		"copilot": {tools: []string{"Bash", "bash", "run_in_terminal", "runTerminalCommand"}, render: renderClaude(false)},
		"gemini":  {tools: []string{"run_shell_command"}, render: renderGemini},
		"cursor":  {tools: []string{"Shell", "Bash", "run_terminal_cmd"}, render: renderCursor, alwaysJSON: true},
	}
)

type payload struct {
	input      *object
	command    string
	cwd        string
	mode       string
	background bool
}

type outcome struct {
	rewritten string
	decision  string
	reason    string
	readOnly  bool
}

type evalEnv struct {
	ReadOnly       bool
	Cwd            string
	Root           string
	PermissionMode string
	Background     bool
	BadMode        bool
	Prefix         string
}

func hookDisabled() bool {
	switch strings.ToLower(os.Getenv("LX_HOOK")) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

func runHook(h host, stdin io.Reader, stdout io.Writer, cwd string, o HookOptions) error {
	out := hookResponse(h, stdin, cwd, o)
	if out == nil && h.alwaysJSON {
		out = []byte("{}")
	}
	if out != nil {
		_, _ = stdout.Write(append(out, '\n'))
	}
	return nil
}

func hookResponse(h host, stdin io.Reader, cwd string, opts HookOptions) (out []byte) {
	defer func() {
		if recover() != nil {
			out = nil
		}
	}()
	if hookDisabled() {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(stdin, MaxHookInput+1))
	if err != nil || len(data) > MaxHookInput {
		return nil
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	p, ok := parsePayload(data, h.tools)
	if !ok {
		return nil
	}
	if p.cwd != "" {
		cwd = p.cwd
	}
	env := evalEnv{
		ReadOnly:       opts.ReadOnly && h.parity,
		Cwd:            p.cwd,
		PermissionMode: p.mode,
		Background:     p.background,
		BadMode:        !modeOK(os.Getenv("LX_MODE")),
		Prefix:         opts.Prefix,
	}
	if env.ReadOnly {
		env.Root = os.Getenv("CLAUDE_PROJECT_DIR")
		if env.Root == "" {
			env.Root = p.cwd
		}
	}
	o := evaluate(p.command, env, func() Rules { return LoadClaudeRules(cwd) })
	return h.render(p, o)
}

func parsePayload(data []byte, tools []string) (*payload, bool) {
	top, err := parseObject(data)
	if err != nil {
		return nil, false
	}
	name, _ := top.getString("tool_name")
	known := false
	for _, t := range tools {
		if name == t {
			known = true
			break
		}
	}
	if !known {
		return nil, false
	}
	raw, ok := top.get("tool_input")
	if !ok {
		return nil, false
	}
	input, err := parseObject(raw)
	if err != nil {
		return nil, false
	}
	cmd, ok := input.getString("command")
	if !ok || strings.TrimSpace(cmd) == "" {
		return nil, false
	}
	p := &payload{input: input, command: cmd}
	p.cwd, _ = top.getString("cwd")
	p.mode, _ = top.getString("permission_mode")
	if bg, ok := input.get("run_in_background"); ok {

		switch strings.TrimSpace(string(bg)) {
		case "false", "null", `""`, `"false"`:
		default:
			p.background = true
		}
	}
	return p, true
}

var parityModes = map[string]bool{"": true, "default": true, "acceptEdits": true, "plan": true}

func evaluate(cmd string, env evalEnv, loadRules func() Rules) outcome {
	return evaluateWith(cmd, env, loadRules, resolvePrefix)
}

func evaluateWith(cmd string, env evalEnv, loadRules func() Rules, prefix func(string) string) outcome {
	a := analyze(cmd)
	targets, _ := a.plan()
	if env.Background || env.BadMode {

		targets = nil
	}
	hasLx := false
	for _, s := range a.segs {
		if s.isLx {
			hasLx = true
			break
		}
	}
	if len(targets) == 0 && !hasLx {
		return outcome{}
	}
	rules := loadRules()

	var lxAsk *outcome
	if hasLx {
		for _, s := range a.segs {
			if !s.isLx {
				continue
			}
			for _, text := range lxTexts(s) {
				if rl := firstMatch(rules.Deny, text, true); rl != "" {
					return outcome{decision: VerdictDeny, reason: "lx: `" + text + "` matches deny rule " + rl}
				}
				if lxAsk == nil {
					if rl := firstMatch(rules.Ask, text, true); rl != "" {
						lxAsk = &outcome{decision: VerdictAsk, reason: "lx: `" + text + "` matches ask rule " + rl}
					}
				}
			}
		}
	}
	if len(targets) == 0 {
		if lxAsk != nil {
			return *lxAsk
		}
		return outcome{}
	}

	verdict, rule, subject := rules.decideAnalysis(a)
	if verdict == VerdictDeny {
		return outcome{}
	}
	rewritten := splice(cmd, targets, prefix(env.Prefix))

	for _, rw := range []string{rewritten, splice(cmd, targets, "lx")} {
		if v, _, _ := rules.decide(rw); v == VerdictDeny {
			return outcome{}
		}
	}
	switch verdict {
	case VerdictAsk:
		return outcome{rewritten: rewritten, decision: VerdictAsk,
			reason: RewriteReason + "; `" + subject + "` matches ask rule " + rule}
	case VerdictAllow:
		return outcome{rewritten: rewritten, decision: VerdictAllow, reason: RewriteReason}
	}

	if env.ReadOnly && parityModes[env.PermissionMode] && !rules.guardsLx(rewritten) &&
		!rules.guardsLx(splice(cmd, targets, "lx")) {
		if ok, _ := a.readOnlyParity(env.Cwd, env.Root, rules.Dirs); ok {
			return outcome{rewritten: rewritten, decision: VerdictAllow, readOnly: true,
				reason: RewriteReason + readOnlyReason}
		}
	}
	return outcome{rewritten: rewritten, reason: RewriteReason}
}

func EvaluateClaude(cmd, cwd string, readOnly bool, rules Rules) (rewritten, decision string) {
	env := evalEnv{ReadOnly: readOnly, Cwd: cwd, Root: cwd, PermissionMode: "default", Prefix: ""}
	o := evaluateWith(cmd, env, func() Rules { return rules }, func(string) string { return "lx" })
	return o.rewritten, o.decision
}

func (r Rules) guardsLx(rewritten string) bool {
	v, _, _ := r.decide(rewritten)
	return v == VerdictDeny || v == VerdictAsk
}

func (p *payload) updatedInput(cmd string) []byte {
	in := &object{members: append([]member(nil), p.input.members...)}
	in.set("command", jsonString(cmd))
	return in.compact()
}

func renderClaude(emitAsk bool) func(*payload, outcome) []byte {
	return func(p *payload, o outcome) []byte {
		decision := o.decision
		if decision == VerdictAsk && !emitAsk {

			decision = ""
			if o.rewritten == "" {
				return nil
			}
		}
		if o.rewritten == "" && decision == "" {
			return nil
		}
		out := &object{}
		out.set("hookEventName", jsonString("PreToolUse"))
		if decision != "" {
			out.set("permissionDecision", jsonString(decision))
		}
		reason := o.reason
		if decision == "" || decision == VerdictAllow && !o.readOnly {
			reason = RewriteReason
		}
		out.set("permissionDecisionReason", jsonString(reason))
		if o.rewritten != "" {
			out.set("updatedInput", p.updatedInput(o.rewritten))
		}
		top := &object{}
		top.set("hookSpecificOutput", out.compact())
		return top.compact()
	}
}

func renderGemini(p *payload, o outcome) []byte {
	top := &object{}
	switch {
	case o.decision == VerdictDeny:
		top.set("decision", jsonString("deny"))
		top.set("reason", jsonString(o.reason))
	case o.rewritten != "":
		hs := &object{}
		hs.set("hookEventName", jsonString("BeforeTool"))
		hs.set("tool_input", p.updatedInput(o.rewritten))
		top.set("hookSpecificOutput", hs.compact())
	default:
		return nil
	}
	return top.compact()
}

func renderCursor(p *payload, o outcome) []byte {
	top := &object{}
	switch {
	case o.decision == VerdictDeny:
		top.set("continue", rawJSON("true"))
		top.set("permission", jsonString("deny"))
		top.set("userMessage", jsonString(o.reason))
		top.set("agentMessage", jsonString(o.reason))
	case o.rewritten != "":
		top.set("continue", rawJSON("true"))
		if o.decision == VerdictAsk {
			top.set("permission", jsonString("ask"))
		}
		in := &object{}
		in.set("command", jsonString(o.rewritten))
		top.set("updated_input", in.compact())
	}
	return top.compact()
}

func rawJSON(literal string) json.RawMessage { return json.RawMessage(literal) }
