//go:build race

package fs

// raceEnabled: the race detector slows these filters ~20×, so timing
// bounds are not asserted under -race.
const raceEnabled = true
