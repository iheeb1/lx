package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/agentctx"
	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tee"
)

type deltaTool struct{}

func (deltaTool) Name() string                                       { return "delta-test-tool" }
func (deltaTool) Match(c *engine.Context) bool                       { return c.Name() == "lxdeltatool" }
func (deltaTool) Apply(c *engine.Context, out string) (string, bool) { return out, true }

func (deltaTool) Items(c *engine.Context, out string) []engine.Item {
	var items []engine.Item
	for _, ln := range strings.Split(out, "\n") {
		if name, _, ok := strings.Cut(strings.TrimPrefix(ln, "FAIL "), ":"); ok && strings.HasPrefix(ln, "FAIL ") {
			items = append(items, engine.Item{Key: name, Block: ln})
		}
	}
	return items
}

func init() { engine.Register(deltaTool{}) }

var deltaArgv = []string{"lxdeltatool", "run"}

func toolOutput(fails ...string) string {
	var b strings.Builder
	for _, f := range fails {
		b.WriteString("FAIL " + f + "\n")
	}
	for i := range 40 {
		fmt.Fprintf(&b, "    detail line %d of a long report\n", i)
	}
	b.WriteString("FAIL\tpkg\t0.2s")
	return b.String()
}

func saveRun(t *testing.T, argv []string, cwd string, exit int, out string) int {
	t.Helper()
	return saveShown(t, argv, cwd, exit, out, "delta-test-tool")
}

func saveShown(t *testing.T, argv []string, cwd string, exit int, out, filter string) int {
	t.Helper()
	id, err := tee.Save(tee.Meta{Argv: argv, Cwd: cwd, Exit: exit, Filter: filter}, out)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func snapWith(runs ...agentctx.Run) *agentctx.Snapshot {
	for i := range runs {
		if runs[i].Command == "" {
			runs[i].Command = "lxdeltatool run"
		}
	}
	return &agentctx.Snapshot{Caller: true, Runs: append([]agentctx.Run{{Command: "lx lxdeltatool run", Pending: true}}, runs...)}
}

func TestSessionDelta(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	prev := toolOutput("alpha: boom", "beta: nil map", "gamma: timeout", "delta: bad input")
	id := saveRun(t, deltaArgv, "/repo", 1, prev)
	cur := toolOutput("beta: nil map", "gamma: timeout", "delta: bad input", "omega: index out of range")
	c := &engine.Context{Argv: deltaArgv, Cwd: "/repo", Exit: 1}
	snap := snapWith(agentctx.Run{Command: "lx lxdeltatool run", TurnsAgo: 2, LxID: id, InContext: true, Failed: true})

	out, ok := sessionDelta(snap, c, cur, cur)
	if !ok {
		t.Fatal("no delta")
	}
	want := fmt.Sprintf("[lx: same command as lx show %d (2 turns ago, still in your context) — only what changed:]\n", id) +
		"new:\nFAIL omega: index out of range\nfixed: alpha\nstill failing:\n  beta\n  gamma\n  delta\nFAIL\tpkg\t0.2s"
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}

	t.Setenv("LX_DELTA", "0")
	if _, ok := sessionDelta(snap, c, cur, cur); ok {
		t.Fatal("LX_DELTA=0 turns it off")
	}
}

func TestSessionDeltaIdentical(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	raw := toolOutput("alpha: boom")
	id := saveRun(t, deltaArgv, "/repo", 1, raw)
	snap := snapWith(agentctx.Run{TurnsAgo: 1, LxID: id, InContext: true})
	out, ok := sessionDelta(snap, &engine.Context{Argv: deltaArgv, Cwd: "/repo", Exit: 1}, raw, raw)
	if !ok || out != fmt.Sprintf("[lx: output identical to lx show %d (1 turn ago, still in your context)]\nFAIL\tpkg\t0.2s", id) {
		t.Fatalf("ok=%v\n%s", ok, out)
	}
	out, _ = sessionDelta(snap, &engine.Context{Argv: deltaArgv, Cwd: "/repo", Exit: 2}, raw, raw)
	if strings.Contains(out, "identical") {
		t.Fatalf("a different exit code is not an identical run:\n%s", out)
	}
}

