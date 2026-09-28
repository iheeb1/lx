package discover

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sessionOutput() string {
	w := strings.Fields("alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november oscar papa quebec romeo sierra tango")
	var b strings.Builder
	for i := 1; i <= 1500; i++ {
		if i == 750 {
			b.WriteString("internal/shard/rebalancer.go:42: ShardRebalancer moved 17 shards off node-3\n")
		}
		fmt.Fprintf(&b, "%s %s %s: wrote %d rows to shard %s-%d\n", w[i%20], w[i*7%20], w[i*3%20], i*13, w[i*11%20], i%97)
	}
	return b.String()
}

func sessionTranscript(t *testing.T, text string, used int) string {
	t.Helper()
	return sessionTranscriptOf(t, "make", text, used)
}

func sessionTranscriptOf(t *testing.T, command, text string, used int) string {
	t.Helper()
	tr := &transcript{cwd: "/home/u/app"}
	msg := func(content ...any) map[string]any {
		return map[string]any{"type": "assistant", "message": map[string]any{"id": "m1", "model": "claude-sonnet-4-5", "role": "assistant",
			"usage": map[string]int{"input_tokens": 1000, "cache_read_input_tokens": used - 1000}, "content": content}}
	}
	tr.line(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "build the shard tool"}})
	if text != "" {
		tr.line(msg(map[string]any{"type": "text", "text": text}))
	}
	tr.line(msg(map[string]any{"type": "tool_use", "id": "t1", "name": "Bash", "input": map[string]any{"command": command}}))
	out := sessionOutput()
	tr.result("t1", out, false, map[string]any{"stdout": out, "stderr": ""})
	tr.use("t2", "Read", map[string]any{"file_path": "/home/u/app/internal/shard/rebalancer.go", "offset": 30, "limit": 30})
	tr.result("t2", "ok", false, nil)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), tr.b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func scanWith(t *testing.T, dir, lxContext string) Report {
	t.Helper()
	t.Setenv("LX_CONTEXT", lxContext)
	r, err := Scan(Options{Dirs: []string{dir}, Fidelity: true})
	if err != nil || r.Candidates != 1 {
		t.Fatalf("%v: %+v", err, r)
	}
	return r
}

func TestScanReplaysTheSessionContext(t *testing.T) {
	plain := scanWith(t, sessionTranscript(t, "", 21000), "")
	for _, c := range []struct {
		name, text string
		used       int
		smaller    bool
		focused    bool
	}{
		{"verify", "Now let me run it again to verify the build.", 21000, true, false},
		{"pressure", "Running make.", 185000, true, false},
		{"focus", "Let me look at what ShardRebalancer did.", 21000, false, true},
		{"nothing", "Running make.", 21000, false, false},
	} {
		dir := sessionTranscript(t, c.text, c.used)
		on, off := scanWith(t, dir, ""), scanWith(t, dir, "0")
		if js(off) != js(plain) {
			t.Errorf("%s: LX_CONTEXT=0 replays differently from a transcript without context:\n%s\n%s", c.name, js(off), js(plain))
		}
		if c.smaller && on.CandidateOut >= off.CandidateOut {
			t.Errorf("%s: %d tokens after with context, %d without", c.name, on.CandidateOut, off.CandidateOut)
		}
		if got := on.ActedOn.Total.LocInView == 1; got != c.focused || off.ActedOn.Total.LocInView != 0 || off.ActedOn.Total.Misses.Budget != 1 {
			t.Errorf("%s: acted-on with context %+v, without %+v", c.name, on.ActedOn.Total, off.ActedOn.Total)
		}
		if !c.smaller && !c.focused && js(on) != js(off) {
			t.Errorf("%s: no cue, yet the replay changed", c.name)
		}
	}
}

func js(r Report) string {
	b, _ := json.Marshal(r)
	return string(b)
}

func TestScanInfersVerifyOnlyForAVerdict(t *testing.T) {
	dir := sessionTranscriptOf(t, "git diff", "Let me verify the diff looks right.", 21000)
	if on, off := scanWith(t, dir, ""), scanWith(t, dir, "0"); js(on) != js(off) {
		t.Errorf("a verify cue shrank a diff:\n%s\n%s", js(on), js(off))
	}
}
