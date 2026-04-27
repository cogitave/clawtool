package cli

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/agents/biam"
)

// ensureBIAMOnce wires the process-wide BIAM runner the first time
// the CLI needs it (e.g. `clawtool send --async`). The CLI is a
// short-lived process, but the SQLite store survives across
// invocations, so identity + store init is cheap and idempotent.
//
// Why this lives in the CLI package: server.go already initialises
// BIAM during `clawtool serve` boot. The bare `clawtool send` /
// `clawtool task` paths run in a separate process, so they need
// their own bootstrap.
var (
	biamOnce   sync.Once
	biamErr    error
	biamHandle *biam.Store
)

// ensureBIAMRunner initialises the BIAM identity + store on first
// call, registers a process-wide async runner, and returns the
// store handle for the caller to close on exit. Subsequent calls
// reuse the cached store.
func ensureBIAMRunner() (*biam.Store, error) {
	biamOnce.Do(func() {
		id, err := biam.LoadOrCreateIdentity("")
		if err != nil {
			biamErr = fmt.Errorf("biam identity: %w", err)
			return
		}
		store, err := biam.OpenStore("")
		if err != nil {
			biamErr = fmt.Errorf("biam store: %w", err)
			return
		}
		biamHandle = store
		runner := biam.NewRunner(store, id, func(ctx context.Context, instance, prompt string, opts map[string]any) (io.ReadCloser, error) {
			return agents.NewSupervisor().Send(ctx, instance, prompt, opts)
		})
		agents.SetGlobalBiamRunner(runner)
	})
	return biamHandle, biamErr
}
