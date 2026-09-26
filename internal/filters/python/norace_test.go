//go:build !race

package python

// raceSlowdown scales the time bounds of the huge-input tests (see
// race_test.go).
const raceSlowdown = 1
