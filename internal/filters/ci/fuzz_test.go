package ci_test

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/filters/ci"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/testenv"
	"github.com/iheeb1/lx/internal/textutil"
)

var fuzzArgv = [][]string{
	{"gh", "run", "view", "1", "--log-failed"},
	{"gh", "run", "view", "1", "--log"},
	{"gh", "api", "repos/o/r/actions/jobs/1/logs"},
	{"gh", "run", "view", "1"},
	{"gh", "run", "watch", "1"},
	{"gh", "pr", "checks"},
	{"turbo", "run", "test"},
}

func FuzzCIFilters(f *testing.F) {
	local, err := fixture.ReadAll("testdata")
	if err != nil {
		f.Fatal(err)
	}
	for i, fc := range local {
		in := fc.Clean()
		if len(in) > 16<<10 {
			in = in[:8<<10] + "\n" + in[len(in)-8<<10:]
		}
		f.Add(uint8(i), uint8(fc.Meta.ExitCode), in)
	}
	f.Add(uint8(0), uint8(0), "j\ts\t2026-09-28T10:00:00.1Z ##[group]Run x\nj\ts\t2026-09-28T10:00:00.2Z ##[endgroup]\nj\ts\t2026-09-28T10:00:00.3Z ##[error]boom")
	f.Add(uint8(2), uint8(0), "2026-09-28T10:00:00Z ##[start-action display=a;id=b]\n2026-09-28T10:00:00Z ##[group]Run \n2026-09-28T10:00:00Z ##[error]Process completed with exit code 9.")
	f.Add(uint8(6), uint8(1), "   • Running test in 2 packages\nweb:test: FAIL a\napi:test: ok\nweb#test:  ERROR  command exited (1)")
	f.Add(uint8(6), uint8(1), "Attaching to a-1, b-1\na-1  | x\nb-1  | error: y\na-1 exited with code 3")
	f.Fuzz(checkCI)
}

func checkCI(t *testing.T, which, exit uint8, in string) {
	t.Helper()
	in = textutil.Clean(in)
	argv := fuzzArgv[int(which)%len(fuzzArgv)]
	c := &engine.Context{Argv: argv, Exit: int(exit % 3), Cwd: "/home/user/src/x", Home: "/home/user"}
	fl := engine.Find(c)
	if fl == nil {
		for _, d := range engine.Detectors() {
			if d.Name == "monorepo" {
				fl = d.Filter
			}
		}
	}
	start := time.Now()
	a, okA := fl.Apply(c, in)
	if d := time.Since(start); d > testenv.Scale(2*time.Second) {
		t.Fatalf("%s took %v", fl.Name(), d)
	}
	b, okB := fl.Apply(c, in)
	if a != b || okA != okB {
		t.Fatalf("%s not deterministic", fl.Name())
	}
	if !okA {
		return
	}
	switch fl.Name() {
	case "gh-run-view", "gh-run-watch", "gh-pr-checks":
		if m := engine.MissingErrorLines(in, a); len(m) > 0 {
			t.Fatalf("%s dropped error line %q", fl.Name(), m[0])
		}
	case "gh-run-log":
		_, texts, _, _, ok := ci.RenderLog(c, in)
		if !ok {
			t.Fatal("Apply accepted what RenderLog refuses")
		}
		flat := squashAll(a)
		for _, tx := range texts {
			if strings.HasPrefix(tx, "##[error]") && !strings.Contains(flat, squash(tx)) {
				t.Fatalf("annotation %q dropped", tx)
			}
		}
	}
}

