package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func isolateCodex(t *testing.T, rules string) (home string) {
	t.Helper()
	isolate(t)
	home = t.TempDir()
	t.Setenv("CODEX_HOME", home)
	old, oldMDM := codexSystemDir, codexMDM
	codexSystemDir = t.TempDir()
	mdm := filepath.Join(codexSystemDir, "Managed Preferences", "com.openai.codex.plist")
	codexMDM = func() []string { return []string{mdm} }
	t.Cleanup(func() { codexSystemDir, codexMDM = old, oldMDM })
	if rules != "" {
		writeFile(t, filepath.Join(home, "rules", "default.rules"), rules)
	}
	return home
}

func codexInput(cwd, cmd string) string {
	return `{"session_id":"019a8f3e-7c21-7d10-b5a4-3f1e2d9c0b77","turn_id":"019a8f3e-9e02-7aa1-8c33-6d5b4a2f1e00",` +
		`"transcript_path":"/Users/me/.codex/sessions/2026/09/28/rollout-2026-09-28T10-12-01-019a8f3e.jsonl",` +
		`"cwd":` + string(jsonString(cwd)) + `,"hook_event_name":"PreToolUse","model":"gpt-5.5-codex",` +
		`"permission_mode":"default","tool_name":"Bash","tool_input":{"command":` + string(jsonString(cmd)) + `},` +
		`"tool_use_id":"call_Qx7Lm2"}`
}

func codexRewrite(cmd string) string {
	return `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow",` +
		`"permissionDecisionReason":"lx: condensed output (lx show <id> for full)","updatedInput":{"command":` +
		string(jsonString(cmd)) + `}}}` + "\n"
}

func TestCodexHookGolden(t *testing.T) {
	const gitForbidden = `prefix_rule(
    pattern = ["git", "push"],
    decision = "forbidden",
    justification = "pushes go through review",
)
`
	cases := []struct {
		name, rules, cmd, want string
	}{
		{"no rules: rewrite", ``, `git status`, codexRewrite("lx git status")},
		{"pipeline with a cut", ``, `go test ./... 2>&1 | tail -n 40`, codexRewrite("lx --fit tail:40 go test ./... 2>&1 | tail -n 40")},
		{"unsupported", ``, `echo hi`, ``},
		{"never-rewrite construct", ``, `git status > out.txt`, ``},
		{"a rule names the program: leave it to Codex", gitForbidden, `git status`, ``},
		{"a rule names another program", gitForbidden, `go test ./...`, codexRewrite("lx go test ./...")},
		{"a rule names a wrapped program", gitForbidden, `uv run git status`, ``},
		{"alternatives in the head", `prefix_rule(pattern = [["gh", "go"], "test"], decision = "prompt")`, `go test ./...`, ``},
		{"positional pattern", `prefix_rule(["go"], "prompt")`, `go vet ./...`, ``},
		{"allow rule still keeps lx out", `prefix_rule(pattern=["npm", "test"], decision="allow")`, `npm test`, ``},
		{"host_executable", `host_executable(name = "git", paths = ["/opt/git/bin/git"])`, `/opt/git/bin/git log`, ``},
		{"a rule on lx itself: no rewrites at all", `prefix_rule(pattern=["lx"], decision="allow")`, `go test ./...`, ``},
		{"unreadable head: no rewrites", `GO = "gx"` + "\n" + `prefix_rule(pattern=[GO, "test"], decision="forbidden")`, `git status`, ``},
		{"concatenated head: no rewrites", `prefix_rule(pattern=["g" + "o", "test"])`, `go test ./...`, ``},
		{"comments are not rules", "# prefix_rule(pattern=[\"git\"])\n", `git status`, codexRewrite("lx git status")},
		{"justification words are not programs", `prefix_rule(pattern=["make"], justification="go test is slow")`, `go test ./...`,
			codexRewrite("lx go test ./...")},
		{"model-written lx, named program: deny", gitForbidden, `lx git push origin main`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":` +
				"\"lx: `git push origin main` is named by a Codex rule in RULES, which Codex does not apply through lx; run it without lx\"}}\n"},
		{"model-written lx behind flags: deny", gitForbidden, `lx --fit 5 git push 2>&1 | tail -5`, `deny`},
		{"model-written lx show: nothing to say", `prefix_rule(pattern=["show"])`, `lx show 7`, ``},
		{"model-written lx, no rule: nothing to say", gitForbidden, `lx go test ./...`, ``},
		{"model-written lx, unreadable rules: deny", `prefix_rule(pattern=[X])`, `lx go test ./...`, `deny`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := isolateCodex(t, c.rules)
			got := runHookFor(t, "codex", codexInput(t.TempDir(), c.cmd))
			want := strings.ReplaceAll(c.want, "RULES", filepath.Join(home, "rules", "default.rules"))
			if want == "deny" {
				if !strings.Contains(got, `"permissionDecision":"deny"`) || strings.Contains(got, "updatedInput") {
					t.Errorf("got %s, want a denial", got)
				}
				return
			}
			if got != want {
				t.Errorf("\ngot  %s\nwant %s", got, want)
			}
		})
	}
}

