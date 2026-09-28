package infra

import (
	"fmt"
	"github.com/iheeb1/lx/internal/testenv"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/tokens"
)

func ctx(exit int, argv ...string) *engine.Context {
	return &engine.Context{Argv: argv, Exit: exit, Cwd: "/home/user/src/shop", Home: "/home/user"}
}

func find(argv ...string) string {
	if f := engine.Find(ctx(0, argv...)); f != nil {
		return f.Name()
	}
	return ""
}

func TestMatch(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{[]string{"docker", "ps"}, "docker-table"},
		{[]string{"docker", "ps", "-a", "--no-trunc"}, "docker-table"},
		{[]string{"/usr/local/bin/docker", "--context", "prod", "container", "ls"}, "docker-table"},
		{[]string{"docker", "-H", "tcp://h:2375", "images"}, "docker-table"},
		{[]string{"docker", "image", "ls"}, "docker-table"},
		{[]string{"docker", "compose", "-f", "dev.yml", "ps"}, "docker-table"},
		{[]string{"docker-compose", "ps"}, "docker-table"},
		{[]string{"docker", "ps", "-q"}, ""},
		{[]string{"docker", "ps", "--format", "{{.ID}}"}, ""},
		{[]string{"docker", "images", "--format=json"}, ""},
		{[]string{"docker", "build", "-f", "Dockerfile.dev", "-t", "x", "."}, "docker-build"},
		{[]string{"docker", "buildx", "build", "--platform", "linux/amd64", "."}, "docker-build"},
		{[]string{"docker", "compose", "build", "api"}, "docker-build"},
		{[]string{"docker", "pull", "node:20"}, "docker-pull"},
		{[]string{"docker", "compose", "pull"}, "docker-pull"},
		{[]string{"docker", "logs", "--since", "1h", "api"}, "logs"},
		{[]string{"docker", "compose", "-f", "dev.yml", "logs", "web"}, "logs"},
		{[]string{"kubectl", "logs", "deploy/api", "-c", "api"}, "logs"},
		{[]string{"kubectl", "-n", "shop", "logs", "api-0", "--previous"}, "logs"},
		{[]string{"journalctl", "-u", "nginx"}, "logs"},
		{[]string{"journalctl", "-o", "json"}, ""},
		{[]string{"journalctl", "--output=json-pretty"}, ""},
		{[]string{"kubectl", "get", "pods"}, "kubectl-get"},
		{[]string{"kubectl", "--context=prod", "-n", "shop", "get", "pods", "-o", "wide"}, "kubectl-get"},
		{[]string{"kubectl", "get", "pods", "-o", "json"}, ""},
		{[]string{"kubectl", "get", "pods", "-ojsonpath={.items}"}, ""},
		{[]string{"kubectl", "top", "pods"}, "kubectl-get"},
		{[]string{"kubectl", "get", "events", "-A"}, "kubectl-events"},
		{[]string{"kubectl", "get", "ev"}, "kubectl-events"},
		{[]string{"kubectl", "events", "--for", "pod/api-0"}, "kubectl-events"},
		{[]string{"kubectl", "describe", "pod", "api-0"}, "kubectl-describe"},
		{[]string{"oc", "describe", "node", "n1"}, "kubectl-describe"},
		{[]string{"kubectl", "apply", "-f", "x.yaml"}, ""},
		{[]string{"docker", "run", "--rm", "alpine"}, ""},
		{[]string{"docker", "inspect", "api"}, ""},
	}
	for _, tc := range cases {
		if got := find(tc.argv...); got != tc.want {
			t.Errorf("%q: %q, want %q", tc.argv, got, tc.want)
		}
	}
}

