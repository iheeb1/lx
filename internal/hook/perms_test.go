package hook

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// isolate points every settings location at empty temp dirs so tests never
// read the developer's real Claude Code configuration.
func isolate(t *testing.T) (project, user string) {
	t.Helper()
	project, user = t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv("CLAUDE_CONFIG_DIR", user)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LX_HOOK", "")
	old := managedPath
	managedPath = ""
	t.Cleanup(func() { managedPath = old })
	return project, user
}

func writeFile(t testing.TB, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pattern, cmd    string
		strict, lenient bool
	}{
		{"*", "anything at all", true, true},
		{"git push:*", "git push", true, true},
		{"git push:*", "git push origin main", true, true},
		{"git push:*", "git   push  origin", true, true},
		{"git push:*", "git pushx", false, false},
		{"git push:*", "git pull", false, false},
		{":*", "rm -rf /", true, true},
		{"git push --force", "git push --force", true, true},
		{"git push --force", "git push --force origin", false, false},
		{" git status ", "git status", true, true},
		{"git * main", "git push origin main", true, true},
		{"git * main", "git push origin dev", false, false},
		{"* --force", "git push --force", true, true},
		{"npm run *", "npm run build", true, true},
		{"npm run *", "npm run", false, true},
		{"git -C * push:*", "git -C /x push origin", true, true},
		{"git -C * push:*", "git -C /x push", true, true},
		{"git -C * push:*", "git -C /x pull", false, false},
		{"", "git status", false, false},
	}
	for _, c := range cases {
		if got := matchPattern(c.pattern, c.cmd, false); got != c.strict {
			t.Errorf("strict match(%q, %q) = %v", c.pattern, c.cmd, got)
		}
		if got := matchPattern(c.pattern, c.cmd, true); got != c.lenient {
			t.Errorf("lenient match(%q, %q) = %v", c.pattern, c.cmd, got)
		}
	}
}

func TestBashPattern(t *testing.T) {
	for rule, want := range map[string]string{
		"Bash": "*", "Bash(*)": "*", "Bash(git push:*)": "git push:*", " Bash( git status ) ": "git status",
	} {
		if got, ok := bashPattern(rule); !ok || got != want {
			t.Errorf("bashPattern(%q) = %q, %v", rule, got, ok)
		}
	}
	for _, rule := range []string{"Read(**/.env)", "Bash()", "WebFetch", "bash(git status)", "Bash(x"} {
		if _, ok := bashPattern(rule); ok {
			t.Errorf("bashPattern(%q) accepted", rule)
		}
	}
}