func TestCodexHookRuleLocations(t *testing.T) {
	const rule = `prefix_rule(pattern=["git"], decision="forbidden")`
	home := isolateCodex(t, "")
	repo := t.TempDir()
	cwd := filepath.Join(repo, "svc", "api")
	for name, path := range map[string]string{
		"user":              filepath.Join(home, "rules", "team.rules"),
		"project":           filepath.Join(cwd, ".codex", "rules", "default.rules"),
		"parent project":    filepath.Join(repo, ".codex", "rules", "default.rules"),
		"system":            filepath.Join(codexSystemDir, "rules", "default.rules"),
		"requirements":      filepath.Join(codexSystemDir, "requirements.toml"),
		"not a .rules file": filepath.Join(home, "rules", "notes.txt"),
	} {
		t.Run(name, func(t *testing.T) {
			content := rule
			if strings.HasSuffix(path, ".toml") {
				content = "[rules]\nprefix_rules = [\n  { pattern = [{ token = \"git\" }, { any_of = [\"push\"] }], decision = \"forbidden\" },\n]\n"
			}
			writeFile(t, path, content)
			t.Cleanup(func() { writeFile(t, path, "") })
			got := runHookFor(t, "codex", codexInput(cwd, "git status"))
			if name == "not a .rules file" {
				if got != codexRewrite("lx git status") {
					t.Errorf("a non-.rules file counted: %s", got)
				}
				return
			}
			if got != "" {
				t.Errorf("rewrote a command a %s rule names: %s", name, got)
			}
		})
	}
}

func TestCodexHookManagedProfile(t *testing.T) {
	isolateCodex(t, "")
	writeFile(t, codexMDM()[0], "<plist/>")
	if got := runHookFor(t, "codex", codexInput(t.TempDir(), "git status")); got != "" {
		t.Errorf("rewrote under an MDM-managed Codex policy lx cannot read: %s", got)
	}
	if got := runHookFor(t, "codex", codexInput(t.TempDir(), "lx go test ./...")); !strings.Contains(got, `"deny"`) {
		t.Errorf("model-written lx under an unreadable policy: %s", got)
	}
}

