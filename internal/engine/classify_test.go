package engine

import (
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		line string
		want Level
	}{
		// Real errors.
		{"src/app.ts(12,5): error TS2322: Type 'string' is not assignable to type 'number'.", Err},
		{"--- FAIL: TestCalledAs/find/conflict (0.00s)", Err},
		{"FAIL\tgithub.com/spf13/cobra\t0.412s", Err},
		{"E       AssertionError: assert '' == '42\\n'", Err},
		{"make: *** [Makefile:12: all] Error 2", Err},
		{"make[1]: *** No rule to make target 'x', needed by 'y'.  Stop.", Err},
		{"Undefined symbols for architecture arm64:", Err},
		{"ld: unknown options: -E", Err},
		{"e: file:///home/user/src/app/A.kt:12:13 Unresolved reference: foo", Err},
		{"E: Unable to locate package foo", Err},
		{"/bin/sh: foo: command not found", Err},
		{"fatal: not a git repository (or any of the parent directories): .git", Err},
		{"panic: runtime error: index out of range [3] with length 3", Err},
		{"Error: Cannot find module './missing'", Err},
		{"npm error code ERESOLVE", Err},
		{"2 failed, 0 errors", Err},
		{"x src/a.test.ts > parses > rejects bad input", Normal},
		// Names, not status.
		{"=== RUN   TestCalledAs/find/conflict", Normal},
		{"--- SKIP: TestErrors (0.00s)", Normal},
		{"go: downloading github.com/pkg/errors v0.9.1", Normal},
		{"   Compiling quick-error v2.0.1", Normal},
		{"examples/error-pages/index.js", Normal},
		{"node_modules/http-errors/index.js", Normal},
		{"gcc -Wall -O2 -Wfatal-errors -c lapi.c", Normal},
		{"cargo test --fail-fast", Normal},
		{"https://github.com/golang/go/issues/fail", Normal},
		{"✓ handles error input", Normal},
		{"0 errors, 0 warnings", Normal},
		{"no failures", Normal},
		{"expect(fn).toThrowError()", Normal},
		{"if err != nil {", Normal},
		// Warnings.
		{"npm warn deprecated inflight@1.0.6: This module is not supported", Warn},
		{"warning: unused variable `x`", Warn},
	}
	for _, tc := range cases {
		if got := Classify(tc.line); got != tc.want {
			t.Errorf("Classify(%q) = %v, want %v", tc.line, got, tc.want)
		}
		if got := classifySlow(tc.line); got != tc.want {
			t.Errorf("classifySlow(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestErrMatchEqualsErrRe(t *testing.T) {
	cases := []string{
		"error", "errors", "errorf", "my_error", "x-error-y", "ERROR:", "Couldn't open", "couldnt", "couldn't",
		"could  not", "could not", "not found", "not  found", "npm ERR! x", "ERR!x", "err!", "segmentation fault",
		"core dumped", "timed out", "out of memory", "out of  memory", "no such file", "Undefined Reference",
		"data race", "DATA RACE", "unable to", "panic:", "TS2322", "error[E0308]", "   E   bad", "✕ x", "FAIL x",
		"--- FAIL: T", "   FAILED x", " ! [rejected] main", "make: *** x", "e: x", "fine line", "",
		"kill", "killed", "skilled", "conflicts", "conflict", "OOM", "boom", "10 errors", "0 error", "fail2ban",
	}
	for _, s := range cases {
		if got, want := errMatch(s), errRe.MatchString(s); got != want {
			t.Errorf("errMatch(%q) = %v, errRe = %v", s, got, want)
		}
	}
}

func FuzzErrMatch(f *testing.F) {
	f.Add("could not open file: error")
	f.Add("couldn't x ERR!y")
	f.Fuzz(func(t *testing.T, s string) {
		if got, want := errMatch(s), errRe.MatchString(s); got != want {
			t.Fatalf("errMatch(%q) = %v, errRe = %v", s, got, want)
		}
		if strings.ContainsAny(s, "Kſ") {
			return
		}
		if got, want := Classify(s), classifySlow(s); got != want {
			t.Fatalf("Classify(%q) = %v, classifySlow = %v", s, got, want)
		}
	})
}

func TestRelativizeKeepsURLs(t *testing.T) {
	c := &Context{Cwd: "/home/user/src/app", Home: "/home/user"}
	cases := map[string]string{
		"/home/user/src/app/x.go:12: bad":                "x.go:12: bad",
		"w: file:///home/user/src/app/A.kt:12:13 unused": "w: file:///home/user/src/app/A.kt:12:13 unused",
		"see /home/user/.cache/x":                        "see ~/.cache/x",
		"/opt/home/user/src/app/x.go":                    "/opt/home/user/src/app/x.go",
	}
	for in, want := range cases {
		if got := Relativize(c, in); got != want {
			t.Errorf("Relativize(%q) = %q, want %q", in, got, want)
		}
	}
}
