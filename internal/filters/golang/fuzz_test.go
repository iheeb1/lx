package golang

import (
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
)

func FuzzFilters(f *testing.F) {
	seed := func(s string, sel uint8) {
		if len(s) > 16<<10 {
			s = s[:strings.LastIndexByte(s[:16<<10], '\n')+1]
		}
		f.Add(s, sel)
	}
	for i, cc := range corpus {
		seed(fixture.Load(f, cc.cat, cc.name).Clean(), uint8(i))
	}
	for i, cc := range captures {
		c, err := fixture.Read("testdata/captures", cc.cat, cc.name)
		if err != nil {
			f.Fatal(err)
		}
		seed(c.Clean(), uint8(i*16+1))
	}
	f.Add("=== RUN   TestA\n--- FAIL: TestA (0.00s)\npanic: x\n\ngoroutine 1 [running]:\nmain.f()\n\t/a.go:1 +0x1\nFAIL\tp\t0.1s", uint8(1))
	f.Add(`{"Action":"output","Package":"p","Test":"T","Output":"--- FAIL: T (0.00s)\n"}`, uint8(1))

	f.Add("--- FAIL: TestA (0.00s)\n    a_test.go:3: boom\nFAIL\n==================\nWARNING: DATA RACE\n  a.f()\n      /a.go:1 +0x1\n==================\nFAIL\tp\t0.1s", uint8(16))
	f.Add("--- FAIL: Example_x (0.00s)\ngot:\na\nwant:\nb\nFAIL\nFAIL\tp\t0.1s", uint8(17))
	f.Add("=== RUN   TestA\npanic: fake\n--- PASS: TestA (0.00s)\n=== RUN   TestA\n--- FAIL: TestA (0.00s)\nxFAIL\nFAIL\tp\t0.1s", uint8(17))

	argvs := [][]string{
		{"go", "test", "./..."},
		{"go", "test", "-v", "-run", "X", "./..."},
		{"go", "test", "-json", "./..."},
		{"go", "build", "./..."},
		{"go", "mod", "tidy"},
		{"go", "list", "-m", "all"},
		{"go", "list", "-json", "./..."},
		{"go", "test", "-v", "-count=2", "./..."},
	}
	filters := []engine.Filter{testJSON{}, testText{}, build{}, mod{}, list{}, machine{}}
	f.Fuzz(func(t *testing.T, out string, sel uint8) {
		c := &engine.Context{Argv: argvs[int(sel)%len(argvs)], Exit: int(sel>>4) & 3, Cwd: "/home/user/src/app", Home: "/home/user"}
		start := time.Now()
		defer func() {
			if d := time.Since(start); d > slowdown*2*time.Second {
				t.Fatalf("whole exec slow: %v on %d bytes", d, len(out))
			}
		}()
		for _, fl := range filters {
			st := time.Now()
			a, okA := fl.Apply(c, out)
			if d := time.Since(st); d > slowdown*1500*time.Millisecond {
				t.Fatalf("%s slow: %v on %d bytes, %d lines", fl.Name(), d, len(out), strings.Count(out, "\n"))
			}
			b, okB := fl.Apply(c, out)
			if a != b || okA != okB {
				t.Fatalf("%s: nondeterministic output", fl.Name())
			}
			if okA && c.Exit != 0 && strings.HasPrefix(fl.Name(), "go-test") &&
				!strings.Contains(a, "FAIL") && !strings.Contains(a, "panic") && !hasBuildLine(a) {
				t.Fatalf("%s: exit %d rendered without any failure evidence:\n%s", fl.Name(), c.Exit, a)
			}

			if okA && strings.HasPrefix(fl.Name(), "go-test") {
				last := a[strings.LastIndexByte(a, '\n')+1:]
				if strings.HasPrefix(last, "[") && strings.Contains(last, " passed") && !strings.Contains(strings.ToLower(last), "fail") &&
					failedPkgLine(a) {
					t.Fatalf("%s: pass-like footer under a FAIL verdict:\n%s", fl.Name(), a)
				}
			}
			if okA && (fl.Name() == "go-machine" || fl.Name() == "go-list") && c.Exit == 0 && !strings.Contains(a, "[go") &&
				strings.TrimRight(a, "\n") != strings.TrimRight(out, "\n") {
				t.Fatalf("%s changed content output", fl.Name())
			}
		}
		if got, ok := TestTextDetect(c, out); ok {
			if again, _ := TestTextDetect(c, out); again != got {
				t.Fatal("detector nondeterministic")
			}
		}
	})
}

func hasBuildLine(s string) bool {
	for _, ln := range strings.Split(s, "\n") {
		if buildLine(ln) || engine.IsError(ln) {
			return true
		}
	}
	return false
}

func failedPkgLine(s string) bool {
	for _, ln := range strings.Split(s, "\n") {
		if failPkgRe.MatchString(ln) {
			return true
		}
	}
	return false
}
