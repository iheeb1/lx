package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func putSettings(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestOutputLimitsPrecedence(t *testing.T) {
	type layers struct{ managed, dropA, dropB, local, project, user string }
	cases := []struct {
		name     string
		in       layers
		bashMax  string
		pass     int
		fail     int
		from     string
		setting  int
		wantNone bool
	}{
		{name: "defaults", pass: 30000, fail: 10000, wantNone: true},
		{name: "user", in: layers{user: `{"bashOutputMaxChars": 50000}`}, pass: 50000, fail: 10000, from: "user"},
		{name: "project over user", in: layers{user: `{"bashOutputMaxChars": 50000}`, project: `{"bashOutputMaxChars": 20000}`}, pass: 20000, fail: 10000, from: "project"},
		{name: "local over project", in: layers{user: `{"bashOutputMaxChars": 50000}`, project: `{"bashOutputMaxChars": 20000}`, local: `{"bashOutputMaxChars": 60000}`}, pass: 60000, fail: 10000, from: "local"},
		{name: "managed over local", in: layers{managed: `{"bashOutputMaxChars": 8000}`, local: `{"bashOutputMaxChars": 60000}`}, pass: 8000, fail: 8000, from: "managed"},
		{name: "last drop-in wins", in: layers{managed: `{"bashOutputMaxChars": 8000}`, dropA: `{"bashOutputMaxChars": 9000}`, dropB: `{"bashOutputMaxChars": 12000}`}, pass: 12000, fail: 10000, from: "dropB"},
		{name: "drop-in over managed file", in: layers{managed: `{"bashOutputMaxChars": 8000}`, dropA: `{"bashOutputMaxChars": 9000}`, dropB: `{"model": "opus"}`}, pass: 9000, fail: 9000, from: "dropA"},
		{name: "clamped up", in: layers{user: `{"bashOutputMaxChars": 1000}`}, pass: 4000, fail: 4000, from: "user", setting: 4000},
		{name: "clamped down", in: layers{user: `{"bashOutputMaxChars": 500000}`}, pass: 128000, fail: 10000, from: "user", setting: 128000},
		{name: "exponent", in: layers{user: `{"bashOutputMaxChars": 2e4}`}, pass: 20000, fail: 10000, from: "user"},
		{name: "integral float", in: layers{user: `{"bashOutputMaxChars": 20000.0}`}, pass: 20000, fail: 10000, from: "user"},
		{name: "string skipped", in: layers{local: `{"bashOutputMaxChars": "5000"}`, project: `{"bashOutputMaxChars": 20000}`}, pass: 20000, fail: 10000, from: "project"},
		{name: "fraction skipped", in: layers{local: `{"bashOutputMaxChars": 5000.5}`, user: `{"bashOutputMaxChars": 6000}`}, pass: 6000, fail: 6000, from: "user"},
		{name: "zero skipped", in: layers{local: `{"bashOutputMaxChars": 0}`}, pass: 30000, fail: 10000, wantNone: true},
		{name: "negative skipped", in: layers{local: `{"bashOutputMaxChars": -3}`}, pass: 30000, fail: 10000, wantNone: true},
		{name: "null skipped", in: layers{local: `{"bashOutputMaxChars": null}`}, pass: 30000, fail: 10000, wantNone: true},
		{name: "bad json skipped", in: layers{local: `{"bashOutputMaxChars": 5000,}`, user: `{"bashOutputMaxChars": 7000}`}, pass: 7000, fail: 7000, from: "user"},
		{name: "nested key ignored", in: layers{user: `{"env": {"bashOutputMaxChars": 5000}}`}, pass: 30000, fail: 10000, wantNone: true},
		{name: "key is case-sensitive", in: layers{user: `{"BashOutputMaxChars": 5000}`}, pass: 30000, fail: 10000, wantNone: true},
		{name: "byte order mark", in: layers{user: "\xef\xbb\xbf{\"bashOutputMaxChars\": 5000}"}, pass: 5000, fail: 5000, from: "user"},
		{name: "duplicate key: last wins", in: layers{user: `{"bashOutputMaxChars": 5000, "bashOutputMaxChars": 70000}`}, pass: 70000, fail: 10000, from: "user"},
		{name: "env raises only the read-back window", bashMax: "50000", pass: 30000, fail: 10000, wantNone: true},
		{name: "env lowers both", bashMax: "5000", pass: 5000, fail: 5000, wantNone: true},
		{name: "env floor", bashMax: "300", pass: 1000, fail: 1000, wantNone: true},
		{name: "env garbage", bashMax: "lots", pass: 30000, fail: 10000, wantNone: true},
		{name: "env padded", bashMax: " 8000 ", pass: 8000, fail: 8000, wantNone: true},
		{name: "setting replaces env", in: layers{user: `{"bashOutputMaxChars": 20000}`}, bashMax: "5000", pass: 20000, fail: 10000, from: "user"},
		{name: "small setting replaces big env", in: layers{user: `{"bashOutputMaxChars": 6000}`}, bashMax: "150000", pass: 6000, fail: 6000, from: "user"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			managed := filepath.Join(root, "managed", "managed-settings.json")
			drops := filepath.Join(root, "managed", "managed-settings.d")
			project, user := filepath.Join(root, "proj"), filepath.Join(root, "user")
			paths := map[string]string{
				"managed": managed,
				"dropA":   filepath.Join(drops, "10-a.json"),
				"dropB":   filepath.Join(drops, "20-b.json"),
				"local":   filepath.Join(project, ".claude", "settings.local.json"),
				"project": filepath.Join(project, ".claude", "settings.json"),
				"user":    filepath.Join(user, "settings.json"),
			}
			for k, v := range map[string]string{"managed": c.in.managed, "dropA": c.in.dropA, "dropB": c.in.dropB,
				"local": c.in.local, "project": c.in.project, "user": c.in.user} {
				if v != "" {
					putSettings(t, paths[k], v)
				}
			}
			putSettings(t, filepath.Join(drops, ".30-hidden.json"), `{"bashOutputMaxChars": 4500}`)
			putSettings(t, filepath.Join(drops, "40-notes.txt"), `{"bashOutputMaxChars": 4600}`)

			got := OutputLimitsFrom(managed, project, filepath.Join(root, "home"), user, c.bashMax)
			want := OutputLimits{Pass: c.pass, Fail: c.fail}
			if !c.wantNone {
				want.Setting, want.From = c.pass, paths[c.from]
				if c.setting != 0 {
					want.Setting = c.setting
				}
			}
			if got != want {
				t.Errorf("got %+v\nwant %+v", got, want)
			}
		})
	}
}