func TestMutations(t *testing.T) {
	local, err := fixture.ReadAll("testdata")
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewPCG(1, 2))
	rounds := 40
	if testenv.Race {
		rounds = 4
	}
	for i, fc := range local {
		lines := strings.Split(fc.Clean(), "\n")
		for range rounds {
			m := append([]string(nil), lines...)
			for range 1 + rng.IntN(6) {
				k := rng.IntN(len(m))
				switch rng.IntN(6) {
				case 0:
					m = append(m[:k], m[k+1:]...)
				case 1:
					m = append(m[:k+1], m[k:]...)
				case 2:
					j := rng.IntN(len(m))
					m[k], m[j] = m[j], m[k]
				case 3:
					if n := len(m[k]); n > 0 {
						m[k] = m[k][:rng.IntN(n)]
					}
				case 4:
					other := strings.Split(local[rng.IntN(len(local))].Clean(), "\n")
					m[k] = other[rng.IntN(len(other))]
				case 5:
					m = m[:k+1]
				}
				if len(m) == 0 {
					m = []string{""}
				}
			}
			checkCI(t, uint8(i), uint8(fc.Meta.ExitCode), strings.Join(m, "\n"))
		}
	}
}

func ghLog(jobs, stepsPerJob, linesPerStep int, fail bool) string {
	var b strings.Builder
	for j := range jobs {
		job := fmt.Sprintf("test (node-%d)", j)
		for s := range stepsPerJob {
			step := fmt.Sprintf("Step %d", s)
			fmt.Fprintf(&b, "%s\t%s\t\ufeff2026-09-28T10:%02d:%02d.0000000Z ##[group]Run go test -v ./pkg%d/...\n", job, step, s%60, 0, s)
			fmt.Fprintf(&b, "%s\t%s\t2026-09-28T10:%02d:00.0000001Z ^[[36;1mgo test -v ./pkg%d/...^[[0m\n", job, step, s%60, s)
			fmt.Fprintf(&b, "%s\t%s\t2026-09-28T10:%02d:00.0000002Z shell: /usr/bin/bash -e {0}\n", job, step, s%60)
			fmt.Fprintf(&b, "%s\t%s\t2026-09-28T10:%02d:00.0000003Z ##[endgroup]\n", job, step, s%60)
			for i := range linesPerStep / 2 {
				fmt.Fprintf(&b, "%s\t%s\t2026-09-28T10:%02d:01.%07dZ === RUN   TestCase%d\n", job, step, s%60, i, i)
				fmt.Fprintf(&b, "%s\t%s\t2026-09-28T10:%02d:01.%07dZ --- PASS: TestCase%d (0.00s)\n", job, step, s%60, i, i)
			}
			if fail && s == stepsPerJob-1 {
				fmt.Fprintf(&b, "%s\t%s\t2026-09-28T10:59:00.0000000Z --- FAIL: TestBroken (0.01s)\n", job, step)
				fmt.Fprintf(&b, "%s\t%s\t2026-09-28T10:59:00.0000001Z     broken_test.go:12: want 1, got 2\n", job, step)
				fmt.Fprintf(&b, "%s\t%s\t2026-09-28T10:59:00.0000002Z FAIL\n", job, step)
				fmt.Fprintf(&b, "%s\t%s\t2026-09-28T10:59:00.0000003Z FAIL\texample.com/m/pkg%d\t0.5s\n", job, step, s)
				fmt.Fprintf(&b, "%s\t%s\t2026-09-28T10:59:01.0000000Z ##[error]Process completed with exit code 1.\n", job, step)
			} else {
				fmt.Fprintf(&b, "%s\t%s\t2026-09-28T10:59:00.0000000Z ok  \texample.com/m/pkg%d\t0.5s\n", job, step, s)
			}
		}
	}
	return b.String()
}

func unknownSteps(steps int) string {
	var b strings.Builder
	b.WriteString("j\tUNKNOWN STEP\t\ufeff2026-09-28T10:00:00.0000000Z Current runner version: '2.337.0'\n")
	for s := range steps {
		fmt.Fprintf(&b, "j\tUNKNOWN STEP\t2026-09-28T10:00:01.0000000Z ##[group]Run echo %d\n", s)
		fmt.Fprintf(&b, "j\tUNKNOWN STEP\t2026-09-28T10:00:01.0000000Z ^[[36;1mecho %d^[[0m\n", s)
		b.WriteString("j\tUNKNOWN STEP\t2026-09-28T10:00:01.0000000Z shell: /usr/bin/bash -e {0}\n")
		b.WriteString("j\tUNKNOWN STEP\t2026-09-28T10:00:01.0000000Z ##[endgroup]\n")
		fmt.Fprintf(&b, "j\tUNKNOWN STEP\t2026-09-28T10:00:02.0000000Z %d\n", s)
	}
	b.WriteString("j\tUNKNOWN STEP\t2026-09-28T10:00:03.0000000Z ##[error]Process completed with exit code 1.\n")
	return b.String()
}

