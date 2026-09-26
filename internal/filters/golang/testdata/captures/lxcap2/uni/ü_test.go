package uni

import "testing"

func TestÜnicode(t *testing.T) {
	t.Run("naïve case", func(t *testing.T) {
		t.Errorf("got %q, want %q", "café", "cafe")
	})
}
