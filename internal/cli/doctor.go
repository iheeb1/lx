package cli

import (
	"os"
	"path/filepath"
	"time"

	"github.com/iheeb1/lx/internal/doctor"
	"github.com/iheeb1/lx/internal/tee"
	"github.com/iheeb1/lx/internal/track"
)

// cmdDoctor is `lx doctor [--json]`: a read-only check that lx's Claude
// Code hook, PATH, permissions and storage work. Exit 1 when a check fails.
func cmdDoctor(args []string) int {
	return doctor.Main(args, os.Stdout, os.Stderr, doctorEnv())
}

// doctorEnv describes this machine the way lx's hook and Claude Code see it.
func doctorEnv() doctor.Env {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	exe, err := os.Executable()
	if err == nil {
		if abs, err := filepath.Abs(exe); err == nil {
			exe = abs
		}
	} else {
		exe = ""
	}
	configDir := os.Getenv("CLAUDE_CONFIG_DIR")
	if configDir == "" && home != "" {
		configDir = filepath.Join(home, ".claude")
	}
	return doctor.Env{
		Home:        home,
		Cwd:         cwd,
		ConfigDir:   configDir,
		ProjectDir:  doctor.FindProjectDir(cwd, home, configDir, os.Getenv),
		ManagedPath: doctor.ManagedSettingsPath(),
		Executable:  exe,
		Version:     versionString(),
		TeeDir:      tee.Dir(),
		HistoryPath: track.Path(),
		Getenv:      os.Getenv,
		Exec:        doctor.ExecWith(nil),
		Now:         time.Now(),
	}
}
