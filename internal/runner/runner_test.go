//go:build unix

package runner

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestHelperProcess is the child the other tests run: it prints
// HELPER_LINES lines, then HELPER_PROMPT (no newline), sleeps HELPER_SLEEP,
// prints HELPER_AFTER more lines and exits HELPER_EXIT. HELPER_TERM=ignore
// records SIGTERM ("helper: got SIGTERM") and exits HELPER_TERM_EXIT
// HELPER_TERM_DELAY later; otherwise SIGTERM kills it. HELPER_BUSY prints
// "Compiling N:" pieces every 100ms instead of sleeping. HELPER_KILL9 kills
// itself with SIGKILL.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("LX_RUNNER_HELPER") != "1" {
		return
	}
	env := func(k string) string { return os.Getenv(k) }
	atoi := func(k string) int { n, _ := strconv.Atoi(env(k)); return n }
	dur := func(k string) time.Duration { d, _ := time.ParseDuration(env(k)); return d }
	if env("HELPER_TERM") == "ignore" {
		ch := make(chan os.Signal, 4)
		signal.Notify(ch, syscall.SIGTERM)
		go func() {
			<-ch
			fmt.Println("helper: got SIGTERM")
			time.Sleep(dur("HELPER_TERM_DELAY"))
			fmt.Println("helper: stopping")
			os.Exit(atoi("HELPER_TERM_EXIT"))
		}()
	}
	for i := 1; i <= atoi("HELPER_LINES"); i++ {
		fmt.Printf("line %d\n", i)
	}
	if p := env("HELPER_PROMPT"); p != "" {
		fmt.Print(p)
	}
	if n := atoi("HELPER_BUSY"); n > 0 {
		for i := 0; i < n; i++ {
			fmt.Printf("Compiling %d:", i)
			time.Sleep(100 * time.Millisecond)
			fmt.Print(" ok\n")
		}
	}
	time.Sleep(dur("HELPER_SLEEP"))
	for i := 1; i <= atoi("HELPER_AFTER"); i++ {
		fmt.Printf("after %d\n", i)
	}
	if env("HELPER_KILL9") == "1" {
		syscall.Kill(os.Getpid(), syscall.SIGKILL)
		time.Sleep(time.Second)
	}
	os.Exit(atoi("HELPER_EXIT"))
}

// helper sets the child's behavior and returns its argv.
func helper(t *testing.T, kv ...string) []string {
	t.Helper()
	t.Setenv("LX_RUNNER_HELPER", "1")
	for _, k := range []string{"HELPER_LINES", "HELPER_PROMPT", "HELPER_SLEEP", "HELPER_AFTER", "HELPER_EXIT",
		"HELPER_TERM", "HELPER_TERM_DELAY", "HELPER_TERM_EXIT", "HELPER_BUSY", "HELPER_KILL9"} {
		t.Setenv(k, "")
	}
	for i := 0; i+1 < len(kv); i += 2 {
		t.Setenv(kv[i], kv[i+1])
	}
	return []string{os.Args[0], "-test.run=^TestHelperProcess$"}
}

// fakeSignals replaces the OS signal source; send delivers one signal.
func fakeSignals(t *testing.T) (send func(os.Signal)) {
	t.Helper()
	var mu sync.Mutex
	var ch chan<- os.Signal
	ready := make(chan struct{})
	old := notifySignals
	notifySignals = func(c chan<- os.Signal) func() {
		mu.Lock()
		ch = c
		mu.Unlock()
		close(ready)
		return func() {}
	}
	t.Cleanup(func() { notifySignals = old })
	return func(s os.Signal) {
		<-ready
		mu.Lock()
		defer mu.Unlock()
		ch <- s
	}
}

// watchCapture exposes the live capture; waitFor polls it.
func watchCapture(t *testing.T) (waitFor func(substr string)) {
	t.Helper()
	var mu sync.Mutex
	var c *capture
	testHookCapture = func(cc *capture) { mu.Lock(); c = cc; mu.Unlock() }
	t.Cleanup(func() { testHookCapture = nil })
	return func(substr string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			cc := c
			mu.Unlock()
			if cc != nil && strings.Contains(cc.String(), substr) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("capture never contained %q", substr)
	}
}

func lines(prefix string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%s %d\n", prefix, i)
	}
	return b.String()
}

