//go:build unix

package laya

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func shortDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "lxl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

type fake struct {
	path string
	mu   sync.Mutex
	reqs []map[string]any
}

func serve(t *testing.T, reply func(req map[string]any) string) *fake {
	t.Helper()
	f := &fake{path: filepath.Join(shortDir(t), "laya.sock")}
	ln, err := net.Listen("unix", f.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadBytes('\n')
					if err != nil {
						return
					}
					var req map[string]any
					json.Unmarshal(line, &req)
					f.mu.Lock()
					f.reqs = append(f.reqs, req)
					f.mu.Unlock()
					out := reply(req)
					if out == "" {
						return
					}
					if _, err := c.Write([]byte(out)); err != nil {
						return
					}
				}
			}()
		}
	}()
	return f
}

func (f *fake) last() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reqs[len(f.reqs)-1]
}

const pong = `{"ok":true,"model":"fake/model","loaded":true,"version":1,"pid":42}` + "\n"

func TestJudge(t *testing.T) {
	f := serve(t, func(req map[string]any) string {
		n := len(req["items"].([]any))
		vs := make([]string, n)
		for i := range vs {
			vs[i] = `{"keep":` + map[bool]string{true: "true", false: "false"}[i%2 == 0] + `,"confidence":0.75}`
		}
		return `{"verdicts":[` + strings.Join(vs, ",") + `],"model_ms":12}` + "\n"
	})
	c := Client{Socket: f.path}
	req := Request{Family: "log", Task: "fix TestParse", Items: []Item{{"a"}, {"b"}, {"c"}}}
	got, err := c.Judge(req, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := []Verdict{{true, 0.75}, {false, 0.75}, {true, 0.75}}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("verdicts %+v", got)
	}
	r := f.last()
	if r["op"] != "judge" || r["family"] != "log" || r["task"] != "fix TestParse" {
		t.Errorf("request %v", r)
	}
	if _, ok := r["min_conf"]; ok {
		t.Errorf("min_conf sent though unset: %v", r)
	}
	if d := r["deadline_ms"].(float64); d <= 0 || d >= 1000 {
		t.Errorf("deadline_ms %v, want under the 1s timeout", d)
	}
	items := r["items"].([]any)
	if items[1].(map[string]any)["text"] != "b" {
		t.Errorf("items %v", items)
	}

	c.Judge(Request{Family: "listing", Items: []Item{{"x"}}, MinConf: 0.9}, time.Second)
	if r := f.last(); r["min_conf"] != 0.9 || r["task"] != "" {
		t.Errorf("request %v", r)
	}
}

func TestJudgeNoItems(t *testing.T) {
	got, err := Client{Socket: "/nonexistent/laya.sock"}.Judge(Request{Family: "log"}, time.Second)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestJudgeBadReplies(t *testing.T) {
	two := []Item{{"a"}, {"b"}}
	for _, c := range []struct {
		reply, want string
	}{
		{`{"error":"loading the model"}`, "loading the model"},
		{`{"verdicts":[{"keep":true,"confidence":0.5}]}`, "1 verdicts for 2 items"},
		{`{"verdicts":[{"keep":true,"confidence":0.5},{"confidence":0.9}]}`, "malformed verdict 1"},
		{`{"verdicts":[{"keep":false},{"keep":true,"confidence":0.5}]}`, "malformed verdict 0"},
		{`{"verdicts":[{"keep":false,"confidence":1.5},{"keep":true,"confidence":0.5}]}`, "malformed verdict 0"},
		{`{"verdicts":[{"keep":false,"confidence":-0.1},{"keep":true,"confidence":0.5}]}`, "malformed verdict 0"},
		{`{"verdicts":[{"keep":"no","confidence":0.1},{"keep":true,"confidence":0.5}]}`, "bad response"},
		{`not json`, "bad response"},
		{`{"verdicts":null}`, "0 verdicts for 2 items"},
		{"", "unexpected EOF"},
	} {
		reply := c.reply
		f := serve(t, func(map[string]any) string {
			if reply == "" {
				return ""
			}
			return reply + "\n"
		})
		_, err := Client{Socket: f.path}.Judge(Request{Family: "log", Items: two}, time.Second)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("reply %q: err %v, want %q", c.reply, err, c.want)
		}
	}
}

