//go:build unix

package doctor

import "syscall"

func writable(p string) (ok, known bool) {
	if p == "" {
		return false, false
	}
	const wOK = 0x2
	return syscall.Access(p, wOK) == nil, true
}
