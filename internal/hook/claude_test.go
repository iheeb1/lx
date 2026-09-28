package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withRules(t *testing.T, settings string) string {
	t.Helper()
	project, _ := isolate(t)
	if settings != "" {
		writeFile(t, filepath.Join(project, ".claude", "settings.json"), settings)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", project)
	return project
}

func runHookFor(t *testing.T, agent, input string) string {
	t.Helper()
	var out bytes.Buffer
	if err := Hook(agent, strings.NewReader(input), &out, "/nonexistent"); err != nil {
		t.Fatalf("hook returned %v", err)
	}
	if s := strings.TrimSpace(out.String()); s != "" && !json.Valid([]byte(s)) {
		t.Fatalf("invalid JSON output: %s", s)
	}
	return out.String()
}

func bashInput(cmd string) string {
	return `{"session_id":"s1","transcript_path":"/t.jsonl","cwd":"/nowhere","permission_mode":"default",` +
		`"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":` + string(jsonString(cmd)) +
		`,"description":"Show status","timeout":120000,"run_in_background":false,` +
		`"x_future":{"nested":[1,2.50,"\u00e9 <&>"]}},"tool_use_id":"toolu_1"}`
}

const preserved = `"description":"Show status","timeout":120000,"run_in_background":false,"x_future":{"nested":[1,2.50,"\u00e9 <&>"]}`

func TestClaudeHookGolden(t *testing.T) {
	const reason = `"permissionDecisionReason":"lx: condensed output (lx show <id> for full)"`
	rewrite := func(decision, cmd string) string {
		d := ""
		if decision != "" {
			d = `"permissionDecision":"` + decision + `",`
		}
		return `{"hookSpecificOutput":{"hookEventName":"PreToolUse",` + d + reason +
			`,"updatedInput":{"command":` + string(jsonString(cmd)) + `,` + preserved + `}}}` + "\n"
	}
	cases := []struct {
		name, settings, cmd, want string
	}{
		{"no rules: rewrite, normal flow", ``, `git status`, rewrite("", "lx git status")},
		{"allow rule", `{"permissions":{"allow":["Bash(git status:*)"]}}`, `git status`,
			rewrite("allow", "lx git status")},
		{"deny on original: stay out of the way", `{"permissions":{"deny":["Bash(git status:*)"]}}`,
			`git status`, ``},
		{"compound, all allowed", `{"permissions":{"allow":["Bash(git:*)","Bash(npm test:*)"]}}`,
			`cd web && npm test && git status`, rewrite("allow", "cd web && lx npm test && lx git status")},
		{"compound, one segment not allowed", `{"permissions":{"allow":["Bash(npm test:*)"]}}`,
			`npm test && curl -s example.com`, rewrite("", "lx npm test && lx curl -s example.com")},
		{"html-ish characters stay literal", ``, `git status && echo "<a>"`,
			rewrite("", `lx git status && echo "<a>"`)},
		{"unsupported command", ``, `echo hi`, ``},
		{"already lx, no rules", ``, `lx git status`, ``},
		{"never-rewrite construct", ``, `git status $(rm -rf /tmp/x)`, ``},
		{"deny elsewhere in the chain", `{"permissions":{"deny":["Bash(rm:*)"],"allow":["Bash(git:*)"]}}`,
			`git status && rm -rf /tmp/x`, ``},
		{"model-written lx + deny", `{"permissions":{"deny":["Bash(git push:*)"]}}`, `lx git push origin main`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",` +
				"\"permissionDecisionReason\":\"lx: `git push origin main` matches deny rule Bash(git push:*)\"}}\n"},
		{"model-written lx behind env and flags + deny", `{"permissions":{"deny":["Bash(git push:*)"]}}`,
			`cd x && FOO=1 lx -v --budget 300 git push`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",` +
				"\"permissionDecisionReason\":\"lx: `git push` matches deny rule Bash(git push:*)\"}}\n"},
		{"model-written lx + ask", `{"permissions":{"ask":["Bash(git push:*)"]}}`, `lx git push`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask",` +
				"\"permissionDecisionReason\":\"lx: `git push` matches ask rule Bash(git push:*)\"}}\n"},
		{"model-written lx, allowed inner: nothing to say", `{"permissions":{"allow":["Bash(git:*)"]}}`,
			`lx git push`, ``},
		{"a head/tail after it: lx fits the cut", ``, `go test ./... 2>&1 | tail -n 40`,
			rewrite("", "lx --fit tail:40 go test ./... 2>&1 | tail -n 40")},
		{"fitted rewrite, allow rule", `{"permissions":{"allow":["Bash(go test:*)"]}}`, `go test ./... 2>&1 | head -20`,
			rewrite("allow", "lx --fit head:20 go test ./... 2>&1 | head -20")},
		{"fitted rewrite, deny rule on the original", `{"permissions":{"deny":["Bash(go test:*)"]}}`,
			`go test ./... 2>&1 | head -20`, ``},
		{"model-written lx --fit + deny", `{"permissions":{"deny":["Bash(git push:*)"]}}`, `lx --fit 5 git push origin main 2>&1 | tail -5`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",` +
				"\"permissionDecisionReason\":\"lx: `git push origin main` matches deny rule Bash(git push:*)\"}}\n"},
		{"model-written lx --fit without a count + deny", `{"permissions":{"deny":["Bash(git push:*)"]}}`, `lx --fit git push`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",` +
				"\"permissionDecisionReason\":\"lx: `git push` matches deny rule Bash(git push:*)\"}}\n"},
		{"model-written lx --fit + ask", `{"permissions":{"ask":["Bash(git push:*)"]}}`, `lx --fit=3 git push`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask",` +
				"\"permissionDecisionReason\":\"lx: `git push` matches ask rule Bash(git push:*)\"}}\n"},
		{"model-written lx --fit + deny rule on lx git", `{"permissions":{"deny":["Bash(lx git push:*)"]}}`, `lx --fit 9 git push`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny",` +
				"\"permissionDecisionReason\":\"lx: `lx git push` matches deny rule Bash(lx git push:*)\"}}\n"},
		{"model-written lx --fit, already fitted: nothing to add", ``, `lx --fit 40 go test ./... 2>&1 | tail -n 40`, ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withRules(t, c.settings)
			if got := runHookFor(t, "claude", bashInput(c.cmd)); got != c.want {
				t.Errorf("\ngot  %s\nwant %s", got, c.want)
			}
		})
	}
}

