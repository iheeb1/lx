package hook

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var resolvePrefix = defaultPrefix

var executable = os.Executable

func defaultPrefix(explicit string) string {
	if explicit != "" && filepath.IsAbs(explicit) && filepath.Base(explicit) == "lx" {
		if fi, err := os.Stat(explicit); err == nil && !fi.IsDir() {
			return shellQuote(filepath.Clean(explicit))
		}
	}
	exe, err := executable()
	if err != nil || exe == "" {
		return "lx"
	}
	if exe, err = filepath.Abs(exe); err != nil || filepath.Base(exe) != "lx" {
		return "lx"
	}
	if p, err := exec.LookPath("lx"); err == nil && sameFile(p, exe) {
		return "lx"
	}
	return shellQuote(exe)
}

func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

func ShellJoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		if a == "" {
			q[i] = "''"
			continue
		}
		q[i] = shellQuote(a)
	}
	return strings.Join(q, " ")
}
