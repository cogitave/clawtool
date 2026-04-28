// Package cli — helper wiring for setup_wizard.go. Lives alongside
// onboard.go so the production callbacks share one import set
// (daemon, agents, biam, secrets) without bloating setup_wizard.go.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/agents/biam"
	"github.com/cogitave/clawtool/internal/daemon"
)

func init() {
	resolvePATH = exec.LookPath
	runDaemonEnsure = func(ctx context.Context) error {
		_, err := daemon.Ensure(ctx)
		return err
	}
	runIdentityEnsure = func() error {
		_, err := biam.LoadOrCreateIdentity("")
		return err
	}
	runSecretsStoreEnsure = func(a *App) error {
		path := a.SecretsPath()
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return os.WriteFile(path,
			[]byte("# clawtool secrets store — mode 0600 by convention.\n# Add per-instance API keys via:\n#   clawtool source set-secret <instance> <KEY> --value <v>\n"),
			0o600)
	}
	runMCPClaim = func(ctx context.Context, host string) error {
		if _, err := daemon.Ensure(ctx); err != nil {
			return fmt.Errorf("ensure daemon: %w", err)
		}
		ad, err := agents.Find(host)
		if err != nil {
			return err
		}
		if _, err := ad.Claim(agents.Options{}); err != nil {
			return err
		}
		return nil
	}
}
