package agentctx

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func bashCall(msg, uuid, id, cmd string) string {
	return fmt.Sprintf(`{"type":"assistant","uuid":%q,"message":{"id":%q,"model":"claude-opus-5-5","usage":{"input_tokens":10,"cache_read_input_tokens":90000},"content":[{"type":"tool_use","id":%q,"name":"Bash","input":{"command":%q}}]}}`, uuid, msg, id, cmd)
}

func bashResult(uuid, id, out string) string {
	return fmt.Sprintf(`{"type":"user","uuid":%q,"message":{"content":[{"type":"tool_result","tool_use_id":%q,"content":%s}]}}`, uuid, id, jsonStr(out))
}

const receipt12 = "FAIL\n[lx: 307→29 lines (−71%) · full output: lx show 12]"

func mainWithRun(t *testing.T, pending string) string {
	t.Helper()
	lines := []string{bashCall("m1", "a1", "t1", "go test ./..."), bashResult("u1", "t1", receipt12)}
	if pending != "" {
		lines = append(lines, bashCall("m2", "a2", "t2", pending))
	}
	return writeLines(t, lines...)
}

func subagent(t *testing.T, main, name, pending string, age time.Duration) string {
	t.Helper()
	p := filepath.Join(strings.TrimSuffix(main, ".jsonl"), "subagents", "agent-"+name+".jsonl")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(strings.Replace(bashCall("s1", "sa1", "st1", pending), `"type":"assistant"`, `"type":"assistant","isSidechain":true`, 1)+"\n"), 0o600)
	if age > 0 {
		old := time.Now().Add(-age)
		os.Chtimes(p, old, old)
	}
	return p
}

func TestLoadCallerProvesContext(t *testing.T) {
	main := mainWithRun(t, "cd /repo && go test ./...")
	s := load(t, Source{Path: main, Argv: []string{"go", "test", "./..."}})
	if !s.Caller || len(s.Runs) != 2 || !s.Runs[1].InContext || s.Runs[1].LxID != 12 {
		t.Fatalf("caller %v runs %+v", s.Caller, s.Runs)
	}
}

func TestLoadWithoutCallerClaimsNothingInContext(t *testing.T) {
	main := mainWithRun(t, "")
	for _, argv := range [][]string{nil, {"go", "test", "./..."}} {
		s := load(t, Source{Path: main, Argv: argv})
		if s.Caller || len(s.Runs) != 1 || s.Runs[0].InContext || s.Runs[0].LxID != 12 {
			t.Fatalf("argv %q: caller %v runs %+v", argv, s.Caller, s.Runs)
		}
	}
}

func TestLoadSubagentNotYetWrittenFallsBackWithoutCaller(t *testing.T) {
	main := mainWithRun(t, "")
	subagent(t, main, "other", "npm test", 0)
	s := load(t, Source{Path: main, Argv: []string{"go", "test", "./..."}})
	if s.Caller || s.Subagent != "" || s.Runs[0].InContext {
		t.Fatalf("the caller's pending call isn't written yet: caller %v sub %q runs %+v", s.Caller, s.Subagent, s.Runs)
	}
}

func TestLoadAmbiguousCaller(t *testing.T) {
	main := mainWithRun(t, "go test ./...")
	subagent(t, main, "twin", "go test ./...", 0)
	s := load(t, Source{Path: main, Argv: []string{"go", "test", "./..."}})
	if s.Caller || s.Runs[1].InContext {
		t.Fatalf("two transcripts wait on the same command: caller %v runs %+v", s.Caller, s.Runs)
	}

	main = mainWithRun(t, "")
	subagent(t, main, "a", "go test ./...", 0)
	subagent(t, main, "b", "go test ./...", 0)
	if s := load(t, Source{Path: main, Argv: []string{"go", "test", "./..."}}); s.Caller || s.Subagent == "" {
		t.Fatalf("two subagents: caller %v sub %q", s.Caller, s.Subagent)
	}
}

func TestLoadTooManySubagentsToProbe(t *testing.T) {
	main := mainWithRun(t, "")
	subagent(t, main, "caller", "go test ./...", 0)
	for i := 0; i < maxProbes; i++ {
		subagent(t, main, fmt.Sprint("busy", i), "sleep 1", time.Duration(i+1)*time.Second)
	}
	s := load(t, Source{Path: main, Argv: []string{"go", "test", "./..."}})
	if s.Subagent != "caller" || s.Caller {
		t.Fatalf("unprobed subagents may wait on the same command: sub %q caller %v", s.Subagent, s.Caller)
	}
	os.Remove(filepath.Join(strings.TrimSuffix(main, ".jsonl"), "subagents", "agent-busy0.jsonl"))
	if s := load(t, Source{Path: main, Argv: []string{"go", "test", "./..."}}); s.Subagent != "caller" || !s.Caller {
		t.Fatalf("all probed: sub %q caller %v", s.Subagent, s.Caller)
	}
}

