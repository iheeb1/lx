package infra

import (
	"fmt"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
)

func applyInfra(t *testing.T, c *engine.Context, in string) string {
	t.Helper()
	f := engine.Find(c)
	if f == nil {
		t.Fatalf("no filter for %v", c.Argv)
	}
	got, ok := f.Apply(c, in)
	if !ok {
		t.Fatalf("%s bailed", f.Name())
	}
	return got
}

func TestReviewBuildKilledShowsUnfinishedStep(t *testing.T) {
	b := []string{"#1 [internal] load build definition from Dockerfile", "#1 transferring dockerfile: 734B done", "#1 DONE 0.0s",
		"#5 [1/3] FROM docker.io/library/node:20", "#5 DONE 0.1s", "#6 [2/3] RUN npm run build"}
	for i := 0; i < 30; i++ {
		b = append(b, fmt.Sprintf("#6 %d.%03d webpack compiling module %d of 900", i, i, i*30))
	}
	b = append(b, "#6 31.002 <--- Last few GCs --->", "#6 31.003 [18:0x5b8]    41022 ms: Mark-Compact 2040.1 (2083.6) -> 2039.8 (2083.6) MB")
	in := strings.Join(b, "\n")
	got := applyInfra(t, ctx(137, "docker", "build", "."), in)
	for _, want := range []string{"#6 [2/3] RUN npm run build  [lx: did not finish]", "#6 31.002 <--- Last few GCs --->",
		"#6 31.003 [18:0x5b8]    41022 ms: Mark-Compact", "docker exited 137 (SIGKILL, often out of memory) but no step reported an error"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}

	if got := applyInfra(t, ctx(0, "docker", "build", "."), in); strings.Contains(got, "did not finish") || strings.Contains(got, "no step reported") {
		t.Errorf("exit 0 marked unfinished:\n%s", got)
	}

	failed := in + "\n#6 ERROR: process \"/bin/sh -c npm run build\" did not complete successfully: exit code: 1\n#7 [3/3] COPY . .\n#7 CANCELED"
	if got := applyInfra(t, ctx(1, "docker", "build", "."), failed); strings.Contains(got, "did not finish") {
		t.Errorf("failed build marked unfinished:\n%s", got)
	}
}

func TestReviewEventMergeKeepsAges(t *testing.T) {
	d := "Name:         x\nNamespace:    y\nEvents:\n  Type     Reason   Age                From     Message\n  ----     ------   ----               ----     -------\n" +
		"  Warning  BackOff  40m (x3 over 45m)  kubelet  Back-off restarting failed container\n" +
		"  Warning  BackOff  5m                 kubelet  Back-off restarting failed container\n" +
		"  Normal   Pulled   1m                 kubelet  ok"
	got := applyInfra(t, ctx(0, "kubectl", "describe", "pod", "x"), d)
	if !strings.Contains(got, "Back-off restarting failed container [×2 rows; others: 40m (x3 over 45m)]") {
		t.Errorf("describe merge lost the other row's age:\n%s", got)
	}
	var ev []string
	ev = append(ev, "LAST SEEN   TYPE      REASON    OBJECT    MESSAGE")
	for i := 9; i >= 1; i-- {
		ev = append(ev, fmt.Sprintf("%-11s Warning   BackOff   pod/x     Back-off restarting failed container", fmt.Sprintf("%dm", i*10)))
	}
	got = applyInfra(t, ctx(0, "kubectl", "get", "events"), strings.Join(ev, "\n"))
	if !strings.Contains(got, "[×9 rows; others: 90m, 80m, …, 20m]") {
		t.Errorf("long merge not abbreviated with first and last ages:\n%s", got)
	}

	wide := "LAST SEEN   TYPE      REASON    OBJECT    SUBOBJECT   SOURCE    MESSAGE   FIRST SEEN   COUNT   NAME\n" +
		"5m          Warning   BackOff   pod/x                 kubelet   failed    10m          3       x.17a1\n" +
		"1m          Warning   BackOff   pod/x                 kubelet   failed    4m           2       x.17a2"
	if got := applyInfra(t, ctx(0, "kubectl", "get", "events", "-o", "wide"), wide); strings.Contains(got, "rows") {
		t.Errorf("distinct events merged in -o wide:\n%s", got)
	}
}

func TestReviewKubectlGetMessagesAndRestarts(t *testing.T) {
	k := []string{"Warning: v1 ComponentStatus is deprecated in v1.19+", "NAME                         READY   STATUS             RESTARTS      AGE"}
	for i := 0; i < 80; i++ {
		rs := "0"
		if i == 70 {
			rs = "57 (2m ago)"
		}
		if i == 71 {
			rs = "3 (2d ago)"
		}
		k = append(k, fmt.Sprintf("api-%05d-abcde              1/1     %-18s %-13s 3d", i, "Running", rs))
	}
	k = append(k, `error: the server doesn't have a resource type "foo"`)
	got := applyInfra(t, ctx(1, "kubectl", "get", "pods,foo"), strings.Join(k, "\n"))
	for _, want := range []string{"Warning: v1 ComponentStatus is deprecated in v1.19+", "api-00070-abcde", `error: the server doesn't have a resource type "foo"`,
		"[lx: 80 rows by STATUS: Running 80]", "the other row follows"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "api-00071-abcde") {
		t.Errorf("a restart two days ago surfaced as recent:\n%s", got)
	}
	if !strings.HasSuffix(got, `error: the server doesn't have a resource type "foo"`) {
		t.Errorf("trailing error not printed after the table:\n%s", got)
	}
}

