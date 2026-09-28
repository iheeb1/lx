package data

import (
	"fmt"
	"github.com/iheeb1/lx/internal/testenv"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tokens"
)

func ctx(exit int, argv ...string) *engine.Context {
	return &engine.Context{Argv: argv, Exit: exit, Cwd: "/home/user/src/app", Home: "/home/user"}
}

func apply(t *testing.T, c *engine.Context, in string) (string, bool) {
	t.Helper()
	f := engine.Find(c)
	if f == nil {
		t.Fatalf("no filter for %v", c.Argv)
	}
	return f.Apply(c, in)
}

func TestMatch(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"curl", "-s", "https://x"}, "curl"},
		{[]string{"/usr/bin/curl", "https://x"}, "curl"},
		{[]string{"wget", "-qO-", "https://x"}, "wget"},
		{[]string{"http", "GET", "https://x"}, "httpie"},
		{[]string{"xh", "https://x"}, "httpie"},
		{[]string{"jq", ".", "a.json"}, "jq"},
		{[]string{"cat", "a.go"}, "cat"},
		{[]string{"bat", "-p", "a.go"}, "cat"},
		{[]string{"head", "-n", "5", "a.go"}, "cat"},
		{[]string{"tail", "-n", "5", "a.go"}, "cat"},
		{[]string{"bash", "-c", `curl -s "https://x" | jq '.[] | {a}'`}, "jq"},
		{[]string{"bash", "-c", `curl -s https://x | head -50`}, "curl"},
		{[]string{"sh", "-c", `FOO=1 cat big.log | tail -n 100`}, "cat"},
		{[]string{"bash", "-c", `curl -s https://x | grep foo`}, ""},
		{[]string{"bash", "-c", `curl -s https://x && echo done`}, ""},
		{[]string{"bash", "-lc", `cat $HOME/x`}, ""},
		{[]string{"catalog"}, ""},
		{[]string{"git", "cat-file", "-p", "HEAD"}, ""},
	}
	for _, tc := range cases {
		f := engine.Find(ctx(0, tc.argv...))
		got := ""
		if f != nil {
			got = f.Name()
		}
		if got != tc.want {
			t.Errorf("%q: filter %q, want %q", tc.argv, got, tc.want)
		}
	}
}

func TestStream(t *testing.T) {
	for _, argv := range [][]string{{"tail", "-f", "x.log"}, {"tail", "-F", "x.log"}, {"tail", "--follow=name", "x"}, {"tail", "-n5", "-f", "x"}, {"bash", "-c", "tail -f x.log"}} {
		f := engine.Find(ctx(0, argv...))
		s, ok := f.(engine.Streamer)
		if !ok || !s.Stream(ctx(0, argv...)) {
			t.Errorf("%q must stream", argv)
		}
	}
	for _, argv := range [][]string{{"tail", "-n", "5", "x"}, {"tail", "-n", "f", "x"}, {"cat", "-f"}} {
		f := engine.Find(ctx(0, argv...))
		if s, ok := f.(engine.Streamer); ok && s.Stream(ctx(0, argv...)) {
			t.Errorf("%q must not stream", argv)
		}
	}
}

func TestEmptyAndSingleLine(t *testing.T) {
	for _, argv := range [][]string{{"curl", "https://x"}, {"curl", "-v", "https://x"}, {"curl", "-i", "https://x"}, {"wget", "-q", "-O-", "x"},
		{"http", "x"}, {"jq", "."}, {"cat", "x"}, {"head", "x"}} {
		for _, in := range []string{"", "x", "{}", "HTTP/2 200", "* ", "< ", "> ", "#", "curl: (6) Could not resolve host: x"} {
			got, ok := apply(t, ctx(0, argv...), in)
			if ok && strings.TrimSpace(in) != "" && got == "" && in != "#" && !strings.HasPrefix(in, "* ") && in != "> " && in != "< " {
				t.Errorf("%q on %q: empty output", argv, in)
			}
		}
	}
}

