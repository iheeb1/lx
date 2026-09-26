package vetfail

import "testing"

func TestHello(t *testing.T) {
	if Hello("x") == "" {
		t.Fatal("empty")
	}
}
