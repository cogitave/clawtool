//go:build unix

package sandbox

import (
	"os/exec"
	"syscall"
)

// applyProcessGroup mirrors internal/tools/core/exec_unix.go's
// helper of the same name. Kept duplicated rather than imported
// because core depends on this package — a back-import would
// cycle. Both implementations must stay in lockstep; the regression
// test in runner_test.go exercises the timeout-preserves-output
// contract that depends on it.
func applyProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
