package hook

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// resolvePrefix returns the shell word a rewrite puts before a command:
// `lx`, or a quoted absolute path when a bare lx would not find this binary.
// explicit is the hook's --prefix. It is a variable so tests can pin it.
var resolvePrefix = defaultPrefix

// executable is os.Executable, replaceable in tests.
var executable = os.Executable

// defaultPrefix picks the word that runs this lx in a rewritten command.
//
//   - an explicit --prefix (written by `lx init` after probing the user's
//     shell) wins, shell-quoted;
//   - otherwise a bare `lx` when `lx` on PATH is this very binary;
//   - otherwise this binary's absolute path, so a rewrite never exits 127.
//
// The word must name a file called lx: permission checks recognize a
// model-written `…/lx git push` by that base name, and a prefix they could
// not recognize would let a copied rewrite slip past deny rules. So a
// binary installed under another name, or any error, falls back to `lx`.
// An explicit prefix that no longer exists (a moved binary) is ignored
// rather than turned into a rewrite that exits 127.
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

// sameFile reports whether a and b name the same file (after symlinks).
func sameFile(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// ShellJoin renders argv as one shell-quoted line (for display).
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
