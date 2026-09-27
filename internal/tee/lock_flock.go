//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package tee

import (
	"errors"
	"os"
	"syscall"
)

// haveLocks: liveness is the spool's lock here (the pid is a fallback).
const haveLocks = true

// lockPart takes an exclusive lock on a spool's .log.part for as long as
// the file stays open. The kernel drops it when lx exits for any reason
// (SIGKILL included), so a reader can tell a live spool from one whose lx
// died even after that lx's pid was reused by another process.
func lockPart(f *os.File) {
	_ = flock(f, syscall.LOCK_EX|syscall.LOCK_NB)
}

// partHeld reports whether a live lx still holds the lock on the spool at
// path: alive, dead, or unknown (no such file, or no locks here).
func partHeld(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return unknown
	}
	defer f.Close() // releases the probe's lock
	switch err := flock(f, syscall.LOCK_SH|syscall.LOCK_NB); {
	case err == nil:
		return dead
	case errors.Is(err, syscall.EWOULDBLOCK):
		return alive
	}
	return unknown // e.g. a file system without flock: ask the pid
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