func TestBailsOnUnknown(t *testing.T) {

	de := "--2026-09-26 10:00:00--  https://x/\nAuflösen des Hostnamens x (x)… 1.2.3.4\nVerbindungsaufbau zu x (x)|1.2.3.4|:443 … verbunden.\nHTTP-Anforderung gesendet, auf Antwort wird gewartet … 200 OK\nLänge: 12 [text/plain]\nWird in »index.html« gespeichert.\n"
	de = strings.Replace(de, "--2026-09-26 10:00:00--  https://x/\n", "", 1)
	if _, ok := apply(t, ctx(0, "wget", "https://x/"), de); ok {
		t.Error("wget: localized log must bail")
	}
	if _, ok := apply(t, ctx(0, "curl", "--trace-ascii", "-", "https://x"), "== Info: Trying 1.2.3.4:443...\n=> Send header, 70 bytes (0x46)\n"); ok {
		t.Error("curl --trace-ascii - must bail")
	}
}

func TestCurlExitWithoutMessage(t *testing.T) {

	body := strings.Repeat(`{"ok": true, "status": "passed"}`+"\n", 3)
	got, ok := apply(t, ctx(28, "curl", "-s", "https://x"), body)
	if !ok || !strings.Contains(got, "[lx: curl exited 28 (operation timed out)") {
		t.Fatalf("exit not explained:\n%s", got)
	}

	got, _ = apply(t, ctx(7, "curl", "https://x"), "curl: (7) Failed to connect to x port 443: Connection refused")
	if strings.Contains(got, "[lx:") {
		t.Fatalf("note added although curl printed its error:\n%s", got)
	}
}

