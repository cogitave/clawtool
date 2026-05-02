package oauth

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/auth0.toml
var auth0Template []byte

// auth0ConfigPath is the in-repo location the recipe writes to.
// Lives under .clawtool/oauth/ so multiple OAuth providers can
// coexist without colliding.
const auth0ConfigPath = ".clawtool/oauth/auth0.toml"

// auth0Recipe scaffolds a `[oauth.auth0]` block for the
// clawtool-managed `.clawtool.toml`. Auth0 is OIDC-compliant and
// — uniquely among the Phase 1 providers — exposes RFC 7591
// dynamic client registration via `/oidc/register`, so a fresh
// install can self-register a client_id at first run instead of
// requiring an operator-provisioned one. The recipe records
// `dynamic_client_registration = true` by default; operators
// flip it off to enforce static credentials.
type auth0Recipe struct{}

func (auth0Recipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "oauth-auth0",
		Category:    setup.CategoryAgents,
		Description: "Drop a starter .clawtool/oauth/auth0.toml with the [oauth.auth0] connection contract (auth0_domain_env=AUTH0_DOMAIN, client_id_env=AUTH0_CLIENT_ID, client_secret_env=AUTH0_CLIENT_SECRET, scopes=[openid,email,profile], Authorization Code + PKCE, RFC 7591 dynamic client registration). Secrets stay in env vars; recipe ships shape only.",
		Upstream:    "https://auth0.com/",
		Stability:   setup.StabilityBeta,
		// Opt-in: not every project wires an external IdP.
		Core: false,
	}
}

func (auth0Recipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	b, err := setup.ReadIfExists(filepath.Join(repo, auth0ConfigPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, "auth0.toml not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, "auth0.toml exists but is not clawtool-managed; Apply will refuse to overwrite", nil
}

// Prereqs is empty: the recipe ships TOML scaffolding only.
// Provisioning the Auth0 tenant + (optionally) flipping on dynamic
// client registration in the dashboard is the operator's job and
// is documented in the asset's header.
func (auth0Recipe) Prereqs() []setup.Prereq { return nil }

func (auth0Recipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	path := filepath.Join(repo, auth0ConfigPath)
	existing, err := setup.ReadIfExists(path)
	if err != nil {
		return err
	}
	if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", auth0ConfigPath)
	}
	return setup.WriteAtomic(path, auth0Template, 0o644)
}

func (auth0Recipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, auth0ConfigPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", auth0ConfigPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", auth0ConfigPath)
	}
	return nil
}

func init() { setup.Register(auth0Recipe{}) }
