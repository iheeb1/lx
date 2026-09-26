//go:build race

package jstest

// raceEnabled: the race detector slows rendering ~20x; timing assertions
// are skipped.
const raceEnabled = true
