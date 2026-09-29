//go:build unix

package tee

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

func fallbackDir() string {
	if os.Getenv("LX_TEE_DIR") != "" {
		return ""
	}
	return filepath.Join(os.TempDir(), "lx-"+strconv.Itoa(os.Getuid()), "runs")
}

func ownedPrivate(p string) bool {
	fi, err := os.Lstat(p)
	if err != nil || !fi.IsDir() {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid() && fi.Mode().Perm()&0o077 == 0
}
