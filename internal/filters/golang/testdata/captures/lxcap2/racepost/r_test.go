package racepost

import (
	"os"
	"sync"
	"testing"
)

var shared int

func TestFirst(t *testing.T) {
	t.Errorf("first failed: got %d, want 1", shared)
}

func TestSecond(t *testing.T) {}

func bump(wg *sync.WaitGroup) {
	defer wg.Done()
	shared++
}

func TestMain(m *testing.M) {
	code := m.Run()
	var wg sync.WaitGroup
	wg.Add(2)
	go bump(&wg)
	go bump(&wg)
	wg.Wait()
	os.Exit(code)
}
