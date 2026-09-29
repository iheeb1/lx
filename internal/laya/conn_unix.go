//go:build unix

package laya

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func dial(path string, deadline time.Time) (*os.File, error) {
	if path == "" {
		return nil, errors.New("no socket path (is $HOME set?)")
	}
	if err := private(path); err != nil {
		return nil, err
	}
	syscall.ForkLock.RLock()
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err == nil {
		syscall.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, os.NewSyscallError("socket", err)
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		syscall.Close(fd)
		return nil, os.NewSyscallError("setnonblock", err)
	}
	for {
		err = syscall.Connect(fd, &syscall.SockaddrUnix{Name: path})
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		syscall.Close(fd)
		return nil, &os.PathError{Op: "connect", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), path)
	if err := f.SetDeadline(deadline); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// Command output goes only to a socket no other user could have put there.
func private(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	dir, err := os.Lstat(filepath.Dir(path))
	if err != nil {
		return err
	}
	switch {
	case fi.Mode().Type() != os.ModeSocket:
		return fmt.Errorf("%s is not a socket", path)
	case !mine(fi):
		return fmt.Errorf("%s belongs to another user", path)
	case !dir.IsDir() || !mine(dir):
		return fmt.Errorf("%s is not a directory of yours", filepath.Dir(path))
	case dir.Mode().Perm()&0o022 != 0:
		return fmt.Errorf("others can write to %s", filepath.Dir(path))
	}
	return nil
}

func mine(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

func DetachAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
