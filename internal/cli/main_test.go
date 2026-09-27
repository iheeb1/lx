package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/iheeb1/lx/internal/engine"
)

// TestMain lets a test run this test binary as lx: with LX_TEST_MAIN=1 the
// binary is `lx <args>` (see run_test.go), so tests can signal and kill a
// real lx process.
func TestMain(m *testing.M) {
	if os.Getenv("LX_TEST_MAIN") == "1" {
		if os.Getenv("LX_TEST_PANIC") == "1" {
			// A bug in lx's own pipeline (see TestPipelinePanicKeepsOutputAndExit).
			process = func(*engine.Context, string, engine.Options) engine.Result { panic("test: pipeline bug") }
		}
		os.Exit(Main(os.Args[1:]))
	}
	// Put an `lx` on PATH so receipts read "lx show N" as they do for a
	// normal install (selfCommand falls back to this binary's path when lx
	// isn't on PATH; TestSelfCommandFallsBackToAbsolutePath covers that).
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
