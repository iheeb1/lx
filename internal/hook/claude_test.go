package hook

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withRules isolates settings and installs a project settings.json.
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

// Without CLAUDE_PROJECT_DIR the payload's cwd locates the project.
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
		// Copilot never gets "ask" (it would force a blocking dialog).
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
