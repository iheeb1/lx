//go:build !unix && !windows

package tee

const (
	unknown = iota
	alive
	dead
)

func pidAlive(pid int) int { return unknown }
