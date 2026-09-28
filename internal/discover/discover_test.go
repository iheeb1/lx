package discover

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fixtures = "testdata/projects"

func statMap(stats []Stat) map[string]Stat {
	m := map[string]Stat{}
	for _, s := range stats {
		m[s.Command] = s
	}
	return m
}

func TestScanFixtures(t *testing.T) {
	r, err := Scan(Options{Dirs: []string{fixtures}})
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, got, want int) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %d, want %d", name, got, want)
		}
	}
	check("Files", r.Files, 3)
	check("BashCalls", r.BashCalls, 8)
	check("Measured", r.Measured, 7)
	check("MissingResults", r.MissingResults, 1)
	check("AlreadyLx", r.AlreadyLx, 1)
	check("Candidates", r.Candidates, 5)
	check("BadLines", r.BadLines, 1)

	if r.WouldSaveTokens != r.CandidateTokens-r.CandidateOut || r.WouldSaveTokens <= 0 {
		t.Errorf("savings: %d = %d - %d", r.WouldSaveTokens, r.CandidateTokens, r.CandidateOut)
	}
	if r.CandidateTokens >= r.BashOutputTokens {
		t.Errorf("candidate tokens %d should be part of all tokens %d", r.CandidateTokens, r.BashOutputTokens)
	}
	if r.WouldSavePct <= 0 || r.WouldSavePct > r.CandidateSavePct || r.CandidateSavePct > 100 {
		t.Errorf("pcts: %.1f of all, %.1f of candidates", r.WouldSavePct, r.CandidateSavePct)
	}

	top := statMap(r.Top)
	for _, k := range []string{"git status", "go test", "grep", "docker logs", "ls"} {
		st, ok := top[k]
		if !ok || st.Count != 1 {
			t.Errorf("top[%q] = %+v (all: %+v)", k, st, r.Top)
			continue
		}
		if st.Saved != st.Tokens-st.TokensAfter || st.TokensAfter > st.Tokens {
			t.Errorf("%s: inconsistent %+v", k, st)
		}
	}
	for i := 1; i < len(r.Top); i++ {
		if r.Top[i].Saved > r.Top[i-1].Saved {
			t.Errorf("Top not sorted by saved tokens: %+v", r.Top)
		}
	}

	if top["git status"].Tokens < 500 {
		t.Errorf("git status measured %d tokens; toolUseResult not preferred", top["git status"].Tokens)
	}
	if un := statMap(r.Unsupported); un["cat"].Count != 1 || len(r.Unsupported) != 1 {
		t.Errorf("unsupported = %+v", r.Unsupported)
	}
}

func TestScanPrivacy(t *testing.T) {
	r, err := Scan(Options{Dirs: []string{fixtures}})
	if err != nil {
		t.Fatal(err)
	}
	var text bytes.Buffer
	r.Text(&text)
	js, _ := json.Marshal(r)
	for _, leak := range []string{"SECRET-VALUE", "secret-file", "internal/pkg", "TODO", "src", "--tail", "web", "-la", "/Users/me"} {
		if strings.Contains(text.String(), leak) || strings.Contains(string(js), leak) {
			t.Errorf("report leaks %q:\n%s\n%s", leak, text.String(), js)
		}
	}
}

