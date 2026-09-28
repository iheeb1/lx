//go:build unix

package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/agentctx"
	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/testenv"
)

const sessSID = "44444444-5555-4666-8777-888888888888"

const sessGen = `awk 'BEGIN{split("alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november oscar papa quebec romeo sierra tango",w," ");` +
	` for(i=1;i<=1500;i++){ if(i==750) print "ShardRebalancer moved 17 shards off node-3";` +
	` if(i==900) print "error: shard 900 failed: disk full";` +
	` printf "%s %s %s: wrote %d rows to shard %s-%d\n", w[(i%20)+1], w[(i*7%20)+1], w[(i*3%20)+1], i*13, w[(i*11%20)+1], i%97 } }'`

type sessFixture struct {
	t     *testing.T
	cfg   string
	path  string
	gen   string
	exit  int
	argv  []string
	extra []string
}

func newSess(t *testing.T, exit int) *sessFixture {
	t.Helper()
	root := t.TempDir()
	gen := filepath.Join(root, "gen.sh")
	if err := os.WriteFile(gen, []byte(fmt.Sprintf("%s\nexit %d\n", sessGen, exit)), 0o644); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	cfg := filepath.Join(root, "claude")
	path := filepath.Join(cfg, "projects", agentctx.Slug(cwd), sessSID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	return &sessFixture{t: t, cfg: cfg, path: path, gen: gen, exit: exit, argv: []string{"sh", gen}}
}

type verdictTool struct{}

func (verdictTool) Name() string                                       { return "verdict-test-tool" }
func (verdictTool) Match(c *engine.Context) bool                       { return c.Name() == "lxverdicttool" }
func (verdictTool) Apply(c *engine.Context, out string) (string, bool) { return out, true }
func (verdictTool) GuardsErrors() bool                                 { return true }

func init() { engine.Register(verdictTool{}) }

func (s *sessFixture) asVerdictTool() {
	s.t.Helper()
	bin := filepath.Join(filepath.Dir(s.gen), "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "lxverdicttool"), []byte("#!/bin/sh\nexec sh "+s.gen+"\n"), 0o755); err != nil {
		s.t.Fatal(err)
	}
	s.argv, s.extra = []string{"lxverdicttool"}, []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}
}

