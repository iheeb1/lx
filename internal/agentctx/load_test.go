package agentctx

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/testenv"
)

const testSID = "11111111-2222-4333-8444-555555555555"

func load(t *testing.T, src Source) *Snapshot {
	t.Helper()
	if src.Agent == "" {
		src.Agent = ClaudeCode
	}
	s, err := Load(src, DefaultTail)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func parsed(t *testing.T, path string) *Snapshot {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &Snapshot{}
	parseClaude(b, s, false, false)
	return s
}

func writeLines(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), testSID+".jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func runCmds(s *Snapshot) []string {
	var out []string
	for _, r := range s.Runs {
		out = append(out, r.Command)
	}
	return out
}

func TestLoadClaudeMain(t *testing.T) {
	s := load(t, Source{Path: "testdata/claude/main.jsonl", SessionID: testSID, Argv: []string{"go", "test", "./internal/engine/..."}})
	if !s.Caller {
		t.Fatal("the pending go test call identifies the caller")
	}
	if s.Model != "claude-sonnet-4-5" || s.Pressure.Used != 154200 || s.Pressure.Window != 200000 || s.WindowFrom != "model" {
		t.Fatalf("model %q used %d window %d from %q", s.Model, s.Pressure.Used, s.Pressure.Window, s.WindowFrom)
	}
	if s.Title != "Fix ParseMode verify handling" || !strings.HasPrefix(s.Prompt, "Fix the failing TestParseMode") {
		t.Fatalf("title %q prompt %q", s.Title, s.Prompt)
	}
	if len(s.Assistant) != 3 || s.Assistant[0] != "Now let me run the tests again to verify the fix." ||
		!strings.HasPrefix(s.Assistant[1], "The test fails because") || !strings.HasPrefix(s.Assistant[2], "I'll read modes.go") {
		t.Fatalf("assistant texts %q", s.Assistant)
	}
	wantFiles := []File{
		{"/repo/internal/engine/modes_test.go", "write", 1},
		{"/repo/internal/engine/modes.go", "edit", 3},
	}
	if fmt.Sprint(s.Files) != fmt.Sprint(wantFiles) {
		t.Fatalf("files %v, want %v", s.Files, wantFiles)
	}
	wantRuns := []Run{
		{Command: "go test ./internal/engine/...", TurnsAgo: 0, InContext: true, Pending: true},
		{Command: "git status", TurnsAgo: 2, InContext: true},
		{Command: "go test ./internal/engine -run TestParseMode", TurnsAgo: 5, LxID: 12, InContext: true, Failed: true},
	}
	if fmt.Sprintf("%+v", s.Runs) != fmt.Sprintf("%+v", wantRuns) {
		t.Fatalf("runs\n%+v\nwant\n%+v", s.Runs, wantRuns)
	}
	if s.Turns != 7 || s.Compactions != 0 || !s.CompactedAt.IsZero() || s.BadLines != 0 || s.Subagent != "" {
		t.Fatalf("turns %d compactions %d bad %d sub %q", s.Turns, s.Compactions, s.BadLines, s.Subagent)
	}
	if m := s.InferMode(); m != engine.ModeVerify {
		t.Fatalf("mode %v", m)
	}
}

func TestLoadIgnoresSidechainsInMainTranscript(t *testing.T) {
	s := load(t, Source{Path: "testdata/claude/main.jsonl"})
	for _, r := range s.Runs {
		if r.Command == "echo sidechain" || r.LxID == 99 {
			t.Fatalf("sidechain run leaked: %+v", r)
		}
	}
	for _, a := range s.Assistant {
		if strings.Contains(a, "Sidechain") {
			t.Fatalf("sidechain text leaked: %q", a)
		}
	}
	for _, term := range s.Focus().Terms {
		if strings.Contains(term.Text, "Sidechain") {
			t.Fatalf("sidechain term leaked: %q", term.Text)
		}
	}
}