func TestStream(t *testing.T) {
	yes := [][]string{
		{"docker", "logs", "-f", "api"}, {"docker", "logs", "--follow", "api"}, {"docker", "logs", "-tf", "api"},
		{"docker", "compose", "logs", "-f"}, {"kubectl", "logs", "-f", "api-0"}, {"journalctl", "-fu", "nginx"},
		{"kubectl", "get", "pods", "-w"}, {"kubectl", "get", "events", "--watch"},
	}
	no := [][]string{
		{"docker", "logs", "api"}, {"docker", "compose", "-f", "dev.yml", "logs", "web"}, {"docker", "logs", "-n", "100", "api"},
		{"journalctl", "-u", "nginx"}, {"kubectl", "get", "pods"}, {"kubectl", "logs", "--follow=false", "api-0"},
	}
	check := func(argv []string, want bool) {
		c := ctx(0, argv...)
		f := engine.Find(c)
		s, ok := f.(engine.Streamer)
		if got := ok && s.Stream(c); got != want {
			t.Errorf("%q: stream=%v, want %v", argv, got, want)
		}
	}
	for _, a := range yes {
		check(a, true)
	}
	for _, a := range no {
		check(a, false)
	}
}

func TestBailsOnUnknown(t *testing.T) {
	cases := []struct {
		argv []string
		in   string
	}{
		{[]string{"docker", "ps"}, "Cannot connect to the Docker daemon at unix:///var/run/docker.sock. Is the docker daemon running?"},
		{[]string{"docker", "ps"}, "CONTAINER ID   IMAGE\n  x  double  spaced  cells  everywhere  y"},
		{[]string{"docker", "build", "."}, "[+] Building 12.3s (8/10)\n => [internal] load build definition from Dockerfile  0.0s\n => CACHED [2/6] WORKDIR /app  0.0s"},
		{[]string{"kubectl", "get", "pods"}, "No resources found in default namespace."},
		{[]string{"kubectl", "describe", "pod", "x"}, "Error from server (NotFound): pods \"x\" not found"},
		{[]string{"kubectl", "get", "events"}, "Nom   Type\nfoo   bar"},
		{[]string{"docker", "images"}, ""},
	}
	for _, tc := range cases {
		c := ctx(1, tc.argv...)
		if got, ok := engine.Find(c).Apply(c, tc.in); ok && got != tc.in {
			t.Errorf("%q: must bail or leave the text, got %q", tc.argv, got)
		}
	}
}

func TestEmptyAndSingleLine(t *testing.T) {
	for _, argv := range [][]string{{"docker", "ps"}, {"docker", "build", "."}, {"docker", "pull", "x"}, {"docker", "logs", "x"},
		{"kubectl", "get", "pods"}, {"kubectl", "describe", "pod", "x"}, {"kubectl", "events"}, {"journalctl"}} {
		for _, in := range []string{"", "x", "#1 [internal] load build definition", "NAME   READY", "Name:  x", "-- No entries --"} {
			c := ctx(0, argv...)
			got, ok := engine.Find(c).Apply(c, in)
			if ok && strings.TrimSpace(in) != "" && strings.TrimSpace(got) == "" {
				t.Errorf("%q on %q: emptied", argv, in)
			}
		}
	}
}

func TestFailedBuildLooksDone(t *testing.T) {
	in := strings.Join([]string{
		"#1 [internal] load build definition from Dockerfile", "#1 DONE 0.0s",
		"#2 [1/2] FROM docker.io/library/alpine:3.20", "#2 CACHED",
		"#3 [2/2] RUN echo ok", "#3 0.201 ok", "#3 DONE 0.2s",
		"#4 exporting to image", "#4 exporting layers 0.1s done", "#4 pushing layers",
		"#4 ERROR: failed to push registry.example.com/x:1: unauthorized: authentication required",
		"------", " > exporting to image:", "------",
		"ERROR: failed to solve: failed to push registry.example.com/x:1: unauthorized: authentication required",
	}, "\n")
	c := ctx(1, "docker", "build", "--push", "-t", "registry.example.com/x:1", ".")
	got, ok := engine.Find(c).Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	if m := engine.MissingErrorLines(in, got); len(m) > 0 {
		t.Errorf("missing %q in\n%s", m, got)
	}
	if regexp.MustCompile(`(?i)success|succeeded|built`).MatchString(got) {
		t.Errorf("pass-like text:\n%s", got)
	}
}

