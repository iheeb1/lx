package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func initAt(t *testing.T, dir string, o InitOptions) (string, error) {
	t.Helper()
	var out bytes.Buffer
	o.ConfigDir, o.Out = dir, &out
	if o.LxPath == "" && !o.Uninstall {
		o.LxPath = "/opt/lx/bin/lx"
	}
	err := InitClaude(o)
	return out.String(), err
}

// keys returns the top-level keys of a JSON object in file order.
func keys(t *testing.T, data string) []string {
	t.Helper()
	o, err := parseObject([]byte(data))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, data)
	}
	var ks []string
	for _, m := range o.members {
		ks = append(ks, m.key)
	}
	return ks
}

func TestInitFresh(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".claude")
	msg, err := initAt(t, dir, InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	want := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "/opt/lx/bin/lx hook claude"
          }
        ]
      }
    ]
  }
}
`
	if got := readFile(t, path); got != want {
		t.Errorf("settings.json:\n%s\nwant:\n%s", got, want)
	}
	fi, _ := os.Stat(path)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Error("backup written for a new file")
	}
	if !strings.Contains(msg, "installed") {
		t.Errorf("message: %q", msg)
	}
}

const otherTools = `{
    "$schema": "https://json.schemastore.org/claude-code-settings.json",
    "permissions": {"allow": ["Bash(npm test:*)"], "deny": ["Read(./.env)"]},
    "hooks": {
        "PostToolUse": [{"matcher": "Edit", "hooks": [{"type": "command", "command": "prettier --write"}]}],
        "PreToolUse": [
            {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/local/bin/audit-bash", "timeout": 5}]},
            {"matcher": "Write", "hooks": [{"type": "command", "command": "guard.sh"}]}
        ]
    },
    "model": "opus",
    "futureSetting": {"n": 1.50, "s": "caf\u00e9 <&>", "list": [true, null]}
}`

func TestInitPreservesOtherTools(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(otherTools), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := initAt(t, dir, InitOptions{}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)

	// Key order and every foreign value survive; only lx's group is added.
	if k := keys(t, got); !reflect.DeepEqual(k, []string{"$schema", "permissions", "hooks", "model", "futureSetting"}) {
		t.Errorf("key order = %q", k)
	}
	for _, s := range []string{`"n": 1.50`, `"s": "caf\u00e9 <&>"`, `"command": "/usr/local/bin/audit-bash"`,
		`"timeout": 5`, `"command": "guard.sh"`, `"command": "prettier --write"`, `"deny": [`} {
		if !strings.Contains(got, s) {
			t.Errorf("lost %s in:\n%s", s, got)
		}
	}
	var v struct {
		Hooks struct {
			Pre []struct {
				Matcher string
				Hooks   []struct{ Command string }
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatal(err)
	}
	pre := v.Hooks.Pre
	if len(pre) != 3 || pre[0].Hooks[0].Command != "/usr/local/bin/audit-bash" || pre[1].Matcher != "Write" ||
		pre[2].Matcher != "Bash" || pre[2].Hooks[0].Command != "/opt/lx/bin/lx hook claude" {
		t.Errorf("PreToolUse = %+v", pre)
	}
	// Backup holds the previous bytes; mode is preserved.
	if b := readFile(t, path+".bak"); b != otherTools {
		t.Errorf("backup differs from the original")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode changed to %v", fi.Mode().Perm())
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("dir has %d entries", len(entries))
	}
}

func TestInitIdempotentAndPathUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if _, err := initAt(t, dir, InitOptions{}); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, path)
	msg, err := initAt(t, dir, InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "already installed") || readFile(t, path) != first {
		t.Errorf("re-run changed the file or said %q", msg)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Error("no-op run wrote a backup")
	}

	// A moved binary updates the existing entry instead of adding one.
	if _, err := initAt(t, dir, InitOptions{LxPath: "/Users/Jane Doe/bin/lx"}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if strings.Count(got, "hook claude") != 1 || !strings.Contains(got, `"command": "'/Users/Jane Doe/bin/lx' hook claude"`) {
		t.Errorf("path not updated in place:\n%s", got)
	}
	// ...and the quoted form is recognized on the next run.
	if msg, _ := initAt(t, dir, InitOptions{LxPath: "/Users/Jane Doe/bin/lx"}); !strings.Contains(msg, "already installed") {
		t.Errorf("quoted path not recognized: %q", msg)
	}
}

func TestInitUninstall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	// lx shares a group with another tool's hook, and also has its own group.
	orig := `{"hooks": {"PreToolUse": [
		{"matcher": "Bash", "hooks": [{"type": "command", "command": "other-tool check"}, {"type": "command", "command": "lx hook claude"}]},
		{"matcher": "Bash", "hooks": [{"type": "command", "command": "/old/path/lx hook claude"}]},
		{"matcher": "Edit", "hooks": [{"type": "command", "command": "lint.sh"}]}
	], "Stop": [{"hooks": [{"type": "command", "command": "notify"}]}]}, "model": "x"}`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := initAt(t, dir, InitOptions{Uninstall: true}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if strings.Contains(got, "hook claude") {
		t.Errorf("lx hook still present:\n%s", got)
	}
	for _, s := range []string{"other-tool check", "lint.sh", "notify", `"model": "x"`} {
		if !strings.Contains(got, s) {
			t.Errorf("uninstall removed %q:\n%s", s, got)
		}
	}
	var v map[string]map[string][]map[string]any
	_ = json.Unmarshal([]byte(got), &v)
	if n := len(v["hooks"]["PreToolUse"]); n != 2 {
		t.Errorf("want 2 groups left (shared + Edit), got %d:\n%s", n, got)
	}

	// Nothing left to remove: report it, don't touch the file.
	before := readFile(t, path)
	msg, err := initAt(t, dir, InitOptions{Uninstall: true})
	if err != nil || !strings.Contains(msg, "no lx hook") || readFile(t, path) != before {
		t.Errorf("second uninstall: %q %v", msg, err)
	}
}

func TestInitUninstallRemovesEmptyContainers(t *testing.T) {
	dir := t.TempDir()
	if _, err := initAt(t, dir, InitOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := initAt(t, dir, InitOptions{Uninstall: true}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "settings.json")); got != "{}\n" {
		t.Errorf("got %q", got)
	}
}

func TestInitRefusesSymlinks(t *testing.T) {
	real := t.TempDir()
	target := filepath.Join(real, "settings.json")
	if err := os.WriteFile(target, []byte(`{"model":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// settings.json is a symlink
	dir := t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, "settings.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := initAt(t, dir, InitOptions{}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("err = %v", err)
	}
	// the .claude directory itself is a symlink
	link := filepath.Join(t.TempDir(), ".claude")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := initAt(t, link, InitOptions{}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("err = %v", err)
	}
	if got := readFile(t, target); got != `{"model":"x"}` {
		t.Errorf("target modified: %s", got)
	}
}

func TestInitDryRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".claude")
	msg, err := initAt(t, dir, InitOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, `"command": "/opt/lx/bin/lx hook claude"`) || !strings.Contains(msg, "dry run") {
		t.Errorf("dry run output: %s", msg)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("dry run created files")
	}
}

