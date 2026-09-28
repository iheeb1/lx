package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
)

func TestParseModeFlag(t *testing.T) {
	for _, k := range []string{"LX_RAW", "LX_OFF", "LX_BUDGET", "LX_MODE"} {
		t.Setenv(k, "")
	}
	for args, want := range map[string]struct {
		mode engine.Mode
		argv string
	}{
		"-m verify go test ./...":                  {engine.ModeVerify, "go test ./..."},
		"--mode debug git log":                     {engine.ModeDebug, "git log"},
		"--mode=minimal ls -la":                    {engine.ModeMinimal, "ls -la"},
		"-m error -b 300 --fit 5 -v -- git status": {engine.ModeError, "git status"},
		"-m auto ls":                               {engine.ModeAuto, "ls"},
		"-m verify -m error ls":                    {engine.ModeError, "ls"},
		"go test -m verify":                        {engine.ModeAuto, "go test -m verify"},
		"ls":                                       {engine.ModeAuto, "ls"},
	} {
		o, argv, err := parseRunFlags(strings.Fields(args))
		if err != nil || o.mode != want.mode || strings.Join(argv, " ") != want.argv {
			t.Errorf("%q: mode %v argv %q err %v, want %v %q", args, o.mode, argv, err, want.mode, want.argv)
		}
		if eo := o.engineOptions(); eo.Mode != want.mode {
			t.Errorf("%q: engine options mode %v", args, eo.Mode)
		}
	}
	for _, args := range [][]string{
		{"-m"}, {"--mode"}, {"-m", "nope", "ls"}, {"-m", "Verify", "ls"}, {"-m", "", "ls"}, {"-m", " verify", "ls"},
		{"--mode=", "ls"}, {"--mode=x", "ls"}, {"-m", "ls"},
		{"-mverify", "ls"}, {"-m=verify", "ls"},
	} {
		if o, argv, err := parseRunFlags(args); err == nil {
			t.Errorf("%q: accepted (mode %v, argv %q)", args, o.mode, argv)
		} else if !strings.Contains(err.Error(), "mode") && !strings.Contains(err.Error(), "unknown lx flag") {
			t.Errorf("%q: unclear error %q", args, err)
		}
	}

	t.Setenv("LX_MODE", "debug")
	if o, _, err := parseRunFlags([]string{"ls"}); err != nil || o.mode != engine.ModeDebug {
		t.Errorf("LX_MODE=debug: mode %v err %v", o.mode, err)
	}
	if o, _, err := parseRunFlags([]string{"-m", "verify", "ls"}); err != nil || o.mode != engine.ModeVerify {
		t.Errorf("LX_MODE=debug -m verify: mode %v err %v", o.mode, err)
	}
	for _, bad := range []string{"verfy", "DEBUG", "auto "} {
		t.Setenv("LX_MODE", bad)
		for _, args := range [][]string{{"ls"}, {"-m", "verify", "ls"}, {"-r", "ls"}} {
			if _, _, err := parseRunFlags(args); err == nil || !strings.Contains(err.Error(), "LX_MODE") {
				t.Errorf("LX_MODE=%q %q: err %v", bad, args, err)
			}
		}
	}
}

func TestUnknownModeRunsNothing(t *testing.T) {
	t.Setenv("LX_MODE", "")
	mark := filepath.Join(t.TempDir(), "ran")
	if code := Main([]string{"-m", "fast", "sh", "-c", "touch " + mark}); code != 2 {
		t.Errorf("lx -m fast: exit %d, want 2", code)
	}
	t.Setenv("LX_MODE", "fast")
	if code := Main([]string{"sh", "-c", "touch " + mark}); code != 2 {
		t.Errorf("LX_MODE=fast lx: exit %d, want 2", code)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("the command ran")
	}
}

func TestUsageNamesModes(t *testing.T) {
	for _, s := range []string{"-m, --mode MODE", "LX_MODE=", "error ", "debug ", "verify ", "minimal "} {
		if !strings.Contains(usage, s) {
			t.Errorf("usage lacks %q", s)
		}
	}
}
