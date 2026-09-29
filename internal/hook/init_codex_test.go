package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func initCodexAt(t *testing.T, dir string, o InitOptions) (string, error) {
	t.Helper()
	var out bytes.Buffer
	o.ConfigDir, o.Out = dir, &out
	if o.LxPath == "" && !o.Uninstall {
		o.LxPath = "/opt/lx/bin/lx"
	}
	err := InitCodex(o)
	return out.String(), err
}

func sameJSON(t *testing.T, a, b string) bool {
	t.Helper()
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		t.Fatalf("invalid JSON:\n%s\n%s", a, b)
	}
	return reflect.DeepEqual(x, y)
}

func TestInitCodexFresh(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".codex")
	msg, err := initCodexAt(t, dir, InitOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hooks.json")
	want := `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "/opt/lx/bin/lx hook codex"
          }
        ]
      }
    ]
  }
}
`
	if got := readFile(t, path); got != want {
		t.Errorf("hooks.json:\n%s\nwant:\n%s", got, want)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
	for _, s := range []string{"installed the PreToolUse hook in " + path, "/hooks", "agents-md"} {
		if !strings.Contains(msg, s) {
			t.Errorf("message lacks %q:\n%s", s, msg)
		}
	}
	if strings.Contains(msg, "--readonly") {
		t.Errorf("the Codex install suggests --readonly:\n%s", msg)
	}
}

func TestInitCodexPreservesOtherHooks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	orig := `{
  "description": "team hooks",
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "/usr/bin/python3 .codex/hooks/policy.py", "statusMessage": "Checking"}]},
      {"matcher": "apply_patch", "hooks": [{"type": "command", "command": "./check-patch"}]}
    ],
    "PostToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "./audit", "timeout": 30}]}],
    "Stop": [{"hooks": [{"type": "command", "command": "lx hook claude"}]}]
  }
}
`
	writeFile(t, path, orig)
	if _, err := initCodexAt(t, dir, InitOptions{}); err != nil {
		t.Fatal(err)
	}
	first := readFile(t, path)
	if readFile(t, path+".bak") != orig {
		t.Error("backup differs from the original")
	}
	var doc struct {
		Description string
		Hooks       map[string][]struct {
			Matcher string
			Hooks   []map[string]any
		}
	}
	if err := json.Unmarshal([]byte(first), &doc); err != nil {
		t.Fatal(err)
	}
	pre := doc.Hooks["PreToolUse"]
	if doc.Description != "team hooks" || len(pre) != 3 || pre[0].Hooks[0]["statusMessage"] != "Checking" ||
		pre[2].Hooks[0]["command"] != "/opt/lx/bin/lx hook codex" || len(doc.Hooks["PostToolUse"]) != 1 || len(doc.Hooks["Stop"]) != 1 {
		t.Fatalf("merged hooks.json:\n%s", first)
	}

	msg, err := initCodexAt(t, dir, InitOptions{})
	if err != nil || !strings.Contains(msg, "already installed") || readFile(t, path) != first {
		t.Fatalf("second install changed the file (%v):\n%s", err, msg)
	}
	if _, err := initCodexAt(t, dir, InitOptions{LxPath: "/new/bin/lx"}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); strings.Count(got, "hook codex") != 1 || !strings.Contains(got, "/new/bin/lx hook codex") {
		t.Fatalf("path update:\n%s", got)
	}

	if _, err := initCodexAt(t, dir, InitOptions{Uninstall: true}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); !sameJSON(t, got, orig) {
		t.Errorf("uninstall did not restore the other hooks:\n%s\nwant:\n%s", got, orig)
	}
}

func TestInitCodexAndClaudeKeepToTheirOwn(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "hooks.json"), `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"lx hook claude"}]}]}}`)
	if _, err := initCodexAt(t, dir, InitOptions{}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, "hooks.json"))
	if !strings.Contains(got, `"lx hook claude"`) || !strings.Contains(got, "/opt/lx/bin/lx hook codex") {
		t.Fatalf("the Codex install replaced a Claude hook:\n%s", got)
	}
	if _, err := initAt(t, dir, InitOptions{Uninstall: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "settings.json")); err == nil {
		t.Fatal("InitClaude touched settings.json, which has no lx hook")
	}
	if _, err := initCodexAt(t, dir, InitOptions{Uninstall: true}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(dir, "hooks.json")); !strings.Contains(got, `"lx hook claude"`) || strings.Contains(got, "hook codex") {
		t.Fatalf("uninstall:\n%s", got)
	}
}

func TestInitCodexDryRunAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	msg, err := initCodexAt(t, dir, InitOptions{DryRun: true})
	if err != nil || !strings.Contains(msg, "would write") || !strings.Contains(msg, "hook codex") {
		t.Fatalf("dry run: %v\n%s", err, msg)
	}
	if _, err := os.Stat(filepath.Join(dir, "hooks.json")); err == nil {
		t.Fatal("dry run wrote hooks.json")
	}
	real := filepath.Join(t.TempDir(), "hooks.json")
	writeFile(t, real, "{}")
	if err := os.Symlink(real, filepath.Join(dir, "hooks.json")); err != nil {
		t.Skip("no symlinks:", err)
	}
	if _, err := initCodexAt(t, dir, InitOptions{}); err == nil || !strings.Contains(err.Error(), "CODEX_HOME") {
		t.Fatalf("symlink: %v", err)
	}
}

func TestInitCodexGlobalUsesCodexHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := InitCodex(InitOptions{Global: true, LxPath: "/opt/lx/bin/lx", Out: &bytes.Buffer{}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, filepath.Join(home, "hooks.json")), "hook codex") {
		t.Error("no hook in $CODEX_HOME/hooks.json")
	}
}

const portableClaude = `sh -c 'command -v lx >/dev/null 2>&1 || exit 0; exec lx hook claude'`

func TestInitPortable(t *testing.T) {
	dir := t.TempDir()
	notOnPath := func() (string, error) { return "", nil }
	msg, err := initAt(t, dir, InitOptions{Portable: true, Probe: notOnPath})
	if err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(dir, "settings.json")
	if got := hookCmd(t, settings); got != portableClaude {
		t.Errorf("command = %s, want %s", got, portableClaude)
	}
	if !strings.Contains(msg, "portable hook does nothing on this machine") || !strings.Contains(msg, "teammate") {
		t.Errorf("message:\n%s", msg)
	}

	if _, err := initAt(t, dir, InitOptions{Portable: true, ReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	if got, want := hookCmd(t, settings), `sh -c 'command -v lx >/dev/null 2>&1 || exit 0; exec lx hook claude --readonly'`; got != want {
		t.Errorf("readonly: %s, want %s", got, want)
	}
	if _, err := initAt(t, dir, InitOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := hookCmd(t, settings); got != "/opt/lx/bin/lx hook claude --readonly" {
		t.Errorf("back to a machine-local hook: %s", got)
	}
	if _, err := initAt(t, dir, InitOptions{Portable: true, NoReadOnly: true}); err != nil {
		t.Fatal(err)
	}
	if got := hookCmd(t, settings); got != portableClaude {
		t.Errorf("portable again: %s", got)
	}
	if _, err := initAt(t, dir, InitOptions{Uninstall: true}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, settings); strings.Contains(got, "lx") {
		t.Errorf("uninstall left the portable hook:\n%s", got)
	}

	cdir := t.TempDir()
	if _, err := initCodexAt(t, cdir, InitOptions{Portable: true}); err != nil {
		t.Fatal(err)
	}
	want := `"command": "sh -c 'command -v lx >/dev/null 2>&1 || exit 0; exec lx hook codex'"`
	if got := readFile(t, filepath.Join(cdir, "hooks.json")); !strings.Contains(got, want) {
		t.Errorf("codex:\n%s\nwant %s", got, want)
	}
}

func TestUnwrapPortable(t *testing.T) {
	for cmd, want := range map[string]string{
		portableClaude: "lx hook claude",
		`/bin/sh -c 'command -v lx >/dev/null 2>&1 || exit 0; exec lx hook codex'`:         "lx hook codex",
		`sh -c 'command -v lx >/dev/null 2>&1 || exit 0; exec lx hook claude --readonly'`:  "lx hook claude --readonly",
		`bash -c 'command -v lx >/dev/null 2>&1 || exit 0; exec lx hook claude'`:           "",
		`sh -c 'command -v lx >/dev/null 2>&1 || exit 0; exec /opt/lx hook claude'`:        "",
		`sh -c 'command -v lx >/dev/null 2>&1 || exit 0; exec lx hook claude' && rm -rf ~`: "",
		`sh -c 'command -v lx >/dev/null 2>&1 || exit 0; exec lx hook claude' extra`:       "",
		`sh -c "command -v lx >/dev/null 2>&1 || exit 0; exec lx hook claude $(id)"`:       "",
		`sh -c 'command -v lx || exit 0; exec lx hook claude'`:                             "",
		`lx hook claude`: "",
	} {
		got, ok := UnwrapPortable(cmd)
		if got != want || ok != (want != "") {
			t.Errorf("UnwrapPortable(%s) = %q, %v; want %q", cmd, got, ok, want)
		}
	}
	for cmd, want := range map[string]bool{
		portableClaude: true,
		`sh -c 'command -v lx >/dev/null 2>&1 || exit 0; exec lx hook claude; rm -rf ~'`:          false,
		`sh -c 'command -v lx >/dev/null 2>&1 || exit 0; exec lx hook claude --prefix /x/lx'`:     false,
		`sh -c 'command -v lx >/dev/null 2>&1 || exit 0; exec lx hook codex'`:                     false,
		`sh -c 'command -v lx >/dev/null 2>&1 || exit 0; exec lx hook claude --readonly --debug'`: false,
	} {
		if got := isLxHookCommand(cmd); got != want {
			t.Errorf("isLxHookCommand(%s) = %v", cmd, got)
		}
	}
}

func TestPortableCommandRuns(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "lx"), "#!/bin/sh\necho \"lx $*\"\ncat >/dev/null\n")
	os.Chmod(filepath.Join(bin, "lx"), 0o755)
	for _, c := range []struct{ path, want string }{{bin, "lx hook claude\n"}, {t.TempDir(), ""}} {
		cmd := exec.Command(sh, "-c", portableClaude)
		cmd.Env = []string{"PATH=" + c.path + ":/usr/bin:/bin"}
		cmd.Stdin = strings.NewReader(bashInput("git status"))
		out, err := cmd.Output()
		if err != nil || string(out) != c.want {
			t.Errorf("PATH=%s: %q, %v; want %q", c.path, out, err, c.want)
		}
	}
}
