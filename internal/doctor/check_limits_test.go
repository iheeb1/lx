package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckLimits(t *testing.T) {
	root := t.TempDir()
	cfg, proj := filepath.Join(root, "cfg"), filepath.Join(root, "proj")
	managed := filepath.Join(root, "managed", "managed-settings.json")
	for p, s := range map[string]string{
		filepath.Join(cfg, "settings.json"):                   `{"bashOutputMaxChars": 60000}`,
		filepath.Join(proj, ".claude", "settings.local.json"): `{"bashOutputMaxChars": 8000}`,
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		env             map[string]string
		cfg, proj, want string
	}{
		{nil, root, "", ""},
		{map[string]string{"CLAUDECODE": "1"}, root, "", "✓ limit       Claude Code shows 30,000 characters of a command's output inline, 10,000 when the command fails; lx keeps each view to 90% of that"},
		{map[string]string{"CLAUDECODE": "1"}, cfg, "", "✓ limit       Claude Code shows 60,000 characters of a command's output inline, 10,000 when the command fails (bashOutputMaxChars in $CFG/settings.json); lx keeps each view to 90% of that"},
		{nil, cfg, proj, "✓ limit       Claude Code shows 8,000 characters of a command's output inline, 8,000 when the command fails (bashOutputMaxChars in $PROJ/.claude/settings.local.json); lx keeps each view to 90% of that"},
		{map[string]string{"CLAUDECODE": "1", "BASH_MAX_OUTPUT_LENGTH": "5000"}, cfg, proj, "✓ limit       Claude Code shows 8,000 characters of a command's output inline, 8,000 when the command fails (bashOutputMaxChars in $PROJ/.claude/settings.local.json; it ignores BASH_MAX_OUTPUT_LENGTH); lx keeps each view to 90% of that"},
		{map[string]string{"LX_MAX_CHARS": "5000", "CLAUDECODE": "1"}, cfg, proj, "✓ limit       LX_MAX_CHARS=5000: views stay under 5,000 characters, receipt included, whether the command passes or fails"},
		{map[string]string{"LX_MAX_CHARS": "0"}, cfg, "", "✓ limit       LX_MAX_CHARS=0: views have no length limit"},
		{map[string]string{"LX_MAX_CHARS": "lots"}, cfg, "", "! limit       LX_MAX_CHARS=lots is not a number of characters, so lx ignores it\n              fix: set it like LX_MAX_CHARS=20000, or unset it\n✓ limit       Claude Code shows 60,000 characters of a command's output inline, 10,000 when the command fails (bashOutputMaxChars in $CFG/settings.json); lx keeps each view to 90% of that"},
	}
	for _, c := range cases {
		s := &state{e: &Env{Home: filepath.Join(root, "home"), ConfigDir: c.cfg, Cwd: c.proj, ManagedPath: managed,
			Getenv: func(k string) string { return c.env[k] }}}
		s.checkLimits()
		var b strings.Builder
		Report{Checks: s.checks}.Text(&b)
		got := strings.TrimSpace(strings.NewReplacer(cfg, "$CFG", proj, "$PROJ").Replace(b.String()))
		if c.want == "" && len(s.checks) > 0 || !strings.HasPrefix(got, c.want) {
			t.Errorf("env %v proj %q:\n%s\nwant\n%s", c.env, c.proj, got, c.want)
		}
	}

	e := Env{Home: filepath.Join(root, "home"), ConfigDir: filepath.Join(root, "none"), Cwd: filepath.Join(root, "home"), ManagedPath: managed}
	for bashMax, want := range map[string]string{
		"50000": "30,000 characters of a command's output inline, 10,000 when the command fails (BASH_MAX_OUTPUT_LENGTH=50000 enlarges only the window",
		"5000":  "5,000 characters of a command's output inline, 5,000 when the command fails (BASH_MAX_OUTPUT_LENGTH=5000);",
	} {
		e.Getenv = func(k string) string { return map[string]string{"BASH_MAX_OUTPUT_LENGTH": bashMax}[k] }
		s := &state{e: &e}
		s.checkLimits()
		if len(s.checks) != 1 || !strings.Contains(s.checks[0].Message, want) {
			t.Errorf("BASH_MAX_OUTPUT_LENGTH=%s: %+v", bashMax, s.checks)
		}
	}
}
