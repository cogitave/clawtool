package oauth

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/cogitave/clawtool/internal/setup"
)

//go:embed assets/authentik.toml
var authentikTemplate []byte

// authentikConfigPath is the in-repo location the recipe writes
// to. Lives under .clawtool/oauth/ so multiple OAuth providers
// can coexist without colliding.
const authentikConfigPath = ".clawtool/oauth/authentik.toml"

// authentikRecipe scaffolds a `[oauth.authentik]` block for the
// clawtool-managed `.clawtool.toml`. Authentik is the open-source
// IdP option in the Phase 1 set — MIT-licensed, self-hostable,
// and OIDC-compliant. Same shape as Keycloak: provider-scoped
// issuer, env-var-resolved credentials, OIDC scope set, PKCE flow.
type authentikRecipe struct{}

func (authentikRecipe) Meta() setup.RecipeMeta {
	return setup.RecipeMeta{
		Name:        "oauth-authentik",
		Category:    setup.CategoryAgents,
		Description: "Drop a starter .clawtool/oauth/authentik.toml with the [oauth.authentik] connection contract (issuer_url, client_id_env=AUTHENTIK_CLIENT_ID, client_secret_env=AUTHENTIK_CLIENT_SECRET, scopes=[openid,email,profile], Authorization Code + PKCE) for the open-source Authentik IdP. Secrets stay in env vars; recipe ships shape only.",
		Upstream:    "https://goauthentik.io/",
		Stability:   setup.StabilityBeta,
		// Opt-in: not every project wires an external IdP.
		Core: false,
	}
}

func (authentikRecipe) Detect(_ context.Context, repo string) (setup.Status, string, error) {
	b, err := setup.ReadIfExists(filepath.Join(repo, authentikConfigPath))
	if err != nil {
		return setup.StatusError, "", err
	}
	if b == nil {
		return setup.StatusAbsent, "authentik.toml not present", nil
	}
	if setup.HasMarker(b, setup.ManagedByMarker) {
		return setup.StatusApplied, "managed-by: clawtool marker present", nil
	}
	return setup.StatusPartial, "authentik.toml exists but is not clawtool-managed; Apply will refuse to overwrite", nil
}

// Prereqs is empty: the recipe ships TOML scaffolding only.
// Provisioning the Authentik provider + application is the
// operator's job and is documented in the asset's header.
func (authentikRecipe) Prereqs() []setup.Prereq { return nil }

func (authentikRecipe) Apply(_ context.Context, repo string, opts setup.Options) error {
	path := filepath.Join(repo, authentikConfigPath)
	existing, err := setup.ReadIfExists(path)
	if err != nil {
		return err
	}
	if existing != nil && !setup.HasMarker(existing, setup.ManagedByMarker) && !setup.IsForced(opts) {
		return fmt.Errorf("%s exists but is not clawtool-managed; refusing to overwrite", authentikConfigPath)
	}
	return setup.WriteAtomic(path, authentikTemplate, 0o644)
}

func (authentikRecipe) Verify(_ context.Context, repo string) error {
	b, err := setup.ReadIfExists(filepath.Join(repo, authentikConfigPath))
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if b == nil {
		return fmt.Errorf("verify: %s missing", authentikConfigPath)
	}
	if !setup.HasMarker(b, setup.ManagedByMarker) {
		return fmt.Errorf("verify: clawtool marker missing in %s", authentikConfigPath)
	}
	return nil
}

func init() { setup.Register(authentikRecipe{}) }