func TestLoadCompaction(t *testing.T) {
	s := load(t, Source{Path: "testdata/claude/compact.jsonl"})
	if s.Compactions != 1 || !s.CompactedAt.Equal(time.Date(2026, 9, 20, 10, 4, 0, 0, time.UTC)) {
		t.Fatalf("compactions %d at %v", s.Compactions, s.CompactedAt)
	}
	if s.Pressure.Used != 30105 || s.Pressure.Window != 1000000 || s.WindowFrom != "usage" {
		t.Fatalf("pressure %+v from %q: a 450K compaction proves a 1M window", s.Pressure, s.WindowFrom)
	}
	for _, r := range s.Runs {
		if r.InContext {
			t.Fatalf("no pending call names the caller, so nothing is in its context: %+v", r)
		}
	}
	s = parsed(t, "testdata/claude/compact.jsonl")
	want := []Run{
		{Command: "go test ./internal/tokens -run TestCJK", TurnsAgo: 0, LxID: 7, InContext: true, Failed: true},
		{Command: "go vet ./...", TurnsAgo: 1, LxID: 4, InContext: true},
		{Command: "go test ./internal/tokens", TurnsAgo: 2, LxID: 3},
	}
	if fmt.Sprintf("%+v", s.Runs) != fmt.Sprintf("%+v", want) {
		t.Fatalf("runs\n%+v\nwant\n%+v", s.Runs, want)
	}
	if s.Prompt != "Refactor the tokenizer in internal/tokens/count.go" || s.CompactedAt.IsZero() {
		t.Fatalf("the compaction summary is not a prompt: %q", s.Prompt)
	}
}

func TestLoadCorruptLines(t *testing.T) {
	s := load(t, Source{Path: "testdata/claude/corrupt.jsonl"})
	if s.BadLines != 8 {
		t.Errorf("bad lines %d, want 8", s.BadLines)
	}
	if len(s.Runs) != 1 || s.Runs[0].Command != "make test" || !s.Runs[0].Pending {
		t.Errorf("runs %+v", s.Runs)
	}
	if len(s.Assistant) != 1 || !strings.Contains(s.Assistant[0], "TestGoodOne") {
		t.Errorf("assistant %q", s.Assistant)
	}
	if s.Title != "bad � utf8" || s.Compactions != 1 || s.Model != "claude-sonnet-4-5" {
		t.Errorf("title %q compactions %d model %q", s.Title, s.Compactions, s.Model)
	}
	if len(s.Files) != 0 {
		t.Errorf("files %v", s.Files)
	}
}

func TestLoadMissingFields(t *testing.T) {
	lines := []string{
		`{"type":"assistant","message":{"id":"a","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1"}]}}`,
		`{"type":"assistant","message":{"id":"b","model":"<synthetic>","usage":{"input_tokens":999999},"content":[{"type":"text","text":"API Error"}]}}`,
	}
	p := filepath.Join(t.TempDir(), "x.jsonl")
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	s := load(t, Source{Path: p})
	if s.Model != "" || s.Pressure.Used != 0 || s.Pressure.Window != defaultWindow || s.WindowFrom != "default" {
		t.Fatalf("synthetic messages carry no model or usage: %q %+v %q", s.Model, s.Pressure, s.WindowFrom)
	}
	if len(s.Runs) != 1 || s.Runs[0].Pending || s.Runs[0].LxID != 0 {
		t.Fatalf("runs %+v", s.Runs)
	}
	if s.Pressure.Known() {
		t.Fatal("pressure without usage must be unknown")
	}
}

func TestLoadCodex(t *testing.T) {
	s := load(t, Source{Agent: Codex, Path: "testdata/codex/rollout.jsonl", SessionID: "01a0deae-5150-79f3-b313-52a0160a0ef6"})
	if s.Model != "gpt-5.5-codex" || s.Pressure.Used != 120000 || s.Pressure.Window != 258400 || s.WindowFrom != "transcript" {
		t.Fatalf("model %q pressure %+v from %q", s.Model, s.Pressure, s.WindowFrom)
	}
	if s.Compactions != 1 || !s.CompactedAt.Equal(time.Date(2026, 9, 26, 18, 0, 6, 0, time.UTC)) {
		t.Fatalf("compactions %d at %v", s.Compactions, s.CompactedAt)
	}
	if s.Prompt != "Now fix the TS2322 error in src/app.tsx and run the build again" {
		t.Fatalf("prompt %q", s.Prompt)
	}
	if len(s.Assistant) != 2 || !strings.HasPrefix(s.Assistant[0], "The TS2322 error") {
		t.Fatalf("assistant %q", s.Assistant)
	}
	want := []Run{
		{Command: "go test ./internal/... -run TestCodexPending", TurnsAgo: 0, InContext: true, Pending: true},
		{Command: "go test ./pkg -run 'TestX'", TurnsAgo: 1, LxID: 31, InContext: true},
		{Command: "go build ./...", TurnsAgo: 1, LxID: 30, InContext: true},
		{Command: "git status", TurnsAgo: 2, InContext: true},
		{Command: "npm test", TurnsAgo: 3, LxID: 21},
	}
	if fmt.Sprintf("%+v", s.Runs) != fmt.Sprintf("%+v", want) {
		t.Fatalf("runs\n%+v\nwant\n%+v", s.Runs, want)
	}
	wantFiles := []File{{"/repo/src/app.tsx", "edit", 0}, {"/repo/src/new.ts", "write", 0}}
	if fmt.Sprint(s.Files) != fmt.Sprint(wantFiles) {
		t.Fatalf("files %v", s.Files)
	}
	if m := s.InferMode(); m != engine.ModeVerify {
		t.Fatalf("mode %v", m)
	}
	f := s.Focus()
	if !hasTerm(f, "TS2322") || !hasTerm(f, "app.tsx") || !hasTerm(f, "renderHeader") {
		t.Fatalf("focus %+v", f)
	}
}

