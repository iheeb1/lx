package leak

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

func worker(ch chan int) {
	<-ch
}

func TestStartsWorker(t *testing.T) {
	go worker(make(chan int))
}

func TestBroken(t *testing.T) {
	t.Fatalf("broken: status = %q, want %q", "down", "up")
}

func TestLater(t *testing.T) {
	fmt.Println("later test chatter")
}

func TestMain(m *testing.M) {
	code := m.Run()
	time.Sleep(10 * time.Millisecond)
	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, true)
	var leaked []string
	for _, g := range strings.Split(string(buf[:n]), "\n\n") {
		if strings.Contains(g, "leak.worker") {
			leaked = append(leaked, g)
		}
	}
	if len(leaked) > 0 {
		fmt.Println("leakcheck: found unexpected goroutines:")
		for _, g := range leaked {
			fmt.Println(g)
		}
		code = 1
	}
	os.Exit(code)
}