func watchSnapshots(n, jobs int) string {
	var b strings.Builder
	for k := range n {
		b.WriteString("Refreshing run status every 3 seconds. Press Ctrl+C to quit.\n\n* main CI · 42\nTriggered via push about 1 minute ago\n\nJOBS\n")
		for j := range jobs {
			state := "✓"
			if j == k%jobs {
				state = "*"
			}
			fmt.Fprintf(&b, "%s job %d in 1m (ID %d)\n  ✓ Set up job\n  %s Run tests\n", state, j, j, state)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func turboLines(n, tasks int) string {
	var b strings.Builder
	b.WriteString("   • Packages in scope: many\n   • Running test in 50 packages\n")
	for i := range n {
		fmt.Fprintf(&b, "pkg%d:test: line %d of the task output\n", i%tasks, i)
	}
	b.WriteString("pkg7:test: Error: boom\n pkg7#test:  ERROR  command (/x) npm run test exited (1)\n Tasks:    49 successful, 50 total\n")
	return b.String()
}

func composeLines(n int) string {
	var b strings.Builder
	b.WriteString("Attaching to api-1, db-1, worker-1\n")
	names := []string{"api-1     ", "db-1      ", "worker-1  "}
	for i := range n {
		fmt.Fprintf(&b, "%s| 2026-09-28T10:00:00Z info: request %d served in %dms\n", names[i%3], i, i%97)
	}
	b.WriteString("worker-1 exited with code 1\n")
	return b.String()
}

func TestHugeInputsAreFast(t *testing.T) {
	cases := map[string]struct {
		argv []string
		in   string
		max  int
	}{
		"log-failed 50k go test -v": {[]string{"gh", "run", "view", "1", "--log-failed"}, ghLog(1, 1, 50000, true), 40},
		"log 3 jobs × 10 steps":     {[]string{"gh", "run", "view", "1", "--log"}, ghLog(3, 10, 1600, true), 200},
		"unknown step 10k steps":    {[]string{"gh", "run", "view", "1", "--log-failed"}, unknownSteps(10000), 30},
		"watch 100 refreshes":       {[]string{"gh", "run", "watch", "42"}, watchSnapshots(100, 160), 200},
		"pr checks 50k rows":        {[]string{"gh", "pr", "checks"}, strings.Repeat("unit\tpass\t1s\thttps://x\t\n", 50000), 5},
		"turbo 50k lines":           {[]string{"turbo", "run", "test"}, turboLines(50000, 50), 400},
		"compose up 50k lines":      {[]string{"docker", "compose", "up"}, composeLines(50000), 60},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := &engine.Context{Argv: tc.argv, Exit: 1, Cwd: "/home/user/src/x", Home: "/home/user"}
			start := time.Now()
			res := engine.Process(c, tc.in, engine.Options{})
			d := time.Since(start)
			if d > testenv.Scale(3*time.Second) {
				t.Errorf("took %v", d)
			}
			if res.FilterPanic != "" {
				t.Fatalf("panic: %s", res.FilterPanic)
			}
			if n := strings.Count(res.Output, "\n") + 1; n > tc.max {
				t.Errorf("%d lines via %s, want <= %d:\n%s", n, res.Filter, tc.max, res.Output[:min(len(res.Output), 3000)])
			}
			t.Logf("%d lines → %d lines via %s in %v", strings.Count(tc.in, "\n"), strings.Count(res.Output, "\n")+1, res.Filter, d)
		})
	}
}
