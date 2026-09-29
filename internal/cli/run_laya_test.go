//go:build unix

package cli

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/agentctx"
	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/laya"
)

type fakeDaemon struct {
	mu   sync.Mutex
	reqs []map[string]any
}

func (f *fakeDaemon) ops(op string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, r := range f.reqs {
		if r["op"] == op {
			out = append(out, r)
		}
	}
	return out
}

func startFakeDaemon(t *testing.T, conf float64) (env []string, f *fakeDaemon) {
	t.Helper()
	home, err := os.MkdirTemp("", "lxj")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	env = []string{"HOME=" + home, "XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "LX_LAYA=1"}
	getenv := func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return filepath.Join(home, ".cache")
		}
		return ""
	}
	sock := laya.PathsFor(getenv, home, "").Socket
	if err := os.MkdirAll(filepath.Dir(sock), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f = &fakeDaemon{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, err := bufio.NewReader(c).ReadBytes('\n')
				if err != nil {
					return
				}
				var req map[string]any
				json.Unmarshal(line, &req)
				f.mu.Lock()
				f.reqs = append(f.reqs, req)
				f.mu.Unlock()
				resp := map[string]any{"ok": true, "loaded": true, "version": laya.Protocol}
				if req["op"] == "judge" {
					items, _ := req["items"].([]any)
					vs := make([]map[string]any, len(items))
					for i := range vs {
						vs[i] = map[string]any{"keep": false, "confidence": conf}
					}
					resp = map[string]any{"verdicts": vs}
				}
				b, _ := json.Marshal(resp)
				c.Write(append(b, '\n'))
			}()
		}
	}()
	return env, f
}

const routineScript = `for a in alpha bravo charlie delta echo foxtrot golf hotel india juliet kilo lima mike november oscar papa quebec; do
for b in amber basalt cobalt dune ember fjord garnet harbor iris jasper kestrel larch moss nettle opal pine quartz; do
echo "  compiling $a module with $b backend"; done; done; echo "error: link step failed"; exit 1`

func TestRunAsksLaya(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	quiet := []string{"LX_HEARTBEAT=off", "LX_PROMPT_IDLE=off"}
	env, f := startFakeDaemon(t, 0.9)
	p := startLx(t, append(env, quiet...), "sh", "-c", routineScript)
	if code := p.wait(20 * time.Second); code != 1 {
		t.Fatalf("exit %d", code)
	}
	out := p.stdout.String()
	if !strings.Contains(out, "lines judged routine (laya)") || !strings.Contains(out, "routine lines folded]") {
		t.Fatalf("no laya fold:\n%s", out)
	}
	if !strings.Contains(out, "error: link step failed") {
		t.Fatalf("error line lost:\n%s", out)
	}
	t.Log("\n" + out)
	js := f.ops("judge")
	if len(js) != 1 || js[0]["family"] != engine.FamilyLines || js[0]["min_conf"] != 0.3 {
		t.Fatalf("judge requests %v", js)
	}
	if n := len(js[0]["items"].([]any)); n == 0 || n > 24 {
		t.Fatalf("%d items", n)
	}

	for _, e := range [][]string{{"LX_LAYA=0"}, {"LX_LAYA_TIMEOUT=1"}} {
		env, f := startFakeDaemon(t, 0.9)
		p := startLx(t, append(append(env, quiet...), e...), "sh", "-c", routineScript)
		if code := p.wait(20 * time.Second); code != 1 {
			t.Fatalf("%v: exit %d", e, code)
		}
		if strings.Contains(p.stdout.String(), "(laya)") {
			t.Fatalf("%v: folded anyway:\n%s", e, p.stdout.String())
		}
		if e[0] == "LX_LAYA=0" && len(f.ops("ping")) != 0 {
			t.Fatalf("LX_LAYA=0 still pinged the daemon")
		}
	}
}

func TestJudgeForWithoutDaemon(t *testing.T) {
	calls := 0
	defer func(a func() bool) { layaAvailable = a }(layaAvailable)
	layaAvailable = func() bool { calls++; return false }
	big := strings.Repeat("some line of output\n", 200)
	if j, _ := judgeFor(big); j != nil || calls != 1 {
		t.Fatalf("judge %v after %d checks", j, calls)
	}
	if j, _ := judgeFor("short\n"); j != nil || calls != 1 {
		t.Fatalf("a small output checked the daemon")
	}
	layaAvailable = func() bool { calls++; return true }
	for _, v := range []string{"0", "off", "false", "no", " OFF "} {
		t.Setenv("LX_LAYA", v)
		if j, _ := judgeFor(big); j != nil || calls != 1 {
			t.Fatalf("LX_LAYA=%q ignored", v)
		}
	}
	t.Setenv("LX_LAYA", "")
	t.Setenv("LX_LAYA_TIMEOUT", "2500")
	if j, d := judgeFor(big); j == nil || d != 2500*time.Millisecond {
		t.Fatalf("judge %v timeout %v", j, d)
	}
}

func TestLayaTimeout(t *testing.T) {
	for v, want := range map[string]time.Duration{
		"": engine.DefaultJudgeTimeout, "800": 800 * time.Millisecond, "2s": 2 * time.Second,
		"1.5s": 1500 * time.Millisecond, "0": engine.DefaultJudgeTimeout, "-3": engine.DefaultJudgeTimeout, "soon": engine.DefaultJudgeTimeout,
		"99999999999999999": engine.DefaultJudgeTimeout,
	} {
		if got := layaTimeout(v); got != want {
			t.Errorf("layaTimeout(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestLayaClientMapsVerdicts(t *testing.T) {
	defer func(j func(laya.Request, time.Duration) ([]laya.Verdict, error)) { layaJudge = j }(layaJudge)
	var got laya.Request
	layaJudge = func(req laya.Request, _ time.Duration) ([]laya.Verdict, error) {
		got = req
		return []laya.Verdict{{Keep: true, Confidence: 0.7}, {Keep: false, Confidence: 0.9}}, nil
	}
	vs, err := layaClient{}.Judge(engine.FamilyLogs, "fix it", []string{"a", "b"}, time.Second)
	if err != nil || len(vs) != 2 || !vs[0].Keep || vs[1].Keep || vs[1].Confidence != 0.9 {
		t.Fatalf("%v %v", vs, err)
	}
	if got.Family != "log" || got.Task != "fix it" || len(got.Items) != 2 || got.Items[1].Text != "b" {
		t.Fatalf("request %+v", got)
	}
}

func TestSessionTask(t *testing.T) {
	for _, c := range []struct {
		title, msg, want string
	}{
		{"", "", ""},
		{"Fix the orders crash.", "", "Fix the orders crash"},
		{"", "Let me check the service logs. Then I'll patch it.", "Let me check the service logs."},
		{"Fix the orders crash", "Why does it fail? Looking at logs.\nMore.", "Fix the orders crash: Why does it fail?"},
		{"", strings.Repeat("é", 300), strings.Repeat("é", 200)},
	} {
		s := session{snap: &agentctx.Snapshot{Title: c.title}}
		if c.msg != "" {
			s.snap.Assistant = []string{c.msg}
		}
		if got := s.task(); got != c.want {
			t.Errorf("task(%q, %q) = %q, want %q", c.title, c.msg, got, c.want)
		}
	}
	if (session{}).task() != "" {
		t.Error("no session, but a task")
	}
}
