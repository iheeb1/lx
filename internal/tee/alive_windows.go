//go:build windows

package tee

import "syscall"

const (
	unknown = iota
	alive
	dead
)

const (
	processQueryLimitedInformation = 0x1000
	stillActive                    = 259 // STILL_ACTIVE
)

// pidAlive reports whether process pid is running. A process that cannot be
// opened (none, or not ours) is not the lx that stored the run; one that
// has exited but is still referenced by a handle reports its exit code
// instead of STILL_ACTIVE.
func pidAlive(pid int) int {
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return dead
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return unknown
	}
	if code != stillActive {
		return dead
	}
	return alive
}
