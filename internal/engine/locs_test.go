package engine_test

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
)

func TestLocReUnchanged(t *testing.T) {
	orig := regexp.MustCompile(`[\w./@-]+\.(?:go|ts|tsx|js|jsx|mjs|cjs|py|rs|rb|java|kt|c|h|cc|cpp|cs|php|swift|vue|svelte)[:(]\d+`)
	for _, s := range []string{
		"args_test.go:73: Expected", "src/a.ts(12,5): error TS2322", "at f (/app/node_modules/x/index.js:10:3)",
		"File \"/usr/lib/python3.12/site-packages/x.py\", line 3", "x.go:1b.go:2", "no locations here", "./pkg/@scope/a.vue:9",
	} {
		if got, want := engine.LocRe.FindAllString(s, -1), orig.FindAllString(s, -1); !slices.Equal(got, want) {
			t.Errorf("%q: %q, want %q", s, got, want)
		}
	}
}

func TestLocKeyAndSplit(t *testing.T) {
	for m, want := range map[string]string{
		"src/a.ts(12":       "a.ts:12",
		"/abs/pkg/x.go:40":  "x.go:40",
		"args_test.go:73":   "args_test.go:73",
		"./rel/path/y.py:7": "y.py:7",
	} {
		if got := engine.LocKey(m); got != want {
			t.Errorf("LocKey(%q) = %q, want %q", m, got, want)
		}
	}
	for m, want := range map[string]string{
		"src/a.ts(12":      "src/a.ts 12 true",
		"/abs/pkg/x.go:40": "/abs/pkg/x.go 40 true",
		"x.go:":            "  false",
		"x.go:4a":          "  false",
		"nothing":          "  false",
	} {
		p, l, ok := engine.SplitLoc(m)
		if got := fmt.Sprintf("%s %s %v", p, l, ok); got != want {
			t.Errorf("SplitLoc(%q) = %q, want %q", m, got, want)
		}
	}
}

func TestAppLocations(t *testing.T) {
	in := strings.Join([]string{
		"    args_test.go:73: Expected x",
		"    args_test.go:73: Expected x again",
		"/home/u/src/app/pkg/args_test.go:73 +0x1d",
		"/usr/local/go/src/testing/testing.go:1792 +0xf3",
		"at f (/app/node_modules/lib/index.js:10:3)",
		"src/app.ts(4,2): error",
	}, "\n")
	if got, want := engine.AppLocations(in), []string{"args_test.go:73", "src/app.ts(4"}; !slices.Equal(got, want) {
		t.Errorf("AppLocations = %q, want %q", got, want)
	}
	if got := engine.AppLocationsMissing(in, "args_test.go:73"); !slices.Equal(got, []string{"src/app.ts(4"}) {
		t.Errorf("AppLocationsMissing = %q", got)
	}
	if got := engine.LocationsMissing("a.go:1 b.go:2 a.go:1", "x/a.go:1"); !slices.Equal(got, []string{"b.go:2"}) {
		t.Errorf("LocationsMissing = %q", got)
	}
}

func TestErrorMessagesMissing(t *testing.T) {
	in := "error: bad thing at 12:4\nok line\nerror: other thing\nerror: bad thing at 99:1"
	out := "error: bad thing at 12:4 99:1 (×2)\n"
	if got := engine.ErrorMessagesMissing(in, out); !slices.Equal(got, []string{"error: other thing"}) {
		t.Errorf("ErrorMessagesMissing = %q", got)
	}
	if got := len(engine.ErrorMessagesMissing(in, "")); got != 2 {
		t.Errorf("distinct error messages = %d, want 2", got)
	}
}

func TestViewLocKeys(t *testing.T) {
	view := strings.Join([]string{
		"[252 matches in 30 files · showing 151]",
		"src/pkg0/file00.go",
		"10:value",
		"17-context",
		"… +5 more in this file",
		"",
		"src/a.go:5:single",
		"",
		"src/b.js",
		"  12:5  error  bad  rule",
		"",
		"not a heading line",
		"99:after a prose line",
		"notes.txt",
		"3:not a source file",
	}, "\n")
	var got []string
	for k := range engine.ViewLocKeys(view) {
		got = append(got, k)
	}
	sort.Strings(got)
	if want := "[a.go:5 b.js:12 file00.go:10 file00.go:17]"; fmt.Sprint(got) != want {
		t.Errorf("keys = %v, want %s", got, want)
	}
}

func TestFindLocsMatchesRegexp(t *testing.T) {
	check := func(name, s string) {
		t.Helper()
		if got, want := engine.FindLocs(s), engine.LocRe.FindAllString(s, -1); !slices.Equal(got, want) {
			t.Fatalf("%s: engine.FindLocs = %q\nregexp   = %q", name, got, want)
		}
	}
	n := 0
	for _, c := range fixture.All(t) {
		check(c.Category+"/"+c.Name, c.Raw)
		check(c.Category+"/"+c.Name+" (clean)", c.Clean())
		n++
	}
	if n < 100 {
		t.Fatalf("only %d corpus captures", n)
	}
	const alphabet = "ab.go:(1\n/ts@-_svelte cpp h"
	seed := uint32(11)
	next := func(k int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>8) % k
	}
	for i := range 5000 {
		var b strings.Builder
		for range next(60) {
			if next(4) == 0 {
				b.WriteString([]string{".go:", ".svelte(", ".c:", ".py:", "\n", ":1", "(2"}[next(7)])
			} else {
				b.WriteByte(alphabet[next(len(alphabet))])
			}
		}
		check(fmt.Sprint("random ", i), b.String())
	}
}
