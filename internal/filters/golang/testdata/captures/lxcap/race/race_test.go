package race

import (
	"sync"
	"testing"
)

type counter struct{ n int }

func (c *counter) inc() { c.n++ }

func TestRace(t *testing.T) {
	var c counter
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); c.inc() }()
	}
	wg.Wait()
	if c.n != 2 {
		t.Logf("n = %d", c.n)
	}
}
