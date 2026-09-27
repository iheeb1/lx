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
//   - otherwise → rewrite with no decision (the normal permission flow),
//     unless HookOptions.ReadOnly applies (see HookWith)
//
// A command the model already wrote as `lx …` is checked with lx stripped,
// so `lx git push` cannot slip past a Bash(git push:*) deny rule. A Bash
// call with run_in_background is never rewritten (lx would buffer output
// the agent polls for), but model-written lx in it is still checked.
func ClaudeHook(stdin io.Reader, stdout io.Writer, cwd string) error {
	return runHook(claudeHost, stdin, stdout, cwd, HookOptions{})
}

// HookOptions are the flags of `lx hook <agent>`.
type HookOptions struct {
	// ReadOnly (claude only): when no user rule decides, approve a rewrite
	// whose every part is a read-only command from a fixed table that reads
	// only inside the project, as Claude Code would have approved the
	// original. Off by default.
	ReadOnly bool
	// Prefix is the absolute path of lx to put in rewritten commands, for
	// when lx is not on the agent shell's PATH. "" picks `lx` or this
	// binary's path (see defaultPrefix).
	Prefix string
}

// Hook runs the PreToolUse hook for agent: "claude" (Claude Code), or the
// experimental "copilot" (VS Code Copilot agent / Copilot CLI, Claude-style
// payloads), "gemini" (Gemini CLI BeforeTool) and "cursor" (Cursor
// preToolUse). It has ClaudeHook's never-fail contract.
func Hook(agent string, stdin io.Reader, stdout io.Writer, cwd string) error {
	return HookWith(agent, stdin, stdout, cwd, HookOptions{})
}

// HookWith is Hook with options. ReadOnly only ever applies to Claude Code:
// lx reads no other agent's permission settings.
func HookWith(agent string, stdin io.Reader, stdout io.Writer, cwd string, o HookOptions) error {
	h, ok := hosts[agent]
	if !ok {
		return nil
	}
	return runHook(h, stdin, stdout, cwd, o)
}

// Agents lists the agent names Hook accepts.
func Agents() []string { return []string{"claude", "copilot", "cursor", "gemini"} }

type host struct {
	tools []string // tool_name values carrying a shell command
	// render the response; returns nil to print nothing.
	render func(p *payload, o outcome) []byte
	// alwaysJSON hosts expect a JSON object even when there is nothing to say.
	alwaysJSON bool
	// parity: --readonly may apply (the host reads Claude Code's settings).
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
	input      *object // tool_input, order preserved
	command    string
	cwd        string // the shell's working directory ("" if not sent)
	mode       string // permission_mode ("" if not sent)
	background bool   // tool_input.run_in_background
}

// outcome is what the hook decided, independent of any host's wire format.
type outcome struct {
	rewritten string // "" when the command is not rewritten
	decision  string // "", "allow", "ask", "deny"
	reason    string
	readOnly  bool // allowed by --readonly parity, not by a user rule
}

// evalEnv is what evaluate needs besides the command and the rules.
type evalEnv struct {
	ReadOnly       bool   // --readonly, on a host that supports it
	Cwd            string // the shell's working directory, from the payload
	Root           string // the project: $CLAUDE_PROJECT_DIR, else Cwd
	PermissionMode string // the payload's permission_mode
	Background     bool   // run_in_background: never rewrite
	Prefix         string // --prefix
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
	return nil // never break the agent's tool call
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
		// Anything but an explicit false counts: a background run that lx
		// buffered would show the agent nothing until it exits.
		switch strings.TrimSpace(string(bg)) {
		case "false", "null", `""`, `"false"`:
		default:
			p.background = true
		}
	}
	return p, true
}

// parityModes are the permission modes in which Claude Code prompts for an
// unknown command but approves read-only ones by itself. Other modes (for
// example bypassPermissions, or dontAsk, which denies what is not
// pre-approved) get no parity decision.
var parityModes = map[string]bool{"": true, "default": true, "acceptEdits": true, "plan": true}

// evaluate decides what to do with cmd. Rules are loaded lazily: commands
// that are neither rewritten nor lx invocations cost no file I/O.
func evaluate(cmd string, env evalEnv, loadRules func() Rules) outcome {
	return evaluateWith(cmd, env, loadRules, resolvePrefix)
}

func evaluateWith(cmd string, env evalEnv, loadRules func() Rules, prefix func(string) string) outcome {
	a := analyze(cmd)
	targets, _ := a.plan()
	if env.Background {
		targets = nil // lx must not buffer a run the agent polls
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

	// Model-written lx: judge what lx will actually run.
	var lxAsk *outcome
	if hasLx {
		for _, s := range a.segs {
			if !s.isLx {
				continue
			}
			for _, inner := range lxInner(s.words[s.cmdIdx:]) {
				for _, text := range append([]string{inner.text, inner.value}, wrappedTexts(strings.Fields(inner.value))...) {
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

	verdict, rule, subject := rules.decideAnalysis(a)
	if verdict == VerdictDeny {
		return outcome{} // let the host deny the original command itself
	}
	rewritten := splice(cmd, targets, prefix(env.Prefix))
	// A deny rule on lx itself (Bash(lx:*)) would make the host refuse
	// every rewrite of a command it allows: leave the original alone.
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
	// A rule written for lx (Bash(lx:*) in ask) must still hold when the
	// rewrite calls lx by its path, which the rule's text doesn't match.
	if env.ReadOnly && parityModes[env.PermissionMode] && !rules.guardsLx(rewritten) &&
		!rules.guardsLx(splice(cmd, targets, "lx")) {
		if ok, _ := a.readOnlyParity(env.Cwd, env.Root, rules.Dirs); ok {
			return outcome{rewritten: rewritten, decision: VerdictAllow, readOnly: true,
				reason: RewriteReason + readOnlyReason}
		}
	}
	return outcome{rewritten: rewritten, reason: RewriteReason}
}

// EvaluateClaude reports what `lx hook claude [--readonly]` would answer for
// cmd run in cwd under rules, in the default permission mode: the rewritten
// command ("" if none) and the decision ("allow", "ask", "deny", or "" for
// Claude Code's normal prompt). For reporting tools such as lx discover,
// which replay other sessions: the project root is cwd itself (never this
// process's $CLAUDE_PROJECT_DIR, which names the current session's project),
// so a command reading above cwd counts as a prompt. Rewrites use "lx".
func EvaluateClaude(cmd, cwd string, readOnly bool, rules Rules) (rewritten, decision string) {
	env := evalEnv{ReadOnly: readOnly, Cwd: cwd, Root: cwd, PermissionMode: "default", Prefix: ""}
	o := evaluateWith(cmd, env, func() Rules { return rules }, func(string) string { return "lx" })
	return o.rewritten, o.decision
}

// guardsLx reports whether a deny or ask rule matches the rewritten command
// itself (say Bash(lx:*) in ask): parity then stays out of the way and
// Claude Code applies that rule to the rewrite.
func (r Rules) guardsLx(rewritten string) bool {
	v, _, _ := r.decide(rewritten)
	return v == VerdictDeny || v == VerdictAsk
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
