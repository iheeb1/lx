package cli

import (
	"os"
	"testing"
)

// hostcapUnset unsets k for the rest of the test (t.Setenv restores it).
func hostcapUnset(t *testing.T, k string) {
	t.Helper()
	t.Setenv(k, "")
	os.Unsetenv(k)
}

// hostcapEnv pins the host-limit environment for a test: no Claude Code,
// no explicit limit, unless the test sets them. Tests run inside Claude
// Code would otherwise inherit CLAUDECODE=1.
func hostcapEnv(t *testing.T, maxChars string) {
	t.Helper()
	hostcapUnset(t, "CLAUDECODE")
	hostcapUnset(t, "BASH_MAX_OUTPUT_LENGTH")
	if maxChars == "" {
		hostcapUnset(t, "LX_MAX_CHARS")
	} else {
		t.Setenv("LX_MAX_CHARS", maxChars)
	}
}

func TestHostCharCap(t *testing.T) {
	cases := []struct {
		maxChars, claude, bashMax string // "-" = unset
		limit, cap                int
	}{
		{"-", "-", "-", 0, 0},
		{"-", "1", "-", 30000, 26800},
		{"-", "1", "50000", 50000, 44800},
		{"-", "1", "0", 30000, 26800},       // not positive: the default
		{"-", "1", "-5", 30000, 26800},      // not positive: the default
		{"-", "1", "lots", 30000, 26800},    // not a number: the default
		{"-", "1", "300", 1000, 700},        // tiny limits count as 1,000
		{"-", "0", "-", 0, 0},               // only CLAUDECODE=1 counts
		{"-", "true", "50000", 0, 0},        //
		{"27000", "-", "-", 27000, 26800},   // explicit
		{" 27000 ", "-", "-", 27000, 26800}, // whitespace tolerated
		{"27000", "1", "90000", 27000, 26800},
		{"0", "1", "-", 0, 0},  // 0 disables, even in Claude Code
		{"-1", "1", "-", 0, 0}, // so does a negative number
		{"500", "-", "-", 1000, 800},
		{"abc", "1", "-", 30000, 26800}, // not a number: ignored
		{"", "-", "-", 0, 0},
	}
	for _, c := range cases {
		for k, v := range map[string]string{"LX_MAX_CHARS": c.maxChars, "CLAUDECODE": c.claude, "BASH_MAX_OUTPUT_LENGTH": c.bashMax} {
			if v == "-" {
				hostcapUnset(t, k)
			} else {
				t.Setenv(k, v)
			}
		}
		limit, capc := hostLimits()
		if limit != c.limit || capc != c.cap || hostCharCap() != c.cap {
			t.Errorf("LX_MAX_CHARS=%q CLAUDECODE=%q BASH_MAX_OUTPUT_LENGTH=%q: got (%d, %d), want (%d, %d)",
				c.maxChars, c.claude, c.bashMax, limit, capc, c.limit, c.cap)
		}
	}
}