func TestInitRejectsBadFiles(t *testing.T) {
	for name, content := range map[string]string{
		"invalid json":     `{"hooks": `,
		"not an object":    `[1,2]`,
		"hooks not object": `{"hooks": []}`,
		"pre not array":    `{"hooks": {"PreToolUse": {}}}`,
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := initAt(t, dir, InitOptions{}); err == nil {
			t.Errorf("%s: no error", name)
		}
		if readFile(t, path) != content {
			t.Errorf("%s: file modified", name)
		}
	}
	// An empty file is treated as {}.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := initAt(t, dir, InitOptions{}); err != nil {
		t.Errorf("empty file: %v", err)
	}
}

func TestInitWarnsAboutRtk(t *testing.T) {
	dir := t.TempDir()
	orig := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"rtk hook claude"}]}]}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	msg, err := initAt(t, dir, InitOptions{})
	if err != nil || !strings.Contains(msg, "warning") || !strings.Contains(msg, "rtk hook claude") {
		t.Errorf("msg=%q err=%v", msg, err)
	}
}

func TestInitGlobalUsesConfigDir(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "claude-profile")
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	var out bytes.Buffer
	if err := InitClaude(InitOptions{Global: true, LxPath: "/bin/lx", Out: &out}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, filepath.Join(cfg, "settings.json")), "/bin/lx hook claude") {
		t.Error("hook not written to $CLAUDE_CONFIG_DIR")
	}
	if fi, _ := os.Stat(cfg); fi.Mode().Perm() != 0o700 {
		t.Errorf("global dir mode %v", fi.Mode().Perm())
	}
}

func TestIsLxHookCommand(t *testing.T) {
	for cmd, want := range map[string]bool{
		"lx hook claude": true, "/usr/local/bin/lx hook claude": true, `'/a b/lx' hook claude`: true,
		"rtk hook claude": false, "lx hook claude --debug": false, "lx hook gemini": false, "": false,
		"/bin/lxx hook claude": false,
	} {
		if got := isLxHookCommand(cmd); got != want {
			t.Errorf("isLxHookCommand(%q) = %v", cmd, got)
		}
	}
}

func TestSnippet(t *testing.T) {
	for _, agent := range []string{"agents-md", "codex", "claude", "copilot", "gemini", "cursor"} {
		s, err := Snippet(agent, "/opt/lx/bin/lx")
		if err != nil || !strings.Contains(s, "/opt/lx/bin/lx") {
			t.Errorf("%s: %v\n%s", agent, err, s)
		}
		if i := strings.Index(s, "\n{"); i >= 0 {
			if !json.Valid([]byte(s[i+1:])) {
				t.Errorf("%s: JSON part does not parse:\n%s", agent, s[i+1:])
			}
		}
	}
	if s, _ := Snippet("claude", "/Users/Jane Doe/lx"); !strings.Contains(s, `'/Users/Jane Doe/lx' hook claude`) {
		t.Errorf("path not shell-quoted:\n%s", s)
	}
	if _, err := Snippet("emacs", "lx"); err == nil {
		t.Error("unknown agent accepted")
	}
}