func TestSessionDeltaPicksTheSameCommand(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	prev := toolOutput("alpha: boom", "beta: nil map", "gamma: timeout")
	cur := toolOutput("beta: nil map", "gamma: timeout")
	c := &engine.Context{Argv: deltaArgv, Cwd: "/repo", Exit: 1}
	same := saveRun(t, deltaArgv, "/repo", 1, prev)
	otherArgs := saveRun(t, []string{"lxdeltatool", "run", "-v"}, "/repo", 1, prev)
	otherDir := saveRun(t, deltaArgv, "/elsewhere", 1, prev)

	snap := snapWith(
		agentctx.Run{TurnsAgo: 1, LxID: otherDir, InContext: true},
		agentctx.Run{TurnsAgo: 2, LxID: otherArgs, InContext: true},
		agentctx.Run{TurnsAgo: 3, LxID: 999},
		agentctx.Run{TurnsAgo: 4, LxID: same, InContext: true},
	)
	out, ok := sessionDelta(snap, c, cur, cur)
	if !ok || !strings.Contains(out, fmt.Sprintf("same command as lx show %d (4 turns ago", same)) {
		t.Fatalf("ok=%v\n%s", ok, out)
	}

	for name, s := range map[string]*agentctx.Snapshot{
		"out of context": snapWith(agentctx.Run{TurnsAgo: 1, LxID: same}),
		"other dir only": snapWith(agentctx.Run{TurnsAgo: 1, LxID: otherDir, InContext: true}),
		"no lx id":       snapWith(agentctx.Run{TurnsAgo: 1, InContext: true}),
		"pending":        snapWith(agentctx.Run{LxID: same, InContext: true, Pending: true}),
		"pruned run":     snapWith(agentctx.Run{TurnsAgo: 1, LxID: 12345, InContext: true}),
		"no session":     nil,
	} {
		if out, ok := sessionDelta(s, c, cur, cur); ok {
			t.Errorf("%s: got a delta:\n%s", name, out)
		}
	}
}

func TestSessionDeltaNeedsAnExplainedFailure(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	id := saveRun(t, deltaArgv, "/repo", 1, toolOutput("alpha: boom"))
	snap := snapWith(agentctx.Run{TurnsAgo: 1, LxID: id, InContext: true})
	cur := strings.Replace(toolOutput(), "FAIL\tpkg", "fatal: out of memory\nFAIL\tpkg", 1)
	if out, ok := sessionDelta(snap, &engine.Context{Argv: deltaArgv, Cwd: "/repo", Exit: 1}, cur, cur); ok {
		t.Fatalf("a failed run with no identifiable failure must be shown in full:\n%s", out)
	}
}

