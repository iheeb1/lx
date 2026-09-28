//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package tee

import (
	"errors"
	"os"
	"syscall"
)

const haveLocks = true

func lockPart(f *os.File) {
	_ = flock(f, syscall.LOCK_EX|syscall.LOCK_NB)
}

func partHeld(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return unknown
	}
	defer f.Close()
	switch err := flock(f, syscall.LOCK_SH|syscall.LOCK_NB); {
	case err == nil:
		return dead
	case errors.Is(err, syscall.EWOULDBLOCK):
		return alive
	}
	return unknown
}

func flock(f *os.File, how int) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := rc.Control(func(fd uintptr) { ferr = syscall.Flock(int(fd), how) }); err != nil {
		return err
	}
	return ferr
}
