//go:build unix

package hook

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestOutputSettingNeverBlocks(t *testing.T) {
	root := t.TempDir()
	proj, user := filepath.Join(root, "proj"), filepath.Join(root, "user")
	putSettings(t, filepath.Join(user, "settings.json"), `{"bashOutputMaxChars": 7000}`)
	if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(proj, ".claude", "settings.local.json"), 0o600); err != nil {
		t.Skip(err)
	}
	if err := os.Symlink("/dev/zero", filepath.Join(proj, ".claude", "settings.json")); err != nil {
		t.Fatal(err)
	}
	done := make(chan OutputLimits, 1)
	go func() { done <- OutputLimitsFrom("", proj, "", user, "") }()
	select {
	case l := <-done:
		if l.Pass != 7000 {
			t.Errorf("got %+v, want 7000 from the user file", l)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO or /dev/zero as settings blocked")
	}
}