func TestLoadSubagentsNewestDirsFirst(t *testing.T) {
	main := mainWithRun(t, "")
	root := filepath.Join(strings.TrimSuffix(main, ".jsonl"), "subagents", "workflows")
	old := time.Now().Add(-time.Hour)
	oldDirs := func(from, n int) {
		for i := from; i < from+n; i++ {
			d := filepath.Join(root, fmt.Sprintf("wf_a%03d", i))
			os.MkdirAll(d, 0o755)
			for j := 0; j < 3; j++ {
				f := filepath.Join(d, fmt.Sprintf("agent-%d.jsonl", j))
				os.WriteFile(f, nil, 0o600)
				os.Chtimes(f, old, old)
			}
			os.Chtimes(d, old, old)
		}
	}
	oldDirs(0, maxSubDirs/5)
	p := filepath.Join(root, "wf_new", "agent-live.jsonl")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(bashCall("s1", "sa1", "st1", "go vet ./...")+"\n"), 0o600)
	argv := []string{"go", "vet", "./..."}
	if s := load(t, Source{Path: main, Argv: argv}); s.Subagent != "live" || !s.Caller {
		t.Fatalf("sub %q caller %v", s.Subagent, s.Caller)
	}
	oldDirs(maxSubDirs/5, maxSubDirs/5)
	s := load(t, Source{Path: main, Argv: argv})
	if s.Subagent != "live" {
		t.Fatalf("the newest dir is read before the budget runs out: sub %q", s.Subagent)
	}
	if s.Caller {
		t.Fatal("subagent transcripts left unread may wait on the same command")
	}
}

func TestCommandMatches(t *testing.T) {
	cases := []struct {
		cmd  string
		argv []string
		want bool
	}{
		{"go test ./...", []string{"go", "test", "./..."}, true},
		{"cd /repo && LX_MODE=verify go test ./... 2>&1 | tail -5", []string{"go", "test", "./..."}, true},
		{"lx go test -run 'TestA|TestB' ./x", []string{"go", "test", "-run", "TestA|TestB", "./x"}, true},
		{`git commit -m "fix: the \"quoted\" bug"`, []string{"git", "commit", "-m", `fix: the "quoted" bug`}, true},
		{"/usr/local/go/bin/go build", []string{"go", "build"}, true},
		{"./lx ctx --json", []string{"lx", "ctx"}, true},
		{"cd /go/src && make test ./...", []string{"go", "test", "./..."}, false},
		{"go test ./... -run X", []string{"go", "test", "./internal"}, false},
		{"gofmt -l *.go", []string{"gofmt", "-l", "a.go", "b.go"}, false},
		{"go test $PKGS", []string{"go", "test", "./a"}, false},
		{"echo 'go test ./...'", []string{"go", "test", "./..."}, false},
		{"go test ./...", nil, false},
	}
	for _, c := range cases {
		if got := commandMatches(c.cmd, c.argv); got != c.want {
			t.Errorf("commandMatches(%q, %q) = %v, want %v", c.cmd, c.argv, got, c.want)
		}
	}
}

func TestLoadPersistedOutputIsNotInContext(t *testing.T) {
	main := writeLines(t,
		bashCall("m1", "a1", "t1", "go test ./..."),
		bashResult("u1", "t1", "<persisted-output>\nOutput too large (80KB). Full output saved to: /tmp/x.txt\n\nPreview (first 2KB):\n[lx: 9→2 lines (−70%) · full output: lx show 3]\n</persisted-output>"),
		bashCall("m2", "a2", "t2", "go vet ./..."))
	s := load(t, Source{Path: main, Argv: []string{"go", "vet", "./..."}})
	if !s.Caller || len(s.Runs) != 2 || s.Runs[1].InContext {
		t.Fatalf("the model only saw a preview: %+v", s.Runs)
	}
}

func TestLoadCompactionKeepsOnlyPreservedPairs(t *testing.T) {
	boundary := `{"type":"system","subtype":"compact_boundary","uuid":"b1","timestamp":"2026-09-20T10:04:00Z","compactMetadata":{"trigger":"auto","preTokens":180000,"preservedMessages":{"uuids":["a1","a2","u2"],"allUuids":["a1","u1","a2","u2"]}}}`
	main := writeLines(t,
		bashCall("m1", "a1", "t1", "make one"), bashResult("u1", "t1", "ok"),
		bashCall("m2", "a2", "t2", "make two"), bashResult("u2", "t2", "ok"),
		boundary,
		bashCall("m3", "a3", "t3", "make three"))
	s := load(t, Source{Path: main, Argv: []string{"make", "three"}})
	got := map[string]bool{}
	for _, r := range s.Runs {
		got[r.Command] = r.InContext
	}
	if !got["make three"] || !got["make two"] || got["make one"] {
		t.Fatalf("in context %v: a tool call whose result was dropped is not in context", got)
	}
}

