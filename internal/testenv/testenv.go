// Package testenv holds test-only helpers shared across packages.
package testenv

import "time"

// Scale stretches a wall-clock limit for the current build: the race
// detector slows code 10–20×, so timing tests that guard against
// algorithmic blowups (not constant factors) scale their limits with it.
func Scale(d time.Duration) time.Duration {
	if Race {
		return 25 * d
	}
	return d
}
