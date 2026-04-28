//go:build darwin

// Apple sandbox-exec (Seatbelt) adapter — macOS primary engine.
// v0.18.2 fills in the .sb profile compiler; this iteration
// ships the engine probe so `sandbox doctor` can report
// availability accurately.
package sandbox

import (
	"context"
	"errors"
	"os/exec"
)

func init() { register(sandboxExecEngine{}) }

type sandboxExecEngine struct{}

func (sandboxExecEngine) Name() string { return "sandbox-exec" }

func (sandboxExecEngine) Available() bool {
	_, err := exec.LookPath("sandbox-exec")
	return err == nil
}

func (sandboxExecEngine) Wrap(_ context.Context, _ *exec.Cmd, _ *Profile) error {
	return errors.New(
		"sandbox: sandbox-exec engine is detected but the .sb profile compiler " +
			"is not yet implemented — surface works, enforcement is pending.",
	)
}