func TestScanSinceAndLimit(t *testing.T) {
	dir := t.TempDir()
	copyTree(t, fixtures, dir)
	old := time.Now().Add(-30 * 24 * time.Hour)
	oldFile := filepath.Join(dir, "-Users-me-api", "resumed.jsonl")
	if err := os.Chtimes(oldFile, old, old); err != nil {
		t.Fatal(err)
	}

	r, err := Scan(Options{Dirs: []string{dir}, Since: time.Now().Add(-7 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if r.Files != 2 || statMap(r.Top)["docker logs"].Count != 0 {
		t.Errorf("Since: files=%d top=%+v", r.Files, r.Top)
	}

	r, err = Scan(Options{Dirs: []string{dir}, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if r.Files != 1 {
		t.Errorf("Limit: files=%d", r.Files)
	}
}

func TestScanMissingDir(t *testing.T) {
	r, err := Scan(Options{Dirs: []string{filepath.Join(t.TempDir(), "nope")}})
	if err != nil || r.Files != 0 {
		t.Errorf("missing dir: %+v %v", r, err)
	}
	var b bytes.Buffer
	r.Text(&b)
	if !strings.Contains(b.String(), "no Bash calls") {
		t.Errorf("empty report text: %q", b.String())
	}
}

func TestScanHugeLines(t *testing.T) {
	dir := t.TempDir()
	var big strings.Builder
	for big.Len() < 3<<20 {
		big.WriteString("ok  \texample.com/app/pkg 0.010s\n")
	}
	var f bytes.Buffer
	writeJSON := func(v any) {
		b, _ := json.Marshal(v)
		f.Write(b)
		f.WriteByte('\n')
	}
	use := func(id, cmd string) {
		writeJSON(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{
			map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]any{"command": cmd}}}}})
	}
	res := func(id, stdout string) {
		writeJSON(map[string]any{"type": "user", "toolUseResult": map[string]any{"stdout": stdout, "stderr": ""},
			"message": map[string]any{"content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": id, "content": "x"}}}})
	}
	use("t1", "go test ./...")
	res("t1", big.String())
	use("t2", "go vet ./...")
	res("t2", big.String()+big.String())
	use("t3", "go build ./...")
	res("t3", "ok\n")
	if err := os.WriteFile(filepath.Join(dir, "big.jsonl"), f.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	old := maxLine
	maxLine = 5 << 20
	defer func() { maxLine = old }()
	r, err := Scan(Options{Dirs: []string{dir}})
	if err != nil {
		t.Fatal(err)
	}
	if r.BashCalls != 3 || r.Measured != 2 || r.MissingResults != 1 || r.Candidates != 2 {
		t.Errorf("huge lines: %+v", r)
	}
	if st := statMap(r.Top)["go test"]; st.Tokens < 100000 || st.Saved <= 0 {
		t.Errorf("go test on 3 MiB: %+v", st)
	}
}

