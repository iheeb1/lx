package sub

import "testing"

type node struct {
	next *node
	v    int
}

func sum(n *node) int { return n.v + sum(n.next) }

func TestTree(t *testing.T) {
	t.Run("leaf", func(t *testing.T) {})
	t.Run("nil_next", func(t *testing.T) {
		if sum(&node{v: 1}) != 1 {
			t.Fatal("bad")
		}
	})
}