func TestDecide(t *testing.T) {
	r := Rules{
		Allow: []string{"Bash(git:*)", "Bash(npm test:*)", "Bash(go test:*)"},
		Ask:   []string{"Bash(git push:*)"},
		Deny:  []string{"Bash(git push --force:*)", "Bash(rm:*)"},
	}
	cases := []struct{ cmd, want string }{
		// precedence deny > ask > allow
		{"git push --force origin", "deny"},
		{"git push origin", "ask"},
		{"git status", "allow"},
		{"curl example.com", ""},
		{"", ""},

		// every segment must be allowed; any deny/ask wins
		{"git status && npm test", "allow"},
		{"git status && curl x", ""},
		{"git status && rm -rf /tmp/x", "deny"},
		{"npm test; git push", "ask"},
		{"git status & rm -rf /tmp/x", "deny"},

		// env assignments and wrappers
		{"FOO=1 git push --force", "deny"},
		{"timeout 60 rm -rf x", "deny"},
		{"env A=1 rm x", "deny"},
		{"timeout 60 git status", "allow"},
		{"LD_PRELOAD=/tmp/evil.so git status", ""},
		{"env LD_PRELOAD=x git status", ""},

		// redirects: only 2>&1 is transparent for allow
		{"git status 2>&1", "allow"},
		{"git status > /etc/passwd", ""},
		{"rm x 2>/dev/null", "deny"},

		// cd into a relative subdirectory needs no rule of its own
		{"cd sub/dir && git status", "allow"},
		{"cd /etc && git status", ""},
		{"cd ../other && git status", ""},
		{"cd ~ && git status", ""},

		// head/tail/cat reading only the pipe need no rule of their own
		{"git log | head -5", "allow"},
		{"git log 2>&1 | tail -n +30", "allow"},
		{"git log | cat", "allow"},
		{"git log | head -5 /etc/shadow", ""},
		{"git log | tail -5 > out", ""},
		{"cd sub", ""},
		{"cd sub && head -3", ""},

		// model-written lx is judged by what it runs
		{"lx git push --force", "deny"},
		{"lx -v git push", "ask"},
		{"lx --budget 100 git push --force", "deny"},
		{"lx --unknown-flag val git push --force", "deny"},
		{"/usr/local/bin/lx rm -rf /", "deny"},
		{"lx git status", "allow"},
		{"lx --unknown-flag git status", ""},

		// unparseable commands are never allowed, but deny still sees inside
		{"git status $(true)", ""},
		{"echo $(git push --force)", "deny"},
		{"git status\ngit log", ""},
		{"git status\nrm -rf x", "deny"},
	}
	for _, c := range cases {
		if got := r.Decide(c.cmd); got != c.want {
			t.Errorf("Decide(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
	if got := (Rules{}).Decide("rm -rf /"); got != "" {
		t.Errorf("empty rules: %q", got)
	}
	if got := (Rules{Allow: []string{"Bash"}}).Decide("anything goes"); got != "allow" {
		t.Errorf("bare Bash rule: %q", got)
	}
}

func TestDecideReportsRule(t *testing.T) {
	r := Rules{Deny: []string{"Bash(git push:*)"}}
	v, rule, subject := r.decide("cd x && FOO=1 git push origin")
	if v != "deny" || rule != "Bash(git push:*)" || subject == "" {
		t.Errorf("decide = %q %q %q", v, rule, subject)
	}
}

func TestLoadClaudeRules(t *testing.T) {
	project, user := isolate(t)
	writeFile(t, filepath.Join(project, ".claude", "settings.json"), `{
		"permissions": {"allow": ["Bash(git status:*)", "Read(**)", 42], "deny": ["Bash(rm:*)"]},
		"hooks": {}
	}`)
	writeFile(t, filepath.Join(project, ".claude", "settings.local.json"), `{"permissions": {"ask": ["Bash(git push:*)"]}}`)
	writeFile(t, filepath.Join(user, "settings.json"), `{"permissions": {"deny": ["Bash(curl:*)"], "allow": ["WebFetch"]}}`)
	writeFile(t, filepath.Join(user, "settings.local.json"), `{not json`)

	nested := filepath.Join(project, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got := LoadClaudeRules(nested)
	want := Rules{
		Allow: []string{"Bash(git status:*)"},
		Ask:   []string{"Bash(git push:*)"},
		Deny:  []string{"Bash(rm:*)", "Bash(curl:*)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rules = %+v\nwant   %+v", got, want)
	}

	// CLAUDE_PROJECT_DIR (set by Claude Code for hooks) wins over the walk.
	other := t.TempDir()
	writeFile(t, filepath.Join(other, ".claude", "settings.json"), `{"permissions": {"allow": ["Bash(make:*)"]}}`)
	t.Setenv("CLAUDE_PROJECT_DIR", other)
	got = LoadClaudeRules(nested)
	if !reflect.DeepEqual(got.Allow, []string{"Bash(make:*)"}) {
		t.Errorf("CLAUDE_PROJECT_DIR ignored: %+v", got)
	}
}

func TestLoadClaudeRulesManaged(t *testing.T) {
	_, _ = isolate(t)
	managed := filepath.Join(t.TempDir(), "managed-settings.json")
	writeFile(t, managed, `{"permissions": {"deny": ["Bash(git push:*)"]}}`)
	managedPath = managed
	if got := LoadClaudeRules(t.TempDir()); !reflect.DeepEqual(got.Deny, []string{"Bash(git push:*)"}) {
		t.Errorf("managed deny not loaded: %+v", got)
	}
}

func TestLxInner(t *testing.T) {
	get := func(cmd string) []string {
		a := analyze(cmd)
		s := a.segs[0]
		var out []string
		for _, c := range lxInner(s.words[s.cmdIdx:]) {
			out = append(out, c.text)
		}
		return out
	}
	for cmd, want := range map[string][]string{
		"lx git status":              {"git status"},
		"lx -r -v git status":        {"git status"},
		"lx -b 500 go test":          {"go test"},
		"lx --budget=5 -- go test":   {"go test"},
		"lx --what x git push":       {"x git push", "git push", "push"},
		`lx git commit -m "a b"`:     {`git commit -m "a b"`},
		"lx":                         nil,
		"FOO=1 timeout 5 lx make up": {"make up"},
	} {
		if got := get(cmd); !reflect.DeepEqual(got, want) {
			t.Errorf("lxInner(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func FuzzDecide(f *testing.F) {
	for _, s := range []string{"lx -b", "lx --x", "cd", "git push && lx", "FOO=1 lx -- rm", "a | head -", "lx $(x)"} {
		f.Add(s)
	}
	r := Rules{
		Allow: []string{"Bash(git:*)", "Bash(lx *)", "Bash"},
		Ask:   []string{"Bash(git push *)", "Bash(*--force*)"},
		Deny:  []string{"Bash(rm:*)", "Bash(* -rf *)"},
	}
	cwd := f.TempDir()
	f.Fuzz(func(t *testing.T, s string) {
		switch r.Decide(s) {
		case "", "allow", "ask", "deny":
		default:
			t.Fatal("bad verdict")
		}
		_ = evaluate(s, evalEnv{}, func() Rules { return r })
		// Parity never turns a user deny into anything but "stay out".
		if o := evaluate(s, evalEnv{ReadOnly: true, Cwd: cwd, Root: cwd}, func() Rules { return r }); o.readOnly && r.Decide(s) != "" {
			t.Fatalf("parity decided %q although the rules say %q", s, r.Decide(s))
		}
	})
}

func TestLoadClaudeRulesDirs(t *testing.T) {
	project, user := isolate(t)
	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(project, ".claude", "settings.json"), `{"permissions": {
		"additionalDirectories": ["../docs", "~/notes", "/abs/lib", 42, {"x":1}, "", "~"]}}`)
	writeFile(t, filepath.Join(project, ".claude", "settings.local.json"),
		`{"permissions": {"additionalDirectories": ["/abs/lib", "vendor/"]}}`)
	// $CLAUDE_CONFIG_DIR is not a .claude folder: relative entries there are skipped.
	writeFile(t, filepath.Join(user, "settings.json"), `{"permissions": {"additionalDirectories": ["rel", "/u/abs"]}}`)
	t.Setenv("CLAUDE_PROJECT_DIR", project)
	got := LoadClaudeRules(project).Dirs
	want := []string{filepath.Join(filepath.Dir(project), "docs"), filepath.Join(home, "notes"), "/abs/lib",
		home, filepath.Join(project, "vendor"), "/u/abs"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Dirs = %q\nwant   %q", got, want)
	}
	// Dirs alone don't make rules non-empty (Decide still says nothing).
	if (Rules{Dirs: []string{"/x"}}).Decide("git status") != "" {
		t.Error("Dirs changed a decision")
	}
}

// Relative additionalDirectories count only in project settings: in user
// settings what they are relative to is unsettled, and a guess could widen
// what --readonly approves.
func TestLoadClaudeRulesDirsUserRelative(t *testing.T) {
	_, _ = isolate(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	writeFile(t, filepath.Join(home, ".claude", "settings.json"),
		`{"permissions": {"additionalDirectories": ["../shared", "/u/abs"]}}`)
	// A project with no .claude of its own finds ~/.claude: still user settings.
	work := filepath.Join(home, "work", "proj")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := LoadClaudeRules(work).Dirs; !reflect.DeepEqual(got, []string{"/u/abs"}) {
		t.Errorf("Dirs = %q, want only /u/abs", got)
	}
	t.Setenv("CLAUDE_PROJECT_DIR", work)
	if got := LoadClaudeRules(work).Dirs; !reflect.DeepEqual(got, []string{"/u/abs"}) {
		t.Errorf("with CLAUDE_PROJECT_DIR: Dirs = %q, want only /u/abs", got)
	}
}
