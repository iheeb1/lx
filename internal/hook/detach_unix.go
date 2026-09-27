//go:build unix

package hook

import (
	"os/exec"
	"syscall"
)

// detach runs cmd in a new session: an interactive shell can't read from or
// draw on the user's terminal, and a timeout kills its whole process group
// (plugins it started included), not just the shell.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return cmd.Process.Kill()
	}
}
