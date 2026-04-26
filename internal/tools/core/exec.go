package core

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
)

// runWithSplitOutput runs cmd, captures stdout and stderr separately, and
// returns the captured output even when the process is killed (e.g., by
// context deadline). exit code is 0 on clean exit, the process's exit
// status when it failed cleanly, or -1 when the process was killed before
// reporting a status.
func runWithSplitOutput(cmd *exec.Cmd) (stdout, stderr string, exitCode int, err error) {
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()

	stdout = outBuf.String()
	stderr = errBuf.String()

	if runErr == nil {
		return stdout, stderr, 0, nil
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return stdout, stderr, exitErr.ExitCode(), runErr
	}

	// Killed by context deadline or other transport-level failure: -1
	// signals "no clean exit status." Caller surfaces timed_out=true
	// from the context state.
	return stdout, stderr, -1, runErr
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "/"
}
