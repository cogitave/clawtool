//go:build unix

// Package sysproc — process-group reaping helpers shared across
// clawtool callsites (Bash tool, Verify tool, hooks subsystem). The
// pattern mirrors internal/tools/core/exec_unix.go but lives in its
// own package so non-tool callers (hooks, future plan runner) can
// reuse it without an import cycle.
package sysproc

import (
	"os/exec"
	"syscall"
)

// ApplyGroup makes cmd run in its own process group so KillGroup can
// SIGKILL the whole tree (including shell children like `sleep` that
// would otherwise hold stdio pipes open and stall Wait).
//
// Callers that use exec.CommandContext can additionally set
// cmd.Cancel themselves to wire context cancellation to the group
// kill — we deliberately don't touch cmd.Cancel here because plain
// exec.Command() rejects a non-nil Cancel at Start time.
func ApplyGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// ApplyGroupWithCtxCancel is the CommandContext-friendly variant: it
// sets Setpgid AND wires cmd.Cancel to the group SIGKILL. Use this
// when you've created the command via exec.CommandContext and want
// ctx-cancellation to reap the whole tree.
func ApplyGroupWithCtxCancel(cmd *exec.Cmd) {
	ApplyGroup(cmd)
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// KillGroup sends SIGKILL to the whole process group cmd.Process
// leads. Safe to call after Start; no-op when Process is nil.
func KillGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