func TestCodexHookNeverApprovesOrAsks(t *testing.T) {
	cmds := []string{
		"git status", "git push origin main", "go test ./... 2>&1 | tail -n 40", "cd web && npm test && git status",
		"lx git push", "lx --fit 9 git push", "rm -rf /tmp/x", "git status && rm -rf /tmp/x", "npm test -- --watch",
		"uv run pytest -x", "env FOO=1 go test ./...", "LX_MODE=verify go test ./...", "git status $(rm -rf /)",
	}
	for _, settings := range []string{``, `{"permissions":{"allow":["Bash(git:*)","Bash(npm test:*)","Bash(lx:*)"],"ask":["Bash(git push:*)"]}}`} {
		for _, rules := range []string{``, `prefix_rule(pattern=["rm"], decision="forbidden")`} {
			isolateCodex(t, rules)
			withRules(t, settings)
			for _, readOnly := range []bool{false, true} {
				for _, cmd := range cmds {
					var out strings.Builder
					if err := HookWith("codex", strings.NewReader(codexInput("/w", cmd)), &out, "/w", HookOptions{ReadOnly: readOnly}); err != nil {
						t.Fatal(err)
					}
					if out.Len() == 0 {
						continue
					}
					var v struct {
						H struct {
							Event    string          `json:"hookEventName"`
							Decision string          `json:"permissionDecision"`
							Reason   string          `json:"permissionDecisionReason"`
							Input    json.RawMessage `json:"updatedInput"`
						} `json:"hookSpecificOutput"`
					}
					dec := json.NewDecoder(strings.NewReader(out.String()))
					dec.DisallowUnknownFields()
					if err := dec.Decode(&v); err != nil {
						t.Fatalf("%q: %v\n%s", cmd, err, out.String())
					}
					var in map[string]string
					json.Unmarshal(v.H.Input, &in)
					switch {
					case v.H.Event != "PreToolUse":
						t.Errorf("%q: event %q", cmd, v.H.Event)
					case v.H.Decision == "allow" && !strings.HasPrefix(in["command"], "lx ") && !strings.Contains(in["command"], " lx "):
						t.Errorf("%q: allow without a rewrite: %s", cmd, out.String())
					case v.H.Decision == "deny" && (strings.TrimSpace(v.H.Reason) == "" || v.H.Input != nil):
						t.Errorf("%q: bad denial: %s", cmd, out.String())
					case v.H.Decision != "allow" && v.H.Decision != "deny":
						t.Errorf("%q: decision %q: %s", cmd, v.H.Decision, out.String())
					}
				}
			}
		}
	}
}

func TestCodexPatternHead(t *testing.T) {
	cases := map[string][]string{
		`prefix_rule(pattern = ["git", "push"])`:                              {"git"},
		`prefix_rule(pattern=[["gh","git"], "push"], decision="prompt")`:      {"gh", "git"},
		`prefix_rule(["npm", "test"])`:                                        {"npm"},
		`prefix_rule(decision="forbidden", pattern=["/usr/bin/curl"])`:        {"/usr/bin/curl"},
		`prefix_rule(pattern=['make', 'deploy'], match=["make deploy prod"])`: {"make"},
		`prefix_rule(pattern=["""go""", "test"])`:                             {"go"},
		`prefix_rule(pattern=["a\"b", "x"])`:                                  {`a"b`},
		`prefix_rule(pattern=[CMD])`:                                          nil,
		`prefix_rule(pattern=["g" + "it"])`:                                   nil,
		`prefix_rule(pattern=[[], "x"])`:                                      nil,
		`prefix_rule(pattern=[["a" + "b", "c"], "x"])`:                        nil,
		`prefix_rule(pattern=[])`:                                             nil,
		`prefix_rule(justification="no pattern")`:                             nil,
	}
	for src, want := range cases {
		toks, _ := starTokens(src, false)
		got, _, ok := patternHead(toks[2:closing(toks, 1)])
		if ok != (want != nil) || !reflect.DeepEqual(got, want) && want != nil {
			t.Errorf("%s: got %q %v, want %q", src, got, ok, want)
		}
	}
}

func TestCodexRulesOnLx(t *testing.T) {
	approved := `prefix_rule(pattern=["lx", "go", "test", "./..."], decision="allow")` + "\n"
	cases := []struct {
		name, rules, cmd, want string
	}{
		{"an approval for an lx command keeps only that program raw", approved, `go test ./...`, ``},
		{"other programs are still rewritten", approved, `git status`, codexRewrite("lx git status")},
		{"a wrapper counts as the program", `prefix_rule(pattern=["lx", "uv"], decision="forbidden")`, `uv run pytest -x`, ``},
		{"the model's lx command is left to that rule", approved, `lx go test ./...`, ``},
		{"alternatives after lx", `prefix_rule(pattern=["lx", ["go", "npm"]])`, `npm test`, ``},
		{"an absolute lx", `prefix_rule(pattern=["/opt/lx/bin/lx", "make"])`, `make`, ``},
		{"lx alone: no rewrites", `prefix_rule(pattern=["lx"], decision="allow")`, `git status`, ``},
		{"lx with a flag: no rewrites", `prefix_rule(pattern=["lx", "--fit"], decision="allow")`, `git status`, ``},
		{"lx with an unreadable second word: no rewrites", `prefix_rule(pattern=["lx", SUB])`, `git status`, ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolateCodex(t, c.rules)
			if got := runHookFor(t, "codex", codexInput(t.TempDir(), c.cmd)); got != c.want {
				t.Errorf("\ngot  %s\nwant %s", got, c.want)
			}
		})
	}
}

