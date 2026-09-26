package flaky

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFlaky fails on its first run in a process and passes afterwards.
func TestFlaky(t *testing.T) {
	marker := filepath.Join(os.TempDir(), "lxcap2-flaky-marker")
	if _, err := os.Stat(marker); err != nil {
		os.WriteFile(marker, nil, 0o644)
		t.Fatalf("first run: got %d, want %d", 1, 2)
	}
	os.Remove(marker)
	t.Log("second run ok")
}

func TestStable(t *testing.T) {}
