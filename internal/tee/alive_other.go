//go:build !unix && !windows

package tee

const (
	unknown = iota
	alive
	dead
)

// pidAlive cannot tell on this platform; unknown is treated like dead, so a
// partial run reads as incomplete rather than as still running.
func pidAlive(pid int) int { return unknown }
