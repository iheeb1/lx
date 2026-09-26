package tokens

import (
	"strings"
	"testing"
	"time"
)

func TestCountTerminatesOnAnyInput(t *testing.T) {
	inputs := []string{"\x00", "a\x00 b", "\x00\n", "\x00\x00\x00", "x\x01\x02y", "\xff\xfe", "\t\x00\t", " \x00"}
	for _, in := range inputs {
		done := make(chan int, 1)
		go func() { done <- Count(in) }()
		select {
		case n := <-done:
			if n < 1 {
				t.Errorf("Count(%q) = %d, want ≥1", in, n)
			}
		case <-time.After(time.Second):
			t.Fatalf("Count(%q) did not terminate", in)
		}
	}
}

func TestCountBasics(t *testing.T) {
	cases := map[string][2]int{ // text → [min, max]
		"":                         {0, 0},
		"hello":                    {1, 1},
		"hello world":              {2, 2},
		"func main() {}":           {4, 7},
		strings.Repeat("=", 80):    {1, 2},
		"2026-09-26T10:11:12.123Z": {10, 16},
		"مرحبا بالعالم":            {3, 14},
		"✓ passes\n✕ fails":        {4, 9},
	}
	for in, want := range cases {
		if got := Count(in); got < want[0] || got > want[1] {
			t.Errorf("Count(%q) = %d, want %d..%d", in, got, want[0], want[1])
		}
	}
}

func FuzzCount(f *testing.F) {
	f.Add("hello\x00world")
	f.Add("  \t\n\r\n x")
	f.Fuzz(func(t *testing.T, s string) {
		if n := Count(s); n < 0 || (s != "" && n == 0 && strings.TrimSpace(s) != "") {
			t.Fatalf("Count(%q) = %d", s, n)
		}
	})
}
