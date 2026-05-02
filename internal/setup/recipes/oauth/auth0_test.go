package oauth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestOAuthRecipe_Auth0_FieldsValid(t *testing.T) {
	r := setup.Lookup("oauth-auth0")
	if r == nil {
		t.Fatal("oauth-auth0 should self-register")
	}
	m := r.Meta()
	if m.Name != "oauth-auth0" {
		t.Errorf("Name = %q, want oauth-auth0", m.Name)
	}
	if m.Category != setup.CategoryAgents {
		t.Errorf("Category = %q, want %q", m.Category, setup.CategoryAgents)
	}
	if m.Upstream == "" {
		t.Error("Upstream must be set (wrap-don't-reinvent invariant)")
	}
	if m.Stability != setup.StabilityBeta {
		t.Errorf("Stability = %q, want beta", m.Stability)
	}
	if m.Core {
		t.Error("Core must be false — OAuth IdP wiring is opt-in")
	}
	if strings.TrimSpace(m.Description) == "" {
		t.Error("Description must be set")
	}

	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(dir, ".clawtool/oauth/auth0.toml"))
	if err != nil {
		t.Fatalf("read after Apply: %v", err)
	}
	if !setup.HasMarker(body, setup.ManagedByMarker) {
		t.Error("written file lacks managed-by marker")
	}
	s := string(body)
	for _, want := range []string{
		"[oauth.auth0]",
		`auth0_domain_env = "AUTH0_DOMAIN"`,
		`client_id_env     = "AUTH0_CLIENT_ID"`,
		`client_secret_env = "AUTH0_CLIENT_SECRET"`,
		`scopes = ["openid", "email", "profile"]`,
		`flow = "authorization_code_pkce"`,
		// RFC 7591 dynamic client registration — Auth0's
		// distinguishing capability among the Phase 1 set.
		"dynamic_client_registration = true",
		"RFC 7591",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("template missing %q", want)
		}
	}
	if err := r.Verify(context.Background(), dir); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestOAuthRecipe_Auth0_DetectAbsent(t *testing.T) {
	r := setup.Lookup("oauth-auth0")
	status, _, err := r.Detect(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want Absent", status)
	}
}

func TestOAuthRecipe_Auth0_RefusesUnmanagedFile(t *testing.T) {
	r := setup.Lookup("oauth-auth0")
	dir := t.TempDir()
	target := filepath.Join(dir, ".clawtool/oauth/auth0.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("# user-authored\n[oauth.auth0]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err == nil {
		t.Fatal("Apply should refuse unmanaged auth0.toml")
	}
	status, _, _ := r.Detect(context.Background(), dir)
	if status != setup.StatusPartial {
		t.Errorf("unmanaged file should detect as Partial; got %q", status)
	}
}

func TestOAuthRecipe_Auth0_ApplyIsIdempotent(t *testing.T) {
	r := setup.Lookup("oauth-auth0")
	dir := t.TempDir()
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), dir, nil); err != nil {
		t.Errorf("re-Apply over clawtool-managed file should succeed; got %v", err)
	}
}
