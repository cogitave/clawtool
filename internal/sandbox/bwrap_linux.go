//go:build linux

// bubblewrap (bwrap) adapter — Linux primary engine. v0.18 ships
// the probe; full Wrap implementation (path bind-mounts + network
// namespace + env scrubbing + cgroup limits) lands v0.18.1.
package sandbox

import (
	"context"
	"errors"
	"os/exec"
)

func init() { register(bwrapEngine{}) }

type bwrapEngine struct{}

func (bwrapEngine) Name() string { return "bwrap" }

func (bwrapEngine) Available() bool {
	_, err := exec.LookPath("bwrap")
	return err == nil
}

func (bwrapEngine) Wrap(_ context.Context, _ *exec.Cmd, _ *Profile) error {
	return errors.New(
		"sandbox: bwrap engine ships its profile compiler in v0.18.1 — " +
			"the surface (clawtool sandbox list / show / doctor) works today, " +
			"--sandbox <profile> on dispatches is logged but not yet enforced. " +
			"See ADR-020 for the rollout phases.",
	)
}