// syncBuf is a spool the runner writes while the test may read.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestHeartbeatSpoolsEveryLaterByte(t *testing.T) {
	argv := helper(t, "HELPER_LINES", "50", "HELPER_SLEEP", "800ms", "HELPER_AFTER", "30")
	calls := 0
	var sofar string
	spool := &syncBuf{}
	res := RunWith(argv, Options{
		Heartbeat: 200 * time.Millisecond,
		OnHeartbeat: func(elapsed time.Duration, b []byte) io.Writer {
			calls++
			sofar = string(b)
			if elapsed < 200*time.Millisecond {
				t.Errorf("heartbeat after %v", elapsed)
			}
			spool.Write(b) // what live.go does: seed the spool with sofar
			return spool
		},
	})
	if calls != 1 {
		t.Fatalf("OnHeartbeat called %d times, want 1", calls)
	}
	if sofar != lines("line", 50) {
		t.Fatalf("sofar = %q", sofar)
	}
	want := lines("line", 50) + lines("after", 30)
	if res.Output != want || res.ExitCode != 0 {
		t.Fatalf("output %q exit %d", res.Output, res.ExitCode)
	}
	if spool.String() != want {
		t.Fatalf("spool = %q, want every byte in order", spool.String())
	}
	if res.Interrupted != "" {
		t.Fatalf("Interrupted = %q without a signal", res.Interrupted)
	}
}

func TestShortRunNoCallbacks(t *testing.T) {
	argv := helper(t, "HELPER_LINES", "3", "HELPER_PROMPT", "Continue? ")
	called := ""
	res := RunWith(argv, Options{
		Heartbeat:   2 * time.Second,
		OnHeartbeat: func(time.Duration, []byte) io.Writer { called += "heartbeat "; return nil },
		PromptIdle:  2 * time.Second,
		OnPrompt:    func(string) { called += "prompt " },
		OnInterrupt: func(os.Signal, time.Duration, []byte) io.Writer { called += "interrupt "; return nil },
	})
	if called != "" {
		t.Fatalf("callbacks on a fast run: %s", called)
	}
	if res.Output != lines("line", 3)+"Continue? " {
		t.Fatalf("output %q", res.Output)
	}
}

func TestTermForwardedExit143(t *testing.T) {
	argv := helper(t, "HELPER_LINES", "20", "HELPER_SLEEP", "10s")
	send := fakeSignals(t)
	waitFor := watchCapture(t)
	go func() {
		waitFor("line 20\n")
		send(syscall.SIGTERM)
	}()
	interrupted := 0
	res := RunWith(argv, Options{OnInterrupt: func(os.Signal, time.Duration, []byte) io.Writer { interrupted++; return nil }})
	if res.Interrupted != "SIGTERM" || res.ExitCode != 143 {
		t.Fatalf("Interrupted %q exit %d", res.Interrupted, res.ExitCode)
	}
	if res.Output != lines("line", 20) {
		t.Fatalf("output %q", res.Output)
	}
	if res.Duration > 5*time.Second {
		t.Fatalf("took %v: the signal was not forwarded", res.Duration)
	}
	if interrupted != 0 {
		t.Fatal("OnInterrupt called although the command stopped within the grace period")
	}
}

func TestGraceCallsOnInterruptWithoutKilling(t *testing.T) {
	argv := helper(t, "HELPER_LINES", "20", "HELPER_SLEEP", "30s",
		"HELPER_TERM", "ignore", "HELPER_TERM_DELAY", "1s", "HELPER_TERM_EXIT", "3")
	send := fakeSignals(t)
	waitFor := watchCapture(t)
	go func() {
		waitFor("line 20\n")
		send(syscall.SIGTERM)
	}()
	var (
		calls int
		sig   os.Signal
		sofar string
		at    time.Duration
	)
	spool := &syncBuf{}
	start := time.Now()
	res := RunWith(argv, Options{
		Grace: 200 * time.Millisecond,
		OnInterrupt: func(s os.Signal, elapsed time.Duration, b []byte) io.Writer {
			calls++
			sig, sofar, at = s, string(b), time.Since(start)
			spool.Write(b)
			return spool
		},
	})
	if calls != 1 || sig != syscall.SIGTERM {
		t.Fatalf("OnInterrupt called %d times (sig %v)", calls, sig)
	}
	if !strings.HasPrefix(sofar, lines("line", 20)) {
		t.Fatalf("sofar = %q", sofar)
	}
	if res.ExitCode != 3 {
		t.Fatalf("exit %d: the command must not be killed, its own status is 3", res.ExitCode)
	}
	if !strings.Contains(res.Output, "helper: got SIGTERM\n") || !strings.HasSuffix(res.Output, "helper: stopping\n") {
		t.Fatalf("output %q", res.Output)
	}
	if spool.String() != res.Output {
		t.Fatalf("spool %q != output %q", spool.String(), res.Output)
	}
	if res.Interrupted != "SIGTERM" {
		t.Fatalf("Interrupted = %q", res.Interrupted)
	}
	if at >= res.Duration {
		t.Fatalf("partial view at %v, not before the exit at %v", at, res.Duration)
	}
}

