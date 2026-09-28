package data

import (
	"github.com/iheeb1/lx/internal/testenv"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/textutil"
)

var fuzzArgv = [][]string{
	{"curl", "-s", "https://x/a.json"},
	{"curl", "-v", "https://x"},
	{"curl", "-i", "-L", "https://x"},
	{"curl", "-I", "https://x"},
	{"curl", "-#", "-o", "f", "https://x"},
	{"wget", "-S", "-O", "-", "https://x"},
	{"wget", "-qO-", "https://x"},
	{"http", "-v", "https://x"},
	{"jq", "."},
	{"jq", "-r", ".[]"},
	{"cat", "main.go"},
	{"cat", "package-lock.json"},
	{"cat", "yarn.lock"},
	{"cat", "data.json"},
	{"cat", "app.log"},
	{"tail", "-n", "100", "x.c"},
	{"bash", "-c", "curl -s https://x | jq '.[]'"},
}

var keptRe = map[string]*regexp.Regexp{
	"curl": regexp.MustCompile(`^curl: \(\d+\) `),
	"jq":   regexp.MustCompile(`^jq: error`),
	"cat":  regexp.MustCompile(`^cat: [^ ].*: No such file`),
}

func FuzzDataFilters(f *testing.F) {
	cases := fixture.All(f)
	if local, err := fixture.ReadAll("testdata"); err == nil {
		cases = append(cases, local...)
	}
	for i, fc := range cases {
		if fc.Category != "data" {
			continue
		}
		in := fc.Clean()
		if len(in) > 16<<10 {
			in = in[:16<<10] + "\n" + in[len(in)-2048:]
		}
		f.Add(uint8(i), uint8(fc.Meta.ExitCode), in)
	}
	f.Add(uint8(0), uint8(6), "  0     0    0     0    0     0      0      0 --:--:-- --:--:-- --:--:--     0curl: (6) Could not resolve host: x")
	f.Add(uint8(1), uint8(0), "< HTTP/1.1 200 OK\n<\n* item\n{ [5 bytes data]\n* Connection #0 to host x left intact")
	f.Add(uint8(9), uint8(5), "[1,\njq: error (at <stdin>:2): Cannot iterate over null")
	f.Add(uint8(10), uint8(1), "cat: nope.go: No such file or directory\npackage main")
	f.Add(uint8(3), uint8(0), "HTTP/2 200\nx: y\n\nHTTP/2 404")

	f.Add(uint8(0), uint8(18), "[\n  {\"id\": 1, \"na"+"curl: (18) transfer closed with 10 bytes remaining to read")
	f.Add(uint8(8), uint8(5), "{\n  \"a\": 1\n}\n  jq: parse error: Expected separator between values at line 3, column 1")
	f.Add(uint8(9), uint8(2), "a\nparse error: Invalid numeric literal at line 1, column 6\nb")
	f.Add(uint8(10), uint8(0), "package main\n<<<<<<< HEAD\nx := 1\n=======\nx := 2\n>>>>>>> feature\n")
	f.Add(uint8(12), uint8(0), "# yarn lockfile v1\n\n<<<<<<< HEAD\na@^1:\n  version \"1\"\n=======\na@^1:\n  version \"2\"\n>>>>>>> b\n")
	f.Add(uint8(10), uint8(1), "}cat: missing.h: No such file or directory\nint x;")
	f.Add(uint8(7), uint8(0), "/x/urllib3/__init__.py:35: NotOpenSSLWarning: x\n  warnings.warn(\n[{\"a\":1},{\"a\":2}]")
	f.Fuzz(func(t *testing.T, which, exit uint8, in string) {
		in = textutil.Clean(in)
		argv := fuzzArgv[int(which)%len(fuzzArgv)]
		c := &engine.Context{Argv: argv, Exit: int(exit % 3), Cwd: "/home/user/src/app", Home: "/home/user"}
		fl := engine.Find(c)
		if fl == nil {
			t.Fatalf("no filter for %v", argv)
		}
		start := time.Now()
		a, okA := fl.Apply(c, in)
		if d := time.Since(start); d > testenv.Scale(2*time.Second) {
			t.Fatalf("%s took %v on %d bytes", fl.Name(), d, len(in))
		}
		b, okB := fl.Apply(c, in)
		if a != b || okA != okB {
			t.Fatalf("%s not deterministic", fl.Name())
		}
		if !okA {
			return
		}
		lines := strings.Split(in, "\n")

		conflicts := hasConflict(lines) && utf8.ValidString(in) && strings.Count(in, "\x00") <= 8
		for _, ln := range lines {
			if re := keptRe[fl.Name()]; re != nil && re.MatchString(ln) && !strings.Contains(a, ln) {
				t.Fatalf("%s dropped %q", fl.Name(), ln)
			}

			switch {
			case fl.Name() == "cat" && conflicts && conflictRe.MatchString(ln) && !strings.Contains(a, ln):
				t.Fatalf("cat dropped %q", ln)
			case fl.Name() == "jq" && isJQDiag(ln):
				msg := ln
				if k := jqGlued(ln); k > 0 {
					msg = ln[k:]
				}
				if !strings.Contains(a, msg) {
					t.Fatalf("jq dropped %q", msg)
				}
			}
		}
	})
}
