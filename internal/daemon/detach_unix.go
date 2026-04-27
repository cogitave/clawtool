//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

// detachCmd makes the child a session leader so it survives the
// parent's exit (no controlling terminal, no stdin).
func detachCmd(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}