func TestLaterSignalsForwardedAtOnce(t *testing.T) {
	argv := helper(t, "HELPER_LINES", "5", "HELPER_SLEEP", "30s",
		"HELPER_TERM", "ignore", "HELPER_TERM_DELAY", "30s")
	send := fakeSignals(t)
	waitFor := watchCapture(t)
	go func() {
		waitFor("line 5\n")
		send(syscall.SIGTERM)
		waitFor("got SIGTERM")
		send(os.Interrupt) // the helper does not handle SIGINT: it dies
	}()
	res := RunWith(argv, Options{Grace: 10 * time.Second})
	if res.ExitCode != 130 || res.Interrupted != "SIGTERM" {
		t.Fatalf("exit %d, Interrupted %q", res.ExitCode, res.Interrupted)
	}
	if res.Duration > 8*time.Second {
		t.Fatalf("took %v", res.Duration)
	}
}

func TestPromptDetected(t *testing.T) {
	argv := helper(t, "HELPER_LINES", "2", "HELPER_PROMPT", "\x1b[1mProceed? [y/N]\x1b[0m ", "HELPER_SLEEP", "1100ms")
	var mu sync.Mutex
	var c *capture
	testHookCapture = func(cc *capture) { mu.Lock(); c = cc; mu.Unlock() }
	t.Cleanup(func() { testHookCapture = nil })
	idle := 300 * time.Millisecond
	var got []string
	var latency time.Duration
	RunWith(argv, Options{PromptIdle: idle, OnPrompt: func(line string) {
		got = append(got, line)
		mu.Lock()
		cc := c
		mu.Unlock()
		cc.mu.Lock()
		latency = time.Since(cc.last)
		cc.mu.Unlock()
	}})
	if len(got) != 1 || got[0] != "Proceed? [y/N] " {
		t.Fatalf("OnPrompt calls = %q", got)
	}
	if latency < idle || latency > idle+600*time.Millisecond {
		t.Fatalf("prompt reported %v after the last output (idle %v)", latency, idle)
	}
}

func TestNoPromptOnBusyOutput(t *testing.T) {
	argv := helper(t, "HELPER_BUSY", "6", "HELPER_SLEEP", "400ms")
	var got []string
	res := RunWith(argv, Options{PromptIdle: 300 * time.Millisecond, OnPrompt: func(line string) { got = append(got, line) }})
	if len(got) != 0 {
		t.Fatalf("OnPrompt on busy output: %q", got)
	}
	if !strings.Contains(res.Output, "Compiling 5: ok\n") {
		t.Fatalf("output %q", res.Output)
	}
}

func TestExitCodesUnchanged(t *testing.T) {
	cases := []struct {
		kv   []string
		want int
	}{
		{[]string{"HELPER_EXIT", "0"}, 0},
		{[]string{"HELPER_EXIT", "1"}, 1},
		{[]string{"HELPER_EXIT", "2"}, 2},
		{[]string{"HELPER_KILL9", "1"}, 137},
	}
	for _, c := range cases {
		argv := helper(t, append([]string{"HELPER_LINES", "3"}, c.kv...)...)
		live := RunWith(argv, Options{
			Heartbeat: time.Hour, OnHeartbeat: func(time.Duration, []byte) io.Writer { return nil },
			PromptIdle: time.Hour, OnPrompt: func(string) {},
			OnInterrupt: func(os.Signal, time.Duration, []byte) io.Writer { return nil },
		})
		plain := Run(argv, nil)
		if live.ExitCode != c.want || plain.ExitCode != c.want {
			t.Errorf("%v: exit %d (RunWith) / %d (Run), want %d", c.kv, live.ExitCode, plain.ExitCode, c.want)
		}
		if live.Output != lines("line", 3) {
			t.Errorf("%v: output %q", c.kv, live.Output)
		}
	}
}

