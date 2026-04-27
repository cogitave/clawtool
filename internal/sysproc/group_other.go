//go:build !unix

package sysproc

import "os/exec"

// ApplyGroup is a no-op on non-unix platforms.
func ApplyGroup(_ *exec.Cmd) {}

// ApplyGroupWithCtxCancel is a no-op on non-unix; the default
// CommandContext kill behaviour (single-process SIGKILL) is the best
// we can do without per-OS job-object plumbing.
func ApplyGroupWithCtxCancel(_ *exec.Cmd) {}

// KillGroup falls back to single-process kill on non-unix.
func KillGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
