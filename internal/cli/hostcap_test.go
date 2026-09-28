package cli

import (
	"os"
	"testing"
)

func hostcapUnset(t *testing.T, k string) {
	t.Helper()
	t.Setenv(k, "")
	os.Unsetenv(k)
}

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
		maxChars, claude, bashMax string
		limit, cap                int
	}{
		{"-", "-", "-", 0, 0},
		{"-", "1", "-", 30000, 26800},
		{"-", "1", "50000", 50000, 44800},
		{"-", "1", "0", 30000, 26800},
		{"-", "1", "-5", 30000, 26800},
		{"-", "1", "lots", 30000, 26800},
		{"-", "1", "300", 1000, 700},
		{"-", "0", "-", 0, 0},
		{"-", "true", "50000", 0, 0},
		{"27000", "-", "-", 27000, 26800},
		{" 27000 ", "-", "-", 27000, 26800},
		{"27000", "1", "90000", 27000, 26800},
		{"0", "1", "-", 0, 0},
		{"-1", "1", "-", 0, 0},
		{"500", "-", "-", 1000, 800},
		{"abc", "1", "-", 30000, 26800},
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
