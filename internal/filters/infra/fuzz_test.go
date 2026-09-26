package infra

import (
	"github.com/iheeb1/lx/internal/testenv"
	"strings"
	"testing"
	"time"

	"github.com/iheeb1/lx/internal/engine"
	"github.com/iheeb1/lx/internal/fixture"
	"github.com/iheeb1/lx/internal/textutil"
)

var fuzzArgv = [][]string{
	{"docker", "build", "."},
	{"docker", "pull", "x"},
	{"docker", "logs", "x"},
	{"docker", "compose", "logs"},
	{"kubectl", "logs", "x"},
	{"journalctl", "-u", "x"},
	{"docker", "ps", "-a"},
	{"docker", "images"},
	{"kubectl", "get", "pods", "-A"},
	{"kubectl", "events"},
	{"kubectl", "describe", "pod", "x"},
}

// FuzzInfraFilters: no panic, deterministic, and the non-Guarded filters
// never drop an error-class line (the Guarded ones only merge event rows).
//
//	go test ./internal/filters/infra -run '^$' -fuzz FuzzInfraFilters -fuzztime 20s
func FuzzInfraFilters(f *testing.F) {
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
	f.Add(uint8(0), uint8(1), "#1 [1/1] RUN x\n#1 0.1 error: boom\n#1 ERROR: process did not complete\nERROR: failed to solve")
	f.Add(uint8(9), uint8(0), "LAST SEEN   TYPE      REASON    OBJECT   MESSAGE\n1m          Warning   BackOff   pod/x    failed\n2m          Warning   BackOff   pod/x    failed")
	f.Fuzz(func(t *testing.T, which, exit uint8, in string) {
		in = textutil.Clean(in)
		argv := fuzzArgv[int(which)%len(fuzzArgv)]
		c := &engine.Context{Argv: argv, Exit: int(exit % 3), Cwd: "/home/user/src/shop", Home: "/home/user"}
		fl := engine.Find(c)
		if fl == nil {
			t.Fatalf("no filter for %v", argv)
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
		if g, ok := fl.(engine.Guarded); ok && g.GuardsErrors() {
			// Merged rows are marked; nothing else may go.
			for _, m := range engine.MissingErrorLines(in, a) {
				if !strings.Contains(a, " rows; others: ") && !strings.Contains(a, " rows]") {
					t.Fatalf("%s dropped %q without merging", fl.Name(), m)
				}
			}
			return
		}
		if m := engine.MissingErrorLines(in, a); len(m) > 0 && fl.Name() != "logs" {
			t.Fatalf("%s dropped error line %q", fl.Name(), m[0])
		}
	})
}
