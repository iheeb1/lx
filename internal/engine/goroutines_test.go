package engine

import (
	"fmt"
	"strings"
	"testing"
)

func TestGroupGoroutines(t *testing.T) {
	in := []string{"panic: test timed out after 10m0s", "\trunning tests:", "\t\tTestSlow (10m0s)", "",
		"goroutine 7 [running]:", "testing.(*M).startAlarm.func1()", "\t/usr/local/go/src/testing/testing.go:2802 +0x2cc", ""}
	for i := 0; i < 3; i++ {
		in = append(in, fmt.Sprintf("goroutine %d [IO wait]:", 20+i),
			fmt.Sprintf("internal/poll.runtime_pollWait(0x%x, 0x72)", 100+i), "\t/usr/local/go/src/runtime/netpoll.go:351 +0xa0",
			"created by net/http.(*Server).Serve in goroutine 1", "\t/usr/local/go/src/net/http/server.go:3464 +0x37c", "")
	}
	for i := 0; i < 2; i++ {
		in = append(in, fmt.Sprintf("goroutine %d [chan receive]:", 40+i),
			fmt.Sprintf("example.com/app.worker(0x%x)", i), "\tworker.go:12 +0x1c",
			"created by example.com/app.Start in goroutine 1", "\tapp.go:30 +0x44", "")
	}
	in = append(in, "FAIL\texample.com/app\t600.1s")
	out, ok := GroupGoroutines(in)
	if !ok {
		t.Fatal("not recognized")
	}
	got := strings.Join(out, "\n")
	for _, must := range []string{"panic: test timed out after 10m0s", "\t\tTestSlow (10m0s)", "goroutine 7 [running]:",
		"2 goroutines [chan receive]:", "example.com/app.worker(0x0)", "\tworker.go:12 +0x1c",
		"3 goroutines [IO wait] in library code: internal/poll.runtime_pollWait … created by net/http.(*Server).Serve",
		"FAIL\texample.com/app\t600.1s"} {
		if !strings.Contains(got, must) {
			t.Errorf("missing %q in:\n%s", must, got)
		}
	}
	if _, ok := GroupGoroutines([]string{"goroutine 1 [running]:", "main.main()", "\tmain.go:3 +0x1"}); ok {
		t.Error("single goroutine should not be a dump")
	}
	if _, ok := GroupGoroutines(nil); ok {
		t.Error("empty input")
	}
}

func TestGoFuncName(t *testing.T) {
	for in, want := range map[string]string{
		"net/http.(*conn).serve(0xc000, {0x1, 0x2})": "net/http.(*conn).serve",
		"main.main()":                          "main.main",
		"created by main.start in goroutine 1": "created by main.start in goroutine 1",
	} {
		if got := goFuncName(in); got != want {
			t.Errorf("goFuncName(%q) = %q", in, got)
		}
	}
}