func TestShellCommandShapes(t *testing.T) {
	cases := map[string]string{
		`{"cmd":"git diff"}`:                         "git diff",
		`{"command":["bash","-lc","go test ./..."]}`: "go test ./...",
		`{"command":["ls","-la"]}`:                   "ls -la",
		`{"command":"make"}`:                         "make",
		`{"workdir":"/x"}`:                           "",
		`not json`:                                   "",
	}
	for in, want := range cases {
		if got := shellCommand([]byte(in)); got != want {
			t.Errorf("shellCommand(%s) = %q, want %q", in, got, want)
		}
	}
	js := "await tools.exec_command({cmd: \"a \\\"b\\\"\"}); tools.exec_command({ 'cmd': 'it\\'s' }); tools.exec_command({cmd: `echo ${x}`})"
	if got := jsCommands(js); fmt.Sprint(got) != fmt.Sprint([]string{`a "b"`, "it's", "echo ${x}"}) {
		t.Errorf("jsCommands = %q", got)
	}
}

func hasTerm(f *engine.Focus, text string) bool {
	if f == nil {
		return false
	}
	for _, t := range f.Terms {
		if t.Text == text {
			return true
		}
	}
	return false
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadFindsTheSubagentThatIssuedTheCommand(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, testSID+".jsonl")
	copyFile(t, "testdata/claude/compact.jsonl", mainPath)
	sub := filepath.Join(dir, testSID, "subagents", "workflows", "wf_1", "agent-a1b2c3.jsonl")
	copyFile(t, "testdata/claude/subagent.jsonl", sub)
	other := filepath.Join(dir, testSID, "subagents", "agent-zzz.jsonl")
	copyFile(t, "testdata/claude/main.jsonl", other)
	src := Source{Agent: ClaudeCode, SessionID: testSID, Path: mainPath}

	src.Argv = []string{"go", "test", "-race"}
	s := load(t, src)
	if s.Subagent != "a1b2c3" || s.Model != "claude-haiku-4-5" || s.Pressure.Used != 60510 || s.Pressure.Window != 200000 {
		t.Fatalf("sub %q model %q pressure %+v", s.Subagent, s.Model, s.Pressure)
	}
	if s.SessionID != testSID || !strings.HasPrefix(s.Prompt, "Investigate why TestWorkerPool") {
		t.Fatalf("session %q prompt %q", s.SessionID, s.Prompt)
	}
	if s.InferMode() != engine.ModeError {
		t.Fatalf("mode %v", s.InferMode())
	}

	src.Argv = []string{"/usr/local/go/bin/go", "test", "./internal/engine/..."}
	if s := load(t, src); s.Subagent != "zzz" {
		t.Fatalf("argv should pick agent-zzz, got %q", s.Subagent)
	}

	src.Argv = []string{"cargo", "build"}
	if s := load(t, src); s.Subagent != "" || s.Path != mainPath {
		t.Fatalf("no transcript issued cargo: want the main one, got %q %q", s.Subagent, s.Path)
	}

	old := time.Now().Add(-time.Hour)
	os.Chtimes(sub, old, old)
	src.Argv = []string{"go", "test", "-race"}
	if s := load(t, src); s.Subagent == "a1b2c3" {
		t.Fatal("a subagent transcript idle for an hour is not the caller")
	}
}

