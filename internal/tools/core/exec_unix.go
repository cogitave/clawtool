//go:build unix

package core

import (
	"os/exec"
	"syscall"
)

// applyProcessGroup makes cmd run in its own process group and arranges for
// context cancellation to kill the entire group (children included).
//
// Without this, a bash child like `sleep` survives bash's SIGKILL and keeps
// the stdout/stderr pipes open, blocking cmd.Wait() until the child exits.
// That defeats ADR-005's "timeout-safe" promise — the user-facing timeout
// fires but the call doesn't return until the runaway child finishes.
//
// We set Setpgid so the new process becomes its own group leader, then
// override Cancel to kill the negative-PID group on context expiry.
func applyProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// Negative PID = whole process group.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
