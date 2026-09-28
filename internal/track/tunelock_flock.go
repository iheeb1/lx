//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package track

import (
	"errors"
	"os"
	"syscall"
	"time"
)

func lockTune(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for wait := 100 * time.Microsecond; ; wait = min(2*wait, 10*time.Millisecond) {
		err := flockFile(f, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {

			_ = f.Close()
			return lockTuneExcl(path + ".excl")
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, errTuneBusy
		}
		time.Sleep(wait)
	}
}

func flockFile(f *os.File, how int) error {
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
