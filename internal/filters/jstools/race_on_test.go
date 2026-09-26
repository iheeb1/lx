//go:build race

package jstools

// raceEnabled: the race detector slows regexp-heavy code 20-40x, so the
// timing bounds do not apply.
const raceEnabled = true
