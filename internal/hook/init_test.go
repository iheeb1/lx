package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

	if b := readFile(t, path+".bak"); b != otherTools {
		t.Errorf("backup differs from the original")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode changed to %v", fi.Mode().Perm())
	}

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

	if _, err := initAt(t, dir, InitOptions{LxPath: "/Users/Jane Doe/bin/lx"}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	if strings.Count(got, "hook claude") != 1 || !strings.Contains(got, `"command": "'/Users/Jane Doe/bin/lx' hook claude"`) {
		t.Errorf("path not updated in place:\n%s", got)
	}

	if msg, _ := initAt(t, dir, InitOptions{LxPath: "/Users/Jane Doe/bin/lx"}); !strings.Contains(msg, "already installed") {
		t.Errorf("quoted path not recognized: %q", msg)
	}
}

func TestInitUninstall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")

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

	dir := t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, "settings.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := initAt(t, dir, InitOptions{}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("err = %v", err)
	}

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
		"/bin/lxx hook claude":      false,
		"lx hook claude --readonly": true, "lx hook claude --prefix /x/lx": true, `lx hook claude --prefix='/a b/lx'`: true,
		"lx hook claude --readonly --prefix /x/lx": true, "lx hook claude --prefix": false,
		"lx hook claude --readonly --debug": false, "lx hook claude --readonly && rm -rf x": false,
		"lx hook claude --prefix $HOME/lx": true, "lx hook claude; other": false, "$HOME/go/bin/lx hook claude": true,
		"$(which lx) hook claude": false,
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