func TestClaudeOutputLimitsFindsLayers(t *testing.T) {
	if l := OutputLimitsFrom(managedPath, "", "", "", ""); l.Setting != 0 {
		t.Skipf("this machine's managed settings set bashOutputMaxChars (%s)", l.From)
	}
	root := t.TempDir()
	home, user, proj := filepath.Join(root, "home"), filepath.Join(root, "cfg"), filepath.Join(root, "home", "src", "app")
	sub := filepath.Join(proj, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CLAUDE_CONFIG_DIR", user)
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Setenv("BASH_MAX_OUTPUT_LENGTH", "")
	putSettings(t, filepath.Join(user, "settings.json"), `{"bashOutputMaxChars": 50000}`)
	putSettings(t, filepath.Join(proj, ".claude", "settings.local.json"), `{"bashOutputMaxChars": 12000}`)
	putSettings(t, filepath.Join(home, ".claude", "settings.local.json"), `{"bashOutputMaxChars": 5000}`)
	putSettings(t, filepath.Join(home, ".claude", "settings.json"), `{"bashOutputMaxChars": 5000}`)

	check := func(cwd string, pass int, from string) {
		t.Helper()
		if l := ClaudeOutputLimits(cwd); l.Pass != pass || l.From != from {
			t.Errorf("cwd %s: got %d from %s, want %d from %s", cwd, l.Pass, l.From, pass, from)
		}
	}
	local := filepath.Join(proj, ".claude", "settings.local.json")
	check(sub, 12000, local)
	check(filepath.Join(home, "notes"), 50000, filepath.Join(user, "settings.json"))
	check(home, 50000, filepath.Join(user, "settings.json"))

	nested := filepath.Join(proj, "pkg", ".claude")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	check(sub, 12000, local)
	putSettings(t, filepath.Join(nested, "settings.json"), `{"bashOutputMaxChars": 20000}`)
	check(sub, 12000, local)
	putSettings(t, filepath.Join(nested, "settings.json"), `{"bashOutputMaxChars": 6000}`)
	check(sub, 6000, filepath.Join(nested, "settings.json"))
	check(proj, 12000, local)
	if err := os.RemoveAll(nested); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CLAUDE_PROJECT_DIR", root)
	check(sub, 50000, filepath.Join(user, "settings.json"))
	t.Setenv("CLAUDE_PROJECT_DIR", proj)
	check(root, 12000, local)
	t.Setenv("CLAUDE_PROJECT_DIR", "")

	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(proj, ".claude"))
	check(sub, 30000, "")
	if link := filepath.Join(root, "cfglink"); os.Symlink(filepath.Join(proj, ".claude"), link) == nil {
		t.Setenv("CLAUDE_CONFIG_DIR", link)
		check(sub, 30000, "")
	}

	old := managedPath
	managedPath = filepath.Join(root, "managed", "managed-settings.json")
	t.Cleanup(func() { managedPath = old })
	putSettings(t, managedPath, `{"bashOutputMaxChars": 7000}`)
	check(sub, 7000, managedPath)
}

func TestOutputSettingSkipsOddFiles(t *testing.T) {
	root := t.TempDir()
	proj, user := filepath.Join(root, "proj"), filepath.Join(root, "user")
	putSettings(t, filepath.Join(user, "settings.json"), `{"bashOutputMaxChars": 7000}`)
	if err := os.MkdirAll(filepath.Join(proj, ".claude", "settings.local.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	big := `{"bashOutputMaxChars": 5000, "pad": "` + strings.Repeat("x", maxSettingsFile) + `"}`
	putSettings(t, filepath.Join(proj, ".claude", "settings.json"), big)
	if l := OutputLimitsFrom("", proj, "", user, ""); l.Pass != 7000 || l.From != filepath.Join(user, "settings.json") {
		t.Errorf("got %+v, want 7000 from the user file", l)
	}
}