func TestLoadPrefersTheMainTranscriptWhenItIssuedTheCommand(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, testSID+".jsonl")
	copyFile(t, "testdata/claude/main.jsonl", mainPath)
	copyFile(t, "testdata/claude/subagent.jsonl", filepath.Join(dir, testSID, "subagents", "agent-a1b2c3.jsonl"))
	s := load(t, Source{SessionID: testSID, Path: mainPath, Argv: []string{"go", "test"}})
	if s.Subagent != "" || s.Model != "claude-sonnet-4-5" {
		t.Fatalf("got subagent %q model %q", s.Subagent, s.Model)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(Source{Agent: ClaudeCode, Path: filepath.Join(t.TempDir(), "nope.jsonl")}, 0); err == nil {
		t.Fatal("missing transcript must be an error")
	}
	if _, err := Load(Source{Agent: "gemini", Path: "testdata/claude/main.jsonl"}, 0); err == nil {
		t.Fatal("unknown agent must be an error")
	}
	fifo := filepath.Join(t.TempDir(), "fifo.jsonl")
	if mkfifo(fifo) == nil {
		done := make(chan error, 1)
		go func() {
			_, err := Load(Source{Agent: ClaudeCode, Path: fifo}, 0)
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("a fifo is not a transcript")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Load blocked on a fifo")
		}
	}
}

func TestLoadLineLongerThanTheTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.jsonl")
	head, _ := os.ReadFile("testdata/claude/main.jsonl")
	big := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"x","content":"` + strings.Repeat("a", 3<<20) + `"}]}}`
	os.WriteFile(p, append(head, big...), 0o600)
	s, err := Load(Source{Agent: ClaudeCode, Path: p}, 1<<20)
	if err != nil || s.Bytes != 0 || len(s.Runs) != 0 {
		t.Fatalf("a tail inside one line has no context: err %v bytes %d runs %d", err, s.Bytes, len(s.Runs))
	}
	os.WriteFile(p, append(append([]byte(big), '\n'), head...), 0o600)
	if s := load(t, Source{Path: p}); len(s.Runs) != 3 || s.Bytes > DefaultTail {
		t.Fatalf("runs %d bytes %d", len(s.Runs), s.Bytes)
	}
}

func TestLoadWindowOverrides(t *testing.T) {
	s := load(t, Source{Path: "testdata/claude/main.jsonl", Window: 500000})
	if s.Pressure.Window != 500000 || s.WindowFrom != "LX_CONTEXT_WINDOW" {
		t.Fatalf("override: %+v %q", s.Pressure, s.WindowFrom)
	}
	cases := map[string]int{
		"claude-opus-5-5":                 1000000,
		"claude-fable-5-1":                1000000,
		"claude-sonnet-4-5":               200000,
		"claude-sonnet-4-5[1m]":           1000000,
		"claude-haiku-4-5-20251001":       200000,
		"us.anthropic.claude-opus-4-8-v1": 1000000,
		"some-local-model":                200000,
	}
	for m, want := range cases {
		if got, _ := modelWindow(m); got != want {
			t.Errorf("modelWindow(%q) = %d, want %d", m, got, want)
		}
	}
}

func writeHuge(t testing.TB, path string, size int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	fmt.Fprintf(w, `{"type":"ai-title","aiTitle":"ANCIENT"}`+"\n")
	fmt.Fprintf(w, `{"type":"assistant","message":{"id":"old","model":"claude-opus-4-8","content":[{"type":"tool_use","id":"t0","name":"Bash","input":{"command":"echo ANCIENT"}}]}}`+"\n")
	out, _ := json.Marshal(strings.Repeat("ok  \tpkg/x\t0.01s \"quoted\" \\ back\n", 1500))
	written := 0
	for i := 0; written < size; i++ {
		n, _ := fmt.Fprintf(w, `{"type":"assistant","message":{"id":"m%d","model":"claude-opus-4-8","content":[{"type":"tool_use","id":"t%d","name":"Bash","input":{"command":"go test ./..."}}]}}`+"\n", i, i)
		m, _ := fmt.Fprintf(w, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t%d","content":%s}]},"toolUseResult":{"stdout":%s}}`+"\n", i, out, out)
		written += n + m
	}
	tail, _ := os.ReadFile("testdata/claude/main.jsonl")
	w.Write(tail)
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func TestLoadHugeTranscriptReadsOnlyTheTail(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a 50 MB transcript")
	}
	home := t.TempDir()
	cwd := "/work/app"
	path := filepath.Join(home, ".claude", "projects", Slug(cwd), testSID+".jsonl")
	os.MkdirAll(filepath.Dir(path), 0o755)
	writeHuge(t, path, 50<<20)
	if st, _ := os.Stat(path); st.Size() < 50<<20 {
		t.Fatalf("fixture is only %d bytes", st.Size())
	}
	env := func(k string) string {
		return map[string]string{"HOME": home, "CLAUDE_CODE_SESSION_ID": testSID}[k]
	}
	best := time.Hour
	var s *Snapshot
	for i := 0; i < 5; i++ {
		start := time.Now()
		src, ok := Find(env, cwd)
		if !ok {
			t.Fatal("not found")
		}
		src.Argv = []string{"go", "test"}
		var err error
		if s, err = Load(src, DefaultTail); err != nil {
			t.Fatal(err)
		}
		best = min(best, time.Since(start))
	}
	if s.Bytes > DefaultTail || s.Title != "Fix ParseMode verify handling" || s.Model != "claude-sonnet-4-5" {
		t.Fatalf("bytes %d title %q model %q", s.Bytes, s.Title, s.Model)
	}
	for _, r := range s.Runs {
		if r.Command == "echo ANCIENT" {
			t.Fatal("read past the tail")
		}
	}
	if len(s.Runs) < 4 || s.Runs[0].Command != "go test ./internal/engine/..." || s.Runs[3].Command != "go test ./..." {
		t.Fatalf("runs %d: %q", len(s.Runs), runCmds(s))
	}
	if limit := testenv.Scale(10 * time.Millisecond); best > limit {
		t.Fatalf("Find+Load on a 50 MB transcript took %v, want < %v", best, limit)
	}
	t.Logf("Find+Load: %v", best)
}

