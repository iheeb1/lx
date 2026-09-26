package engine

import (
	"fmt"
	"strings"
	"testing"
)

// Synthetic log fixtures, generated deterministically (the public loghub
// samples are not redistributable). Each mimics a real format.

// lcg is a tiny deterministic pseudo-random sequence.
type lcg uint64

func (l *lcg) next(n int) int {
	*l = *l*6364136223846793005 + 1442695040888963407
	return int((uint64(*l) >> 33) % uint64(n))
}

func hdfsLog() []string {
	r := lcg(1)
	var out []string
	for i := 0; i < 600; i++ {
		ts := fmt.Sprintf("081109 %02d%02d%02d", 20+i/3600, (i/60)%60, i%60)
		blk := fmt.Sprintf("blk_%d", 1000000000000+r.next(1<<30))
		ip := fmt.Sprintf("10.251.%d.%d", r.next(255), r.next(255))
		switch i % 4 {
		case 0:
			out = append(out, fmt.Sprintf("%s %d INFO dfs.DataNode$PacketResponder: PacketResponder %d for block %s terminating", ts, 100+r.next(900), r.next(3), blk))
		case 1:
			out = append(out, fmt.Sprintf("%s %d INFO dfs.DataNode$DataXceiver: Receiving block %s src: /%s:%d dest: /%s:50010", ts, 100+r.next(900), blk, ip, 40000+r.next(20000), ip))
		case 2:
			out = append(out, fmt.Sprintf("%s %d INFO dfs.FSNamesystem: BLOCK* NameSystem.addStoredBlock: blockMap updated: %s:50010 is added to %s size %d", ts, 100+r.next(900), ip, blk, 67108864-r.next(100000)))
		default:
			if i%100 == 3 {
				out = append(out, fmt.Sprintf("%s %d WARN dfs.DataNode$DataXceiver: %s:50010:Got exception while serving %s to /%s:", ts, 100+r.next(900), ip, blk, ip))
				out = append(out, "java.io.IOException: Connection reset by peer")
				continue
			}
			out = append(out, fmt.Sprintf("%s %d INFO dfs.DataNode: %s Served block %s to /%s", ts, 100+r.next(900), ip, blk, ip))
		}
	}
	return out
}

func nginxLog() []string {
	r := lcg(2)
	paths := []string{"/api/users/%d", "/api/orders/%d", "/static/app.js", "/health"}
	codes := []int{200, 200, 200, 304, 404}
	var out []string
	for i := 0; i < 400; i++ {
		p := paths[r.next(len(paths))]
		if strings.Contains(p, "%d") {
			p = fmt.Sprintf(p, 10+r.next(990))
		}
		out = append(out, fmt.Sprintf(`192.168.1.%d - - [26/Sep/2026:10:%02d:%02d +0000] "GET %s HTTP/1.1" %d %d "-" "Mozilla/5.0"`,
			r.next(255), i/60, i%60, p, codes[r.next(len(codes))], 100+r.next(5000)))
	}
	return out
}

func jsonLinesLog() []string {
	r := lcg(3)
	var out []string
	for i := 0; i < 300; i++ {
		ts := fmt.Sprintf("2026-09-26T10:%02d:%02d.%03dZ", i/60, i%60, r.next(1000))
		if i == 150 || i == 151 {
			out = append(out, fmt.Sprintf(`{"ts":"%s","level":"error","msg":"db query failed","err":"pq: deadlock detected","query_id":%d}`, ts, 7000+i))
			continue
		}
		out = append(out, fmt.Sprintf(`{"ts":"%s","level":"info","msg":"request completed","method":"GET","path":"/api/items/%d","status":200,"duration_ms":%d}`, ts, r.next(500), 1+r.next(900)))
	}
	return out
}

func composeLog() []string {
	var out []string
	for i := 0; i < 120; i++ {
		ts := fmt.Sprintf("2026-09-26T10:00:%02d.%03dZ", i/2, i*7%1000)
		switch i % 3 {
		case 0:
			out = append(out, fmt.Sprintf("db-1      | %s LOG:  checkpoint complete: wrote %d buffers", ts, 10+i))
		case 1:
			out = append(out, fmt.Sprintf("web-1     | %s INFO  GET /health 200 %dms", ts, 1+i%9))
		default:
			out = append(out, fmt.Sprintf("worker-1  | %s INFO  job %d done", ts, 1000+i))
		}
		if i == 90 {
			out = append(out,
				"web-1     | Traceback (most recent call last):",
				`web-1     |   File "/app/server.py", line 42, in handle`,
				"web-1     |     conn = db.connect()",
				"web-1     | psycopg2.OperationalError: could not connect to server: Connection refused",
				"web-1 exited with code 1")
		}
	}
	return out
}

func TestTemplateLogsFixtures(t *testing.T) {
	for name, in := range map[string][]string{
		"hdfs": hdfsLog(), "nginx": nginxLog(), "jsonl": jsonLinesLog(), "compose": composeLog(),
	} {
		out, ok := TemplateLogs(in)
		if !ok {
			t.Errorf("%s: not templated", name)
			continue
		}
		joined := strings.Join(out, "\n")
		if !strings.HasPrefix(out[0], "[log: ") {
			t.Errorf("%s: header %q", name, out[0])
		}
		if m := MissingErrorLines(strings.Join(in, "\n"), joined); len(m) > 0 {
			t.Errorf("%s: error lines lost: %q", name, m)
		}
		if len(out)*4 > len(in) {
			t.Errorf("%s: weak compression %d → %d lines", name, len(in), len(out))
		}
		if again, _ := TemplateLogs(in); strings.Join(again, "\n") != joined {
			t.Errorf("%s: not deterministic", name)
		}
		t.Logf("%s: %d → %d lines\n%s", name, len(in), len(out), joined)
	}
}

func TestTemplateLogsDetails(t *testing.T) {
	out, _ := TemplateLogs(hdfsLog())
	joined := strings.Join(out, "\n")
	// Warnings keep their own template; the exception record stays verbatim.
	if !strings.Contains(joined, "WARN dfs.DataNode$DataXceiver") || !strings.Contains(joined, "java.io.IOException: Connection reset by peer") {
		t.Errorf("warning/error record missing:\n%s", joined)
	}
	if !strings.Contains(joined, "[×150]") || !strings.Contains(joined, "vars:") {
		t.Errorf("counts or vars missing:\n%s", joined)
	}
	out, _ = TemplateLogs(jsonLinesLog())
	joined = strings.Join(out, "\n")
	if !strings.Contains(joined, `"err":"pq: deadlock detected","query_id":7150}`) || !strings.Contains(joined, "duration_ms 1–") {
		t.Errorf("jsonl:\n%s", joined)
	}
}

func TestTemplateLogsRejects(t *testing.T) {
	var prose []string
	for i := 0; i < 50; i++ {
		prose = append(prose, fmt.Sprintf("line %d of ordinary program output", i))
	}
	if _, ok := TemplateLogs(prose); ok {
		t.Error("non-log output templated")
	}
	if _, ok := TemplateLogs(nginxLog()[:10]); ok {
		t.Error("too-short log templated")
	}
	var blame []string
	for i := 0; i < 50; i++ {
		blame = append(blame, fmt.Sprintf("e0f8f1a4 (Some Author 2014-06-17 19:42:05 +0200 %3d) code line %d", i, i))
	}
	if _, ok := TemplateLogs(blame); ok {
		t.Error("git blame templated")
	}
}
