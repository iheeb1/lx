//go:build unix

package tee

import (
	"errors"
	"syscall"
)

const (
	unknown = iota
	alive
	dead
)

// pidAlive reports whether process pid exists and is ours. A process that
// exists but belongs to another user (EPERM) is not the lx that stored the
// run: its pid was reused.
func pidAlive(pid int) int {
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil:
		return alive
	case errors.Is(err, syscall.ESRCH), errors.Is(err, syscall.EPERM):
		return dead
	}
	return unknown
}