func TestScanNULOutput(t *testing.T) {
	dir := t.TempDir()
	lines := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"n1","name":"Bash","input":{"command":"ls -la"}}]}}
{"type":"user","toolUseResult":{"stdout":"a\u0000 b\u0000\u0000\n","stderr":""},"message":{"content":[{"type":"tool_result","tool_use_id":"n1","content":"x"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"n2","name":"Bash","input":{"command":"cat bin"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"n2","content":"\u0000"}]}}
`
	if err := os.WriteFile(filepath.Join(dir, "nul.jsonl"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan Report, 1)
	go func() {
		r, _ := Scan(Options{Dirs: []string{dir}})
		done <- r
	}()
	select {
	case r := <-done:
		if r.Measured != 2 {
			t.Errorf("measured %d", r.Measured)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Scan hangs on NUL output")
	}
}

func TestReadLine(t *testing.T) {
	in := "short\n" + strings.Repeat("a", 100) + "\n" + strings.Repeat("b", 30) + "\r\nlast"
	br := bufio.NewReaderSize(strings.NewReader(in), 16)
	var got []string
	for {
		line, err := readLine(br, 50)
		got = append(got, string(line))
		if err != nil {
			if err != io.EOF {
				t.Fatal(err)
			}
			break
		}
	}
	want := []string{"short", "", strings.Repeat("b", 30), "last"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("lines = %q", got)
	}
}

func TestResultText(t *testing.T) {
	for raw, want := range map[string]string{
		`"plain"`: "plain",
		`[{"type":"text","text":"a"},{"type":"image","source":{}},{"type":"text","text":"b"}]`: "a\nb",
		`null`: "",
		``:     "",
		`42`:   "",
	} {
		if got := resultText(json.RawMessage(raw)); got != want {
			t.Errorf("resultText(%s) = %q", raw, got)
		}
	}
	for raw, want := range map[string]string{
		`{"stdout":"out\n","stderr":"err\n"}`: "out\nerr\n",
		`{"stdout":"","stderr":"err"}`:        "err",
		`{"stdout":"out"}`:                    "out",
	} {
		if got, ok := fullOutput(json.RawMessage(raw)); !ok || got != want {
			t.Errorf("fullOutput(%s) = %q, %v", raw, got, ok)
		}
	}
	for _, raw := range []string{`"Error: x"`, `{"type":"text"}`, ``, `[1]`} {
		if _, ok := fullOutput(json.RawMessage(raw)); ok {
			t.Errorf("fullOutput(%s) accepted", raw)
		}
	}
}

func TestKey(t *testing.T) {
	for argv, want := range map[string]string{
		"git status --short":            "git status",
		"/usr/bin/git -C /repo log -5":  "git log",
		"npm run build":                 "npm run",
		"npm test":                      "npm test",
		"python3 -m pytest -x tests/":   "python3 -m pytest",
		"cargo +nightly build":          "cargo build",
		"kubectl -n prod get pods":      "kubectl get",
		"docker compose -f x.yml logs":  "docker compose",
		"grep -rn secret src":           "grep",
		"ls -la /Users/me":              "ls",
		"pytest tests/test_secret.py":   "pytest",
		"git /some/path":                "git",
		"make SECRET=1":                 "make",
		"gh pr view 123":                "gh pr",
		"terraform plan -var=token=abc": "terraform plan",
	} {
		if got := Key(strings.Fields(argv)); got != want {
			t.Errorf("Key(%q) = %q, want %q", argv, got, want)
		}
	}
	if got := unsupportedKey("cd x && cat y", [][]string{{"cd", "x"}, {"cat", "y"}}); got != "cat" {
		t.Errorf("unsupportedKey = %q", got)
	}
	if got := unsupportedKey(`echo "== a" && cat a`, [][]string{{"echo", "== a"}, {"cat", "a"}}); got != "cat" {
		t.Errorf("unsupportedKey = %q", got)
	}
	if got := unsupportedKey("echo hi", [][]string{{"echo", "hi"}}); got != "echo" {
		t.Errorf("unsupportedKey = %q", got)
	}
	if got := unsupportedKey("$(weird) stuff", nil); got != "(other)" {
		t.Errorf("unsupportedKey fallback = %q", got)
	}
}

func TestTextLayout(t *testing.T) {
	r := Report{
		Files: 2, BashCalls: 12000, Measured: 11990, BashOutputTokens: 2_500_000,
		Candidates: 800, CandidateTokens: 1_200_000, CandidateOut: 400_000, WouldSaveTokens: 800_000,
		WouldSavePct: 32, CandidateSavePct: 66.7, AlreadyLx: 3, MissingResults: 10,
		Top: []Stat{
			{Command: "go test", Count: 300, Tokens: 900_000, TokensAfter: 200_000, Saved: 700_000},
			{Command: "git status", Count: 500, Tokens: 300_000, TokensAfter: 200_000, Saved: 100_000},
		},
		Unsupported: []Stat{{Command: "cat", Count: 90, Tokens: 50_000}},
	}
	var b bytes.Buffer
	r.Text(&b)
	out := b.String()
	for _, s := range []string{"12,000 Bash calls", "2.5M output tokens", "−800k", "32.0% of all Bash output",
		"go test", "####################", "Not rewritten", "cat"} {
		if !strings.Contains(out, s) {
			t.Errorf("text missing %q:\n%s", s, out)
		}
	}

	lines := strings.Split(out, "\n")
	var rows []string
	for _, l := range lines {
		if strings.HasPrefix(l, "  go test") || strings.HasPrefix(l, "  git status") {
			rows = append(rows, l)
		}
	}
	if len(rows) != 2 || strings.Index(rows[0], "300") != strings.Index(rows[1], "500") {
		t.Errorf("misaligned rows:\n%s", strings.Join(rows, "\n"))
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestKeyPrintable(t *testing.T) {
	for argv0, want := range map[string]string{
		"./scripts/build.sh":        "build.sh",
		"/bin/\x1b]0;pwned\x07tool": "?]0;pwned?tool",
		"ev\u202eil":                "ev?il",
		"a\x9bb":                    "a?b",
		"my tool":                   "my tool",
		strings.Repeat("z", 100):    strings.Repeat("z", maxKeyName) + "…",
		"go":                        "go",
	} {
		if got := Key([]string{argv0, "-x"}); got != want {
			t.Errorf("Key(%q) = %q, want %q", argv0, got, want)
		}
	}
	if got := unsupportedKey("\x1b[2J", [][]string{{"\x1b[2J"}}); got != "?[2J" {
		t.Errorf("unsupportedKey = %q", got)
	}
}
