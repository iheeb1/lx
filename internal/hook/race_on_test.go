//go:build race

package hook

// raceEnabled: the race detector slows the hook's many stat calls several
// times over, so per-call timing bounds are not asserted under -race.
const raceEnabled = true
