package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
)

func TestMain(m *testing.M) {
	if os.Getenv("LX_TEST_MAIN") == "1" {
		if os.Getenv("LX_LAYA") != "1" {
			layaAvailable = func() bool { return false }
		}
		if os.Getenv("LX_TEST_PANIC") == "1" {
			process = func(*engine.Context, string, engine.Options) engine.Result { panic("test: pipeline bug") }
		}
		os.Exit(Main(os.Args[1:]))
	}
	for _, k := range []string{"CLAUDE_CODE_SESSION_ID", "CODEX_THREAD_ID", "LX_CONTEXT", "LX_CONTEXT_WINDOW"} {
		os.Unsetenv(k)
	}
	layaAvailable = func() bool { return false }

	if dir, err := os.MkdirTemp("", "lx-path-"); err == nil {
		stub := filepath.Join(dir, "lx")
		if os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755) == nil {
			os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
		code := m.Run()
		os.RemoveAll(dir)
		os.Exit(code)
	}
	os.Exit(m.Run())
}
