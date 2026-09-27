//go:build unix

package doctor

import "syscall"

// writable reports whether this user may write p (a file or directory),
// without writing anything. known is false when it cannot tell.
func writable(p string) (ok, known bool) {
	if p == "" {
		return false, false
	}
	const wOK = 0x2
	return syscall.Access(p, wOK) == nil, true
}
