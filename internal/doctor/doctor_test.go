package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/lazyre"
)

func TestFailedAndExitCode(t *testing.T) {
	for _, c := range []struct {
		statuses []string
		failed   bool
	}{
		{nil, false},
		{[]string{OK, OK}, false},
		{[]string{OK, Warn, Skip}, false},
		{[]string{OK, Fail}, true},
		{[]string{Fail, Warn, Fail}, true},
	} {
		var r Report
		for _, s := range c.statuses {
			r.Checks = append(r.Checks, Check{ID: "x", Status: s, Message: "m"})
		}
		if r.Failed() != c.failed {
			t.Errorf("%v: Failed() = %v", c.statuses, r.Failed())
		}
	}
}

func emptyEnv(t *testing.T) Env {
	root := t.TempDir()
	return Env{
		Home: filepath.Join(root, "home"), Cwd: root, ConfigDir: filepath.Join(root, "home", ".claude"),
		Executable: filepath.Join(root, "lx"), Version: "lx 1.0",
		TeeDir: filepath.Join(root, "runs"), HistoryPath: filepath.Join(root, "history.jsonl"),
		Now: testNow,
	}
}

func TestMainExitCodeMatchesReport(t *testing.T) {
	e := emptyEnv(t)
	var out, errb bytes.Buffer
	code := Main(nil, &out, &errb, e)
	r := Run(e)
	if !r.Failed() || code != 1 {
		t.Fatalf("no hook: Failed()=%v exit=%d\n%s", r.Failed(), code, out.String())
	}
	if !strings.Contains(out.String(), "✗ hook ") || !strings.Contains(out.String(), "fix: lx init") {
		t.Errorf("text output:\n%s", out.String())
	}

	out.Reset()
	if code := Main([]string{"--json"}, &out, &errb, e); code != 1 {
		t.Errorf("--json exit %d", code)
	}
	var raw map[string]any
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out.String())
	}
	for _, k := range []string{"version", "binary", "checks"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("JSON lacks %q", k)
		}
	}
	checks := raw["checks"].([]any)
	first := checks[0].(map[string]any)
	for _, k := range []string{"id", "status", "message", "fix"} {
		if _, ok := first[k]; !ok {
			t.Errorf("check lacks %q", k)
		}
	}

	out.Reset()
	errb.Reset()
	if code := Main([]string{"--fix"}, &out, &errb, e); code != 2 || out.Len() != 0 || !strings.Contains(errb.String(), Usage) {
		t.Errorf("--fix: exit %d, stdout %q, stderr %q", code, out.String(), errb.String())
	}
	out.Reset()
	if code := Main([]string{"--help"}, &out, &errb, e); code != 0 || !strings.Contains(out.String(), Usage) {
		t.Errorf("--help: exit %d, %q", code, out.String())
	}
}

func TestCheckOrderAndIDs(t *testing.T) {
	r := Run(emptyEnv(t))
	var ids []string
	for _, c := range r.Checks {
		if len(ids) == 0 || ids[len(ids)-1] != c.ID {
			ids = append(ids, c.ID)
		}
		switch c.Status {
		case OK, Warn, Fail, Skip:
		default:
			t.Errorf("bad status %q", c.Status)
		}
		if c.Message == "" || strings.Contains(c.Message, "\n") || strings.Contains(c.Fix, "\n") {
			t.Errorf("check %s: message %q fix %q", c.ID, c.Message, c.Fix)
		}
	}
	want := "binary hook hook-binary hook-run path rtk perms env settings storage activity"
	if got := strings.Join(ids, " "); got != want {
		t.Errorf("check order:\n got %s\nwant %s", got, want)
	}
}