func BenchmarkLoad(b *testing.B) {
	path := filepath.Join(b.TempDir(), "t.jsonl")
	writeHuge(b, path, 8<<20)
	src := Source{Agent: ClaudeCode, Path: path}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Load(src, DefaultTail); err != nil {
			b.Fatal(err)
		}
	}
}

func TestLoadPromptSkipsNotices(t *testing.T) {
	lines := []string{
		`{"type":"user","message":{"content":[{"type":"text","text":"[Harness] fix TestBracketed in b.go"}]}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"<task-notification>done</task-notification>"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"x","content":"ok"},{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`,
		`{"type":"user","isMeta":true,"message":{"content":"meta text"}}`,
	}
	p := filepath.Join(t.TempDir(), "x.jsonl")
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	if s := load(t, Source{Path: p}); s.Prompt != "[Harness] fix TestBracketed in b.go" {
		t.Fatalf("prompt %q", s.Prompt)
	}
}

func TestLoadCodexTruncatedOutputIsNotInContext(t *testing.T) {
	for _, cut := range []string{"Warning: truncated output (original token count: 21000)\n", "…19507 tokens truncated…\n"} {
		out := "Process exited with code 1\nOutput:\n" + cut + "FAIL\n[lx: 900→80 lines (−90%) · full output: lx show 7]"
		body, _ := json.Marshal(out)
		path := filepath.Join(t.TempDir(), "rollout.jsonl")
		lines := []string{
			`{"timestamp":"2026-09-26T18:00:00.000Z","type":"session_meta","payload":{"id":"s1","session_id":"s1","cwd":"/repo"}}`,
			`{"timestamp":"2026-09-26T18:00:01.000Z","type":"turn_context","payload":{"cwd":"/repo","model":"gpt-5.5-codex"}}`,
			`{"timestamp":"2026-09-26T18:00:02.000Z","type":"response_item","payload":{"type":"function_call","id":"fc1","name":"exec_command","arguments":"{\"cmd\": \"go test ./...\", \"workdir\": \"/repo\"}","call_id":"c1"}}`,
			`{"timestamp":"2026-09-26T18:00:03.000Z","type":"response_item","payload":{"type":"function_call_output","id":"fo1","call_id":"c1","output":` + string(body) + `}}`,
		}
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		s := load(t, Source{Agent: Codex, Path: path, SessionID: "s1"})
		for _, r := range s.Runs {
			if r.Command == "go test ./..." && r.InContext {
				t.Fatalf("%q: Codex cut the output, so the model never saw all of it: %+v", cut, r)
			}
		}
	}
}
