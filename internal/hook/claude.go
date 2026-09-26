package hook

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
)

// MaxHookInput caps how much of the hook payload is read (1 MiB). A larger
// payload is ignored: the agent runs its command unchanged.
const MaxHookInput = 1 << 20

// RewriteReason is shown with every rewrite.
const RewriteReason = "lx: condensed output (lx show <id> for full)"

// ClaudeHook answers Claude Code's PreToolUse hook for the Bash tool.
//
// It never fails the tool call: on any problem it prints nothing and returns
// nil, and Claude Code runs the original command. When it rewrites, every
// tool_input field is passed back verbatim except "command". Permission
// decisions mirror the user's own rules evaluated on the ORIGINAL command
// (so rewriting never widens or narrows what is allowed):
//
//   - any segment denied → print nothing; Claude Code's deny applies natively
//   - any segment "ask"  → rewrite with permissionDecision "ask"
//   - every segment allowed → rewrite with permissionDecision "allow"
//   - otherwise → rewrite with no decision (the normal permission flow)
//
// A command the model already wrote as `lx …` is checked with lx stripped,
// so `lx git push` cannot slip past a Bash(git push:*) deny rule.
func ClaudeHook(stdin io.Reader, stdout io.Writer, cwd string) error {
	return runHook(claudeHost, stdin, stdout, cwd)
}

// Hook runs the PreToolUse hook for agent: "claude" (Claude Code), or the
// experimental "copilot" (VS Code Copilot agent / Copilot CLI, Claude-style
// payloads), "gemini" (Gemini CLI BeforeTool) and "cursor" (Cursor
// preToolUse). It has ClaudeHook's never-fail contract.
func Hook(agent string, stdin io.Reader, stdout io.Writer, cwd string) error {
	h, ok := hosts[agent]
	if !ok {
		return nil
	}
	return runHook(h, stdin, stdout, cwd)
}

// Agents lists the agent names Hook accepts.
func Agents() []string { return []string{"claude", "copilot", "cursor", "gemini"} }

type host struct {
	tools []string // tool_name values carrying a shell command
	// render the response; returns nil to print nothing.
	render func(p *payload, o outcome) []byte
	// alwaysJSON hosts expect a JSON object even when there is nothing to say.
	alwaysJSON bool
}

var (
	claudeHost = host{tools: []string{"Bash"}, render: renderClaude(true)}
	hosts      = map[string]host{
		"claude":  claudeHost,
		"copilot": {tools: []string{"Bash", "bash", "run_in_terminal", "runTerminalCommand"}, render: renderClaude(false)},
		"gemini":  {tools: []string{"run_shell_command"}, render: renderGemini},
		"cursor":  {tools: []string{"Shell", "Bash", "run_terminal_cmd"}, render: renderCursor, alwaysJSON: true},
	}
)

type payload struct {
	input   *object // tool_input, order preserved
	command string
}

// outcome is what the hook decided, independent of any host's wire format.
type outcome struct {
	rewritten string // "" when the command is not rewritten
	decision  string // "", "allow", "ask", "deny"
	reason    string
}

func hookDisabled() bool {
	switch strings.ToLower(os.Getenv("LX_HOOK")) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

func runHook(h host, stdin io.Reader, stdout io.Writer, cwd string) error {
	out := hookResponse(h, stdin, cwd)
	if out == nil && h.alwaysJSON {
		out = []byte("{}")
	}
	if out != nil {
		_, _ = stdout.Write(append(out, '\n'))
	}
	return nil // never break the agent's tool call
}

func hookResponse(h host, stdin io.Reader, cwd string) (out []byte) {
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
	p, payloadCwd, ok := parsePayload(data, h.tools)
	if !ok {
		return nil
	}
	if payloadCwd != "" {
		cwd = payloadCwd
	}
	o := evaluate(p.command, func() Rules { return LoadClaudeRules(cwd) })
	return h.render(p, o)
}

func parsePayload(data []byte, tools []string) (*payload, string, bool) {
	top, err := parseObject(data)
	if err != nil {
		return nil, "", false
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
		return nil, "", false
	}
	raw, ok := top.get("tool_input")
	if !ok {
		return nil, "", false
	}
	input, err := parseObject(raw)
	if err != nil {
		return nil, "", false
	}
	cmd, ok := input.getString("command")
	if !ok || strings.TrimSpace(cmd) == "" {
		return nil, "", false
	}
	cwd, _ := top.getString("cwd")
	return &payload{input: input, command: cmd}, cwd, true
}

// evaluate decides what to do with cmd. Rules are loaded lazily: commands
// that are neither rewritten nor lx invocations cost no file I/O.
func evaluate(cmd string, loadRules func() Rules) outcome {
	a := analyze(cmd)
	targets, _ := a.plan()
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

	// Model-written lx: judge what lx will actually run.
	var lxAsk *outcome
	if hasLx {
		for _, s := range a.segs {
			if !s.isLx {
				continue
			}
			for _, inner := range lxInner(s.words[s.cmdIdx:]) {
				for _, text := range []string{inner.text, inner.value} {
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
	}
	if len(targets) == 0 {
		if lxAsk != nil {
			return *lxAsk
		}
		return outcome{}
	}

	rewritten := splice(cmd, targets)
	verdict, rule, subject := rules.decideAnalysis(a)
	switch verdict {
	case VerdictDeny:
		return outcome{} // let the host deny the original command itself
	case VerdictAsk:
		return outcome{rewritten: rewritten, decision: VerdictAsk,
			reason: RewriteReason + "; `" + subject + "` matches ask rule " + rule}
	case VerdictAllow:
		return outcome{rewritten: rewritten, decision: VerdictAllow, reason: RewriteReason}
	}
	return outcome{rewritten: rewritten, reason: RewriteReason}
}

// updatedInput is tool_input with only "command" replaced.
func (p *payload) updatedInput(cmd string) []byte {
	in := &object{members: append([]member(nil), p.input.members...)}
	in.set("command", jsonString(cmd))
	return in.compact()
}

func renderClaude(emitAsk bool) func(*payload, outcome) []byte {
	return func(p *payload, o outcome) []byte {
		decision := o.decision
		if decision == VerdictAsk && !emitAsk {
			// Copilot CLI turns "ask" into a blocking dialog with no way to
			// remember the answer; leave approval to its own flow.
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
		if decision == "" || decision == VerdictAllow {
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

// renderGemini: Gemini CLI BeforeTool. lx does not read Gemini's own
// permission settings, so it never asserts "allow"; a rewrite carries no
// decision and Gemini's normal confirmation flow applies to it.
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

// renderCursor: Cursor preToolUse. Cursor expects a JSON object on every
// path; like Gemini it never gets an "allow" from lx.
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
