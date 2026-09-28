package data

import (
	"fmt"
	"github.com/iheeb1/lx/internal/testenv"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tokens"
)

func bigSource(n int) []string {
	src := make([]string, n)
	for i := range src {
		src[i] = fmt.Sprintf("\tx%d := compute(%d, \"some string literal value\") // comment", i, i)
	}
	return src
}

func jsonRecords(n int) []string {
	ls := []string{"["}
	for i := 0; i < n; i++ {
		ls = append(ls, fmt.Sprintf(`  {"id": %d, "name": "item-%d", "description": "a longer description of item %d, with words"},`, i, i, i))
	}
	return append(ls, `  {"id": -1}`, "]")
}

func TestReviewJQBareParseErrorKept(t *testing.T) {
	js := jsonRecords(600)
	in := strings.Join(js[:300], "\n") + "\nparse error: Invalid numeric literal at line 1, column 6\n" + strings.Join(js[300:], "\n")
	got, ok := apply(t, ctx(2, "jq", "-r", ".[]", "x.json"), in)
	if !ok || !strings.Contains(got, "parse error: Invalid numeric literal at line 1, column 6") {
		t.Fatalf("jq 1.6 parse error dropped:\n%s", head(got, 400))
	}
}

func TestReviewJQGluedErrorKept(t *testing.T) {
	js := jsonRecords(600)
	js[400] = js[400][:25] + "jq: error (at <stdin>:401): Cannot index number with \"x\""
	for _, argv := range [][]string{{"jq", "-r", ".[]"}, {"jq", "."}} {
		got, ok := apply(t, ctx(5, argv...), strings.Join(js, "\n"))
		if !ok || !strings.Contains(got, `jq: error (at <stdin>:401): Cannot index number with "x"`) {
			t.Errorf("%v: glued jq error dropped:\n%s", argv, head(got, 400))
		}
	}
}