func TestNotFound(t *testing.T) {
	res := RunWith([]string{"lx-no-such-command-xyz"}, Options{Heartbeat: time.Millisecond, OnHeartbeat: func(time.Duration, []byte) io.Writer { return nil }})
	if !res.NotFound || res.ExitCode != 127 {
		t.Fatalf("%+v", res)
	}
}

type failingWriter struct{ after int }

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.after -= len(p); w.after < 0 {
		return 0, errors.New("disk full")
	}
	return len(p), nil
}

// A broken spool or a panicking callback never changes what lx captures or
// the exit status.
func TestCallbackFailuresAreContained(t *testing.T) {
	argv := helper(t, "HELPER_LINES", "10", "HELPER_SLEEP", "500ms", "HELPER_AFTER", "400", "HELPER_EXIT", "4")
	res := RunWith(argv, Options{
		Heartbeat:   100 * time.Millisecond,
		OnHeartbeat: func(time.Duration, []byte) io.Writer { return &failingWriter{after: 50} },
	})
	if res.ExitCode != 4 || res.Output != lines("line", 10)+lines("after", 400) {
		t.Fatalf("broken spool changed the run: exit %d, %d bytes", res.ExitCode, len(res.Output))
	}
	res = RunWith(argv, Options{
		Heartbeat:   100 * time.Millisecond,
		OnHeartbeat: func(time.Duration, []byte) io.Writer { panic("bug") },
		PromptIdle:  50 * time.Millisecond,
	})
	if res.ExitCode != 4 || res.Output != lines("line", 10)+lines("after", 400) {
		t.Fatalf("panicking callback changed the run: exit %d, %d bytes", res.ExitCode, len(res.Output))
	}
}

func TestPromptLine(t *testing.T) {
	yes := map[string]string{
		"Proceed? [y/N] ":                     "Proceed? [y/N] ",
		"downloading...\nContinue (yes/no)? ": "Continue (yes/no)? ",
		"Password:":                           "Password:",
		"Enter passphrase for key 'id':":      "Enter passphrase for key 'id':",
		"Overwrite file.txt [Y/n]":            "Overwrite file.txt [Y/n]",
		"> ":                                  "> ",
		"Username: ":                          "Username: ",
		"\x1b[32m?\x1b[0m Pick a template ›\x1b[0m\r\x1b[2K? Which one? ": "? Which one? ",
		"line\nEnter your password":                                       "Enter your password",
		// npx, npm init, cp -i, multi-choice and "press enter" prompts.
		"Need to install the following packages:\n  cowsay@1.6.0\nOk to proceed? (y) ": "Ok to proceed? (y) ",
		"package name: (lx) ":             "package name: (lx) ",
		"overwrite x.txt? (y/n [n]) ":     "overwrite x.txt? (y/n [n]) ",
		"Overwrite? [y/N/a/q] ":           "Overwrite? [y/N/a/q] ",
		"Press ENTER to continue...":      "Press ENTER to continue...",
		"Press any key to continue . . .": "Press any key to continue . . .",
	}
	for in, want := range yes {
		got, ok := PromptLine(in)
		if !ok || got != want {
			t.Errorf("PromptLine(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	no := []string{
		"",
		"all done\n",
		"Proceed? [y/N]\n",
		"Compiling foo",
		"   \t ",
		"progress 45%\r",
		strings.Repeat("x", 200) + ":",
		"bin\x00ary:",
		"progress (3/10)",
		"done in 0.3s (cached)",
		"Pressed keys: 3 so far",
		"see the log: (" + strings.Repeat("z", 60) + ")", // too long for a default
	}
	for _, in := range no {
		if got, ok := PromptLine(in); ok {
			t.Errorf("PromptLine(%q) = %q, want no prompt", in, got)
		}
	}
	// The detector's window: a line that began before it is too long.
	if _, ok := promptLine(strings.Repeat("y", 300)+"?", false); ok {
		t.Error("a line longer than the window counted as a prompt")
	}
}
