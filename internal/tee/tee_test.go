package tee

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	raw := "line 1\n\x1b[31mred\x1b[0m\nlast"
	id, err := Save(Meta{Argv: []string{"go", "test"}, Exit: 1}, raw)
	if err != nil {
		t.Fatal(err)
	}
	got, m, err := Load(id)
	if err != nil {
		t.Fatal(err)
	}
	if got != raw {
		t.Fatalf("output not byte-identical: %q", got)
	}
	if m.Exit != 1 || m.Argv[1] != "test" {
		t.Fatalf("meta = %+v", m)
	}
	st, _ := os.Stat(filepath.Join(Dir(), "1.log"))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", st.Mode().Perm())
	}
}

func TestIDsIncreaseAndPrune(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	var last int
	for i := 0; i < Keep+5; i++ {
		id, err := Save(Meta{}, "x")
		if err != nil {
			t.Fatal(err)
		}
		if id <= last {
			t.Fatalf("id %d after %d", id, last)
		}
		last = id
	}
	if n := len(list(Dir())); n != Keep {
		t.Fatalf("kept %d runs, want %d", n, Keep)
	}
	if _, _, err := Load(1); err == nil {
		t.Fatal("oldest run should have been pruned")
	}
	if r := Recent(3); len(r) != 3 || r[0].ID != last {
		t.Fatalf("Recent = %+v", r)
	}
}

func TestDisabled(t *testing.T) {
	t.Setenv("LX_TEE_DIR", t.TempDir())
	t.Setenv("LX_TEE", "0")
	if _, err := Save(Meta{}, "x"); err == nil {
		t.Fatal("expected error when disabled")
	}
}