func TestReviewConflictMarkersSurvive(t *testing.T) {
	src := bigSource(1500)
	hunk := []string{"<<<<<<< HEAD", "\ty := 1", "=======", "\ty := 2", ">>>>>>> feature"}
	src = append(src[:700], append(hunk, src[700:]...)...)
	got, ok := apply(t, ctx(0, "cat", "main.go"), strings.Join(src, "\n"))
	if !ok {
		t.Fatal("bailed")
	}
	for i, h := range hunk {
		want := fmt.Sprintf("  L%d: %s", 701+i, h)
		if !strings.Contains(got, want) {
			t.Errorf("conflict line %q not listed:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "merge-conflict markers") {
		t.Error("header does not say the file has conflict markers")
	}

	var y []string
	y = append(y, "# yarn lockfile v1", "")
	for i := 0; i < 400; i++ {
		y = append(y, fmt.Sprintf("pkg-%d@^1.0.0:", i), fmt.Sprintf("  version \"1.0.%d\"", i),
			fmt.Sprintf("  resolved \"https://registry.yarnpkg.com/pkg-%d/-/pkg-%d-1.0.%d.tgz#0123456789abcdef\"", i, i, i), "")
	}
	clean := strings.Join(y, "\n")
	if got, _ := apply(t, ctx(0, "cat", "yarn.lock"), clean); !strings.HasPrefix(got, "[lx: lockfile yarn.lock") {
		t.Fatalf("control: lockfile not summarized:\n%s", head(got, 300))
	}
	y = append(y[:800], append([]string{"<<<<<<< HEAD", "  version \"2.0.0\"", "=======", "  version \"3.0.0\"", ">>>>>>> feature"}, y[800:]...)...)
	got, _ = apply(t, ctx(0, "cat", "yarn.lock"), strings.Join(y, "\n"))
	if strings.HasPrefix(got, "[lx: lockfile") || !strings.Contains(got, "<<<<<<< HEAD") || !strings.Contains(got, `version "3.0.0"`) {
		t.Fatalf("conflicted lockfile summarized or its hunk hidden:\n%s", head(got, 600))
	}

	if hasConflict([]string{"Title", "=======", "text"}) {
		t.Error("lone ======= taken for a conflict")
	}
}

func TestReviewCatGluedDiagnosticListed(t *testing.T) {
	src := bigSource(1200)
	src[800] += "cat: missing.go: No such file or directory"
	got, _ := apply(t, ctx(1, "cat", "a.go", "missing.go", "b.go"), strings.Join(src, "\n"))
	if !strings.Contains(got, "  L801: "+src[800]) {
		t.Fatalf("glued cat diagnostic not listed:\n%s", got)
	}

	src[800] = strings.Repeat("a", 50000) + "cat: missing.go: No such file or directory"
	got, _ = apply(t, ctx(1, "cat", "a.js", "missing.go", "b.js"), strings.Join(src, "\n"))
	if !strings.Contains(got, "cat: missing.go: No such file or directory") || strings.Contains(got, strings.Repeat("a", 5000)) {
		t.Fatalf("glued diagnostic on a long line not listed, or listed whole:\n%s", head(got, 300))
	}
}

func TestReviewCurlGluedExitMessage(t *testing.T) {
	js := jsonRecords(900)
	js[600] = js[600][:30] + "curl: (18) transfer closed with 48213 bytes remaining to read"
	got, ok := apply(t, ctx(18, "curl", "-sS", "https://x/api"), strings.Join(js[:601], "\n"))
	if !ok {
		t.Fatal("bailed")
	}
	if !regexp.MustCompile(`(?m)^curl: \(18\) transfer closed with 48213 bytes remaining to read$`).MatchString(got) {
		t.Errorf("glued curl message not shown on its own line:\n%s", got)
	}
	if strings.Contains(got, "printed no error message") {
		t.Errorf("claims curl printed no message:\n%s", got)
	}

	in := "line one\nthe log says: curl: (6) Could not resolve host: x\nline three"
	for _, exit := range []int{0, 7} {
		got, _ := apply(t, ctx(exit, "curl", "-s", "https://x/log.txt"), in)
		if !strings.HasPrefix(got, in) {
			t.Errorf("exit %d: body text changed:\n%s", exit, got)
		}
	}
}

func TestReviewBodyLineNumbersExact(t *testing.T) {
	js := jsonRecords(900)
	var raw []string
	raw = append(raw, js[:500]...)
	raw = append(raw, " 60 60989   60 36593    0     0  33.7M      0 --:--:-- --:--:-- --:--:-- 34.8M")
	raw = append(raw, "curl: (18) transfer closed with 24396 bytes remaining to read")
	raw = append(raw, js[500:880]...)
	got, _ := apply(t, ctx(18, "curl", "https://x/api"), strings.Join(raw, "\n"))
	m := regexp.MustCompile(`(?m)^… lines (\d+)-(\d+) omitted`).FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("no omission marker:\n%s", head(got, 300))
	}
	b, _ := strconv.Atoi(m[2])
	out := strings.Split(got, "\n")
	for i, ln := range out {
		if strings.HasPrefix(ln, "… lines ") {
			for i++; i < len(out) && strings.HasPrefix(out[i], "  "+"outline") || i < len(out) && strings.HasPrefix(out[i], "  L"); i++ {
			}
			if out[i] != raw[b] {
				t.Fatalf("line after the marker is %q, want line %d %q", out[i], b+1, raw[b])
			}
			break
		}
	}
}

func TestReviewMeterNoteCondensed(t *testing.T) {
	js := jsonRecords(400)
	js[100] = " 60 60989   60 36593    0     0  33.7M      0 --:--:-- --:--:-- --:--:-- 34.8M" + js[100]
	got, _ := apply(t, ctx(0, "curl", "https://x/api"), strings.Join(js, "\n"))
	if strings.Contains(got, "into the output here") || !strings.Contains(got, "printed into the body above (1×)") {
		t.Fatalf("meter note:\n%s", lxNotes(got))
	}
}

func TestReviewHTTPieExitNotes(t *testing.T) {
	usage := "usage:\n    http [METHOD] URL [REQUEST_ITEM ...]\n\nerror:\n    Request body (from stdin, --raw or a file) and request data (key=value)\ncannot be mixed.\n\nfor more information:\n    run 'http --help' or visit https://httpie.io/docs/cli"
	got, _ := apply(t, ctx(1, "http", "POST", "https://x", "a=b"), usage)
	if strings.Contains(got, "printed no error message") {
		t.Errorf("usage error called silent:\n%s", got)
	}
	got, _ = apply(t, ctx(4, "http", "--check-status", "https://x"), `{"message": "Not Found"}`)
	if !strings.Contains(got, "[lx: http exited 4 (HTTP 4xx response (--check-status)); it printed no error message]") {
		t.Errorf("silent exit 4 not explained:\n%s", got)
	}
	got, _ = apply(t, ctx(4, "http", "--check-status", "https://x"), "http: warning: HTTP 404 Not Found\n\n{\"message\": \"Not Found\"}")
	if strings.Contains(got, "printed no error message") {
		t.Errorf("warning present but called silent:\n%s", got)
	}

	v := "GET /x HTTP/1.1\nAccept: */*\nHost: x\n\nHTTP/1.1 200 OK\nContent-Type: text/plain\n\nhello"
	if got, _ := apply(t, ctx(0, "http", "-v", "https://x"), v); !strings.Contains(got, "[lx: 2 request header lines not shown]") {
		t.Errorf("request headers dropped without a count:\n%s", got)
	}
}

func TestReviewJSONBodyAfterPrelude(t *testing.T) {
	var b strings.Builder
	b.WriteString("/home/user/.venv/lib/python3.9/site-packages/urllib3/__init__.py:35: NotOpenSSLWarning: urllib3 v2 only supports OpenSSL 1.1.1+\n  warnings.warn(\n[")
	for i := 0; i < 400; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"number":%d,"title":"issue %d","state":"open","body":"some longer body text for the issue number %d"}`, i, i, i)
	}
	b.WriteString("]")
	got, _ := apply(t, ctx(0, "http", "https://x/issues"), b.String())
	if !strings.Contains(got, "[lx: JSON body condensed") || !strings.HasPrefix(got, "/home/user/.venv/lib/python3.9/site-packages/urllib3/__init__.py:35: NotOpenSSLWarning") {
		t.Fatalf("prelude + JSON body:\n%s", head(got, 600))
	}

	if k := jsonStart([]string{"warning", "[not json", "x"}); k != -1 {
		t.Errorf("jsonStart = %d for invalid JSON", k)
	}
}

func TestReviewWget(t *testing.T) {
	de := "--2026-09-26 02:14:02--  https://go.dev/dl/nope.tar.gz\nAuflösen des Hostnamens go.dev (go.dev)… 216.239.32.21\nVerbindungsaufbau zu go.dev (go.dev)|216.239.32.21|:443 … verbunden.\nHTTP-Anforderung gesendet, auf Antwort wird gewartet … 404 Not Found\n2026-09-26 02:14:02 FEHLER 404: Not Found."
	if got, ok := apply(t, ctx(8, "wget", "https://go.dev/dl/nope.tar.gz"), de); ok {
		t.Errorf("localized wget log not bailed:\n%s", got)
	}

	in := "--2026-09-26 02:14:02--  https://x/a\nHTTP request sent, awaiting response... 200 OK\nLength: 10 [text/plain]\nSaving to: ‘a’\nSome new wget notice nobody listed.\n2026-09-26 02:14:02 (1 MB/s) - ‘a’ saved [10/10]"
	if got, ok := apply(t, ctx(0, "wget", "https://x/a"), in); !ok || !strings.Contains(got, "Saving to: ‘a’\nSome new wget notice nobody listed.\n2026") {
		t.Errorf("unknown log line moved or dropped (ok=%v):\n%s", ok, got)
	}
	got, _ := apply(t, ctx(8, "wget", "-q", "https://x/a"), "")
	if !strings.Contains(got, "[lx: wget exited 8 (the server issued an error response (4xx/5xx)); wget printed no error message]") {
		t.Errorf("silent wget failure not explained: %q", got)
	}

	got, _ = apply(t, ctx(8, "wget", "-qO-", "--content-on-error", "https://x/a"), `{"error": "not found"}`)
	if !strings.Contains(got, "wget printed no error message") {
		t.Errorf("body text taken for wget's message:\n%s", got)
	}
	for _, tc := range []struct {
		args   []string
		stdout bool
	}{
		{[]string{"-O", "-", "u"}, true}, {[]string{"-qO-", "u"}, true}, {[]string{"-qO", "-", "u"}, true},
		{[]string{"--output-document=-", "u"}, true}, {[]string{"--output-document", "-", "u"}, true},
		{[]string{"-O", "out.tgz", "u"}, false}, {[]string{"-q", "u"}, false}, {[]string{"-O/dev/stdout", "u"}, true},
	} {
		if _, _, s := wgetArgs(tc.args); s != tc.stdout {
			t.Errorf("wgetArgs(%q) stdout = %v", tc.args, s)
		}
	}
}

func TestReviewSplitGlued(t *testing.T) {
	out, idx := splitGlued([]string{"a", "bXc", "d"}, func(s string) int { return strings.Index(s, "X") })
	if strings.Join(out, "|") != "a|b|Xc|d" || fmt.Sprint(idx) != "[0 1 1 2]" {
		t.Fatalf("got %q %v", out, idx)
	}
	out, idx = splitGlued([]string{"a", "b"}, func(string) int { return -1 })
	if strings.Join(out, "|") != "a|b" || fmt.Sprint(idx) != "[0 1]" {
		t.Fatalf("got %q %v", out, idx)
	}
}

func TestReviewHTMLCapMarked(t *testing.T) {
	var b strings.Builder
	b.WriteString("<!doctype html><html><head><title>T</title></head><body>")
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&b, "<p>paragraph %d with enough words to count as text</p>\n", i)
	}
	got, _ := apply(t, ctx(0, "curl", "-s", "https://x/"), b.String())
	if !regexp.MustCompile(`\[lx: … \+[\d,]+ more lines of page text not shown\]`).MatchString(got) {
		t.Fatalf("cap marker:\n%s", lxNotes(got))
	}
	if engine.IsError(got) {
		t.Log("view has error-class text")
	}
}

func TestReviewLatin1IsText(t *testing.T) {
	src := bigSource(300)
	src[100] = "assert(string.byte(\"\\xe4l\\0\xf3u\", 1, -1)) -- Latin-1 \xa9 1994"
	in := strings.Join(src, "\n")
	if got, _ := apply(t, ctx(0, "cat", "strings.lua"), in); got != in {
		t.Fatalf("Latin-1 text changed or hidden:\n%s", head(got, 300))
	}
	if got, _ := apply(t, ctx(0, "curl", "-s", "https://x/a.txt"), in); !strings.HasPrefix(got, src[0]) {
		t.Fatalf("Latin-1 body hidden:\n%s", head(got, 300))
	}
	for _, tc := range []struct {
		in  string
		bin bool
	}{
		{"plain text\n", false},
		{"caf\xe9 cr\xe8me\n", false},
		{"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x01\x00\x00\x00\x01\x00\x08\x06\x00\x00\x00\x5c\x72\xa8\x66", true},
		{strings.Repeat("\xff\xfe\x01\x02", 100), true},
		{"a\x00b\n", false},
	} {
		if got := looksBinary(tc.in); got != tc.bin {
			t.Errorf("looksBinary(%q) = %v", head(tc.in, 30), got)
		}
	}
}

func TestReviewHugeNewPathsFast(t *testing.T) {
	src := bigSource(50000)
	for i := 1000; i < 50000; i += 5000 {
		src[i] = "<<<<<<< HEAD"
		src[i+2] = "======="
		src[i+4] = ">>>>>>> feature"
		src[i+7] += "cat: missing.go: No such file or directory"
	}
	js := jsonRecords(50000)
	for i := 1000; i < 50000; i += 7000 {
		js[i] = js[i][:20] + "jq: error (at <stdin>:1): Cannot index number with \"x\""
	}
	var wlog strings.Builder
	for i := range 50000 {
		fmt.Fprintf(&wlog, "--2026-09-26 02:14:02--  https://x/f%d\nHTTP request sent, awaiting response... 200 OK\nSaving to: ‘f%d’\n", i, i)
	}
	curlBody := append(append([]string(nil), js[:30000]...), " 60 60989   60 36593    0     0  33.7M      0 --:--:-- --:--:-- --:--:-- 34.8M")
	curlBody = append(curlBody, js[30000:40000]...)
	curlBody[len(curlBody)-1] += "curl: (18) transfer closed with 1 bytes remaining to read"
	latin := strings.Join(src, "\n") + "\n-- \xa9 1994 caf\xe9"
	for _, tc := range []struct {
		c  *engine.Context
		in string
	}{
		{ctx(0, "cat", "main.go"), strings.Join(src, "\n")},
		{ctx(1, "cat", "a.go", "missing.go"), strings.Join(src, "\n")},
		{ctx(0, "cat", "strings.lua"), latin},
		{ctx(5, "jq", "-r", ".[]"), strings.Join(js, "\n")},
		{ctx(5, "jq", "."), strings.Join(js, "\n")},
		{ctx(18, "curl", "https://x/api"), strings.Join(curlBody, "\n")},
		{ctx(0, "wget", "-i", "urls.txt"), wlog.String()},
	} {
		start := time.Now()
		got, ok := apply(t, tc.c, tc.in)
		d := time.Since(start)
		t.Logf("%v: %v, %d tokens", tc.c.Argv, d, tokens.Count(got))
		if d > testenv.Scale(time.Second) || !ok {
			t.Errorf("%v: %v ok=%v", tc.c.Argv, d, ok)
		}
	}
}

func TestReviewWgetManyRequests(t *testing.T) {
	var b strings.Builder
	for i := range 300 {
		fmt.Fprintf(&b, "--2026-09-26 02:14:02--  https://x/f%d\nReusing existing connection to x:443.\nHTTP request sent, awaiting response... ", i)
		switch i {
		case 150:
			b.WriteString("404 Not Found\n2026-09-26 02:14:02 ERROR 404: Not Found.\n\n")
		case 200:
			b.WriteString("500 Internal Server Error\n2026-09-26 02:14:02 ERROR 500: Internal Server Error.\n\n")
		default:
			fmt.Fprintf(&b, "200 OK\nLength: 10 [text/html]\nSaving to: ‘x/f%d’\n\n2026-09-26 02:14:02 (1 MB/s) - ‘x/f%d’ saved [10/10]\n\n", i, i)
		}
	}
	b.WriteString("FINISHED --2026-09-26 02:14:09--\nTotal wall clock time: 7.1s\nDownloaded: 298 files, 2.9K in 0.01s (290 KB/s)")
	got, ok := apply(t, ctx(8, "wget", "-r", "https://x/"), b.String())
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{"https://x/f150", "ERROR 404: Not Found.", "https://x/f200", "ERROR 500: Internal Server Error.",
		"https://x/f0\n", "https://x/f299", "Downloaded: 298 files", "[lx: 140 requests that succeeded (status below 400, no error) not shown]",
		"[lx: 49 requests that succeeded (status below 400, no error) not shown]", "[lx: 94 requests that succeeded"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	if m := engine.MissingErrorLines(b.String(), got); len(m) > 0 {
		t.Errorf("error lines dropped: %q", m)
	}
}

func TestReviewCurlVerboseGluedInfoRejoined(t *testing.T) {
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < 300; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, `{"id": %d, "title": "issue %d: flaky test in package %d", "labels": ["bug"]}`, i, i, i%9)
	}
	b.WriteString("]")
	doc := b.String()
	cut := len(doc) * 2 / 3
	in := "> GET /issues HTTP/1.1\n> Host: x\n>\n< HTTP/1.1 200 OK\n< Content-Type: application/json\n<\n{ [34900 bytes data]\n" +
		doc[:cut] + "* Connection #0 to host 127.0.0.1 left intact\n" + doc[cut:]
	got, _ := apply(t, ctx(0, "curl", "-sv", "https://x/issues"), in)
	if !strings.Contains(got, "[lx: JSON body condensed") || strings.Contains(got, "left intact") {
		t.Fatalf("glued info line not split out / JSON not rejoined:\n%s", head(got, 800))
	}

	if k := gluedInfo(`{"note": "a * b * c"}`); k != -1 {
		t.Errorf("gluedInfo split body text at %d", k)
	}
}

func TestReviewMeterAtBodyStartMarked(t *testing.T) {
	in := "< HTTP/1.1 200 OK\n< Content-Type: application/json\n<\n" +
		"100 34900  100 34900    0     0  32.6M      0 --:--:-- --:--:-- --:--:-- 33.2M\n" +
		"* Connection #0 to host 127.0.0.1 left intact\n" + `ation"], "comments": 9}]`
	got, _ := apply(t, ctx(0, "curl", "-v", "https://x/issues"), in)
	if !strings.Contains(got, meterNote) {
		t.Fatalf("no meter note:\n%s", got)
	}
}

