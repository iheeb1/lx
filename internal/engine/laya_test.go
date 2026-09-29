package engine

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type stubJudge struct {
	mu       sync.Mutex
	sleeps   []time.Duration
	calls    int
	items    [][]string
	timeouts []time.Duration
}

func (s *stubJudge) Judge(family, task string, items []string, timeout time.Duration) ([]JudgeVerdict, error) {
	s.mu.Lock()
	var d time.Duration
	if s.calls < len(s.sleeps) {
		d = s.sleeps[s.calls]
	}
	s.calls++
	s.items = append(s.items, items)
	s.timeouts = append(s.timeouts, timeout)
	s.mu.Unlock()
	time.Sleep(d)
	out := make([]JudgeVerdict, len(items))
	for i := range out {
		out[i] = JudgeVerdict{Confidence: 1}
	}
	return out, nil
}

func (s *stubJudge) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func judgeCtx(j Judge, timeout time.Duration) *Context {
	return &Context{Argv: []string{"tool"}, jr: newJudgeRun(&Context{}, Options{Judge: j, JudgeTimeout: timeout})}
}

func TestCountLine(t *testing.T) {
	var journal []string
	for i := 0; i < 60; i++ {
		journal = append(journal, fmt.Sprintf("Sep 26 09:%02d:%02d web01 shop-api[2201]: level=info msg=\"request completed\" path=/api/p/%d status=200 duration=%dms", i/6, i%60, i, i+3))
	}
	for name, c := range map[string]struct {
		lines []string
		fail  string
		want  string
	}{
		"hdfs":    {hdfsLog(), "081109 235959 1 ERROR dfs.Worker: worker %d failed", "[×150] INFO dfs.DataNode$PacketResponder: PacketResponder <*> for block blk_<N> terminating"},
		"jsonl":   {jsonLinesLog(), `{"ts":"2026-09-26T11:00:00.000Z","level":"error","msg":"worker %d failed"}`, `[×298] "level":"info" "msg":"request completed" "method":"GET" "path":<*> "status":200 "duration_ms":<*>`},
		"journal": {journal, `Sep 26 10:00:00 web01 shop-api[2201]: level=error msg="worker %d failed"`, `[×60] web01 shop-api[2201]: level=info msg="request completed" path=<*> status=200 duration=<DUR>`},
	} {
		lines := append([]string(nil), c.lines...)
		for i := 0; i < 90; i++ {
			lines = append(lines, fmt.Sprintf(c.fail, i))
		}
		out, ok := TemplateLogs(lines)
		if !ok {
			t.Fatalf("%s: not templated", name)
		}
		if body := strings.Join(out, "\n") + "\n"; !strings.Contains(body, "\n"+c.want+"\n") {
			t.Errorf("%s: want %s in\n%s", name, c.want, body)
		}
		if n := strings.Count(strings.Join(out, "\n"), " [×90"); n != 1 {
			t.Errorf("%s: the 90 failures are not one record:\n%s", name, strings.Join(out, "\n"))
		}
	}
}

func TestTemplateLogsForWithoutJudge(t *testing.T) {
	for _, in := range [][]string{hdfsLog(), nginxLog(), jsonLinesLog(), composeLog()} {
		a, _ := TemplateLogs(in)
		b, _ := TemplateLogsFor(&Context{Argv: []string{"tool"}}, in)
		sj := &stubJudge{}
		c, _ := TemplateLogsFor(judgeCtx(sj, time.Second), in)
		if strings.Join(a, "\n") != strings.Join(b, "\n") || strings.Join(a, "\n") != strings.Join(c, "\n") || sj.count() != 0 {
			t.Errorf("small log view changed (%d judge calls)", sj.count())
		}
	}
}

func TestJudgeAskCachesAndSharesDeadline(t *testing.T) {
	const first = 200 * time.Millisecond
	sj := &stubJudge{sleeps: []time.Duration{first, time.Minute}}
	r := newJudgeRun(&Context{}, Options{Judge: sj, JudgeTimeout: 2 * time.Second})
	if v := r.ask(FamilyLogs, []string{"a", "b"}); len(v) != 2 || !routine(v[0], 0.9) {
		t.Fatalf("first ask: %v", v)
	}
	if v := r.ask(FamilyLogs, []string{"b", "a"}); len(v) != 2 || sj.count() != 1 {
		t.Fatalf("cached ask called the judge (%d calls)", sj.count())
	}
	start := time.Now()
	if v := r.ask(FamilyLogs, []string{"c"}); v != nil {
		t.Fatalf("ask past the shared deadline answered %v", v)
	}
	if el := time.Since(start); el > 10*time.Second {
		t.Errorf("waited %v for an overrunning judge", el)
	}
	sj.mu.Lock()
	got := sj.timeouts[1]
	sj.mu.Unlock()
	if got <= 0 || got > 2*time.Second-first {
		t.Errorf("second call given %v, want what the first left of 2s", got)
	}
	if !r.off || r.ask(FamilyLines, []string{"d"}) != nil || sj.count() != 2 {
		t.Errorf("judge still asked after a timeout (%d calls)", sj.count())
	}
}

func TestJudgeChunksProtectsFramesAndLocations(t *testing.T) {
	var lines []string
	for i := 0; i < 150; i++ {
		switch {
		case i == 60:
			lines = append(lines, "Exception in thread main")
		case i > 60 && i < 75:
			lines = append(lines, fmt.Sprintf("    at com.example.app.Service.handle%d(Service.java:%d)", i, i))
		case i == 100:
			lines = append(lines, "see src/app/config.ts:42 for the default")
		default:
			lines = append(lines, fmt.Sprintf("preparing stage %c%c", 'a'+i%26, 'a'+i/26))
		}
	}
	sj := &stubJudge{}
	out := JudgeChunks(judgeCtx(sj, time.Second), lines)
	if sj.count() != 1 {
		t.Fatalf("%d calls", sj.count())
	}
	for _, it := range sj.items[0] {
		if strings.Contains(it, "Service.java") || strings.Contains(it, "config.ts") || strings.Contains(it, "Exception") {
			t.Errorf("protected line sent: %q", it)
		}
	}
	joined := strings.Join(out, "\n")
	for _, s := range []string{"Service.java:74", "Service.java:61", "src/app/config.ts:42", "Exception in thread main", "preparing stage aa", "preparing stage tf"} {
		if !strings.Contains(joined, s) {
			t.Errorf("%q folded:\n%s", s, joined)
		}
	}
	if len(out) >= len(lines) {
		t.Errorf("nothing folded")
	}
}