func TestCurlHeaders(t *testing.T) {
	head := "HTTP/1.1 %d X\nDate: Sat, 26 Sep 2026 10:00:00 GMT\nContent-Type: application/json\nETag: \"abc\"\nSet-Cookie: sid=s3cr3t; Path=/; HttpOnly\nRetry-After: 30\nX-RateLimit-Remaining: 0\nCache-Control: no-cache\n\n{\"message\": \"slow down\"}"
	got, _ := apply(t, ctx(0, "curl", "-i", "https://x"), fmt.Sprintf(head, 200))
	for _, want := range []string{"HTTP/1.1 200 X", "Content-Type: application/json", "Retry-After: 30", "X-RateLimit-Remaining: 0", "Set-Cookie: sid=… (value hidden)", "not shown: date, etag, cache-control", `{"message": "slow down"}`} {
		if !strings.Contains(got, want) {
			t.Errorf("200: missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "s3cr3t") {
		t.Error("cookie value shown")
	}

	for _, tc := range []struct {
		c    *engine.Context
		code int
	}{
		{ctx(0, "curl", "-i", "https://x"), 429},
		{ctx(0, "curl", "-sI", "https://x"), 200},
		{ctx(0, "curl", "-i", "-H", "Origin: https://app", "https://x"), 200},
		{ctx(0, "curl", "-i", "--header=origin: https://app", "https://x"), 200},
	} {
		in := fmt.Sprintf(head, tc.code)
		got, _ := apply(t, tc.c, in)
		for _, ln := range strings.Split(in, "\n") {
			if !strings.Contains(got, ln) {
				t.Errorf("%v %d: header %q not kept", tc.c.Argv, tc.code, ln)
			}
		}
	}
}

func TestCurlVerbose(t *testing.T) {
	in := strings.Join([]string{
		"*   Trying 1.2.3.4:443...",
		"* Connected to x (1.2.3.4) port 443",
		"* SSL certificate problem: certificate has expired",
		"} [5 bytes data]",
		"> GET /readme HTTP/1.1",
		"> Host: x",
		"> User-Agent: curl/8.7.1",
		">",
		"< HTTP/1.1 200 OK",
		"< Content-Type: text/markdown",
		"< Server: nginx",
		"<",
		"{ [120 bytes data]",
		"# Title",
		"* first item",
		"* second item with an error word",
		"> a quoted line",
		"<",
		"* Connection #0 to host x left intact",
	}, "\n")
	got, _ := apply(t, ctx(0, "curl", "-v", "https://x/readme"), in)
	for _, want := range []string{"* SSL certificate problem: certificate has expired", "> GET /readme HTTP/1.1", "< HTTP/1.1 200 OK",
		"< Content-Type: text/markdown", "* first item", "* second item with an error word\n> a quoted line\n<"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	for _, not := range []string{"Trying", "Connected to", "bytes data", "Host: x", "left intact", "Server: nginx"} {
		if strings.Contains(got, not) {
			t.Errorf("%q should be hidden:\n%s", not, got)
		}
	}
	if !strings.Contains(got, `[lx: -v output not shown: 3 "* " lines (connection, TLS, HTTP/2 stream), 2 request header lines]`) {
		t.Errorf("hidden lines not counted:\n%s", got)
	}
}

func TestCurlMeterGlued(t *testing.T) {
	frame := "  0     0    0     0    0     0      0      0 --:--:-- --:--:-- --:--:--     0"
	in := "  % Total    % Received % Xferd  Average Speed   Time    Time     Time  Current\n" +
		"                                 Dload  Upload   Total   Spent    Left  Speed\n" +
		frame + "curl: (6) Could not resolve host: x\n"
	got, _ := apply(t, ctx(6, "curl", "https://x"), in)
	if got != "curl: (6) Could not resolve host: x" {
		t.Fatalf("got %q", got)
	}

	got, _ = apply(t, ctx(0, "curl", "https://x"), frame+"42\n")
	if got != "42" {
		t.Fatalf("glued digits: got %q", got)
	}

	body := "{\n  \"a\": 1,\n" + "100  6805  100  6805    0     0   6595      0  0:00:01  0:00:01 --:--:--  6600\n" + "  \"b\": 2\n}"
	got, _ = apply(t, ctx(0, "curl", "https://x"), body)
	if !strings.Contains(got, meterNote) || !strings.Contains(got, `"b": 2`) {
		t.Fatalf("meter in body not marked:\n%s", got)
	}
}

func TestBodies(t *testing.T) {

	var items []string
	for i := range 60 {
		items = append(items, fmt.Sprintf(`{"id": %d, "name": "item-%d", "state": "open", "url": "https://api.x/items/%d"}`, i, i, i))
	}
	j := `{"error": {"code": "rate_limited", "message": "API rate limit exceeded for 1.2.3.4"}, "items": [` + strings.Join(items, ",\n") + `]}`
	got, _ := apply(t, ctx(0, "curl", "-s", "https://x"), j)
	if !strings.HasPrefix(got, "[lx: JSON body condensed") || !strings.Contains(got, `error: {"code":"rate_limited","message":"API rate limit exceeded for 1.2.3.4"}`) {
		t.Fatalf("error field not first/verbatim:\n%s", got)
	}

	got, _ = apply(t, ctx(0, "curl", "-s", "https://x/logo.png"), "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\xff\xfe"+strings.Repeat("\x00\xde\xad", 400))
	if !strings.HasPrefix(got, "[lx: binary body") {
		t.Fatalf("binary: %q", got)
	}

	page := "<!DOCTYPE html><html><head><title>Docs &amp; Guides</title><style>body{}</style></head><body><nav><a>Home</a><a>About</a></nav>" +
		"<main><h1>Install</h1><p>Run <code>go install</code> &mdash; then:</p><pre>func main() {\n\tfmt.Println(\"hi\")\n}</pre>" +
		paragraphs(200) +
		"<script>var error = 'not text';</script></main><footer>© 2026</footer></body></html>"
	got, _ = apply(t, ctx(0, "curl", "-s", "https://x/docs"), page)
	for _, want := range []string{"[lx: HTML body", "title: Docs & Guides", "# Install", "Run go install — then:", "func main() {\n\tfmt.Println(\"hi\")\n}", "of page text not shown]"} {
		if !strings.Contains(got, want) {
			t.Errorf("html: missing %q in\n%s", want, head(got, 800))
		}
	}
	for _, not := range []string{"Home", "not text", "body{}", "©"} {
		if strings.Contains(got, not) {
			t.Errorf("html: %q should be gone", not)
		}
	}
	if n := tokens.Count(got); n > htmlTextCap+200 {
		t.Errorf("html view has %d tokens", n)
	}

	got, _ = apply(t, ctx(0, "curl", "-s", "-w", "%{http_code}", "https://x"), j)
	if got != j {
		t.Errorf("-w: body changed")
	}
}

func paragraphs(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "<p>Paragraph %d: lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor.</p>", i)
	}
	return b.String()
}

func head(s string, n int) string { return s[:min(len(s), n)] }

func TestHugeOutputsAreFast(t *testing.T) {
	var src strings.Builder
	for i := range 50000 {
		if i%40 == 0 {
			fmt.Fprintf(&src, "func handler%d(w http.ResponseWriter, r *http.Request) {\n", i)
			continue
		}
		fmt.Fprintf(&src, "\tlog.Printf(\"request %%d failed: %%v\", %d, err) // error path\n", i)
	}
	var js strings.Builder
	js.WriteString("[\n")
	for i := range 50000 {
		fmt.Fprintf(&js, "  {\"id\": %d, \"name\": \"n%d\", \"ok\": %v}", i, i, i%2 == 0)
		if i < 49999 {
			js.WriteString(",")
		}
		js.WriteString("\n")
	}
	js.WriteString("]")
	var logs strings.Builder
	for i := range 50000 {
		fmt.Fprintf(&logs, "2026-09-26T10:%02d:%02d.000Z INFO request id=%d path=/api/items status=200 dur=%dms\n", i/60%60, i%60, i, i%97)
	}
	cases := []struct {
		c  *engine.Context
		in string
	}{
		{ctx(0, "cat", "server.go"), src.String()},
		{ctx(0, "cat", "items.json"), js.String()},
		{ctx(0, "cat", "app.log"), logs.String()},
		{ctx(0, "curl", "-s", "https://x/items"), js.String()},
		{ctx(0, "curl", "-v", "https://x/items"), "< HTTP/1.1 200 OK\n<\n" + js.String()},
		{ctx(0, "jq", ".", "items.json"), js.String()},
		{ctx(0, "jq", "-r", ".[].name", "items.json"), src.String()},
		{ctx(0, "wget", "-qO-", "https://x/items"), js.String()},
		{ctx(0, "http", "https://x/items"), js.String()},
		{ctx(0, "curl", "-s", "https://x/page"), "<html><title>t</title><body>" + strings.Repeat("<p>para <b>x</b></p>\n", 50000) + "</body></html>"},
		{ctx(0, "cat", "bundle.min.js"), "!function(){" + strings.Repeat("var a=b+c;if(a){d(e,f)}", 40000) + "}();"},
	}
	for _, tc := range cases {
		start := time.Now()
		got, ok := apply(t, tc.c, tc.in)
		d := time.Since(start)
		t.Logf("%v: %v", tc.c.Argv, d)
		if d > testenv.Scale(3*time.Second) {
			t.Errorf("%v: %v", tc.c.Argv, d)
		}
		if !ok {
			t.Errorf("%v: bailed", tc.c.Argv)
			continue
		}
		if n := tokens.Count(got); n > engine.DefaultBudget {
			t.Errorf("%v: %d tokens", tc.c.Argv, n)
		}
	}
}

func TestFileView(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		switch {
		case i%100 == 1:
			fmt.Fprintf(&b, "func F%d() error {\n", i)
		case i%100 == 0:
			b.WriteString("}\n")
		default:
			fmt.Fprintf(&b, "\treturn fmt.Errorf(\"line %d\") // %s\n", i, strings.Repeat("x", 40))
		}
	}
	in := strings.TrimRight(b.String(), "\n")
	got, _ := apply(t, ctx(0, "cat", "big.go"), in)
	if !strings.Contains(got, "sed -n '") || !strings.Contains(got, "big.go") || !strings.Contains(got, "  L") {
		t.Fatalf("no marker/outline:\n%s", got[:500])
	}

	got, _ = apply(t, ctx(0, "cat", "-n", "big.go"), in)
	if !strings.Contains(got, "lx show <id> --lines") {
		t.Fatal("cat -n must not suggest sed")
	}

	got, _ = apply(t, ctx(0, "bash", "-c", "cat big.go | head -n 3000"), in)
	if !strings.Contains(got, "sed -n '") {
		t.Fatal("cat | head keeps line numbers: sed hint expected")
	}
	got, _ = apply(t, ctx(0, "bash", "-c", "cat big.go | tail -n 3000"), in)
	if strings.Contains(got, "sed -n '") || !strings.Contains(got, "lx show <id> --lines") {
		t.Fatal("cat | tail renumbers lines: lx show hint expected")
	}

	mid := strings.Split(in, "\n")
	mid[1500] = "cat: other.go: Permission denied"
	got, _ = apply(t, ctx(1, "cat", "a.go", "other.go", "b.go"), strings.Join(mid, "\n"))
	if !strings.Contains(got, "L1501: cat: other.go: Permission denied") {
		t.Fatal("diagnostic in the omitted range was dropped")
	}

	small := "package main\n\nfunc main() {}\n// TODO: handle error\n"
	if got, ok := apply(t, ctx(0, "cat", "main.go"), small); !ok || got != small {
		t.Fatal("small file changed")
	}

	got, _ = apply(t, ctx(0, "cat", "a.out"), "\x7fELF\x02\x01\x01\x00"+strings.Repeat("\x00\x01\xfe", 300))
	if !strings.HasPrefix(got, "[lx: binary data") {
		t.Fatalf("binary: %q", got)
	}
}