func TestReviewLockfileNeedsItsFormat(t *testing.T) {
	src := append([]string{"cat: missing.c: No such file or directory"}, bigSource(600)...)
	src[300], src[301] = "fail:", "done:"
	got, _ := apply(t, ctx(1, "cat", "yarn.lock"), strings.Join(src, "\n"))
	if strings.HasPrefix(got, "[lx: lockfile") || !strings.Contains(got, "cat: missing.c: No such file or directory") {
		t.Fatalf("non-lockfile summarized:\n%s", head(got, 300))
	}
	gem := append([]string{"    rack (3.0.9)"}, bigSource(600)...)
	if got, _ := apply(t, ctx(0, "cat", "Gemfile.lock"), strings.Join(gem, "\n")); strings.HasPrefix(got, "[lx: lockfile") {
		t.Fatalf("Gemfile.lock without GEM/specs summarized:\n%s", head(got, 300))
	}
	var y strings.Builder
	y.WriteString("# yarn lockfile v1\n\n")
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&y, "pkg-%d@^1.0.0:\n  version \"1.0.%d\"\n  resolved \"https://registry.yarnpkg.com/pkg-%d/-/pkg-%d-1.0.%d.tgz#0123456789abcdef\"\n\n", i, i, i, i, i)
	}
	got, _ = apply(t, ctx(1, "cat", "yarn.lock"), "cat: yarn.lock: Input/output error\n"+y.String())
	if !strings.HasPrefix(got, "cat: yarn.lock: Input/output error\n[lx: lockfile yarn.lock (yarn v1): 300 packages") {
		t.Fatalf("summary lost the diagnostic:\n%s", head(got, 300))
	}
}