func TestLoadSkipsSyntheticMessages(t *testing.T) {
	main := writeLines(t,
		`{"type":"assistant","uuid":"a0","message":{"id":"m0","model":"claude-opus-5-5","usage":{"input_tokens":5,"cache_read_input_tokens":1000},"content":[{"type":"text","text":"Let me debug TestRealWork."}]}}`,
		`{"type":"assistant","uuid":"a1","message":{"id":"m1","model":"<synthetic>","usage":{"input_tokens":0},"content":[{"type":"text","text":"API Error: 529 Overloaded. Let me verify ErrOverloaded."}]}}`)
	s := load(t, Source{Path: main})
	if len(s.Assistant) != 1 || s.Turns != 1 || s.Pressure.Used != 1005 || s.InferMode().String() != "error" {
		t.Fatalf("texts %q turns %d used %d", s.Assistant, s.Turns, s.Pressure.Used)
	}
}

func TestLoadIgnoresAbsurdUsage(t *testing.T) {
	main := writeLines(t,
		`{"type":"system","subtype":"compact_boundary","uuid":"b1","compactMetadata":{"preTokens":1e300}}`,
		`{"type":"assistant","uuid":"a0","message":{"id":"m0","model":"claude-sonnet-4-5","usage":{"input_tokens":9000000000000000000,"cache_read_input_tokens":-5,"cache_creation_input_tokens":1000},"content":[]}}`)
	s := load(t, Source{Path: main})
	if s.Pressure.Used != 1000 || s.Pressure.Window != 200000 {
		t.Fatalf("pressure %+v", s.Pressure)
	}
}

func TestLoadUsageAfterACompaction(t *testing.T) {
	before := `{"type":"assistant","uuid":"a0","message":{"id":"m0","model":"claude-sonnet-4-5","usage":{"input_tokens":10,"cache_read_input_tokens":190000},"content":[]}}`
	boundary := `{"type":"system","subtype":"compact_boundary","uuid":"b1","compactMetadata":{"trigger":"auto","preTokens":190010,"postTokens":21000}}`
	if s := load(t, Source{Path: writeLines(t, before, boundary)}); s.Pressure.Used != 21000 {
		t.Fatalf("the compaction is newer than any usage: %+v", s.Pressure)
	}
	if s := load(t, Source{Path: writeLines(t, before, strings.Replace(boundary, `,"postTokens":21000`, "", 1))}); s.Pressure.Known() {
		t.Fatalf("usage from before the compaction is stale: %+v", s.Pressure)
	}
}

func TestReadTailKeepsALineStartingAtTheCut(t *testing.T) {
	first := `{"type":"ai-title","aiTitle":"dropped"}`
	last := `{"type":"ai-title","aiTitle":"kept"}`
	p := writeLines(t, first, last)
	buf, err := readTail(p, len(last)+1)
	if err != nil || string(buf) != last+"\n" {
		t.Fatalf("%q %v", buf, err)
	}
	if buf, _ := readTail(p, len(last)); len(buf) != 0 {
		t.Fatalf("a partial line is dropped: %q", buf)
	}
	all := len(first) + len(last) + 2
	if buf, _ := readTail(p, all-1); string(buf) != last+"\n" {
		t.Fatalf("the tail never exceeds max: %q", buf)
	}
	if buf, _ := readTail(p, all); len(buf) != all {
		t.Fatalf("a file that fits is read whole: %q", buf)
	}
}

func TestLoadMiddleCutIsNotInContext(t *testing.T) {
	main := writeLines(t,
		bashCall("m1", "a1", "t1", "go test ./..."),
		bashResult("u1", "t1", "Error: Exit code 1\n--- FAIL: TestA (0.00s)\n... [4811 characters truncated] ...\nFAIL\n[lx: 900→80 lines (−90%) · full output: lx show 3]"),
		bashCall("m2", "a2", "t2", "go vet ./..."))
	s := load(t, Source{Path: main, Argv: []string{"go", "vet", "./..."}})
	if !s.Caller || len(s.Runs) != 2 || s.Runs[1].InContext {
		t.Fatalf("the host cut the middle of the view, so the model never saw all of it: %+v", s.Runs)
	}
}
