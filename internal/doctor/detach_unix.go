//go:build unix

package doctor

import (
	"os/exec"
	"syscall"
)

// detach runs cmd in its own session, with no controlling terminal: an
// interactive login shell (the PATH probe) cannot take over the user's
// terminal or stop to read from it. On timeout the whole process group
// is killed, so no grandchild outlives doctor.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
