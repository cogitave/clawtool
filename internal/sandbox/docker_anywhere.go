// Docker fallback — ADR-020. Available on every OS as long as
// the daemon is reachable. v0.18.3 lands the actual `docker run`
// translation (volume mounts for paths, --network none/host for
// network policy, --memory / --cpus / --pids-limit for limits).
//
// Lives outside any //go:build tag so the adapter is registered
// on every platform; Available() does the real probe.
package sandbox

import (
	"context"
	"errors"
	"os/exec"
)

func init() { register(dockerEngine{}) }

type dockerEngine struct{}

func (dockerEngine) Name() string { return "docker" }

func (dockerEngine) Available() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	// Probe the daemon — `docker info` is cheap and tells us
	// whether the user can actually run containers (not just
	// has the client installed).
	cmd := exec.Command("docker", "info")
	return cmd.Run() == nil
}

func (dockerEngine) Wrap(_ context.Context, _ *exec.Cmd, _ *Profile) error {
	return errors.New(
		"sandbox: docker engine is detected but the run-flag compiler " +
			"is not yet implemented — surface works, enforcement is pending.",
	)
}
