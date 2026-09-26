package buildfail

import "testing"

func TestDouble(t *testing.T) {
	if Triple(2) != 6 {
		t.Fatal("x")
	}
	var unused int
}
