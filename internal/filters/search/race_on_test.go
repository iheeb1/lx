//go:build race

package search

// raceEnabled: the race detector slows these filters ~20×, so timing
// bounds are not asserted under -race.
const raceEnabled = true
