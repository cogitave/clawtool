// Package oauth ships starter OAuth/OIDC integration scaffolds for
// the identity providers clawtool's agent layer supports out of the
// box. Each recipe drops a `.clawtool/oauth/<provider>.toml` marker
// containing the connection contract (issuer URL, env-var names for
// client credentials, scope set, flow). Secrets are NEVER written
// to disk — only environment variable names are recorded, and
// clawtool resolves them through its standard secrets pipeline.
//
// Phase 1 ships Keycloak, Auth0, and Authentik. Okta, Entra ID,
// Google Workspace, and GitHub OAuth are deferred to v1.1. Phase 4
// follow-ups (RFC 9728 protected-resource-metadata advertisement,
// JWT validation, JWKS caching) are not part of these starters —
// the recipes ship the connection contract only.
package oauth

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/keycloak.toml
var keycloakTemplate []byte

// keycloakConfigPath is the in-repo location the recipe writes to.
// Lives under .clawtool/oauth/ so multiple OAuth providers can
// coexist without colliding.
const keycloakConfigPath = ".clawtool/oauth/keycloak.toml"

// keycloakRecipe scaffolds a `[oauth.keycloak]` block for the
// clawtool-managed `.clawtool.toml`. Keycloak is Apache-2.0,
// realm-scoped, and ships first-class OIDC discovery — the recipe
// records the issuer URL, the env-var names for the client
// credentials, and the scope set, and stops there. Token-handling
// middleware lands in a follow-up phase.
type keycloakRecipe struct{}

func (keycloakRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "oauth-keycloak",
		Category:    setup.CategoryAgents,
		Description: "Drop a starter .clawtool/oauth/keycloak.toml with the [oauth.keycloak] connection contract (issuer_url, client_id_env=KEYCLOAK_CLIENT_ID, client_secret_env=KEYCLOAK_CLIENT_SECRET, scopes=[openid,email,profile], Authorization Code + PKCE). Secrets stay in env vars; recipe ships shape only.",
		Upstream:    "https://www.keycloak.org/",
		Stability:   setup.StabilityBeta,
		// Opt-in: not every project wires an external IdP.
		Core: false,
	}
}

func (keycloakRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	b, err := setup.ReadIfExists(filepath.Join(repo, keycloakConfigPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, "keycloak.toml not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, "keycloak.toml exists but is not clawtool-managed; Apply will refuse to overwrite", nil
}

// Prereqs is empty: the recipe ships TOML scaffolding only.
// Provisioning a Keycloak realm + client is the operator's job
// and is documented in the asset's header.
func (keycloakRecipe) Prereqs() []setup.Prereq { return nil }

func (keycloakRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	path := filepath.Join(repo, keycloakConfigPath)
	existing, err := setup.ReadIfExists(path)
	if err != nil {
		return err
	}
	if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", keycloakConfigPath)
	}
	return setup.WriteAtomic(path, keycloakTemplate, 0o644)
}

func (keycloakRecipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, keycloakConfigPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", keycloakConfigPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", keycloakConfigPath)
	}
	return nil
}

func init() { setup.Register(keycloakRecipe{}) }