func TestClaudeHookAsk(t *testing.T) {
	withRules(t, `{"permissions":{"ask":["Bash(git push:*)"],"allow":["Bash(git:*)"]}}`)
	out := runHookFor(t, "claude", bashInput("git status && git push"))
	var v struct {
		H struct {
			Decision string          `json:"permissionDecision"`
			Reason   string          `json:"permissionDecisionReason"`
			Input    json.RawMessage `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatal(err, out)
	}
	if v.H.Decision != "ask" || !strings.Contains(v.H.Reason, "Bash(git push:*)") {
		t.Errorf("decision=%q reason=%q", v.H.Decision, v.H.Reason)
	}
	if !strings.Contains(string(v.H.Input), `"lx git status && lx git push"`) {
		t.Errorf("updatedInput = %s", v.H.Input)
	}
}

func TestClaudeHookIgnores(t *testing.T) {
	withRules(t, "")
	big := `{"tool_name":"Bash","tool_input":{"command":"git status","pad":"` + strings.Repeat("x", MaxHookInput) + `"}}`
	for name, input := range map[string]string{
		"other tool":      `{"tool_name":"Read","tool_input":{"file_path":"/x"}}`,
		"no command":      `{"tool_name":"Bash","tool_input":{}}`,
		"empty command":   `{"tool_name":"Bash","tool_input":{"command":"  "}}`,
		"command not str": `{"tool_name":"Bash","tool_input":{"command":["git","status"]}}`,
		"no tool_input":   `{"tool_name":"Bash"}`,
		"malformed":       `{"tool_name":"Bash","tool_input":{"command":"git status"`,
		"not an object":   `["Bash"]`,
		"trailing junk":   `{"tool_name":"Bash","tool_input":{"command":"git status"}} x`,
		"empty":           ``,
		"over 1 MiB":      big,
	} {
		if out := runHookFor(t, "claude", input); out != "" {
			t.Errorf("%s: printed %q", name, out)
		}
	}
}

func TestClaudeHookBOMAndDisable(t *testing.T) {
	withRules(t, "")
	if out := runHookFor(t, "claude", "\xef\xbb\xbf"+bashInput("git status")); !strings.Contains(out, `"lx git status"`) {
		t.Errorf("BOM input not handled: %q", out)
	}
	t.Setenv("LX_HOOK", "0")
	if out := runHookFor(t, "claude", bashInput("git status")); out != "" {
		t.Errorf("LX_HOOK=0 still printed %q", out)
	}
}

func TestClaudeHookUsesPayloadCwd(t *testing.T) {
	project, _ := isolate(t)
	writeFile(t, filepath.Join(project, ".claude", "settings.json"), `{"permissions":{"allow":["Bash(git status)"]}}`)
	input := `{"tool_name":"Bash","cwd":` + string(jsonString(project)) + `,"tool_input":{"command":"git status"}}`
	out := runHookFor(t, "claude", input)
	if !strings.Contains(out, `"permissionDecision":"allow"`) {
		t.Errorf("rules from payload cwd not used: %s", out)
	}
}

func TestOtherAgents(t *testing.T) {
	withRules(t, `{"permissions":{"ask":["Bash(git push:*)"],"deny":["Bash(rm:*)"]}}`)
	cases := []struct{ agent, input, want string }{
		{"copilot", `{"tool_name":"run_in_terminal","tool_input":{"command":"git status","explanation":"x"}}`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecisionReason":"lx: condensed output (lx show <id> for full)","updatedInput":{"command":"lx git status","explanation":"x"}}}` + "\n"},

		{"copilot", `{"tool_name":"Bash","tool_input":{"command":"git push"}}`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecisionReason":"lx: condensed output (lx show <id> for full)","updatedInput":{"command":"lx git push"}}}` + "\n"},
		{"copilot", `{"tool_name":"Bash","tool_input":{"command":"lx git push"}}`, ``},
		{"gemini", `{"tool_name":"run_shell_command","tool_input":{"command":"go test ./...","dir_path":"."}}`,
			`{"hookSpecificOutput":{"hookEventName":"BeforeTool","tool_input":{"command":"lx go test ./...","dir_path":"."}}}` + "\n"},
		{"gemini", `{"tool_name":"run_shell_command","tool_input":{"command":"lx rm -rf x"}}`,
			"{\"decision\":\"deny\",\"reason\":\"lx: `rm -rf x` matches deny rule Bash(rm:*)\"}\n"},
		{"gemini", `{"tool_name":"read_file","tool_input":{"path":"x"}}`, ``},
		{"cursor", `{"tool_name":"Shell","tool_input":{"command":"git status"}}`,
			`{"continue":true,"updated_input":{"command":"lx git status"}}` + "\n"},
		{"cursor", `{"tool_name":"Shell","tool_input":{"command":"git push"}}`,
			`{"continue":true,"permission":"ask","updated_input":{"command":"lx git push"}}` + "\n"},
		{"cursor", `{"tool_name":"Read","tool_input":{}}`, "{}\n"},
		{"cursor", `garbage`, "{}\n"},
		{"nope", `{"tool_name":"Bash","tool_input":{"command":"git status"}}`, ``},
	}
	for _, c := range cases {
		if got := runHookFor(t, c.agent, c.input); got != c.want {
			t.Errorf("%s %s\ngot  %s\nwant %s", c.agent, c.input, got, c.want)
		}
	}
}

func TestClaudeHookFast(t *testing.T) {
	withRules(t, `{"permissions":{"allow":["Bash(git:*)","Bash(npm test:*)"],"deny":["Bash(rm:*)"]}}`)
	input := bashInput(`cd web && FOO=1 npm test -- --runInBand 2>&1 | tail -40 && git status`)
	const n = 300
	start := time.Now()
	for i := 0; i < n; i++ {
		var out bytes.Buffer
		_ = ClaudeHook(strings.NewReader(input), &out, "/x")
		if out.Len() == 0 {
			t.Fatal("no output")
		}
	}
	if avg := time.Since(start) / n; avg > 2*time.Millisecond {
		t.Errorf("ClaudeHook averages %v per call, want < 2ms", avg)
	}
}

func BenchmarkClaudeHook(b *testing.B) {
	project := b.TempDir()
	b.Setenv("CLAUDE_CONFIG_DIR", b.TempDir())
	b.Setenv("CLAUDE_PROJECT_DIR", project)
	writeFile(b, filepath.Join(project, ".claude", "settings.json"),
		`{"permissions":{"allow":["Bash(git:*)","Bash(npm test:*)"],"deny":["Bash(rm:*)"]}}`)
	input := bashInput(`cd web && FOO=1 npm test 2>&1 | tail -40 && git status`)
	for b.Loop() {
		var out bytes.Buffer
		_ = ClaudeHook(strings.NewReader(input), &out, "/x")
	}
}

func runHookWith(t *testing.T, agent, input string, o HookOptions) string {
	t.Helper()
	var out bytes.Buffer
	if err := HookWith(agent, strings.NewReader(input), &out, "/nonexistent", o); err != nil {
		t.Fatalf("hook returned %v", err)
	}
	if s := strings.TrimSpace(out.String()); s != "" && !json.Valid([]byte(s)) {
		t.Fatalf("invalid JSON output: %s", s)
	}
	return out.String()
}

func payloadIn(cmd, cwd, mode string, background bool) string {
	top := &object{}
	top.set("session_id", jsonString("s1"))
	if cwd != "" {
		top.set("cwd", jsonString(cwd))
	}
	if mode != "" {
		top.set("permission_mode", jsonString(mode))
	}
	top.set("hook_event_name", jsonString("PreToolUse"))
	top.set("tool_name", jsonString("Bash"))
	in := &object{}
	in.set("command", jsonString(cmd))
	in.set("description", jsonString("d"))
	if background {
		in.set("run_in_background", rawJSON("true"))
	}
	top.set("tool_input", in.compact())
	return string(top.compact())
}

type hookOut struct {
	H struct {
		Decision string `json:"permissionDecision"`
		Reason   string `json:"permissionDecisionReason"`
		Input    *struct {
			Command    string `json:"command"`
			Background *bool  `json:"run_in_background"`
		} `json:"updatedInput"`
	} `json:"hookSpecificOutput"`
}

func decode(t *testing.T, out string) hookOut {
	t.Helper()
	var v hookOut
	if strings.TrimSpace(out) == "" {
		return v
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	return v
}

const parityReason = RewriteReason + readOnlyReason

func TestClaudeHookReadOnly(t *testing.T) {
	ro := HookOptions{ReadOnly: true}
	cases := []struct {
		name, settings, cmd, mode string
		bg                        bool
		opts                      HookOptions
		decision, rewritten       string
	}{
		{"git status", ``, `git status`, "default", false, ro, "allow", "lx git status"},
		{"no permission_mode sent", ``, `git status`, "", false, ro, "allow", "lx git status"},
		{"acceptEdits", ``, `git log --oneline -5`, "acceptEdits", false, ro, "allow", "lx git log --oneline -5"},
		{"plan", ``, `git diff`, "plan", false, ro, "allow", "lx git diff"},
		{"cd, list, head", ``, `cd src && ls -la 2>&1 | head -20`, "default", false, ro, "allow", "cd src && lx --fit head:20 ls -la 2>&1 | head -20"},
		{"log through tail", ``, `git log --oneline 2>&1 | tail -n 40`, "default", false, ro, "allow", "lx --fit tail:40 git log --oneline 2>&1 | tail -n 40"},
		{"ask rule on lx itself, with --fit", `{"permissions":{"ask":["Bash(lx:*)"]}}`, `git log 2>&1 | head -5`, "default", false, ro, "", "lx --fit head:5 git log 2>&1 | head -5"},
		{"ask rule on lx git, with --fit", `{"permissions":{"ask":["Bash(lx git:*)"]}}`, `git log 2>&1 | head -5`, "default", false, ro, "", "lx --fit head:5 git log 2>&1 | head -5"},
		{"deny rule on lx git, with --fit", `{"permissions":{"deny":["Bash(lx git log:*)"]}}`, `git log 2>&1 | head -5`, "default", false, ro, "", ""},
		{"search", ``, `rg -n foo src && grep -rn TODO src`, "default", false, ro, "allow", "lx rg -n foo src && lx grep -rn TODO src"},
		{"one part not read-only", ``, `git status && npm test`, "default", false, ro, "", "lx git status && lx npm test"},
		{"outside the project", ``, `ls ../..`, "default", false, ro, "", "lx ls ../.."},
		{"env assignment", ``, `LD_PRELOAD=x git status`, "default", false, ro, "", "LD_PRELOAD=x lx git status"},
		{"wrapper", ``, `timeout 5 git status`, "default", false, ro, "", "timeout 5 lx git status"},
		{"git -C", ``, `git -C ../x status`, "default", false, ro, "", "lx git -C ../x status"},
		{"bypassPermissions", ``, `git status`, "bypassPermissions", false, ro, "", "lx git status"},
		{"dontAsk", ``, `git status`, "dontAsk", false, ro, "", "lx git status"},
		{"without --readonly", ``, `git status`, "default", false, HookOptions{}, "", "lx git status"},
		{"deny rule", `{"permissions":{"deny":["Bash(git status:*)"]}}`, `git status`, "default", false, ro, "", ""},
		{"ask rule", `{"permissions":{"ask":["Bash(git log:*)"]}}`, `git log`, "default", false, ro, "ask", "lx git log"},
		{"allow rule keeps its reason", `{"permissions":{"allow":["Bash(git status:*)"]}}`, `git status`, "default", false, ro, "allow", "lx git status"},
		{"background", ``, `git status`, "default", true, ro, "", ""},
		{"ask rule on lx itself", `{"permissions":{"ask":["Bash(lx:*)"]}}`, `git status`, "default", false, ro, "", "lx git status"},

		{"deny rule on lx itself", `{"permissions":{"deny":["Bash(lx ls:*)"]}}`, `ls -la`, "default", false, ro, "", ""},
		{"unrelated allow rule, rest read-only", `{"permissions":{"allow":["Bash(npm test:*)"]}}`, `git status && ls`, "default", false, ro, "allow", "lx git status && lx ls"},
		{"file redirect never rewritten", ``, `git status > f`, "default", false, ro, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			project := withRules(t, c.settings)
			if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
				t.Fatal(err)
			}
			v := decode(t, runHookWith(t, "claude", payloadIn(c.cmd, project, c.mode, c.bg), c.opts))
			got := ""
			if v.H.Input != nil {
				got = v.H.Input.Command
			}
			if v.H.Decision != c.decision || got != c.rewritten {
				t.Fatalf("decision=%q rewrite=%q, want %q %q (reason %q)", v.H.Decision, got, c.decision, c.rewritten, v.H.Reason)
			}
			switch {
			case c.decision == "allow" && !strings.Contains(c.settings, "git status:*"):
				if v.H.Reason != parityReason {
					t.Errorf("reason = %q", v.H.Reason)
				}
			case c.rewritten != "" && c.decision != "ask":
				if v.H.Reason != RewriteReason {
					t.Errorf("reason = %q", v.H.Reason)
				}
			}
		})
	}
}

func TestClaudeHookGoldenWithReadOnly(t *testing.T) {

	for _, cmd := range []string{`git status`, `cd web && npm test && git status`, `lx git push origin main`} {
		withRules(t, "")
		a := runHookFor(t, "claude", bashInput(cmd))
		b := runHookWith(t, "claude", bashInput(cmd), HookOptions{ReadOnly: true})
		if a != b {
			t.Errorf("%s: --readonly changed the output\n%s\n%s", cmd, a, b)
		}
	}
}

func TestClaudeHookReadOnlyRoot(t *testing.T) {
	ro := HookOptions{ReadOnly: true}
	project := withRules(t, "")

	t.Setenv("HOME", project)
	if v := decode(t, runHookWith(t, "claude", payloadIn("git status", project, "default", false), ro)); v.H.Decision != "" {
		t.Errorf("root = $HOME: decision %q", v.H.Decision)
	}
	t.Setenv("HOME", t.TempDir())

	if v := decode(t, runHookWith(t, "claude", payloadIn("git status", "", "default", false), ro)); v.H.Decision != "" {
		t.Errorf("no cwd: decision %q", v.H.Decision)
	}

	t.Setenv("CLAUDE_PROJECT_DIR", "")
	sub := filepath.Join(project, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if v := decode(t, runHookWith(t, "claude", payloadIn("ls ..", sub, "default", false), ro)); v.H.Decision != "" {
		t.Errorf("ls .. above the payload cwd: decision %q", v.H.Decision)
	}
	if v := decode(t, runHookWith(t, "claude", payloadIn("ls .", sub, "default", false), ro)); v.H.Decision != "allow" {
		t.Errorf("ls . in the payload cwd: decision %q", v.H.Decision)
	}

	extra := t.TempDir()
	writeFile(t, filepath.Join(project, ".claude", "settings.json"),
		`{"permissions":{"additionalDirectories":[`+string(jsonString(extra))+`]}}`)
	t.Setenv("CLAUDE_PROJECT_DIR", project)
	if v := decode(t, runHookWith(t, "claude", payloadIn("ls "+extra, project, "default", false), ro)); v.H.Decision != "allow" {
		t.Errorf("additional directory: decision %q", v.H.Decision)
	}
}

func TestReadOnlyOnlyForClaude(t *testing.T) {
	project := withRules(t, "")
	for _, agent := range []string{"copilot", "gemini", "cursor"} {
		tool := map[string]string{"copilot": "Bash", "gemini": "run_shell_command", "cursor": "Shell"}[agent]
		in := `{"tool_name":"` + tool + `","cwd":` + string(jsonString(project)) + `,"tool_input":{"command":"git status"}}`
		out := runHookWith(t, agent, in, HookOptions{ReadOnly: true})
		if strings.Contains(out, "allow") || strings.Contains(out, "read-only") {
			t.Errorf("%s got a parity decision: %s", agent, out)
		}
		if !strings.Contains(out, "lx git status") {
			t.Errorf("%s: not rewritten: %s", agent, out)
		}
	}
}

func TestClaudeHookBackground(t *testing.T) {
	project := withRules(t, `{"permissions":{"deny":["Bash(git push:*)"],"ask":["Bash(npm publish:*)"]}}`)
	for _, ro := range []bool{false, true} {
		o := HookOptions{ReadOnly: ro}
		if out := runHookWith(t, "claude", payloadIn("go test ./...", project, "default", true), o); out != "" {
			t.Errorf("background run rewritten: %s", out)
		}
		v := decode(t, runHookWith(t, "claude", payloadIn("lx git push", project, "default", true), o))
		if v.H.Decision != "deny" || v.H.Input != nil {
			t.Errorf("background model-written lx + deny: %+v", v.H)
		}
		v = decode(t, runHookWith(t, "claude", payloadIn("lx npm publish", project, "default", true), o))
		if v.H.Decision != "ask" || v.H.Input != nil {
			t.Errorf("background model-written lx + ask: %+v", v.H)
		}
		v = decode(t, runHookWith(t, "claude", payloadIn("lx git push", project, "default", false), o))
		if v.H.Decision != "deny" {
			t.Errorf("model-written lx + deny: %+v", v.H)
		}
	}

	for _, bg := range []string{`true`, `"true"`, `1`} {
		in := `{"tool_name":"Bash","tool_input":{"command":"git status","run_in_background":` + bg + `}}`
		if out := runHookFor(t, "claude", in); out != "" {
			t.Errorf("run_in_background %s: %s", bg, out)
		}
	}
	for _, bg := range []string{`false`, `null`} {
		in := `{"tool_name":"Bash","tool_input":{"command":"git status","run_in_background":` + bg + `}}`
		if out := runHookFor(t, "claude", in); !strings.Contains(out, `"lx git status"`) || !strings.Contains(out, `"run_in_background":`+bg) {
			t.Errorf("run_in_background %s: %s", bg, out)
		}
	}
}

func TestClaudeHookReadOnlyFast(t *testing.T) {
	project := withRules(t, `{"permissions":{"deny":["Bash(rm:*)"]}}`)
	if err := os.MkdirAll(filepath.Join(project, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	input := payloadIn(`cd src && git status && ls -la | head -20 && rg -n foo . && grep -rn TODO .`, project, "default", false)
	const n = 300
	start := time.Now()
	for i := 0; i < n; i++ {
		var out bytes.Buffer
		_ = HookWith("claude", strings.NewReader(input), &out, "/x", HookOptions{ReadOnly: true})
		if !strings.Contains(out.String(), `"permissionDecision":"allow"`) {
			t.Fatalf("not approved: %s", out.String())
		}
	}
	if avg := time.Since(start) / n; avg > 2*time.Millisecond && !raceEnabled {
		t.Errorf("--readonly hook averages %v per call, want < 2ms", avg)
	}
}

func TestEvaluateClaude(t *testing.T) {
	project := withRules(t, "")
	cases := []struct {
		cmd      string
		readOnly bool
		rules    Rules
		rw, dec  string
	}{
		{"git status", false, Rules{}, "lx git status", ""},
		{"git status", true, Rules{}, "lx git status", "allow"},
		{"git status && npm test", true, Rules{}, "lx git status && lx npm test", ""},
		{"git status", true, Rules{Deny: []string{"Bash(git:*)"}}, "", ""},
		{"git push", true, Rules{Ask: []string{"Bash(git push:*)"}}, "lx git push", "ask"},
		{"npm test", false, Rules{Allow: []string{"Bash(npm test:*)"}}, "lx npm test", "allow"},
		{"lx git push", true, Rules{Deny: []string{"Bash(git push:*)"}}, "", "deny"},
		{"echo hi", true, Rules{}, "", ""},
		{"git log 2>&1 | head -n 5", true, Rules{}, "lx --fit head:5 git log 2>&1 | head -n 5", "allow"},
		{"git log 2>&1 | head -n 5", true, Rules{Ask: []string{"Bash(lx git:*)"}}, "lx --fit head:5 git log 2>&1 | head -n 5", ""},
		{"lx --fit 5 git push 2>&1 | head -n 5", true, Rules{Deny: []string{"Bash(git push:*)"}}, "", "deny"},
	}
	for _, c := range cases {
		rw, dec := EvaluateClaude(c.cmd, project, c.readOnly, c.rules)
		if rw != c.rw || dec != c.dec {
			t.Errorf("EvaluateClaude(%q, ro=%v) = %q %q, want %q %q", c.cmd, c.readOnly, rw, dec, c.rw, c.dec)
		}
	}

	t.Setenv("CLAUDE_PROJECT_DIR", t.TempDir())
	if _, dec := EvaluateClaude("git status", project, true, Rules{}); dec != "allow" {
		t.Errorf("EvaluateClaude under another CLAUDE_PROJECT_DIR: decision %q, want allow", dec)
	}
}

func TestClaudeHookReadOnlyReview(t *testing.T) {
	ro := HookOptions{ReadOnly: true}
	project := withRules(t, "")
	t.Setenv("SHELL", "/bin/bash")

	v := decode(t, runHookWith(t, "claude", payloadIn(`git status | cat -$IFS/etc/passwd`, project, "default", false), ro))
	if v.H.Decision != "" || v.H.Input == nil {
		t.Errorf("$IFS in a pipe filter: decision %q, input %+v", v.H.Decision, v.H.Input)
	}

	const lx = "/Users/John Doe/bin/lx"
	for _, rules := range []string{`{"permissions":{"ask":["Bash(lx:*)"]}}`, `{"permissions":{"deny":["Bash(lx git:*)"]}}`} {
		project := withRules(t, rules)
		v := decode(t, runHookWith(t, "claude", payloadIn("git status", project, "default", false),
			HookOptions{ReadOnly: true, Prefix: lx}))
		if v.H.Decision == "allow" {
			t.Errorf("%s: --readonly approved %q", rules, v.H.Input.Command)
		}
	}
}

func TestNoRewriteWhenLxIsDenied(t *testing.T) {
	for _, rule := range []string{"Bash(lx:*)", "Bash(lx *)", "Bash(lx git status)"} {
		rules := Rules{Deny: []string{rule}}
		o := evaluateWith("git status", evalEnv{Cwd: "/tmp", Root: "/tmp", PermissionMode: "default"},
			func() Rules { return rules }, func(string) string { return "lx" })
		if o.rewritten != "" || o.decision != "" {
			t.Errorf("%s: rewrote to %q (%q); want no output", rule, o.rewritten, o.decision)
		}
	}

	for _, rule := range []string{"Bash(lx:*)", "Bash(lx git status)", "Bash(lx git:*)", "Bash(lx git status *)"} {
		rules := Rules{Deny: []string{rule}}
		o := evaluateWith("git status 2>&1 | tail -5", evalEnv{Cwd: "/tmp", Root: "/tmp", PermissionMode: "default"},
			func() Rules { return rules }, func(string) string { return "/opt/lx/bin/lx" })
		if o.rewritten != "" || o.decision != "" {
			t.Errorf("%s: rewrote to %q (%q); want no output", rule, o.rewritten, o.decision)
		}
	}

	rules := Rules{Deny: []string{"Bash(git push:*)"}}
	o := evaluateWith("git status", evalEnv{Cwd: "/tmp", Root: "/tmp", PermissionMode: "default"},
		func() Rules { return rules }, func(string) string { return "lx" })
	if o.rewritten != "lx git status" {
		t.Errorf("unrelated deny blocked the rewrite: %+v", o)
	}
}
