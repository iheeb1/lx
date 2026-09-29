package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/iheeb1/lx/internal/hook"
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

func hostcapSettings(t *testing.T) (user string, reads *int) {
	t.Helper()
	root := t.TempDir()
	user = filepath.Join(root, "user")
	reads = new(int)
	old := claudeLimits
	claudeLimits = func(string) hook.OutputLimits {
		*reads++
		return hook.OutputLimitsFrom(filepath.Join(root, "managed", "managed-settings.json"), filepath.Join(root, "proj"), "", user, os.Getenv("BASH_MAX_OUTPUT_LENGTH"))
	}
	t.Cleanup(func() { claudeLimits = old })
	return user, reads
}

func TestHostCharCap(t *testing.T) {
	hostcapSettings(t)
	cases := []struct {
		maxChars, claude, bashMax string
		limit, cap                int
		failLimit, failCap        int
	}{
		{"-", "-", "-", 0, 0, 0, 0},
		{"-", "1", "-", 30000, 26800, 10000, 8800},
		{"-", "1", "50000", 30000, 26800, 10000, 8800},
		{"-", "1", "0", 30000, 26800, 10000, 8800},
		{"-", "1", "-5", 30000, 26800, 10000, 8800},
		{"-", "1", "lots", 30000, 26800, 10000, 8800},
		{"-", "1", "300", 1000, 700, 1000, 700},
		{"-", "1", "5000", 5000, 4300, 5000, 4300},
		{"-", "1", "12000", 12000, 10600, 10000, 8800},
		{"-", "0", "-", 0, 0, 0, 0},
		{"-", "true", "50000", 0, 0, 0, 0},
		{"27000", "-", "-", 27000, 26800, 27000, 26800},
		{" 27000 ", "-", "-", 27000, 26800, 27000, 26800},
		{"27000", "1", "90000", 27000, 26800, 27000, 26800},
		{"0", "1", "-", 0, 0, 0, 0},
		{"-1", "1", "-", 0, 0, 0, 0},
		{"500", "-", "-", 1000, 800, 1000, 800},
		{"500", "1", "-", 1000, 800, 1000, 800},
		{"abc", "1", "-", 30000, 26800, 10000, 8800},
		{"", "-", "-", 0, 0, 0, 0},
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
		if limit != c.limit || capc != c.cap || hostCharCap() != c.cap || hostCharCapFor(0) != c.cap {
			t.Errorf("LX_MAX_CHARS=%q CLAUDECODE=%q BASH_MAX_OUTPUT_LENGTH=%q: got (%d, %d), want (%d, %d)",
				c.maxChars, c.claude, c.bashMax, limit, capc, c.limit, c.cap)
		}
		for _, exit := range []int{1, 2, 124, 130, 255, exitUnknown} {
			limit, capc := hostLimitsFor(exit)
			if limit != c.failLimit || capc != c.failCap || hostCharCapFor(exit) != c.failCap {
				t.Errorf("exit %d, LX_MAX_CHARS=%q CLAUDECODE=%q BASH_MAX_OUTPUT_LENGTH=%q: got (%d, %d), want (%d, %d)",
					exit, c.maxChars, c.claude, c.bashMax, limit, capc, c.failLimit, c.failCap)
			}
		}
	}
}

func TestHostCharCapSettings(t *testing.T) {
	user, reads := hostcapSettings(t)
	hostcapEnv(t, "")
	settings := filepath.Join(user, "settings.json")
	put := func(s string) {
		t.Helper()
		if err := os.MkdirAll(user, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settings, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		setting, bashMax, maxChars string
		pass, fail                 int
	}{
		{`{"bashOutputMaxChars": 60000}`, "-", "-", 53800, 8800},
		{`{"bashOutputMaxChars": 60000}`, "5000", "-", 53800, 8800},
		{`{"bashOutputMaxChars": 20000}`, "90000", "-", 17800, 8800},
		{`{"bashOutputMaxChars": 8000}`, "-", "-", 7000, 7000},
		{`{"bashOutputMaxChars": 100}`, "-", "-", 3400, 3400},
		{`{"bashOutputMaxChars": 900000}`, "-", "-", 115000, 8800},
		{`{"bashOutputMaxChars": "8000"}`, "5000", "-", 4300, 4300},
		{`{"bashOutputMaxChars": 60000}`, "-", "3000", 2800, 2800},
		{`{"bashOutputMaxChars": 60000}`, "-", "0", 0, 0},
	}
	for _, c := range cases {
		put(c.setting)
		t.Setenv("CLAUDECODE", "1")
		for k, v := range map[string]string{"BASH_MAX_OUTPUT_LENGTH": c.bashMax, "LX_MAX_CHARS": c.maxChars} {
			if v == "-" {
				hostcapUnset(t, k)
			} else {
				t.Setenv(k, v)
			}
		}
		if p, f := hostCharCapFor(0), hostCharCapFor(1); p != c.pass || f != c.fail {
			t.Errorf("%s BASH_MAX_OUTPUT_LENGTH=%s LX_MAX_CHARS=%s: caps (%d, %d), want (%d, %d)", c.setting, c.bashMax, c.maxChars, p, f, c.pass, c.fail)
		}
	}

	hostcapEnv(t, "")
	*reads = 0
	hostCharCapFor(1)
	t.Setenv("LX_MAX_CHARS", "5000")
	t.Setenv("CLAUDECODE", "1")
	hostCharCapFor(1)
	if *reads != 0 {
		t.Errorf("settings read %d times outside Claude Code or under LX_MAX_CHARS", *reads)
	}
}
