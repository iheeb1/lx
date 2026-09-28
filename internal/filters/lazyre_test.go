package filters

import (
	"testing"

	"github.com/iheeb1/lx/internal/lazyre"
)

func TestAllPatternsCompile(t *testing.T) {
	if n := lazyre.Count(); n < 400 {
		t.Fatalf("only %d lazy patterns registered; were the filter packages linked?", n)
	}
	if bad := lazyre.CompileAll(); len(bad) > 0 {
		t.Fatalf("invalid patterns: %q", bad)
	}
}
