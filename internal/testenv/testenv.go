// Package testenv has test helpers.
package testenv

import "time"

func Scale(d time.Duration) time.Duration {
	if Race {
		return 25 * d
	}
	return d
}