func sessLine(v map[string]any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func sessAssistant(id string, used int, blocks ...map[string]any) string {
	return sessLine(map[string]any{"type": "assistant", "uuid": "a-" + id, "timestamp": "2026-09-28T10:00:00Z",
		"message": map[string]any{"id": id, "model": "claude-sonnet-4-5", "content": blocks,
			"usage": map[string]int{"input_tokens": 500, "cache_creation_input_tokens": 500, "cache_read_input_tokens": used - 1000}}})
}

func sessText(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func sessBash(id, cmd string) map[string]any {
	return map[string]any{"type": "tool_use", "id": id, "name": "Bash", "input": map[string]string{"command": cmd}}
}

func (s *sessFixture) write(text string, used int) {
	s.t.Helper()
	s.writeLines(
		sessLine(map[string]any{"type": "user", "uuid": "u1", "message": map[string]any{"role": "user", "content": "the shard job looks off"}}),
		sessAssistant("m1", used, sessText(text)),
		sessAssistant("m1", used, sessBash("t1", "sh "+s.gen)),
	)
}

func (s *sessFixture) writeLines(lines ...string) {
	s.t.Helper()
	if err := os.WriteFile(s.path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

func (s *sessFixture) env(extra ...string) []string {
	return append([]string{"CLAUDECODE=1", "CLAUDE_CONFIG_DIR=" + s.cfg, "CLAUDE_CODE_SESSION_ID=" + sessSID}, extra...)
}

func (s *sessFixture) run(env []string, args ...string) string {
	s.t.Helper()
	env = append(append(env, s.extra...), "LX_TEE_DIR="+s.t.TempDir(), "LX_HEARTBEAT=off", "LX_PROMPT_IDLE=off")
	p := startLx(s.t, env, append(args, s.argv...)...)
	if code := p.wait(20 * time.Second); code != s.exit {
		s.t.Fatalf("exit %d, want the command's %d\nstderr: %s", code, s.exit, p.stderr.String())
	}
	return p.stdout.String()
}

func (s *sessFixture) baseline() string {
	s.t.Helper()
	return s.run([]string{"CLAUDECODE=1"})
}

func lastLine(s string) string {
	lines := nonEmptyLines(s)
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

func TestSessionFocusKeepsWhatTheAgentAsked(t *testing.T) {
	s := newSess(t, 0)
	s.write("Let me look at what ShardRebalancer did.", 21000)
	base, got := s.baseline(), s.run(s.env())
	if strings.Contains(base, "ShardRebalancer") {
		t.Fatalf("the plain budget cut already keeps the line; the fixture proves nothing:\n%s", lastLine(base))
	}
	if !strings.Contains(got, "ShardRebalancer moved 17 shards off node-3\n") || !strings.HasSuffix(lastLine(got), " · focus: ShardRebalancer]") {
		t.Fatalf("focus ignored: %s", lastLine(got))
	}
	for _, v := range []string{"0", "off"} {
		if off := s.run(s.env("LX_CONTEXT=" + v)); off != base {
			t.Fatalf("LX_CONTEXT=%s is not the plain view:\n%s\nwant\n%s", v, lastLine(off), lastLine(base))
		}
	}
}

func TestSessionPressureTightensTheView(t *testing.T) {
	s := newSess(t, 0)
	s.write("Running the generator now.", 185000)
	base, got := s.baseline(), s.run(s.env())
	if n, m := len(nonEmptyLines(got)), len(nonEmptyLines(base)); n >= m*3/4 {
		t.Fatalf("%d lines under 92%% context, %d without", n, m)
	}
	if !strings.Contains(got, "error: shard 900 failed: disk full") || !strings.HasSuffix(lastLine(got), " · context 92% full]") {
		t.Fatalf("view under pressure:\n%s", lastLine(got))
	}
	if off := s.run(s.env("LX_CONTEXT=0")); off != base {
		t.Fatalf("LX_CONTEXT=0 is not the plain view:\n%s", lastLine(off))
	}
}

func TestSessionNeverShrinksATunedView(t *testing.T) {
	s := newSess(t, 0)
	big := strings.Replace(sessGen, "i<=1500", "i<=4000", 1)
	if err := os.WriteFile(s.gen, []byte(big+"\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.write("Now let me run it again to verify the fix.", 185000)
	cwd, _ := os.Getwd()
	data := t.TempDir()
	tuneSeedDir(t, data, cwd, "sh", 2)
	tune := []string{"LX_DATA_DIR=" + data, "LX_TRACK=", "LX_TUNE="}
	plain, got := s.run(append([]string{"CLAUDECODE=1"}, tune...)), s.run(s.env(tune...))
	last := lastLine(got)
	if !strings.Contains(lastLine(plain), "loosened") || !strings.Contains(last, "loosened") {
		t.Fatalf("the command is not loosened: %s", lastLine(plain))
	}
	if strings.Contains(last, "context 92% full") || strings.Contains(last, "mode verify") || len(got) < len(plain)*9/10 {
		t.Fatalf("a loosened view shrank under the session: %d bytes, %d without\n%s", len(got), len(plain), last)
	}
}

func TestSessionInfersTheMode(t *testing.T) {
	for _, c := range []struct {
		text    string
		exit    int
		verdict bool
		env     []string
		args    []string
		note    string
		fewer   bool
	}{
		{"Now let me run it again to verify the fix.", 0, true, nil, nil, " · mode verify (from your last message) · ", true},
		{"Now let me run it again to verify the fix.", 0, false, nil, nil, "", false},
		{"Now let me run it again to verify the fix.", 3, false, nil, nil, " · mode verify→error (from your last message) · ", false},
		{"Let me investigate why the shard job fails.", 3, false, nil, nil, " · mode error (from your last message) · ", false},
		{"Now let me run it again to verify the fix.", 0, true, []string{"LX_MODE=debug"}, nil, " · mode debug · ", false},
		{"Now let me run it again to verify the fix.", 0, true, nil, []string{"-m", "auto"}, "", false},
	} {
		s := newSess(t, c.exit)
		if c.verdict {
			s.asVerdictTool()
		}
		s.write(c.text, 21000)
		base, got := s.baseline(), s.run(s.env(c.env...), c.args...)
		last := lastLine(got)
		switch {
		case c.note == "" && got != base:
			t.Errorf("%q %v %q: the mode changed without a verdict to check or against an explicit mode: %s", c.text, s.argv, c.args, last)
		case c.note != "" && !strings.Contains(last, c.note):
			t.Errorf("%q exit %d %q: receipt %s, want %q", c.text, c.exit, c.env, last, c.note)
		case c.fewer && len(got) >= len(base):
			t.Errorf("%q: verify kept %d bytes, auto %d", c.text, len(got), len(base))
		}
		if c.exit != 0 && !strings.Contains(got, "error: shard 900 failed: disk full") {
			t.Errorf("%q: the error line is gone", c.text)
		}
		if off := s.run(s.env(append(c.env, "LX_CONTEXT=0")...), c.args...); c.env == nil && c.args == nil && off != base {
			t.Errorf("%q exit %d: LX_CONTEXT=0 is not the plain view:\n%s", c.text, c.exit, lastLine(off))
		}
	}
}

func TestSessionFailuresAreSilent(t *testing.T) {
	s := newSess(t, 3)
	base := s.baseline()
	if err := os.WriteFile(s.path, []byte("{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\n\x00\xff garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, env := range [][]string{
		s.env(),
		{"CLAUDECODE=1", "CLAUDE_CONFIG_DIR=" + s.cfg, "CLAUDE_CODE_SESSION_ID=99999999-0000-4000-8000-000000000000"},
		{"CLAUDECODE=1", "CLAUDE_CONFIG_DIR=" + s.cfg, "CLAUDE_CODE_SESSION_ID=../../etc"},
		{"CLAUDECODE=1", "CLAUDE_CONFIG_DIR=", "CLAUDE_CODE_SESSION_ID=" + sessSID, "HOME=/nonexistent"},
	} {
		if got := s.run(env); got != base {
			t.Errorf("%q: output changed without a usable transcript:\n%s", env, lastLine(got))
		}
	}
}

func TestSessionPipe(t *testing.T) {
	s := newSess(t, 0)
	s.write("Let me look at what ShardRebalancer did.", 21000)
	out, err := exec.Command("sh", s.gen).CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	pipe := func(env []string) string {
		cmd := exec.Command(os.Args[0], "pipe", "--as", "sh gen.sh")
		cmd.Env = append(lxEnv(t), env...)
		cmd.Stdin = strings.NewReader(string(out))
		b, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	base, got := pipe([]string{"CLAUDECODE=1"}), pipe(s.env())
	if !strings.Contains(got, "ShardRebalancer moved") || !strings.HasSuffix(lastLine(got), " · focus: ShardRebalancer]") || strings.Contains(base, "ShardRebalancer") {
		t.Fatalf("pipe focus: %s / %s", lastLine(got), lastLine(base))
	}
	if off := pipe(s.env("LX_CONTEXT=0")); off != base {
		t.Fatalf("LX_CONTEXT=0: %s", lastLine(off))
	}
}

func TestSessionLookupIsFast(t *testing.T) {
	if testing.Short() {
		t.Skip("writes a 40 MB transcript")
	}
	s := newSess(t, 0)
	f, err := os.Create(s.path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	body, _ := json.Marshal(strings.Repeat("--- PASS: TestSomething (0.00s)\n    thing_test.go:12: \"quoted\" ok\n", 400))
	for i, n := 0, 0; n < 40<<20; i++ {
		a := sessAssistant(fmt.Sprintf("m%d", i), 90000+i, sessText("Let me check internal/engine/modes.go and run TestParseMode."), sessBash(fmt.Sprintf("t%d", i), "go test ./internal/engine -run TestParseMode"))
		u := fmt.Sprintf(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t%d","content":%s}]},"toolUseResult":{"stdout":%s,"stderr":""}}`, i, body, body)
		k, _ := fmt.Fprintln(w, a)
		m, _ := fmt.Fprintln(w, u)
		n += k + m
	}
	fmt.Fprintln(w, sessAssistant("last", 150000, sessText("Now let me run the tests again to verify."), sessBash("tl", "sh "+s.gen)))
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	for k, v := range map[string]string{"CLAUDECODE": "1", "CLAUDE_CONFIG_DIR": s.cfg, "CLAUDE_CODE_SESSION_ID": sessSID} {
		t.Setenv(k, v)
	}
	cwd, _ := os.Getwd()
	raw := strings.Repeat("some output line that is long enough to count\n", 200)
	best := time.Hour
	for i := 0; i < 7; i++ {
		start := time.Now()
		sess := openSession([]string{"sh", s.gen}, cwd, raw, 0)
		eo := sess.options(&engine.Context{Argv: []string{"sh", s.gen}, Exit: 1}, runOpts{}.engineOptions(), false, false)
		d := time.Since(start)
		if sess.snap == nil || !sess.snap.Caller || eo.Focus == nil || !eo.Pressure.Known() || !sess.inferred {
			t.Fatalf("snapshot %+v options %+v", sess.snap, eo)
		}
		best = min(best, d)
	}
	if limit := testenv.Scale(5 * time.Millisecond); best > limit {
		t.Fatalf("session lookup took %v, want < %v", best, limit)
	}
	t.Logf("session lookup + focus: %v", best)
}

func TestSessionDeltaReplacesARepeatedRun(t *testing.T) {
	s := newSess(t, 3)
	s.write("Running the generator now.", 21000)
	tee := t.TempDir()
	run := func(env ...string) string {
		p := startLx(t, append(s.env(env...), "LX_TEE_DIR="+tee, "LX_HEARTBEAT=off", "LX_PROMPT_IDLE=off"), "sh", s.gen)
		if code := p.wait(20 * time.Second); code != 3 {
			t.Fatalf("exit %d, want 3", code)
		}
		return p.stdout.String()
	}
	first := run()
	receipt := lastLine(first)
	if !strings.HasSuffix(receipt, "full output: lx show 1]") {
		t.Fatalf("first run: %s", receipt)
	}
	result := func(id string) string {
		return sessLine(map[string]any{"type": "user", "uuid": "r-" + id, "message": map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "tool_result", "tool_use_id": id, "is_error": true, "content": "error: shard 900 failed: disk full\n" + receipt}}}})
	}
	prompt := sessLine(map[string]any{"type": "user", "uuid": "u1", "message": map[string]any{"role": "user", "content": "the shard job looks off"}})
	again := sessAssistant("m2", 30000, sessBash("t2", "sh "+s.gen))
	s.writeLines(prompt, sessAssistant("m1", 21000, sessBash("t1", "sh "+s.gen)), result("t1"), again)

	got := run()
	lines := nonEmptyLines(got)
	if len(lines) > 3 || !strings.HasPrefix(got, "[lx: output identical to lx show 1 (1 turn ago, still in your context)]\n") ||
		!strings.HasSuffix(lines[len(lines)-1], "full output: lx show 2]") {
		t.Fatalf("repeated run:\n%s", got)
	}
	if raw := run("LX_TEST_PANIC=1"); strings.Contains(raw, "same command") || strings.Count(raw, "\n") < 1500 {
		t.Fatalf("after a condensing failure the output must be the command's own, got %d lines", strings.Count(raw, "\n"))
	}
	for _, flags := range [][]string{{"-m", "debug"}, {"--mode=error"}, {"-b", "20000"}} {
		p := startLx(t, append(s.env(), "LX_TEE_DIR="+tee, "LX_HEARTBEAT=off", "LX_PROMPT_IDLE=off"), append(flags, "sh", s.gen)...)
		if code := p.wait(20 * time.Second); code != 3 || strings.Contains(p.stdout.String(), "in your context") ||
			!strings.Contains(p.stdout.String(), "error: shard 900 failed: disk full") {
			t.Fatalf("lx %s asks for more than the usual view, got (exit %d):\n%s", flags, code, p.stdout.String())
		}
	}
	for _, env := range []string{"LX_DELTA=0", "LX_CONTEXT=0"} {
		if off := run(env); off != strings.Replace(first, "lx show 1]", "lx show "+lastID(off)+"]", 1) {
			t.Fatalf("%s: not the plain view:\n%s", env, lastLine(off))
		}
	}

	boundary := sessLine(map[string]any{"type": "system", "subtype": "compact_boundary", "uuid": "c1", "timestamp": "2026-09-28T10:00:00Z",
		"compactMetadata": map[string]any{"trigger": "auto", "preTokens": 150000, "postTokens": 20000}})
	s.writeLines(prompt, sessAssistant("m1", 21000, sessBash("t1", "sh "+s.gen)), result("t1"), boundary, again)
	if after := run(); after != strings.Replace(first, "lx show 1]", "lx show "+lastID(after)+"]", 1) {
		t.Fatalf("after a compaction the old run is gone from the context, yet:\n%s", after)
	}
}

func lastID(out string) string {
	l := lastLine(out)
	i := strings.LastIndex(l, "lx show ")
	if i < 0 {
		return ""
	}
	return strings.TrimSuffix(l[i+len("lx show "):], "]")
}

func TestSessionCtxCommand(t *testing.T) {
	s := newSess(t, 0)
	s.write("Let me look at what ShardRebalancer did.", 21000)
	p := startLx(t, s.env(), "ctx", "--json")
	if code := p.wait(10 * time.Second); code != 0 || !strings.Contains(p.stdout.String(), `"agent": "claude-code"`) ||
		!strings.Contains(p.stdout.String(), `"text": "ShardRebalancer"`) {
		t.Fatalf("lx ctx: exit %d\n%s%s", code, p.stdout.String(), p.stderr.String())
	}
	p = startLx(t, s.env("LX_CONTEXT=0"), "ctx")
	if code := p.wait(10 * time.Second); code != 1 || p.stdout.String() != "" || !strings.Contains(p.stderr.String(), "LX_CONTEXT=0") {
		t.Fatalf("lx ctx with LX_CONTEXT=0: exit %d, stdout %q, stderr %q", code, p.stdout.String(), p.stderr.String())
	}
}