func TestTableParsing(t *testing.T) {
	cols, ok := parseHeader("CONTAINER ID   IMAGE     COMMAND   CREATED   STATUS    PORTS     NAMES")
	if !ok || len(cols) != 7 || cols[0].name != "CONTAINER ID" {
		t.Fatalf("%v %v", cols, ok)
	}

	tb, ok := parseTable([]string{
		"IMAGE          ID             DISK USAGE   EXTRA",
		"alpine:3.20    d9e853e87e55       13.7MB   U",
		"big:1          aaaaaaaaaaaa   1234.5678MB",
	})
	if !ok || tb.cells[0][2] != "13.7MB" || tb.cells[1][2] != "1234.5678MB" || tb.cells[1][3] != "" || tb.cells[0][3] != "U" {
		t.Fatalf("%q %v", tb.cells, ok)
	}

	tb, ok = parseTable([]string{"LAST SEEN   TYPE      REASON    OBJECT    MESSAGE", "5m          Warning   BackOff   pod/x     a  b   c"})
	if !ok || tb.cells[0][4] != "a  b   c" {
		t.Fatalf("%q %v", tb.cells, ok)
	}
}

func TestHugeOutputsAreFast(t *testing.T) {
	var logs, build, pods, ps, desc, evs strings.Builder
	for i := range 50000 {
		fmt.Fprintf(&logs, "2026-09-26T10:%02d:%02d.%03dZ INFO GET /api/items/%d 200 %dms\n", i/60%60, i%60, i%1000, i, i%97)
		if i%5000 == 17 {
			fmt.Fprintf(&logs, "2026-09-26T10:00:00.000Z ERROR request %d failed: connection reset by peer\n", i)
		}
	}
	build.WriteString("#1 [1/2] FROM alpine\n#1 DONE 0.1s\n#2 [2/2] RUN make\n")
	for i := range 50000 {
		fmt.Fprintf(&build, "#2 %d.%03d cc -c src/file%d.c -o obj/file%d.o\n", i/1000, i%1000, i, i)
	}
	build.WriteString("#2 49.999 src/file7.c:12:5: error: expected ';'\n#2 ERROR: process \"/bin/sh -c make\" did not complete successfully: exit code: 2\nERROR: failed to solve: process \"/bin/sh -c make\" did not complete successfully: exit code: 2\n")
	pods.WriteString("NAME                READY   STATUS             RESTARTS   AGE\n")
	ps.WriteString("CONTAINER ID   IMAGE          COMMAND       CREATED       STATUS                     PORTS     NAMES\n")
	evs.WriteString("LAST SEEN   TYPE      REASON      OBJECT          MESSAGE\n")
	desc.WriteString("Name:   big\nNamespace:  x\nEvents:\n  Type     Reason   Age    From     Message\n  ----     ------   ----   ----     -------\n")
	for i := range 50000 {
		st := "Running"
		if i%977 == 0 {
			st = "CrashLoopBackOff"
		}
		fmt.Fprintf(&pods, "api-%010d      1/1     %-16s   0          3d\n", i, st)
		fmt.Fprintf(&ps, "%012x   shop/api:1     \"/app/api\"    3 days ago    Exited (%d) 2 days ago     %-8s  run-%d\n", i, i%3, "", i)
		fmt.Fprintf(&evs, "%-12s%-10s%-12s%-16sBack-off restarting failed container api\n", fmt.Sprintf("%dm", i%90), "Warning", "BackOff", fmt.Sprintf("pod/api-%d", i%50))
		fmt.Fprintf(&desc, "  Warning  BackOff  %-7skubelet  Back-off restarting failed container api\n", fmt.Sprintf("%dm", i%90))
	}
	cases := []struct {
		c  *engine.Context
		in string
	}{
		{ctx(0, "docker", "logs", "api"), logs.String()},
		{ctx(0, "journalctl", "-u", "api"), logs.String()},
		{ctx(2, "docker", "build", "."), build.String()},
		{ctx(0, "kubectl", "get", "pods"), pods.String()},
		{ctx(0, "docker", "ps", "-a"), ps.String()},
		{ctx(0, "kubectl", "events"), evs.String()},
		{ctx(0, "kubectl", "describe", "pod", "big"), desc.String()},
	}
	for _, tc := range cases {
		start := time.Now()
		c := tc.c
		got, ok := engine.Find(c).Apply(c, tc.in)
		d := time.Since(start)
		t.Logf("%v: %v, %d tokens", c.Argv, d, tokens.Count(got))
		if d > testenv.Scale(3*time.Second) {
			t.Errorf("%v took %v", c.Argv, d)
		}
		if !ok {
			t.Errorf("%v bailed", c.Argv)
		}
	}
}

