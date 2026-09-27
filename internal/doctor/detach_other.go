//go:build !unix

package doctor

import "os/exec"

func detach(cmd *exec.Cmd) {}