func hookCmd(t *testing.T, path string) string {
	t.Helper()
	var v struct {
		Hooks struct {
			Pre []struct {
				Hooks []struct{ Command string }
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(readFile(t, path)), &v); err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, g := range v.Hooks.Pre {
		for _, h := range g.Hooks {
			if isLxHookCommand(h.Command) {
				found = append(found, h.Command)
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("lx hooks = %q", found)
	}
	return found[0]
}

func fakeShell(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fakesh")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProbeShellLx(t *testing.T) {

	sh := fakeShell(t, `[ "$1" = "-lic" ] && [ "$2" = "command -v lx" ] || exit 3
echo "Welcome to your shell"
if read -r line; then echo /from/stdin; fi
echo "/opt/lx/bin/lx  "`)
	if got, err := ProbeShellLx(sh, 5*time.Second); err != nil || got != "/opt/lx/bin/lx" {
		t.Errorf("probe = %q, %v", got, err)
	}

	if got, err := ProbeShellLx(fakeShell(t, "exit 1"), 5*time.Second); err != nil || got != "" {
		t.Errorf("not found: %q, %v", got, err)
	}

	if got, err := ProbeShellLx(fakeShell(t, "echo \"alias lx='ls -x'\""), 5*time.Second); err != nil || got != "" {
		t.Errorf("alias: %q, %v", got, err)
	}

	start := time.Now()
	got, err := ProbeShellLx(fakeShell(t, "sleep 10 &\nsleep 10"), 300*time.Millisecond)
	if err == nil || got != "" || time.Since(start) > 3*time.Second {
		t.Errorf("hang: %q, %v after %v", got, err, time.Since(start))
	}
	if _, err := ProbeShellLx(filepath.Join(t.TempDir(), "nosuchshell"), time.Second); err == nil {
		t.Error("missing shell: no error")
	}

	if _, err := ProbeShellLx("", 5*time.Second); err != nil {
		t.Errorf("/bin/sh: %v", err)
	}
}

func TestInitProbe(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	lx := filepath.Join(home, "go", "bin", "lx")
	cases := []struct {
		name, script string
		wantCmd      string
		wantMsg      string
	}{
		{"same file", "echo " + lx, lx + " hook claude", ""},
		{"not on PATH", "exit 1", lx + " hook claude --prefix " + lx,
			"lx: lx is not on your shell's PATH; rewritten commands will call " + lx +
				" directly. To use plain lx: export PATH=$HOME/go/bin:$PATH"},
		{"another lx", "echo /usr/local/bin/lx", lx + " hook claude --prefix " + lx,
			"lx: your shell's lx is /usr/local/bin/lx, not this binary; rewritten commands will call " + lx + " directly"},
		{"timeout", "sleep 10", lx + " hook claude --prefix " + lx,
			"lx: could not check your shell's PATH (fakesh did not answer within 300ms); rewritten commands will call " + lx + " directly"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			sh := fakeShell(t, c.script)
			timeout := 10 * time.Second
			if c.name == "timeout" {
				timeout = 300 * time.Millisecond
			}
			msg, err := initAt(t, dir, InitOptions{LxPath: lx,
				Probe: func() (string, error) { return ProbeShellLx(sh, timeout) }})
			if err != nil {
				t.Fatal(err)
			}
			if got := hookCmd(t, filepath.Join(dir, "settings.json")); got != c.wantCmd {
				t.Errorf("command = %q, want %q", got, c.wantCmd)
			}
			if c.wantMsg != "" && !strings.Contains(msg, c.wantMsg+"\n") {
				t.Errorf("message:\n%s\nwant line:\n%s", msg, c.wantMsg)
			}
			if c.wantMsg == "" && strings.Contains(msg, "PATH") {
				t.Errorf("unexpected PATH message:\n%s", msg)
			}
			if !strings.Contains(msg, ReadOnlyTip) {
				t.Errorf("plain install without the --readonly tip:\n%s", msg)
			}
		})
	}

	dir := t.TempDir()
	spaced := "/Users/Jane Doe/bin/lx"
	if _, err := initAt(t, dir, InitOptions{LxPath: spaced, Probe: func() (string, error) { return "", nil }}); err != nil {
		t.Fatal(err)
	}
	want := `'/Users/Jane Doe/bin/lx' hook claude --prefix '/Users/Jane Doe/bin/lx'`
	if got := hookCmd(t, filepath.Join(dir, "settings.json")); got != want {
		t.Errorf("command = %q, want %q", got, want)
	}
	if f, ok := parseLxHook(want); !ok || f.prefix != spaced {
		t.Errorf("parseLxHook = %+v, %v", f, ok)
	}

	msg, _ := initAt(t, t.TempDir(), InitOptions{Probe: func() (string, error) { return "", errors.New("boom") }})
	if !strings.Contains(msg, "could not check your shell's PATH (boom)") {
		t.Errorf("message: %s", msg)
	}
}

func TestInitReadOnlyFlagPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	msg, err := initAt(t, dir, InitOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := hookCmd(t, path); got != "/opt/lx/bin/lx hook claude --readonly" {
		t.Errorf("--readonly: %q", got)
	}
	if strings.Contains(msg, "tip:") {
		t.Errorf("tip printed with --readonly: %s", msg)
	}

	before := readFile(t, path)
	msg, _ = initAt(t, dir, InitOptions{})
	if readFile(t, path) != before || !strings.Contains(msg, "already installed") || strings.Contains(msg, "tip:") {
		t.Errorf("plain re-run: %s\n%s", msg, readFile(t, path))
	}

	if _, err := initAt(t, dir, InitOptions{LxPath: "/new/lx"}); err != nil {
		t.Fatal(err)
	}
	if got := hookCmd(t, path); got != "/new/lx hook claude --readonly" {
		t.Errorf("moved binary: %q", got)
	}

	msg, _ = initAt(t, dir, InitOptions{LxPath: "/new/lx", NoReadOnly: true})
	if got := hookCmd(t, path); got != "/new/lx hook claude" {
		t.Errorf("--no-readonly: %q", got)
	}
	if !strings.Contains(msg, ReadOnlyTip) {
		t.Errorf("no tip after --no-readonly: %s", msg)
	}

	if _, err := initAt(t, dir, InitOptions{LxPath: "/new/lx", ReadOnly: true, Prefix: "/new/lx"}); err != nil {
		t.Fatal(err)
	}
	if got := hookCmd(t, path); got != "/new/lx hook claude --readonly --prefix /new/lx" {
		t.Errorf("both: %q", got)
	}
}

func TestInitRoundTrip(t *testing.T) {
	root, err := parseObject([]byte(otherTools))
	if err != nil {
		t.Fatal(err)
	}
	canonical := string(root.indented())
	for name, o := range map[string]InitOptions{
		"plain":    {},
		"readonly": {ReadOnly: true},
		"prefix":   {Prefix: "/Users/Jane Doe/lx"},
		"both":     {ReadOnly: true, Probe: func() (string, error) { return "", nil }},
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, "settings.json")
		if err := os.WriteFile(path, []byte(canonical), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := initAt(t, dir, o); err != nil {
			t.Fatal(err)
		}
		if readFile(t, path) == canonical {
			t.Fatalf("%s: install changed nothing", name)
		}
		if _, err := initAt(t, dir, InitOptions{Uninstall: true}); err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, path); got != canonical {
			t.Errorf("%s: round trip changed the file:\n%s\nwant:\n%s", name, got, canonical)
		}
	}

	dir := t.TempDir()
	if _, err := initAt(t, dir, InitOptions{ReadOnly: true, Prefix: "/x/lx"}); err != nil {
		t.Fatal(err)
	}
	if _, err := initAt(t, dir, InitOptions{Uninstall: true}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "settings.json")); got != "{}\n" {
		t.Errorf("got %q", got)
	}
}

func TestInitLeavesLookalikes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	orig := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"lx hook claude --debug"},` +
		`{"type":"command","command":"lx hook claude --readonly && notify"}]}]}}`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := initAt(t, dir, InitOptions{Uninstall: true}); err != nil {
		t.Fatal(err)
	}
	if readFile(t, path) != orig {
		t.Error("uninstall touched a user hook")
	}
	if _, err := initAt(t, dir, InitOptions{}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, path)
	for _, s := range []string{"lx hook claude --debug", "lx hook claude --readonly \u0026\u0026 notify", "/opt/lx/bin/lx hook claude\""} {
		if !strings.Contains(got, s) && !strings.Contains(got, strings.ReplaceAll(s, "\\u0026", "&")) {
			t.Errorf("missing %s in:\n%s", s, got)
		}
	}
}

func TestInitPrefixNotNamedLx(t *testing.T) {
	for _, c := range []struct {
		probe func() (string, error)
		msg   string
	}{
		{func() (string, error) { return "", nil }, "which is not on your shell's PATH"},
		{func() (string, error) { return "/usr/local/bin/lx", nil }, "which your shell finds at /usr/local/bin/lx"},
		{func() (string, error) { return "", errors.New("boom") }, "could not check your shell's PATH (boom)"},
	} {
		dir := t.TempDir()
		msg, err := initAt(t, dir, InitOptions{LxPath: "/opt/bin/lx-dev", Probe: c.probe})
		if err != nil {
			t.Fatal(err)
		}
		if got := readFile(t, filepath.Join(dir, "settings.json")); strings.Contains(got, "--prefix") {
			t.Errorf("--prefix written for lx-dev:\n%s", got)
		}
		if !strings.Contains(msg, "named lx-dev, not lx") || !strings.Contains(msg, c.msg) {
			t.Errorf("message:\n%s\nwant %q", msg, c.msg)
		}
	}
}
