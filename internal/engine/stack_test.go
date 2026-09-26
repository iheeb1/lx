package engine

import (
	"strings"
	"testing"
)

func TestFoldStacksNode(t *testing.T) {
	in := strings.Split(`Error: boom
    at throwIt (node_modules/lib/a.js:1:1)
    at b (node_modules/lib/b.js:2:2)
    at c (node_modules/lib/c.js:3:3)
    at d (node_modules/lib/d.js:4:4)
    at handler (src/app.js:10:5)
    at e (node_modules/express/router.js:5:5)
    at f (node:internal/process/task_queues:90:21)
    at g (node:internal/timers:504:21)
    at h (node:events:630:28)
after`, "\n")
	out := strings.Join(FoldStacks(&Context{Cwd: "/w"}, in), "\n")
	want := `Error: boom
    at throwIt (node_modules/lib/a.js:1:1)
    … 2 library frames (lib)
    at d (node_modules/lib/d.js:4:4)
    at handler (src/app.js:10:5)
    at e (node_modules/express/router.js:5:5)
    … 3 library frames (node:internal, node:events)
after`
	if out != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestFoldStacksAppOnlyAndShort(t *testing.T) {
	var app []string
	for i := 0; i < 10; i++ {
		app = append(app, "    at f (src/app.js:1:1)")
	}
	if got := FoldStacks(nil, app); len(got) != len(app) {
		t.Errorf("app-only trace folded: %q", got)
	}
	short := []string{"    at a (node_modules/x/a.js:1:1)", "    at b (node_modules/x/b.js:1:1)", "    at c (node_modules/x/c.js:1:1)"}
	if got := FoldStacks(nil, short); len(got) != 3 {
		t.Errorf("short trace folded: %q", got)
	}
	if got := FoldStacks(nil, nil); len(got) != 0 {
		t.Errorf("empty: %q", got)
	}
}

func TestFoldStacksPython(t *testing.T) {
	in := []string{"Traceback (most recent call last):", `  File "app.py", line 3, in <module>`, "    main()"}
	for i := 0; i < 6; i++ {
		in = append(in, `  File "/usr/lib/python3.11/site-packages/requests/x.py", line 9, in f`, "    g()")
	}
	in = append(in, `  File "/usr/lib/python3.11/site-packages/urllib3/y.py", line 1, in h`, "    raise X()", "    ^^^^^^^^^", "ValueError: bad")
	out := FoldStacks(nil, in)
	got := strings.Join(out, "\n")
	for _, must := range []string{`File "app.py"`, "main()", "urllib3/y.py", "^^^^^^^^^", "ValueError: bad", "… 5 library frames (requests)"} {
		if !strings.Contains(got, must) {
			t.Errorf("missing %q in:\n%s", must, got)
		}
	}
}

func TestFoldStacksGoAndJava(t *testing.T) {
	var in []string
	in = append(in, "main.handler(0x1)", "\thandler.go:12 +0x1a")
	for i := 0; i < 6; i++ {
		in = append(in, "net/http.serve(0x1)", "\t/usr/local/go/src/net/http/server.go:100 +0x2b")
	}
	out := FoldStacks(nil, in)
	if len(out) != 5 || out[4] != "… 5 library frames (net/http)" {
		t.Errorf("go: %q", out)
	}
	java := []string{"java.lang.IllegalStateException: bad", "\tat com.acme.App.run(App.java:10)"}
	for i := 0; i < 8; i++ {
		java = append(java, "\tat org.junit.runners.ParentRunner.run(ParentRunner.java:413)")
	}
	java = append(java, "Caused by: java.io.IOException: disk", "\t... 12 more")
	out = FoldStacks(nil, java)
	got := strings.Join(out, "\n")
	if !strings.Contains(got, "\t… 7 library frames (org.junit.runners)") || !strings.Contains(got, "Caused by: java.io.IOException: disk") || !strings.Contains(got, "... 12 more") {
		t.Errorf("java:\n%s", got)
	}
}

func TestFoldStacksKeepsErrorFrames(t *testing.T) {
	in := []string{"    at a (node_modules/x/a.js:1:1)"}
	for i := 0; i < 3; i++ {
		in = append(in, "    at b (node_modules/x/b.js:1:1)")
	}
	in = append(in, "    at error (node_modules/x/e.js:1:1)")
	for i := 0; i < 3; i++ {
		in = append(in, "    at c (node_modules/x/c.js:1:1)")
	}
	got := strings.Join(FoldStacks(nil, in), "\n")
	if !strings.Contains(got, "at error (node_modules/x/e.js:1:1)") {
		t.Errorf("error-class frame folded:\n%s", got)
	}
}