func TestLockfiles(t *testing.T) {
	var pb strings.Builder
	for i := range 400 {
		fmt.Fprintf(&pb, "# padding comment %d to get over the lockfile threshold\n", i)
	}
	pad := pb.String()
	blob := strings.TrimSuffix(strings.ReplaceAll(pad, "\n", " "), " ")
	cases := []struct {
		name, body, want string
	}{
		{"yarn.lock", "# yarn lockfile v1\n" + pad + "\"@babel/code-frame@^7.0.0\", \"@babel/code-frame@^7.10.4\":\n  version \"7.12.13\"\n  resolved \"https://r/x.tgz\"\n\nabbrev@1:\n  version \"1.1.1\"\n",
			"2 packages"},
		{"yarn.lock", pad + "__metadata:\n  version: 6\n\n\"@babel/core@npm:^7.0.0\":\n  version: 7.24.0\n  resolution: \"@babel/core@npm:7.24.0\"\n\n\"left-pad@npm:1.3.0\":\n  version: 1.3.0\n",
			"(yarn berry): 2 packages"},
		{"Cargo.lock", pad + "version = 3\n\n[[package]]\nname = \"serde\"\nversion = \"1.0.200\"\n\n[[package]]\nname = \"tokio\"\nversion = \"1.37.0\"\ndependencies = [\n \"bytes\",\n]\n",
			"packages: serde 1.0.200, tokio 1.37.0"},
		{"Gemfile.lock", pad + "GEM\n  remote: https://rubygems.org/\n  specs:\n    rack (3.0.9)\n    rails (7.1.3)\n      rack (>= 2.2.4)\n\nPLATFORMS\n  ruby\n",
			"packages: rack 3.0.9, rails 7.1.3"},
		{"composer.lock", `{"_readme": ["` + blob + `"], "packages": [{"name": "monolog/monolog", "version": "3.5.0"}], "packages-dev": [{"name": "phpunit/phpunit", "version": "10.5.0"}]}`,
			"packages: monolog/monolog 3.5.0, phpunit/phpunit 10.5.0"},
		{"package-lock.json", `{"name": "a", "lockfileVersion": 1, "requires": true, "dependencies": {"left-pad": {"version": "1.3.0", "integrity": "` + blob + `"}, "lodash": {"version": "4.17.21", "dependencies": {"x": {"version": "0.1.0"}}}}}`,
			"(npm, lockfileVersion 1): 3 packages"},
	}
	for _, tc := range cases {
		got, ok := apply(t, ctx(0, "cat", tc.name), tc.body)
		if !ok || !strings.HasPrefix(got, "[lx: lockfile "+tc.name) || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got\n%s", tc.name, head(got, 300))
		}
	}

	got, _ := apply(t, ctx(0, "cat", "Cargo.lock"), pad+"not toml at all\n")
	if strings.HasPrefix(got, "[lx: lockfile") {
		t.Error("summarized an unparseable lockfile")
	}

	if got, _ := apply(t, ctx(0, "head", "-n", "400", "yarn.lock"), pad); got != pad {
		t.Error("head of a lockfile changed")
	}
}

