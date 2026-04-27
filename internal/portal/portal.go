// Package portal implements the saved web-UI target ("portal")
// concept defined in ADR-018. A portal pairs a base URL with login
// cookies, CSS selectors, and a "response done" predicate so that
// `clawtool portal ask <name> "<prompt>"` can drive a headless
// browser session against a chat web UI without per-vendor code.
//
// Per ADR-017: this is a Tool surface, not a Transport. The
// supervisor never sees portals; the dispatch surface stays reserved
// for stable LLM-CLI wire formats.
//
// v0.16.1 (this iteration) ships the persistence + read-only CLI/MCP
// surface — Add/List/Remove/Use/Which/Unset, manual TOML editing,
// cookie export workflow. The CDP-driven Ask flow follows in
// v0.16.2 once the websocket client lands.
package portal

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/cogitave/clawtool/internal/config"
)

// Predicate types accepted by config.PortalPredicate.Type. Helpers
// in this package validate and (eventually) evaluate them.
const (
	PredicateSelectorExists  = "selector_exists"
	PredicateSelectorVisible = "selector_visible"
	PredicateEvalTruthy      = "eval_truthy"

	DefaultTimeoutMs      = 180_000
	DefaultViewportWidth  = 1440
	DefaultViewportHeight = 1000
	DefaultLocale         = "en-US"
)

// SecretsScopePrefix is the prefix every portal's secrets scope
// uses — keeps the secrets.toml namespace tidy and makes
// cross-references obvious.
const SecretsScopePrefix = "portal."

// validPredicateTypes is the closed set; anything else is an error
// at validation time so the operator notices typos before the first
// dispatch.
var validPredicateTypes = map[string]bool{
	PredicateSelectorExists:  true,
	PredicateSelectorVisible: true,
	PredicateEvalTruthy:      true,
}

// Cookie mirrors the subset of Chrome DevTools Network.Cookie shape
// we serialise to / from secrets.toml.
type Cookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain,omitempty"`
	Path     string `json:"path,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
	HTTPOnly bool   `json:"httpOnly,omitempty"`
	SameSite string `json:"sameSite,omitempty"`
	Expires  int64  `json:"expires,omitempty"` // epoch seconds; 0 = session
}

// Validate checks one PortalConfig is internally consistent. Called
// at registration time (CLI add, server boot) so a malformed entry
// never reaches the dispatch path.
func Validate(name string, p config.PortalConfig) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("portal: name is required")
	}
	if p.BaseURL == "" {
		return fmt.Errorf("portal %q: base_url is required", name)
	}
	if !(strings.HasPrefix(p.BaseURL, "http://") || strings.HasPrefix(p.BaseURL, "https://")) {
		return fmt.Errorf("portal %q: base_url must start with http:// or https://", name)
	}
	if p.SecretsScope == "" {
		return fmt.Errorf("portal %q: secrets_scope is required (cookies live in secrets.toml under this key)", name)
	}
	if !strings.HasPrefix(p.SecretsScope, SecretsScopePrefix) {
		return fmt.Errorf("portal %q: secrets_scope must start with %q (got %q)", name, SecretsScopePrefix, p.SecretsScope)
	}
	if p.Selectors.Input == "" {
		return fmt.Errorf("portal %q: selectors.input is required", name)
	}
	if p.ResponseDonePredicate.Type == "" {
		return fmt.Errorf("portal %q: response_done_predicate is required (the ask flow has no other way to know generation finished)", name)
	}
	if err := validatePredicate(name, "response_done_predicate", p.ResponseDonePredicate); err != nil {
		return err
	}
	if p.LoginCheck.Type != "" {
		if err := validatePredicate(name, "login_check", p.LoginCheck); err != nil {
			return err
		}
	}
	if p.ReadyPredicate.Type != "" {
		if err := validatePredicate(name, "ready_predicate", p.ReadyPredicate); err != nil {
			return err
		}
	}
	return nil
}

func validatePredicate(name, label string, p config.PortalPredicate) error {
	if !validPredicateTypes[p.Type] {
		return fmt.Errorf("portal %q: %s.type must be one of selector_exists | selector_visible | eval_truthy (got %q)", name, label, p.Type)
	}
	if strings.TrimSpace(p.Value) == "" {
		return fmt.Errorf("portal %q: %s.value cannot be empty", name, label)
	}
	return nil
}

// Names returns the configured portal names, sorted. Stable output
// for CLI list, MCP discovery, and alias generation.
func Names(cfg config.Config) []string {
	out := make([]string, 0, len(cfg.Portals))
	for n := range cfg.Portals {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Defaults fills in fall-through values an Ask flow needs. Mutates
// p in place. Idempotent — safe to call any number of times.
func Defaults(p *config.PortalConfig) {
	if p.StartURL == "" {
		p.StartURL = p.BaseURL
	}
	if p.TimeoutMs <= 0 {
		p.TimeoutMs = DefaultTimeoutMs
	}
	if p.Browser.ViewportWidth <= 0 {
		p.Browser.ViewportWidth = DefaultViewportWidth
	}
	if p.Browser.ViewportHeight <= 0 {
		p.Browser.ViewportHeight = DefaultViewportHeight
	}
	if p.Browser.Locale == "" {
		p.Browser.Locale = DefaultLocale
	}
}

// ParseCookies decodes the cookies_json payload stored in
// secrets.toml. Tolerant: accepts either a JSON array of Cookie
// objects or a single object (one cookie). Empty / whitespace-only
// input → no error, no cookies.
func ParseCookies(raw string) ([]Cookie, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if raw[0] == '[' {
		var arr []Cookie
		if err := json.Unmarshal([]byte(raw), &arr); err != nil {
			return nil, fmt.Errorf("portal: parse cookies array: %w", err)
		}
		return arr, nil
	}
	if raw[0] == '{' {
		var one Cookie
		if err := json.Unmarshal([]byte(raw), &one); err != nil {
			return nil, fmt.Errorf("portal: parse cookies object: %w", err)
		}
		return []Cookie{one}, nil
	}
	return nil, fmt.Errorf("portal: cookies_json must be a JSON array or object")
}

// AssertAuthCookies checks that every name in want exists in have.
// Used after ParseCookies to catch a cookies.json export that's
// missing the actual session cookie (common: user copied a single
// CSRF cookie thinking it was the login one).
func AssertAuthCookies(have []Cookie, want []string) error {
	if len(want) == 0 {
		return nil
	}
	present := map[string]bool{}
	for _, c := range have {
		present[c.Name] = true
	}
	var missing []string
	for _, n := range want {
		if !present[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("portal: cookies missing required auth names: %s", strings.Join(missing, ", "))
	}
	return nil
}

// AskNotImplementedError is the canonical sentinel returned by the
// stub Ask path until v0.16.2 lands the CDP driver. CLI / MCP
// surfaces match against it to give a uniform deferred-feature
// message.
var AskNotImplementedError = errors.New("portal ask: CDP driver lands in v0.16.2 — see ADR-018 in the wiki for the full design")
