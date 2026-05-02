// Package onboard hosts reusable wizard-step building blocks for
// the clawtool onboard flow. ADR-036 Phase 1 lands the first
// step here — DeviceCodeStep — so the OAuth Device Authorization
// Grant (RFC 8628) prompt has one home that both the interactive
// wizard (internal/cli/onboard*) and the headless `clawtool agent
// claim` path can call into without duplicating the poller / UX
// glue.
//
// Why a tiny package instead of a method on the wizard model:
//
//   - The agent-claim recipe runs outside the Bubble Tea wizard,
//     so it needs the step as a library function it can drive
//     directly with stdout-shaped UX hooks. A method on
//     internal/cli's wizard model would couple the recipe to the
//     TUI.
//   - The step is the natural seam for additional issuers (per-
//     source OAuth, hosted-clawtool sign-in) so they all share
//     one persistence / error-handling shape.
//
// What the step does NOT own: the issuer's client_id, the secrets
// scope name, the verification-URL renderer. Those are caller-
// supplied via Step.Config / Step.Render so the wizard can wrap
// them in lipgloss while the recipe path stays plain stdout.
package onboard

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cogitave/clawtool/internal/auth/devicecode"
	"github.com/cogitave/clawtool/internal/secrets"
)

// PromptInfo is what DeviceCodeStep hands to the renderer once
// the issuer returns the device-code envelope. The renderer's
// job is to surface UserCode + VerificationURI to the operator
// (and ideally launch a browser to VerificationURIComplete /
// VerificationURI). The step blocks on Poll until the renderer
// returns; renderer return value is informational (logged
// upstream) and never aborts the poll.
type PromptInfo struct {
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresInSeconds        int
}

// Renderer is the caller-provided UX hook. Wizards wrap this in
// their boxed UX; the agent-claim recipe uses a stdout shim.
// A nil Renderer is fine — the step then only logs the user-code
// to ctx-Err and the operator never sees it. Default callers
// always provide one.
type Renderer func(PromptInfo)

// SecretsSink is the persistence contract. The default sink
// writes to ~/.config/clawtool/secrets.toml under the configured
// scope; tests inject an in-memory sink so they don't touch the
// operator's real file. Save returns nil on success, the
// underlying error on failure (the step then surfaces a
// "saved-but-poll-succeeded" message so the operator knows the
// token is in memory but not durable).
type SecretsSink interface {
	Save(scope, key, value string) error
}

// DefaultSecretsSink is the production sink: load + Set + Save
// secrets.toml at the canonical path with mode 0600. Idempotent.
type DefaultSecretsSink struct{}

// Save loads the canonical secrets store, writes (scope, key) =
// value, and saves at mode 0600.
func (DefaultSecretsSink) Save(scope, key, value string) error {
	path := secrets.DefaultPath()
	store, err := secrets.LoadOrEmpty(path)
	if err != nil {
		return fmt.Errorf("load secrets: %w", err)
	}
	store.Set(scope, key, value)
	if err := store.Save(path); err != nil {
		return fmt.Errorf("save secrets: %w", err)
	}
	return nil
}

// Step orchestrates one OAuth Device Authorization Grant.
// Construct via NewDeviceCodeStep; Run executes the full sequence
// (request → render → poll → persist) and returns the token + a
// non-nil error iff the flow failed before persistence.
type Step struct {
	// Cfg is the issuer-specific devicecode.Config (endpoints,
	// client_id, scopes). Required.
	Cfg devicecode.Config
	// SecretsScope is the secrets.toml [scopes.<scope>] table
	// the access token is written under. Required.
	SecretsScope string
	// SecretsKey is the field name within the scope. Defaults
	// to "oauth_token" so existing star.go-style callers don't
	// need to override.
	SecretsKey string
	// Render is the UX hook invoked once with the device code
	// envelope. Optional but strongly recommended.
	Render Renderer
	// Sink is where the token lands. Defaults to
	// DefaultSecretsSink (XDG secrets.toml, mode 0600).
	Sink SecretsSink
}

// NewDeviceCodeStep returns a Step with sane defaults filled in
// (SecretsKey="oauth_token", Sink=DefaultSecretsSink). The caller
// must still set Cfg + SecretsScope.
func NewDeviceCodeStep(cfg devicecode.Config, scope string, render Renderer) *Step {
	return &Step{
		Cfg:          cfg,
		SecretsScope: scope,
		SecretsKey:   "oauth_token",
		Render:       render,
		Sink:         DefaultSecretsSink{},
	}
}

// Run executes the full device-code flow. Returns the access
// token on success; on failure returns the underlying error
// (wrapping devicecode.ErrAuthorizationDenied,
// ErrDeviceCodeExpired, etc. so callers can errors.Is them).
//
// Persistence is best-effort: a Save failure is appended to the
// returned error chain but the token itself is still returned so
// the caller can use it for the rest of the session.
func (s *Step) Run(ctx context.Context) (string, error) {
	if s == nil {
		return "", errors.New("onboard: nil DeviceCodeStep")
	}
	if strings.TrimSpace(s.SecretsScope) == "" {
		return "", errors.New("onboard: SecretsScope is required")
	}
	dc, err := devicecode.RequestDeviceCode(ctx, s.Cfg)
	if err != nil {
		return "", fmt.Errorf("device code: %w", err)
	}
	if s.Render != nil {
		s.Render(PromptInfo{
			UserCode:                dc.UserCode,
			VerificationURI:         dc.VerificationURI,
			VerificationURIComplete: dc.VerificationURIComplete,
			ExpiresInSeconds:        dc.ExpiresIn,
		})
	}
	tok, err := devicecode.Poll(ctx, s.Cfg, dc)
	if err != nil {
		return "", fmt.Errorf("poll: %w", err)
	}
	key := s.SecretsKey
	if key == "" {
		key = "oauth_token"
	}
	sink := s.Sink
	if sink == nil {
		sink = DefaultSecretsSink{}
	}
	if err := sink.Save(s.SecretsScope, key, tok.AccessToken); err != nil {
		// Token is in memory + the caller can still use it,
		// but the operator's next run will re-authorise. Make
		// that visible up the chain.
		return tok.AccessToken, fmt.Errorf("persist token: %w", err)
	}
	return tok.AccessToken, nil
}