func TestTimeout(t *testing.T) {
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	f := serve(t, func(map[string]any) string { <-block; return pong })
	start := time.Now()
	_, err := Client{Socket: f.path}.Judge(Request{Family: "log", Items: []Item{{"a"}}}, 150*time.Millisecond)
	if err == nil || !os.IsTimeout(errorsUnwrapAll(err)) {
		t.Fatalf("err %v, want a timeout", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("gave up after %v", d)
	}
	if ready(f.path, 50*time.Millisecond) {
		t.Error("a daemon that doesn't answer is ready")
	}
	if _, err := (Client{Socket: f.path}).Judge(Request{Family: "log", Items: []Item{{"a"}}}, 10*time.Millisecond); err == nil || !strings.Contains(err.Error(), "no time") {
		t.Errorf("tiny timeout: %v", err)
	}
}

func errorsUnwrapAll(err error) error {
	for {
		u, ok := err.(interface{ Unwrap() error })
		if !ok || u.Unwrap() == nil {
			return err
		}
		err = u.Unwrap()
	}
}

func TestHugeReply(t *testing.T) {
	f := serve(t, func(map[string]any) string { return `{"error":"` + strings.Repeat("x", maxResponse+10) + `"}` + "\n" })
	_, err := Client{Socket: f.path}.Ping(5 * time.Second)
	if err == nil || !strings.Contains(err.Error(), "over 16 MiB") {
		t.Fatalf("err %.200v", err)
	}
}

func TestPingAndReady(t *testing.T) {
	for _, c := range []struct {
		reply string
		ready bool
	}{
		{pong, true},
		{`{"ok":true,"model":"m","loaded":false,"version":1,"pid":1}` + "\n", false},
		{`{"ok":true,"model":"m","loaded":true,"version":2,"pid":1}` + "\n", false},
		{`{"ok":false,"error":"sick"}` + "\n", false},
		{`{"error":"unknown op"}` + "\n", false},
	} {
		f := serve(t, func(map[string]any) string { return c.reply })
		if got := ready(f.path, time.Second); got != c.ready {
			t.Errorf("%s: ready=%v", c.reply, got)
		}
	}
	f := serve(t, func(map[string]any) string { return pong })
	in, err := Client{Socket: f.path}.Ping(time.Second)
	if err != nil || in.Model != "fake/model" || in.Pid != 42 || !in.Loaded {
		t.Fatalf("%+v %v", in, err)
	}
	if f.last()["op"] != "ping" {
		t.Errorf("op %v", f.last())
	}
}

func TestNoDaemon(t *testing.T) {
	dir := shortDir(t)
	missing := filepath.Join(dir, "laya.sock")
	if ready(missing, time.Second) {
		t.Error("ready without a socket")
	}
	if _, err := (Client{Socket: missing}).Ping(time.Second); err == nil {
		t.Error("ping without a socket")
	}

	ln, err := net.Listen("unix", missing)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	start := time.Now()
	if _, err := (Client{Socket: missing}).Ping(time.Second); err == nil || !strings.Contains(err.Error(), "connect") {
		t.Errorf("stale socket: %v", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Error("a stale socket took long to fail")
	}

	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0o600)
	if ready(file, time.Second) {
		t.Error("a regular file is ready")
	}
	if _, err := (Client{}).Ping(time.Second); err == nil {
		t.Error("empty socket path")
	}
	long := filepath.Join(dir, strings.Repeat("x", 200), "laya.sock")
	if _, err := (Client{Socket: long}).Ping(time.Second); err == nil {
		t.Error("an over-long socket path")
	}
}

func TestOnlyAPrivateSocket(t *testing.T) {
	f := serve(t, func(map[string]any) string { return pong })
	if !ready(f.path, time.Second) {
		t.Fatal("the fake daemon isn't ready")
	}

	other := shortDir(t)
	link := filepath.Join(other, "laya.sock")
	if err := os.Symlink(f.path, link); err != nil {
		t.Fatal(err)
	}
	if ready(link, time.Second) {
		t.Error("followed a symlink to a socket")
	}

	dir := filepath.Dir(f.path)
	os.Chmod(dir, 0o777)
	_, err := Client{Socket: f.path}.Ping(time.Second)
	os.Chmod(dir, 0o700)
	if err == nil || !strings.Contains(err.Error(), "others can write to") {
		t.Errorf("a socket in a world-writable directory: %v", err)
	}
	if !ready(f.path, time.Second) {
		t.Error("not ready once the directory is private again")
	}
}

func TestRefused(t *testing.T) {
	dir := shortDir(t)
	path := filepath.Join(dir, "laya.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	if _, err := (Client{Socket: path}).Ping(time.Second); !Refused(err) {
		t.Errorf("stale socket: %v", err)
	}

	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	f := serve(t, func(map[string]any) string { <-block; return pong })
	if _, err := (Client{Socket: f.path}).Ping(50 * time.Millisecond); err == nil || Refused(err) {
		t.Errorf("a silent daemon: %v", err)
	}
	if _, err := (Client{Socket: filepath.Join(dir, "none")}).Ping(time.Second); err == nil || Refused(err) {
		t.Errorf("no socket: %v", err)
	}
}

func TestPaths(t *testing.T) {
	env := map[string]string{"XDG_CACHE_HOME": "/xdg/cache"}
	p := PathsFor(func(k string) string { return env[k] }, "/home/u", "/data/lx")
	cache := "/xdg/cache/lx"
	if runtime.GOOS == "darwin" {
		cache = "/home/u/Library/Caches/lx"
	}
	if p.Socket != cache+"/laya.sock" || p.Pid != cache+"/laya.pid" || p.Log != cache+"/laya.log" {
		t.Errorf("cache paths %+v", p)
	}
	if p.Venv != "/data/lx/laya/venv" || p.Script != "/data/lx/laya/lx_laya.py" || p.VenvPython() != "/data/lx/laya/venv/bin/python" {
		t.Errorf("data paths %+v", p)
	}
	env["XDG_CACHE_HOME"] = "relative"
	if p := PathsFor(func(k string) string { return env[k] }, "/home/u", ""); runtime.GOOS != "darwin" && p.Socket != "/home/u/.cache/lx/laya.sock" {
		t.Errorf("relative XDG_CACHE_HOME: %s", p.Socket)
	}
	if p := PathsFor(func(string) string { return "" }, "", ""); p.Socket != "" || p.Venv != "" || p.VenvPython() != "" {
		t.Errorf("no home: %+v", p)
	}

	dir := shortDir(t)
	p = PathsFor(func(string) string { return "" }, dir, dir)
	none := func(string) string { return "" }
	if py, _ := p.Python(none); py != "" {
		t.Errorf("python %q before setup", py)
	}
	os.MkdirAll(filepath.Dir(p.VenvPython()), 0o700)
	os.WriteFile(p.VenvPython(), nil, 0o700)
	if py, fromEnv := p.Python(none); py != p.VenvPython() || fromEnv {
		t.Errorf("python %q %v", py, fromEnv)
	}
	if py, fromEnv := p.Python(func(k string) string { return map[string]string{"LX_LAYA_PYTHON": "/opt/py"}[k] }); py != "/opt/py" || !fromEnv {
		t.Errorf("LX_LAYA_PYTHON: %q %v", py, fromEnv)
	}
}

func TestStatusAndShutdown(t *testing.T) {
	f := serve(t, func(req map[string]any) string {
		switch req["op"] {
		case "status":
			return `{"ok":true,"model":"m","loaded":true,"version":1,"pid":7,"rss_bytes":1048576,"last":{"items":3,"judged":2,"model_ms":40,"total_ms":45,"ago_s":1.5}}` + "\n"
		case "shutdown":
			return `{"ok":true}` + "\n"
		}
		return `{"error":"?"}` + "\n"
	})
	c := Client{Socket: f.path}
	in, err := c.Status(time.Second)
	if err != nil || in.RSS != 1<<20 || in.Last == nil || in.Last.Judged != 2 || in.Last.AgoS != 1.5 {
		t.Fatalf("%+v %v", in, err)
	}
	if err := c.Shutdown(time.Second); err != nil {
		t.Fatal(err)
	}
	if f.last()["op"] != "shutdown" {
		t.Error(f.last())
	}
}

func TestConcurrentCalls(t *testing.T) {
	f := serve(t, func(map[string]any) string { return pong })
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			if _, err := (Client{Socket: f.path}).Ping(2 * time.Second); err != nil {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
