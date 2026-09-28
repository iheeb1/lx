package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMask(t *testing.T) {
	cases := map[string]string{
		"2026-09-26T10:00:01.123Z started":        "<TS> started",
		"at 10:00:01 took 12ms, 3.2MB":            "at <TIME> took <DUR>, <SIZE>",
		"id 123e4567-e89b-12d3-a456-426614174000": "id <UUID>",
		"from 10.0.0.1:5432 ptr 0x7f00ab":         "from <IP> ptr <HEX>",
		"sha deadbeef1234 word deadbeef":          "sha <HEX> word deadbeef",
		"retry 3 of 12":                           "retry 3 of <N>",
		"no digits here":                          "no digits here",
		"blk_-6952295868487656571":                "blk_-<N>",
		"":                                        "",
		"héllo 42 wörld":                          "héllo <N> wörld",
	}
	for in, want := range cases {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCollapseSimilar(t *testing.T) {
	var in []string
	for _, s := range []string{"01", "02", "03", "04", "05"} {
		in = append(in, "10:00:"+s+" heartbeat ok seq=1"+s)
	}
	out := CollapseSimilar(in)
	want := []string{in[0], "  … 3 similar lines …", in[4]}
	if strings.Join(out, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %q", out)
	}

	if got := CollapseSimilar(in[:3]); len(got) != 3 {
		t.Errorf("run of 3 folded: %q", got)
	}
}

func TestCollapseSimilarKeepsErrorsAndLocations(t *testing.T) {
	in := []string{
		"10:00:01 heartbeat ok",
		"10:00:02 heartbeat ok",
		"10:00:03 error: heartbeat failed",
		"10:00:04 heartbeat ok",
		"10:00:05 heartbeat ok",
		"src/a.go:10: unused x",
		"src/a.go:11: unused x",
		"src/a.go:12: unused x",
		"src/a.go:13: unused x",
	}
	out := CollapseSimilar(in)
	if strings.Join(out, "\n") != strings.Join(in, "\n") {
		t.Fatalf("error line or locations folded:\n%s", strings.Join(out, "\n"))
	}
}

func TestCollapseSimilarEdgeCases(t *testing.T) {
	if got := CollapseSimilar(nil); len(got) != 0 {
		t.Errorf("nil: %q", got)
	}
	if got := CollapseSimilar([]string{"one"}); len(got) != 1 {
		t.Errorf("single: %q", got)
	}
	in := []string{"", "", "", "", ""}
	if got := CollapseSimilar(in); len(got) != 5 {
		t.Errorf("blank lines folded: %q", got)
	}
	in = []string{"\tstep 10 done", "\tstep 11 done", "\tstep 12 done", "\tstep 13 done"}
	if got := CollapseSimilar(in); got[1] != "\t… 2 similar lines …" {
		t.Errorf("indent not kept: %q", got)
	}
}

func TestClassifyPrefilter(t *testing.T) {
	lines := []string{
		"E   no problems x", "e  x", "expect(a).toThrowE  x", "TS2322: nope", "ts1234",
		"KILLED", "Killed", "ſegfault", "✗ broken", "⚠ careful", "no errors.x",
		"Errorf(E  x", "timed out", "could not open", "all good", "",
	}
	files, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "corpus", "*", "*.txt"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, strings.Split(string(b), "\n")...)
	}
	for _, ln := range lines {
		if len(ln) > 20000 {
			continue
		}
		if got, want := Classify(ln), classifySlow(ln); got != want {
			t.Errorf("Classify(%q) = %v, reference %v", ln, got, want)
		}
	}
}
