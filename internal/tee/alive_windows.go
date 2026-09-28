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
	stillActive                    = 259
)

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
