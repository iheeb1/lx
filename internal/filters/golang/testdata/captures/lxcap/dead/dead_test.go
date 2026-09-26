package dead

import "testing"

func TestDeadlock(t *testing.T) {
	ch := make(chan int)
	ch <- 1
}
