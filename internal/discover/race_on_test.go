//go:build race

package discover

// raceSlowdown scales timing limits: the race detector slows code ~10x.
const raceSlowdown = 10