func deltaTranscript(t *testing.T, id int, compact string) *agentctx.Snapshot {
	t.Helper()
	receipt := fmt.Sprintf(`FAIL alpha: boom\nFAIL beta: nil map\n[lx: 43→12 lines (−72%%) · full output: lx show %d]`, id)
	lines := []string{
		`{"type":"user","uuid":"u1","timestamp":"2026-09-20T10:00:00Z","message":{"role":"user","content":"fix the failing checks"}}`,
		`{"type":"assistant","uuid":"a1","timestamp":"2026-09-20T10:01:00Z","message":{"id":"m1","model":"claude-sonnet-4-5","usage":{"input_tokens":1000,"cache_creation_input_tokens":0,"cache_read_input_tokens":9000},"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"lx lxdeltatool run"}}]}}`,
		`{"type":"user","uuid":"u2","timestamp":"2026-09-20T10:01:10Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","is_error":true,"content":"` + receipt + `"}]}}`,
	}
	if compact != "" {
		lines = append(lines, `{"type":"system","subtype":"compact_boundary","uuid":"s1","timestamp":"2026-09-20T10:04:00Z","compactMetadata":`+compact+`}`)
	}
	lines = append(lines,
		`{"type":"assistant","uuid":"a2","timestamp":"2026-09-20T10:05:00Z","message":{"id":"m2","model":"claude-sonnet-4-5","usage":{"input_tokens":500,"cache_creation_input_tokens":500,"cache_read_input_tokens":19000},"content":[{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"/repo/a.go","old_string":"x","new_string":"y"}}]}}`,
		`{"type":"user","uuid":"u3","timestamp":"2026-09-20T10:05:10Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"ok"}]}}`,
		`{"type":"assistant","uuid":"a3","timestamp":"2026-09-20T10:06:00Z","message":{"id":"m3","model":"claude-sonnet-4-5","usage":{"input_tokens":500,"cache_creation_input_tokens":500,"cache_read_input_tokens":19000},"content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"lx lxdeltatool run"}}]}}`,
	)
	p := filepath.Join(t.TempDir(), ctxSID+".jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := agentctx.Load(agentctx.Source{Agent: agentctx.ClaudeCode, SessionID: ctxSID, Path: p, Argv: deltaArgv}, agentctx.DefaultTail)
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func TestSessionDeltaAcrossCompaction(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	prev := toolOutput("alpha: boom", "beta: nil map")
	id := saveRun(t, deltaArgv, "/repo", 1, prev)
	cur := toolOutput("beta: nil map")
	c := &engine.Context{Argv: deltaArgv, Cwd: "/repo", Exit: 1}

	out, ok := sessionDelta(deltaTranscript(t, id, ""), c, cur, cur)
	if !ok || !strings.Contains(out, fmt.Sprintf("same command as lx show %d (2 turns ago, still in your context)", id)) {
		t.Fatalf("before any compaction the previous run is in context (ok=%v):\n%s", ok, out)
	}
	if out, ok := sessionDelta(deltaTranscript(t, id, `{"trigger":"auto","preTokens":180000,"postTokens":20000}`), c, cur, cur); ok {
		t.Fatalf("the previous run was compacted away, yet a delta was used:\n%s", out)
	}
	if out, ok := sessionDelta(deltaTranscript(t, id, `{"trigger":"auto","preTokens":180000,"postTokens":20000,"preservedMessages":{"uuids":["a1"]}}`), c, cur, cur); ok {
		t.Fatalf("only the call was kept, not its output, yet a delta was used:\n%s", out)
	}
	kept := `{"trigger":"manual","preTokens":180000,"postTokens":20000,"preservedMessages":{"uuids":["a1","u2"]}}`
	if out, ok := sessionDelta(deltaTranscript(t, id, kept), c, cur, cur); !ok {
		t.Fatalf("the compaction kept the run verbatim, so the delta applies:\n%s", out)
	}
}

func TestSessionDeltaChain(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	first := saveRun(t, deltaArgv, "/repo", 1, toolOutput("alpha: boom", "beta: nil map", "gamma: timeout"))
	second := saveShown(t, deltaArgv, "/repo", 1, toolOutput("beta: nil map", "gamma: timeout"), deltaFilter)
	cur := toolOutput("gamma: timeout")
	c := &engine.Context{Argv: deltaArgv, Cwd: "/repo", Exit: 1}

	snap := snapWith(agentctx.Run{TurnsAgo: 1, LxID: second, InContext: true}, agentctx.Run{TurnsAgo: 3, LxID: first, InContext: true})
	out, ok := sessionDelta(snap, c, cur, cur)
	if !ok || !strings.Contains(out, fmt.Sprintf("same command as lx show %d (1 turn ago", second)) || !strings.Contains(out, "fixed: beta\n") {
		t.Fatalf("ok=%v\n%s", ok, out)
	}
	snap.Runs[2].InContext = false
	if out, ok := sessionDelta(snap, c, cur, cur); ok {
		t.Fatalf("the full view behind the last delta is gone from the context, yet:\n%s", out)
	}
	if out, ok := sessionDelta(snapWith(agentctx.Run{TurnsAgo: 1, LxID: second, InContext: true}), c, cur, cur); ok {
		t.Fatalf("no full view of the command in the part of the transcript read, yet:\n%s", out)
	}
}

func TestSessionDeltaNeedsTheWholeEarlierView(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	id := saveRun(t, deltaArgv, "/repo", 1, toolOutput("alpha: boom", "beta: nil map", "gamma: timeout", "delta: bad input"))
	cur := toolOutput("beta: nil map", "gamma: timeout", "delta: bad input")
	c := &engine.Context{Argv: deltaArgv, Cwd: "/repo", Exit: 1}
	for cmd, want := range map[string]bool{
		"lxdeltatool run":         true,
		"lx lxdeltatool run 2>&1": true,
		"cd /repo && lxdeltatool run 2>/dev/null; echo done": true,
		"LX_MODE=minimal lxdeltatool run":                    false,
		"lx -m minimal lxdeltatool run":                      false,
		"lx --budget 2000 lxdeltatool run":                   false,
		"timeout 60 lxdeltatool run || true":                 true,
		"lxdeltatool run 2>&1 | tail -3":                     false,
		"lx --fit tail:3 lxdeltatool run | tail -3":          false,
		"lxdeltatool run > out.txt; cat out.txt":             false,
		"lxdeltatool run &> out.txt; tail -5 out.txt":        false,
		`out=$(lxdeltatool run); echo "$out" | head`:         false,
		"lxdeltatool run &":                                  false,
		"cat notes.txt":                                      false,
		"lx show 3":                                          false,
		"echo lxdeltatool run":                               false,
	} {
		snap := snapWith(agentctx.Run{Command: cmd, TurnsAgo: 1, LxID: id, InContext: true})
		if out, ok := sessionDelta(snap, c, cur, cur); ok != want {
			t.Errorf("%q: delta=%v, want %v\n%s", cmd, ok, want, out)
		}
	}

	piped := saveRun(t, deltaArgv, "/repo", 1, toolOutput("beta: nil map", "gamma: timeout", "delta: bad input", "omega: index out of range"))
	snap := snapWith(agentctx.Run{Command: "lxdeltatool run | head -5", TurnsAgo: 1, LxID: piped, InContext: true},
		agentctx.Run{TurnsAgo: 3, LxID: id, InContext: true})
	out, ok := sessionDelta(snap, c, cur, cur)
	if !ok || !strings.Contains(out, fmt.Sprintf("same command as lx show %d (3 turns ago", id)) || !strings.Contains(out, "fixed: alpha\n") {
		t.Fatalf("a piped run is skipped for the last one shown whole (ok=%v):\n%s", ok, out)
	}
}

func TestRerouted(t *testing.T) {
	for cmd, want := range map[string]bool{
		"go test ./...":                    false,
		"go test ./... 2>&1":               false,
		"go test ./... 2>>err.log && echo": false,
		`go test -run 'A|B' ./...`:         false,
		`go test -run "A|B" ./...`:         false,
		`go test ./... \| x`:               false,
		`echo '$(x)'`:                      false,
		"go test ./... | tail":             true,
		"go test ./... |& tail":            true,
		"go test ./... 1> out":             true,
		"go test ./... >> out":             true,
		"go test ./... >&2":                true,
		"go test ./... &> out":             true,
		"go test ./... &":                  true,
		"diff <(go test ./...) old":        true,
		"x=`go test ./...`":                true,
		`echo "$(go test ./...)"`:          true,
	} {
		if got := rerouted(cmd); got != want {
			t.Errorf("rerouted(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestSessionDeltaOffValues(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	raw := toolOutput("alpha: boom")
	id := saveRun(t, deltaArgv, "/repo", 1, raw)
	snap := snapWith(agentctx.Run{TurnsAgo: 1, LxID: id, InContext: true})
	for _, v := range []string{"0", "off", "false", "no", " OFF "} {
		t.Setenv("LX_DELTA", v)
		if _, ok := sessionDelta(snap, &engine.Context{Argv: deltaArgv, Cwd: "/repo", Exit: 1}, raw, raw); ok {
			t.Errorf("LX_DELTA=%q left the delta on", v)
		}
	}
}
