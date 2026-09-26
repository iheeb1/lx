//go:build race

package python

// raceSlowdown scales the time bounds of the huge-input tests: the race
// detector makes the filters about ten times slower.
const raceSlowdown = 10
