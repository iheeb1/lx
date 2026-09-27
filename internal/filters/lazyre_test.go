package filters

import (
	"testing"

	"github.com/iheeb1/lx/internal/lazyre"
)

// Every filter's patterns compile lazily; compile them all here so an
// invalid pattern fails the build, not a user's command.
func TestAllPatternsCompile(t *testing.T) {
	if n := lazyre.Count(); n < 400 {
		t.Fatalf("only %d lazy patterns registered; were the filter packages linked?", n)
	}
	if bad := lazyre.CompileAll(); len(bad) > 0 {
		t.Fatalf("invalid patterns: %q", bad)
	}
}
