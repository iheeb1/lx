package engine_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
)

func routineLog(n int) []string {
	var ls []string
	for i := 0; i < n; i++ {
		ls = append(ls, fmt.Sprintf("2024-01-01T10:%02d:%02dZ INFO server handled request id=%d path=/api/v1/items status=200 dur=%dms", (i/60)%60, i%60, 1000+i, 10+i%37))
	}
	return ls
}

func fakeWord(i int) string {
	const a = "bcdfghjklmnpqrstvwxz"
	return string([]byte{a[i%20], 'a', a[(i/20)%20], 'o', a[(i/400)%20]})
}

func TestLogErrorsKeepTheirWords(t *testing.T) {
	var jsonMsgs, zap, py, words, req, cont, bare, huge []string

	for i := 0; i < 60; i++ {
		jsonMsgs = append(jsonMsgs, fmt.Sprintf(`{"ts":"2024-01-01T10:00:%02dZ","level":"info","msg":"handled request %d"}`, i, i))
		zap = append(zap, fmt.Sprintf(`{"level":"info","ts":1700000%03d.5,"caller":"http/server.go:88","msg":"request served","path":"/v1/items"}`, i))
		py = append(py, fmt.Sprintf("INFO:app.http:served request %d", i))
	}
	jsonWant := []string{"connection refused", "is 100% full", "user 42 not found", "index out of range", "certificate for api-2 expired"}
	for i, m := range []string{"failed to connect to 10.0.0.5:5432: connection refused", "disk /dev/sda1 is 100% full", "user 42 not found",
		"panic: runtime error: index out of range [3] with length 3", "TLS certificate for api-2 expired"} {
		jsonMsgs = append(jsonMsgs, fmt.Sprintf(`{"ts":"2024-01-01T11:00:%02dZ","level":"error","msg":%q}`, i, m))
	}
	zapWant := []string{`relation \"users\" does not exist`, "context deadline exceeded", "password authentication failed", "driver: bad connection"}
	for i, e := range []string{`pq: relation \"users\" does not exist`, "context deadline exceeded", `pq: password authentication failed for user \"app\"`, "driver: bad connection"} {
		zap = append(zap, fmt.Sprintf(`{"level":"error","ts":1700001%03d.5,"caller":"db/pool.go:120","msg":"query failed","error":"%s"}`, i, e))
	}
	pyWant := []string{"app.db:migration", "app.cache:eviction", "app.auth:login", "app.mail:delivery"}
	for _, m := range pyWant {
		py = append(py, "ERROR:"+m+" failed")
	}
	words = routineLog(60)
	wordWant := []string{"refused", "reset", "aborted", "timeout", "unreachable"}
	for i, w := range wordWant {
		words = append(words, fmt.Sprintf("2024-01-01T11:00:0%dZ ERROR upstream payments call failed: connection %s", i, w))
	}
	req = append(routineLog(60),
		"2024-01-01T11:00:00Z ERROR [req-abc123] user=42 took=15ms upstream payments failed: connection refused",
		"2024-01-01T11:00:05Z ERROR [req-def456] user=43 took=17ms upstream payments failed: connection reset")
	cont = routineLog(60)
	for i, code := range []string{"503", "401", "422"} {
		cont = append(cont, fmt.Sprintf("2024-01-01T12:00:0%dZ ERROR payment capture failed", i), "  upstream responded HTTP "+code)
	}
	bare = routineLog(1500)
	for i := 0; i < 150; i++ {
		bare = append(bare, fmt.Sprintf("2024-01-01T11:%02d:%02dZ ERROR %s %s job aborted after retries exhausted on node %s in zone %s",
			i/60%60, i%60, fakeWord(i), fakeWord(i+7), fakeWord(i+11), fakeWord(i+13)))
	}
	bare = append(bare, "2024-01-01T12:00:00Z ERROR upstream payments call failed: connection refused",
		"2024-01-01T12:00:05Z ERROR upstream payments call failed: connection reset")
	pad := strings.Repeat("abcdefghi ", 100)
	huge = routineLog(80)
	huge = append(huge[:40], append([]string{"2024-01-01T11:00:00Z WARN payload dump " + pad + " upstream said: permission denied for role app " + pad + " end"}, huge[40:]...)...)

	for _, c := range []struct {
		name string
		argv []string
		in   []string
		want []string
	}{
		{"json messages", []string{"kubectl", "logs", "x"}, jsonMsgs, jsonWant},
		{"json error field", []string{"kubectl", "logs", "x"}, zap, zapWant},
		{"level fused to the logger", dockerLogs, py, pyWant},
		{"one word apart", dockerLogs, words, wordWant},
		{"word apart among values", dockerLogs, req, []string{"connection refused", "connection reset"}},
		{"continuation lines", dockerLogs, cont, []string{"HTTP 503", "401", "422"}},
		{"bare tier", dockerLogs, bare, []string{"refused", "reset"}},
		{"long line, generic engine", []string{"myapp"}, huge, []string{"permission denied"}},
	} {
		in := strings.Join(c.in, "\n")
		res := engine.Process(&engine.Context{Argv: c.argv}, in, engine.Options{})
		for _, w := range c.want {
			if !strings.Contains(res.Output, w) {
				t.Errorf("%s: %q missing from\n%s", c.name, w, res.Output)
			}
		}
		if m := engine.MissingErrorKinds(in, res.Output); len(m) > 0 {
			t.Errorf("%s: error kinds missing: %q", c.name, m)
		}
	}
}

func TestTightLogErrorsKeepCauses(t *testing.T) {
	causes := []string{"java.net.ConnectException: Connection refused", "java.sql.SQLTransientConnectionException: pool exhausted",
		"java.lang.OutOfMemoryError: Java heap space", "javax.net.ssl.SSLHandshakeException: certificate expired",
		"java.io.FileNotFoundException: /etc/app/keys.pem (No such file or directory)"}
	ls := routineLog(2000)
	var out []string
	for i := 0; i < 150; i++ {
		out = append(out, ls[i*13:(i+1)*13]...)
		out = append(out,
			fmt.Sprintf("2024-01-01T11:%02d:%02dZ ERROR %s %s job aborted", (i/60)%60, i%60, fakeWord(i), fakeWord(i+7)),
			fmt.Sprintf("Caused by: %s while calling %s", causes[i%len(causes)], fakeWord(i+3)),
			fmt.Sprintf("\tat com.acme.%s.Worker.run(Worker.java:%d)", fakeWord(i), 10+i))
	}
	in := strings.Join(out, "\n")
	res := engine.Process(&engine.Context{Argv: dockerLogs}, in, engine.Options{})
	if !strings.HasPrefix(res.Output, "[log: ") {
		t.Fatalf("not a log view:\n%s", res.Output)
	}
	for _, c := range causes {
		if n := strings.Count(res.Output, strings.Fields(c)[0]); n != 30 {
			t.Errorf("%s shown %d times, want all 30", strings.Fields(c)[0], n)
		}
	}
	if !strings.Contains(res.Output, "\nERROR ") {
		t.Errorf("the budget no longer forces compact errors; the test lost its point")
	}
	if m := engine.MissingErrorKinds(in, res.Output); len(m) > 0 {
		t.Errorf("%d error kinds missing, e.g. %q", len(m), m[0])
	}
	if res.OutTokens > engine.DefaultBudget+200 {
		t.Errorf("%d tokens", res.OutTokens)
	}
}
