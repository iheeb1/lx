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