func TestRunWithoutInjectedFuncs(t *testing.T) {
	e := emptyEnv(t)
	cfg := e.ConfigDir
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"` + e.Executable + ` hook claude"}]}]}}`
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.Executable, []byte("#!/bin/sh\ntouch \"$0.ran\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.Now = time.Time{}
	r := Run(e)
	if exists(e.Executable + ".ran") {
		t.Fatal("a program ran although Exec was nil")
	}
	var run *Check
	for i := range r.Checks {
		if r.Checks[i].ID == "hook-run" {
			run = &r.Checks[i]
		}
	}
	if run == nil || run.Status != Fail || !strings.Contains(run.Message, "disabled") {
		t.Errorf("hook-run with no Exec: %+v", run)
	}
}

func TestFindProjectDir(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(root, "home")
	proj := filepath.Join(home, "code", "app")
	for _, d := range []string{filepath.Join(home, ".claude"), filepath.Join(proj, ".claude"), filepath.Join(proj, "src", "deep"), filepath.Join(home, "notes")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	none := func(string) string { return "" }
	cfg := filepath.Join(home, ".claude")
	if got := FindProjectDir(filepath.Join(proj, "src", "deep"), home, cfg, none); got != proj {
		t.Errorf("walk up: %q, want %q", got, proj)
	}

	if got := FindProjectDir(filepath.Join(home, "notes"), home, cfg, none); got != "" {
		t.Errorf("home's .claude taken for a project: %q", got)
	}

	other := filepath.Join(root, "work")
	if err := os.MkdirAll(filepath.Join(other, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := FindProjectDir(other, home, filepath.Join(other, ".claude"), none); got != "" {
		t.Errorf("config dir taken for a project: %q", got)
	}
	env := func(k string) string {
		if k == "CLAUDE_PROJECT_DIR" {
			return "/elsewhere"
		}
		return ""
	}
	if got := FindProjectDir(proj, home, cfg, env); got != "/elsewhere" {
		t.Errorf("CLAUDE_PROJECT_DIR ignored: %q", got)
	}
}

func TestSettingsParse(t *testing.T) {
	cases := []struct {
		name, data string
		parseErr   bool
		shape      bool
		hooks      int
	}{
		{"empty", "", false, false, 0},
		{"spaces", " \n ", false, false, 0},
		{"bom", "\xef\xbb\xbf{\"hooks\":{\"PreToolUse\":[{\"hooks\":[{\"type\":\"command\",\"command\":\"lx hook claude\"}]}]}}", false, false, 1},
		{"array", "[]", true, false, 0},
		{"null", "null", true, false, 0},
		{"string", `"x"`, true, false, 0},
		{"trailing", `{} {}`, true, false, 0},
		{"truncated", `{"hooks": {`, true, false, 0},
		{"comment", "// c\n{}", true, false, 0},
		{"hooks-array", `{"hooks": []}`, false, true, 0},
		{"pre-object", `{"hooks": {"PreToolUse": {}}}`, false, true, 0},
		{"hooks-null", `{"hooks": null}`, false, false, 0},
		{"odd-group", `{"hooks": {"PreToolUse": [1, "x", {"matcher": 5, "hooks": [{"command": "lx hook claude"}]}, {"hooks": [{"command": 3}, {}]}]}}`, false, false, 1},
	}
	for _, c := range cases {
		f := &settingsFile{Path: c.name}
		f.parse([]byte(c.data))
		if (f.ParseErr != nil) != c.parseErr || (f.Shape != "") != c.shape || len(f.Hooks) != c.hooks {
			t.Errorf("%s: parseErr=%v shape=%q hooks=%d", c.name, f.ParseErr, f.Shape, len(f.Hooks))
		}
	}
	f := &settingsFile{}
	f.parse([]byte(`{"env": {"LX_TEE": 0, "LX_HOOK": "off", "Z": true}, "disableAllHooks": true,
		"permissions": {"allow": ["Bash(lx:*)", 7], "deny": ["Bash(rm:*)"]}}`))
	if len(f.Env) != 3 || f.Env[0] != (envVar{"LX_HOOK", "off"}) || f.Env[1] != (envVar{"LX_TEE", "0"}) {
		t.Errorf("env: %+v", f.Env)
	}
	if !f.DisableAllHooks || len(f.Allow) != 1 || len(f.Deny) != 1 {
		t.Errorf("parsed: %+v", f)
	}
	f = &settingsFile{}
	f.parse([]byte("{\n  \"a\": 1,\n  \"b\": ]\n}"))
	if f.ParseErr == nil || !strings.HasPrefix(f.ParseErr.Error(), "line 3, column 8") {
		t.Errorf("position: %v", f.ParseErr)
	}
}

func TestSettingsFilesDedup(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	cfg := filepath.Join(root, "home", ".claude")
	if err := os.MkdirAll(cfg, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(root, "home"), link); err != nil {
		t.Fatal(err)
	}

	e := &Env{ConfigDir: cfg, ProjectDir: link, ManagedPath: filepath.Join(cfg, "settings.json")}
	files := e.settingsFiles()
	var paths []string
	for _, f := range files {
		paths = append(paths, f.Label)
	}
	if got := strings.Join(paths, ","); got != "user,user local" {
		t.Errorf("files: %s", got)
	}
}

func TestStripEscapes(t *testing.T) {
	in := "\x1b]1337;SetUserVar=a\x07\x1b[1;32m/usr/bin/lx\x1b[0m\r\n\x1b]133;D\x1b\\tail"
	if got := stripEscapes(in); got != "/usr/bin/lx\ntail" {
		t.Errorf("got %q", got)
	}
	if got := stripEscapes("plain"); got != "plain" {
		t.Errorf("got %q", got)
	}
}

func TestExecWith(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell")
	}
	run := ExecWith([]string{"PATH=/usr/bin:/bin", "X=from-env"})
	out, err := run(context.Background(), "/bin/sh", []string{"-c", "cat; echo $X"}, "in\n")
	if err != nil || out != "in\nfrom-env\n" {
		t.Errorf("out %q err %v", out, err)
	}
	_, err = run(context.Background(), "/bin/sh", []string{"-c", "echo oops >&2; exit 3"}, "")
	var ee *exec.ExitError
	if !errors.As(err, &ee) || !strings.Contains(err.Error(), "oops") {
		t.Errorf("exit error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = run(ctx, "/bin/sh", []string{"-c", "sleep 30 & sleep 30"}, "")
	if err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("timeout: err %v after %v", err, time.Since(start))
	}

	out, _ = run(context.Background(), "/bin/sh", []string{"-c", "head -c 3000000 /dev/zero"}, "")
	if len(out) != maxCapture {
		t.Errorf("captured %d bytes", len(out))
	}
}

func TestStoreUsage(t *testing.T) {
	dir := t.TempDir()
	for name, size := range map[string]int{"1.log": 10, "1.json": 5, "2.log.part": 7, "2.json": 3, ".seq": 1, "notes.txt": 100, "x.log": 4,
		"2024.notes": 9, "+3.log": 2, ".DS_Store": 50} {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if u := storeUsage(dir); u.runs != 2 || u.bytes != 26 || u.foreign != 4 {
		t.Errorf("usage %+v", u)
	}
}

func TestHistoryLastRun(t *testing.T) {
	e := emptyEnv(t)
	var b strings.Builder
	for i := 0; i < 5000; i++ {
		b.WriteString(`{"t":1000,"cmd":"git status","raw":1,"out":1}` + "\n")
	}
	b.WriteString(`{"t":2000,"cmd":"go test"}` + "\n")
	b.WriteString(`{"t":2200,"kind":"some-future-run"}` + "\n")
	b.WriteString(`{"t":3000,"kind":"show","of":1}` + "\n")
	b.WriteString("garbage\n")
	b.WriteString(`{"t":2500`)
	if err := os.WriteFile(e.HistoryPath, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newState(&e)
	h := s.history()
	if h.records != 5005 || h.lastRun.Unix() != 2200 {
		t.Errorf("records %d last %v", h.records, h.lastRun.Unix())
	}
}

func TestRecentFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a", "b.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	if recentFile(dir, time.Now().Add(-24*time.Hour)) {
		t.Error("an old file counted as recent")
	}
	if !recentFile(dir, time.Now().Add(-72*time.Hour)) {
		t.Error("a recent file was missed")
	}
	if recentFile(filepath.Join(dir, "missing"), time.Time{}) {
		t.Error("a missing directory has recent files")
	}

	link := filepath.Join(t.TempDir(), "projects")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if !recentFile(link, time.Now().Add(-72*time.Hour)) {
		t.Error("a symlinked transcript directory was not walked")
	}
}

func TestFormatting(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1536: "1.5 KB", 512 << 20: "512 MB", 3 << 30: "3 GB"} {
		if got := fmtBytes(n); got != want {
			t.Errorf("fmtBytes(%d) = %q, want %q", n, got, want)
		}
	}
	for d, want := range map[time.Duration]string{0: "<1 ms", 7 * time.Millisecond: "7 ms"} {
		if got := fmtLatency(d); got != want {
			t.Errorf("fmtLatency(%v) = %q", d, got)
		}
	}
	for d, want := range map[time.Duration]string{time.Second: "just now", 5 * time.Minute: "5 min ago", 3 * time.Hour: "3 h ago", 100 * time.Hour: "4 days ago"} {
		if got := fmtAgo(d); got != want {
			t.Errorf("fmtAgo(%v) = %q, want %q", d, got, want)
		}
	}
	s := &state{e: &Env{Home: "/Users/me"}}
	for in, want := range map[string]string{"/Users/me": "~", "/Users/me/x": "~/x", "/Users/meme/x": "/Users/meme/x", "/tmp": "/tmp"} {
		if got := s.show(in); got != want {
			t.Errorf("show(%q) = %q", in, got)
		}
	}
}

func TestLazyPatternsCompile(t *testing.T) {
	if bad := lazyre.CompileAll(); len(bad) > 0 {
		t.Errorf("invalid patterns: %q", bad)
	}
}

func TestUnverifiedHookNeverRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	lx := filepath.Join(root, "bin", "lx")
	if err := os.MkdirAll(filepath.Dir(lx), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lx, []byte("#!/bin/sh\ncat >/dev/null\necho '{}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "SENTINEL")
	cmds := []string{
		`touch $SENTINEL; ` + lx + ` hook claude`,
		lx + ` hook claude; touch $SENTINEL`,
		lx + ` hook claude && touch "$SENTINEL"`,
		lx + ` hook claude $(touch $SENTINEL)`,
		lx + " hook claude `touch $SENTINEL`",
		lx + ` hook claude > $SENTINEL`,
		lx + " hook claude\ntouch $SENTINEL",
		`SENTINEL_X=1 ` + lx + ` hook claude`,
		`sh -c 'touch $SENTINEL' ` + lx + ` hook claude`,
		`touch${IFS}$SENTINEL;` + lx + ` hook claude`,
	}
	for i, c := range cmds {
		cfg := filepath.Join(root, "home"+itoa(i), ".claude")
		if err := os.MkdirAll(cfg, 0o700); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(c)
		settings := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":` + string(b) + `}]}]}}`
		if err := os.WriteFile(filepath.Join(cfg, "settings.json"), []byte(settings), 0o600); err != nil {
			t.Fatal(err)
		}
		var calls []string
		real := ExecWith([]string{"PATH=/usr/bin:/bin", "SENTINEL=" + sentinel})
		e := Env{
			Home: filepath.Dir(cfg), Cwd: root, ConfigDir: cfg, Executable: lx, Version: "lx t",
			TeeDir: filepath.Join(root, "runs"), HistoryPath: filepath.Join(root, "h.jsonl"), Now: testNow,
			Getenv: func(k string) string {
				switch k {
				case "SENTINEL":
					return sentinel
				case "SHELL":
					return fakeShell
				}
				return ""
			},
			Exec: func(ctx context.Context, name string, args []string, stdin string) (string, error) {
				calls = append(calls, name+" "+strings.Join(args, " "))
				if name == fakeShell {
					return lx + "\n", nil
				}
				return real(ctx, name, args, stdin)
			},
		}
		r := Run(e)
		if exists(sentinel) {
			t.Fatalf("%q: the sentinel was created", c)
		}
		for _, call := range calls {
			if call != fakeShell+" -lic "+probeScript {
				t.Errorf("%q: doctor ran %q", c, call)
			}
		}
		for _, ch := range r.Checks {
			if ch.ID == "hook-run" && ch.Status != Skip {
				t.Errorf("%q: hook-run %s: %s", c, ch.Status, ch.Message)
			}
		}
	}
}

func TestPrintable(t *testing.T) {
	for in, want := range map[string]string{
		"plain ✓ text":       "plain ✓ text",
		"a\nb\tc":            `a\nb\tc`,
		"\x1b[2K\r✓ hook ok": `\x1b[2K\x0d✓ hook ok`,
		"x\u202egnp.exe":     `x\u202egnp.exe`,
		"bad \xff byte":      `bad \xff byte`,
		"del\x7f":            `del\x7f`,
		"line\u2028sep":      `line\u2028sep`,
	} {
		if got := printable(in); got != want {
			t.Errorf("printable(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReportIsOneLinePerCheck(t *testing.T) {
	e := emptyEnv(t)
	if err := os.MkdirAll(e.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd, _ := json.Marshal("\x1b[1A\x1b[2K\r✓ hook fine\n" + e.Executable + " hook claude")
	settings := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":` + string(cmd) + `}]}]}}`
	if err := os.WriteFile(filepath.Join(e.ConfigDir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	Main(nil, &out, &out, e)
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if strings.ContainsAny(line, "\x1b\r") {
			t.Errorf("raw control character in %q", line)
		}
		if !strings.HasPrefix(line, "✓ ") && !strings.HasPrefix(line, "! ") && !strings.HasPrefix(line, "✗ ") &&
			!strings.HasPrefix(line, "- ") && !strings.HasPrefix(line, "              fix: ") && !strings.HasPrefix(line, "lx doctor: ") {
			t.Errorf("stray line %q", line)
		}
	}
}

func TestZones(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	home := filepath.Join(root, "home")
	repo := filepath.Join(home, "code", "repo")
	for _, d := range []string{filepath.Join(home, ".git"), filepath.Join(repo, ".git"), filepath.Join(repo, "sub", ".claude"), filepath.Join(home, "notes")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	zones := func(cwd, proj string) string {
		s := &state{e: &Env{Home: home, Cwd: cwd, ProjectDir: proj}}
		return strings.ReplaceAll(strings.Join(s.zones(), ","), root, "$R")
	}
	if got := zones(filepath.Join(repo, "sub"), filepath.Join(repo, "sub")); got != "$R/home/code/repo/sub,$R/home/code/repo" {
		t.Errorf("nested .claude in a repo: %s", got)
	}
	if got := zones(filepath.Join(home, "notes"), ""); got != "" {
		t.Errorf("dotfiles repository in home: %s", got)
	}
	if got := zones(filepath.Join(repo, "sub", "deep"), ""); got != "$R/home/code/repo" {
		t.Errorf("a repository without .claude: %s", got)
	}
	if got := zones(home, ""); got != "" {
		t.Errorf("home itself: %s", got)
	}
	if got := zones("/", ""); got != "" {
		t.Errorf("root: %s", got)
	}
}

func TestAllowManagedHooksOnly(t *testing.T) {
	e := emptyEnv(t)
	e.ManagedPath = filepath.Join(filepath.Dir(e.ConfigDir), "managed.json")
	if err := os.MkdirAll(e.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	user := `{"allowManagedHooksOnly": true, "hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"` + e.Executable + ` hook claude"}]}]}}`
	if err := os.WriteFile(filepath.Join(e.ConfigDir, "settings.json"), []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	managedOnly := func() bool {
		for _, c := range Run(e).Checks {
			if c.ID == "hook" && strings.Contains(c.Message, "allowManagedHooksOnly") {
				return c.Status == Fail
			}
		}
		return false
	}
	if managedOnly() {
		t.Error("allowManagedHooksOnly in user settings was honored")
	}
	if err := os.WriteFile(e.ManagedPath, []byte(`{"allowManagedHooksOnly": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if !managedOnly() {
		t.Error("allowManagedHooksOnly in managed settings was ignored")
	}

	managed := `{"allowManagedHooksOnly": true, "hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"` + e.Executable + ` hook claude --readonly"}]}]}}`
	if err := os.WriteFile(e.ManagedPath, []byte(managed), 0o600); err != nil {
		t.Fatal(err)
	}
	if managedOnly() {
		t.Error("a managed lx hook was reported as never running")
	}
}

func TestLxGuards(t *testing.T) {
	cases := []struct {
		deny, ask []string
		rewrite   string
		want      string
	}{
		{deny: []string{"Bash(lx:*)"}, want: "fail"},
		{deny: []string{"Bash(lx *)"}, want: "fail"},
		{ask: []string{"Bash(lx git:*)"}, want: "warn"},
		{deny: []string{"Bash", "Bash(*)", "Bash(git:*)", "Bash(lx)", "Bash(lx git push:*)", "Read(**)"}},
		{ask: []string{"Bash(git status:*)"}},

		{deny: []string{"Bash(/opt/lx/lx:*)"}, rewrite: "/opt/lx/lx", want: "fail"},
		{deny: []string{"Bash(lx:*)"}, rewrite: "/opt/lx/lx", want: "fail"},
	}
	for _, c := range cases {
		f := &settingsFile{Path: "/s.json", Scope: "user", Deny: c.deny, Ask: c.ask}
		s := &state{e: &Env{}, files: []*settingsFile{f}, rewriteBin: c.rewrite}
		s.checkLxGuards()
		var got []string
		for _, ch := range s.checks {
			got = append(got, ch.Status)
		}
		if strings.Join(got, " ") != c.want {
			t.Errorf("deny %q ask %q rewrite %q: %v, want %q", c.deny, c.ask, c.rewrite, got, c.want)
		}
	}
}

func TestTeeNotADirectory(t *testing.T) {
	e := emptyEnv(t)
	if err := os.WriteFile(e.TeeDir, []byte("my notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	fix := func(env map[string]string) string {
		e.Getenv = func(k string) string { return env[k] }
		for _, c := range Run(e).Checks {
			if c.ID == "storage" && c.Status == Fail {
				return c.Fix
			}
		}
		t.Fatal("no storage failure")
		return ""
	}
	if f := fix(nil); !strings.HasPrefix(f, "rm ") {
		t.Errorf("default location: fix %q", f)
	}
	if f := fix(map[string]string{"LX_TEE_DIR": e.TeeDir}); strings.Contains(f, "rm ") || !strings.Contains(f, "LX_TEE_DIR") {
		t.Errorf("LX_TEE_DIR set: fix %q", f)
	}
}
