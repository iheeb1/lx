package doctor

import "testing"

func TestBadMode(t *testing.T) {
	for v, want := range map[string]bool{"": false, "auto": false, "verify": false, "minimal": false, "verfy": true, "Debug": true, " error": true} {
		if got := badMode(v); got != want {
			t.Errorf("badMode(%q) = %v, want %v", v, got, want)
		}
	}
}