func TestReviewDockerTableNoteHonest(t *testing.T) {
	p := []string{"CONTAINER ID   IMAGE          COMMAND       CREATED        STATUS                     PORTS     NAMES"}
	for i := 0; i < 70; i++ {
		name := fmt.Sprintf("job-%d", i)
		if i == 65 {
			name = "cache-oom-probe"
		}
		p = append(p, fmt.Sprintf("%012x   alpine:3.20    \"true\"        3 days ago     Exited (0) 3 days ago                shop-%s", i, name))
	}
	got := applyInfra(t, ctx(0, "docker", "ps", "-a"), strings.Join(p, "\n"))
	if !strings.Contains(got, "shop-cache-oom-probe") || !strings.Contains(got, "or an error word in the row follows") {
		t.Errorf("note:\n%s", got)
	}
}

func TestReviewLegacyStepsRan(t *testing.T) {
	in := "Step 1/3 : FROM alpine\n ---> abcdef123456\nStep 2/3 : RUN make\n ---> Running in 0123456789ab\nmain.c:3:1: error: expected ';'\nThe command '/bin/sh -c make' returned a non-zero code: 2"
	got := applyInfra(t, ctx(2, "docker", "build", "."), in)
	if !strings.Contains(got, "[lx: 2 of 3 steps ran") || !strings.Contains(got, "main.c:3:1: error: expected ';'") {
		t.Errorf("legacy note:\n%s", got)
	}
}

func TestReviewConstantColumnKeepsErrorRows(t *testing.T) {
	row := func(a ...any) string { return fmt.Sprintf("%-15s%-16s%-25s%-15s%-15s%-10s%s", a...) }
	in := strings.Join([]string{
		row("CONTAINER ID", "IMAGE", "COMMAND", "CREATED", "STATUS", "PORTS", "NAMES"),
		row("a1b2c3d4e5f6", "shop/reporter", `"/app/fail-over.sh"`, "2 hours ago", "Up 2 hours", "", "shop-reporter-1"),
		row("b1b2c3d4e5f6", "shop/reporter", `"/app/fail-over.sh"`, "2 hours ago", "Up 2 hours", "", "shop-reporter-2"),
		row("c1b2c3d4e5f6", "shop/reporter", `"/app/fail-over.sh"`, "2 hours ago", "Up 2 hours", "", "shop-reporter-3"),
	}, "\n")
	c := ctx(0, "docker", "ps")
	if m := engine.MissingErrorLines(in, applyInfra(t, c, in)); len(m) > 0 {
		t.Errorf("rows rewritten: %q", m)
	}
	if r := engine.Process(c, in, engine.Options{}); r.GuardAdded != 0 {
		t.Errorf("guard re-added %d lines:\n%s", r.GuardAdded, r.Output)
	}
}

func TestReviewCellSpansMatchesRegexp(t *testing.T) {
	for _, s := range []string{"", " ", "a", "a b", "a  b", "a\tb", " a b  c   d ", "CONTAINER ID   IMAGE", "x \ty", "é  ü x", "a \x00b", "a b\t c  "} {
		var want [][2]int
		for _, loc := range cellSplitRe.FindAllStringIndex(s, -1) {
			want = append(want, [2]int{loc[0], loc[1]})
		}
		if got := cellSpans(s); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("cellSpans(%q) = %v, want %v", s, got, want)
		}
	}
}

func FuzzCellSpans(f *testing.F) {
	f.Add("CONTAINER ID   IMAGE   x y")
	f.Fuzz(func(t *testing.T, s string) {
		var want [][2]int
		for _, loc := range cellSplitRe.FindAllStringIndex(s, -1) {
			want = append(want, [2]int{loc[0], loc[1]})
		}
		if got := cellSpans(s); fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("cellSpans(%q) = %v, want %v", s, got, want)
		}
	})
}

func TestReviewDescribeKeepsBlocksTheEventsNeed(t *testing.T) {
	base := "Name:         api-0\nNamespace:    shop\nStatus:       Pending\n" +
		"Volumes:\n  certs:\n    Type:        Secret (a volume populated by a Secret)\n    SecretName:  api-tls\n" +
		"QoS Class:    Burstable\nNode-Selectors:  disktype=ssd\nTolerations:  node.kubernetes.io/not-ready:NoExecute op=Exists for 300s\n" +
		"Events:\n  Type     Reason       Age   From     Message\n  ----     ------       ----  ----     -------\n"
	mount := base + "  Warning  FailedMount  1m    kubelet  MountVolume.SetUp failed for volume \"certs\" : secret \"api-tls\" not found"
	got := applyInfra(t, ctx(0, "kubectl", "describe", "pod", "api-0"), mount)
	if !strings.Contains(got, "SecretName:  api-tls") || strings.Contains(got, "Node-Selectors:  disktype=ssd") {
		t.Errorf("mount failure: Volumes dropped or Node-Selectors kept:\n%s", got)
	}
	sched := base + "  Warning  FailedScheduling  1m  default-scheduler  0/3 nodes are available: 3 node(s) didn't match Pod's node affinity/selector."
	got = applyInfra(t, ctx(0, "kubectl", "describe", "pod", "api-0"), sched)
	if !strings.Contains(got, "Node-Selectors:  disktype=ssd") || !strings.Contains(got, "Tolerations:") || strings.Contains(got, "SecretName") {
		t.Errorf("scheduling failure: selectors dropped or Volumes kept:\n%s", got)
	}
}
