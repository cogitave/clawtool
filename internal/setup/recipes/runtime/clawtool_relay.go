package runtime

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/clawtool-relay.compose.yml
var clawtoolRelayCompose []byte

const clawtoolRelayPath = "compose.relay.yml"

// clawtoolRelayRecipe drops a docker-compose file that runs clawtool's
// HTTP gateway alongside an optional caddy reverse proxy. Per ADR-014
// Phase 3: a project that wants a remote-triggerable agent gets one
// with `clawtool init`, no copy-paste from external docs.
//
// The recipe wraps clawtool itself (no external upstream beyond the
// container runtime), so Upstream points at clawtool's own ADR-014
// for the canonical contract. Stability ships at Beta until at least
// one operator has fronted it with caddy in real production for a
// week — same gating discipline ADR-013's brain recipe used.
type clawtoolRelayRecipe struct{}

func (clawtoolRelayRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "clawtool-relay",
		Category:    setup.CategoryRuntime,
		Description: "Drop a docker-compose file that runs clawtool's HTTP gateway (POST /v1/send_message + bearer-token auth) plus an optional caddy reverse proxy.",
		Upstream:    "https://github.com/cogitave/clawtool/blob/main/docs/http-api.md",
		Stability:   setup.StabilityBeta,
	}
}

func (clawtoolRelayRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	path := filepath.Join(repo, clawtoolRelayPath)
	b, err := setup.ReadIfExists(path)
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, "compose.relay.yml not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, "compose.relay.yml exists but is not clawtool-managed; Apply will refuse to overwrite without force", nil
}

func (clawtoolRelayRecipe) Prereqs() []setup.Prereq { return nil }

func (clawtoolRelayRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	path := filepath.Join(repo, clawtoolRelayPath)
	if existing, err := setup.ReadIfExists(path); err != nil {
		return err
	} else if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", clawtoolRelayPath)
	}
	return setup.WriteAtomic(path, clawtoolRelayCompose, 0o644)
}

func (clawtoolRelayRecipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, clawtoolRelayPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", clawtoolRelayPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", clawtoolRelayPath)
	}
	return nil
}

func init() { setup.Register(clawtoolRelayRecipe{}) }