func TestCodexRuleStrings(t *testing.T) {
	cases := []struct {
		name, rules string
		rewrite     bool
	}{
		{"hex escape", `prefix_rule(pattern=["\x67it", "push"], decision="forbidden")`, false},
		{"octal escape", `prefix_rule(pattern=["\147it", "push"], decision="forbidden")`, false},
		{"unicode escape", `prefix_rule(pattern=["\u0067it"], decision="forbidden")`, false},
		{"escaped quote", `prefix_rule(pattern=["g\"it"])`, true},
		{"bad escape: unreadable", `prefix_rule(pattern=["\qmake"])`, false},
		{"unterminated string: unreadable", `prefix_rule(pattern=["make`, false},
		{"raw string keeps its backslashes", `prefix_rule(pattern=["make"], match=[r"make \d"])`, true},
		{"raw head: unreadable", `prefix_rule(pattern=[r"git"])`, false},
		{"f-string head: unreadable", `prefix_rule(pattern=[f"git"])`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isolateCodex(t, c.rules)
			got := runHookFor(t, "codex", codexInput(t.TempDir(), "git status"))
			if (got != "") != c.rewrite {
				t.Errorf("rules %s: got %q", c.rules, got)
			}
		})
	}
}

func TestCodexRequirementsFiles(t *testing.T) {
	const rule = "[rules]\nprefix_rules = [{ pattern = [{ token = \"git\" }], decision = \"forbidden\" }]\n"
	for name, c := range map[string]struct{ file, content string }{
		"requirements.toml":                     {"requirements.toml", rule},
		"legacy managed_config.toml":            {"managed_config.toml", rule},
		"literal string ending in a backslash":  {"requirements.toml", "paths = ['C:\\tools\\']\n" + rule + "note = \"it's ok\" # \"\n"},
		"multi-line literal string":             {"requirements.toml", "note = '''a \\'''\n" + rule},
		"line-ending backslash in basic string": {"requirements.toml", "note = \"\"\"a \\\n  b\"\"\"\n" + rule},
	} {
		t.Run(name, func(t *testing.T) {
			isolateCodex(t, "")
			writeFile(t, filepath.Join(codexSystemDir, c.file), c.content)
			if got := runHookFor(t, "codex", codexInput(t.TempDir(), "git status")); got != "" {
				t.Errorf("rewrote a command %s forbids: %s", c.file, got)
			}
		})
	}
}

func TestCodexUnreadableRules(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads everything")
	}
	home := isolateCodex(t, `prefix_rule(pattern=["make"])`)
	dir := filepath.Join(home, "rules")
	os.Chmod(dir, 0)
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	if got := runHookFor(t, "codex", codexInput(t.TempDir(), "git status")); got != "" {
		t.Errorf("rewrote with an unreadable rules directory: %s", got)
	}
	if got := runHookFor(t, "codex", codexInput(t.TempDir(), "lx git status")); !strings.Contains(got, `"deny"`) {
		t.Errorf("model-written lx with an unreadable rules directory: %s", got)
	}
	os.Chmod(dir, 0o755)
	os.Chmod(filepath.Join(dir, "default.rules"), 0)
	if got := runHookFor(t, "codex", codexInput(t.TempDir(), "git status")); got != "" {
		t.Errorf("rewrote with an unreadable rules file: %s", got)
	}
	os.Chmod(filepath.Join(dir, "default.rules"), 0o644)
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, ".codex"), "not a directory")
	if got := runHookFor(t, "codex", codexInput(cwd, "git status")); got != codexRewrite("lx git status") {
		t.Errorf("a .codex file stopped rewrites: %q", got)
	}
}