func TestJQ(t *testing.T) {
	in := "{\n  \"a\": 1\n}\njq: error (at <stdin>:3): Cannot iterate over null"
	if got, _ := apply(t, ctx(5, "jq", ".[]"), in); got != in {
		t.Errorf("small jq output changed: %q", got)
	}
	var b strings.Builder
	for i := range 3000 {
		fmt.Fprintf(&b, "{\"number\": %d, \"title\": \"issue %d fails\"}\n", i, i)
	}
	b.WriteString("jq: error (at <stdin>:3000): Cannot index number with \"x\"")
	got, _ := apply(t, ctx(5, "jq", "-c", ".[]"), b.String())
	if !strings.Contains(got, "jq: error (at <stdin>:3000): Cannot index number with \"x\"") {
		t.Errorf("jq error dropped:\n%s", got[:300])
	}
}

func TestURLExt(t *testing.T) {
	for in, want := range map[string]string{
		"https://raw.githubusercontent.com/golang/go/master/src/strings/strings.go": ".go",
		"https://x/a/b.py?raw=1": ".py",
		"http://x":               "",
		"http://":                "",
		"https://x/":             "",
	} {
		if got := urlExt([]string{"-s", in}); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}

func TestShellSplit(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{`curl -s "https://x?a=1&b=2" | jq '.[] | {a, b: .c}'`, `[[curl -s https://x?a=1&b=2] [jq .[] | {a, b: .c}]]`, true},
		{`cat a\ b.txt 2>&1 | head`, `[[cat a b.txt] [head]]`, true},
		{`curl x > out.json`, ``, false},
		{`echo "$HOME"`, ``, false},
		{`a || b`, ``, false},
		{`"unterminated`, ``, false},
	} {
		st, ok := splitShell(tc.in)
		if ok != tc.ok || ok && fmt.Sprint(st) != tc.want {
			t.Errorf("%q: %v %v", tc.in, st, ok)
		}
	}
}

func TestWgetAndHTTPie(t *testing.T) {
	got, ok := apply(t, ctx(0, "wget", "-q", "-O", "-", "https://x"), `{"a": 1}`)
	if !ok || got != `{"a": 1}` {
		t.Errorf("wget -qO-: %q", got)
	}
	got, _ = apply(t, ctx(4, "wget", "https://x"), "--2026-09-26 10:00:00--  https://x/\nResolving x (x)... 1.2.3.4\nConnecting to x (x)|1.2.3.4|:443... failed: Connection refused.\nRetrying.\n")
	for _, want := range []string{"failed: Connection refused.", "Retrying."} {
		if !strings.Contains(got, want) {
			t.Errorf("wget failure: missing %q in\n%s", want, got)
		}
	}
	got, _ = apply(t, ctx(1, "http", "https://x"), "http: error: ConnectionError: HTTPSConnectionPool(host='x', port=443): Max retries exceeded with url: / while doing a GET request to URL: https://x/")
	if !strings.HasPrefix(got, "http: error: ConnectionError") {
		t.Errorf("httpie error: %q", got)
	}
}