func TestPull(t *testing.T) {
	in := "Using default tag: latest\nlatest: Pulling from library/x\n" +
		"aaaaaaaaaaaa: Pulling fs layer\nbbbbbbbbbbbb: Already exists\naaaaaaaaaaaa: Retrying in 5 seconds\n" +
		"aaaaaaaaaaaa: Download complete\naaaaaaaaaaaa: Pull complete\nDigest: sha256:abc\nStatus: Downloaded newer image for x:latest"
	c := ctx(0, "docker", "pull", "x")
	got, _ := engine.Find(c).Apply(c, in)
	want := "Using default tag: latest\nlatest: Pulling from library/x\n[lx: 2 layers, 1 already existed; per-layer progress not shown]\naaaaaaaaaaaa: Retrying in 5 seconds\nDigest: sha256:abc\nStatus: Downloaded newer image for x:latest"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestLogsFallback(t *testing.T) {

	var b strings.Builder
	for i := range 30 {
		fmt.Fprintf(&b, "processed batch %d of 30\n", i+10)
	}
	b.WriteString("Traceback (most recent call last):\n  File \"/app/main.py\", line 12, in <module>\n    run()\nValueError: bad batch\n")
	c := ctx(1, "docker", "logs", "job")
	got, ok := engine.Find(c).Apply(c, b.String())
	if !ok || !strings.Contains(got, "ValueError: bad batch") || !strings.Contains(got, "similar lines") {
		t.Errorf("got\n%s", got)
	}
}

func TestDescribeKeepsErrorBlocks(t *testing.T) {
	in := strings.Join([]string{
		"Name:         api-0",
		"Namespace:    shop",
		"Status:       Pending",
		"Volumes:",
		"  certs:",
		"    Type:        Secret (a volume populated by a Secret)",
		"    SecretName:  api-tls",
		"    Optional:    false",
		"  cache:",
		"    Type:       EmptyDir (a temporary directory that shares a pod's lifetime)",
		"QoS Class:    BestEffort",
		"Tolerations:  node.kubernetes.io/not-ready:NoExecute op=Exists for 300s",
		"Events:",
		"  Type     Reason       Age                From     Message",
		"  ----     ------       ----               ----     -------",
		"  Warning  FailedMount  2m (x5 over 10m)   kubelet  MountVolume.SetUp failed for volume \"certs\" : secret \"api-tls\" not found",
		"  Warning  FailedMount  30s (x2 over 1m)   kubelet  MountVolume.SetUp failed for volume \"certs\" : secret \"api-tls\" not found",
	}, "\n")
	c := ctx(0, "kubectl", "describe", "pod", "api-0")
	got, ok := engine.Find(c).Apply(c, in)
	if !ok {
		t.Fatal("bailed")
	}
	for _, want := range []string{"Status:       Pending", `30s (x2 over 1m)   kubelet  MountVolume.SetUp failed for volume "certs" : secret "api-tls" not found [×2 rows; others: 2m (x5 over 10m)]`,
		"SecretName:  api-tls",
		"[lx: not shown: QoS Class, Tolerations]"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}

	noMount := strings.ReplaceAll(strings.ReplaceAll(in, "FailedMount", "BackOff    "), "MountVolume.SetUp failed for volume \"certs\" : secret \"api-tls\" not found", "Back-off restarting failed container")
	if got, _ := engine.Find(c).Apply(c, noMount); strings.Contains(got, "SecretName:  api-tls") {
		t.Errorf("Volumes kept without a mount failure:\n%s", got)
	}
	in2 := strings.Replace(noMount, "    Optional:    false", "    Optional:    false (error: secret not found)", 1)
	got, _ = engine.Find(c).Apply(c, in2)
	if !strings.Contains(got, "SecretName:  api-tls") {
		t.Errorf("Volumes block with an error line was dropped:\n%s", got)
	}
}
