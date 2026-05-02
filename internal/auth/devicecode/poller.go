// Package devicecode is clawtool's issuer-agnostic OAuth 2.0
// Device Authorization Grant (RFC 8628) helper. ADR-036 Phase 1
// promotes this out of the GitHub-specific shim so:
//
//   - The onboard wizard's auth-prompt phase (DeviceCodeStep) and
//     the existing star-on-GitHub flow share one poll loop instead
//     of each carrying their own copy of the slow_down /
//     authorization_pending / expired_token state machine.
//   - Future issuers (per-source OAuth, hosted-clawtool sign-in)
//     drop in by pointing Config at their device + token endpoints
//     and providing the form fields they want submitted.
//
// Token storage is the caller's job (internal/secrets is the only
// blessed sink); the poller stays stateless so tests can drive it
// with httptest fixtures.
package devicecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MinPollInterval is the floor RFC 8628 §3.5 recommends and that
// every major issuer (GitHub, Google, Auth0) enforces. Servers
// commonly return 5; we never go below it even when the response
// quotes a smaller number.
const MinPollInterval = 5 * time.Second

// DeviceCode is the response envelope from the device
// authorization endpoint (RFC 8628 §3.2). The wizard / CLI shows
// VerificationURI + UserCode to the operator and polls the token
// endpoint with DeviceCode until the user authorises or the code
// expires.
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	// VerificationURIComplete is the optional pre-filled URL
	// (verification_uri + user_code embedded) some issuers
	// return; preferred for browser-launch when present.
	VerificationURIComplete string        `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int           `json:"expires_in"` // seconds
	Interval                int           `json:"interval"`   // server-recommended poll cadence
	Expires                 time.Time     `json:"-"`          // computed
	PollEvery               time.Duration `json:"-"`          // computed
}

// Token is the successful poll response.
type Token struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	RefreshToken string `json:"refresh_token,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
}

// Config wires the poller to a specific issuer + client.
type Config struct {
	// HTTP is the client used for both the device-code request
	// and the token poll. Defaults to a 30s-timeout client.
	HTTP *http.Client
	// DeviceEndpoint is the absolute URL of the issuer's
	// /device/code (or equivalent) endpoint.
	DeviceEndpoint string
	// TokenEndpoint is the absolute URL of the issuer's
	// /token endpoint. Polled with grant_type=device_code.
	TokenEndpoint string
	// ClientID is the OAuth client_id. Public-by-design under
	// device flow; no client_secret is used.
	ClientID string
	// Scopes is the space-separated scope list submitted on the
	// initial device-code request.
	Scopes string
	// UserAgent is the User-Agent header set on every request.
	// Defaults to "clawtool-devicecode/1".
	UserAgent string
	// ExtraDeviceFields lets callers add issuer-specific form
	// fields to the device-code POST (e.g. "audience" for Auth0).
	ExtraDeviceFields url.Values
	// ExtraTokenFields is the same escape hatch for the token
	// poll (rare, but Auth0 + Okta sometimes want it).
	ExtraTokenFields url.Values
	// MinInterval, when non-zero, acts as an absolute override
	// on the poll cadence — replaces both the issuer's
	// suggested interval and the RFC 8628 §3.5 5-second floor.
	// Test-only knob: production callers leave this zero so
	// the spec floor takes effect (and the issuer's value, if
	// higher, wins). Setting it negative is treated the same
	// as zero.
	MinInterval time.Duration
}

// ErrNoClientID is returned when Config.ClientID is empty.
var ErrNoClientID = errors.New("devicecode: client_id is not configured")

// ErrAuthorizationDenied is returned when the issuer reports
// access_denied (RFC 8628 §3.5).
var ErrAuthorizationDenied = errors.New("devicecode: authorization denied by user")

// ErrDeviceCodeExpired is returned when the device code's
// lifetime ran out before the user authorised. Callers typically
// restart the flow with a fresh code.
var ErrDeviceCodeExpired = errors.New("devicecode: device code expired before authorisation")

// httpClient returns the configured client or a sane default.
func (c *Config) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Config) userAgent() string {
	if strings.TrimSpace(c.UserAgent) != "" {
		return c.UserAgent
	}
	return "clawtool-devicecode/1"
}

// RequestDeviceCode performs the initial RFC 8628 §3.1 POST to
// the device-code endpoint and returns the parsed envelope with
// computed Expires + PollEvery.
func RequestDeviceCode(ctx context.Context, cfg Config) (*DeviceCode, error) {
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, ErrNoClientID
	}
	if strings.TrimSpace(cfg.DeviceEndpoint) == "" {
		return nil, fmt.Errorf("devicecode: DeviceEndpoint is required")
	}
	form := url.Values{
		"client_id": {cfg.ClientID},
	}
	if cfg.Scopes != "" {
		form.Set("scope", cfg.Scopes)
	}
	for k, vs := range cfg.ExtraDeviceFields {
		for _, v := range vs {
			form.Add(k, v)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		cfg.DeviceEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("devicecode: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", cfg.userAgent())
	resp, err := cfg.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("devicecode: device-code request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("devicecode: endpoint returned %s", resp.Status)
	}
	var dc DeviceCode
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return nil, fmt.Errorf("devicecode: decode response: %w", err)
	}
	if dc.DeviceCode == "" {
		return nil, fmt.Errorf("devicecode: response missing device_code")
	}
	if dc.ExpiresIn <= 0 {
		// Reasonable fallback: 15 minutes is the RFC 8628
		// example, and every issuer we've integrated against
		// uses 600-900s.
		dc.ExpiresIn = 900
	}
	dc.Expires = time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	if dc.Interval <= 0 {
		dc.Interval = 5
	}
	dc.PollEvery = time.Duration(dc.Interval) * time.Second
	if cfg.MinInterval > 0 {
		// Test-only override: bypass spec floor + issuer hint.
		dc.PollEvery = cfg.MinInterval
	} else if dc.PollEvery < MinPollInterval {
		dc.PollEvery = MinPollInterval
	}
	return &dc, nil
}

// Poll runs the RFC 8628 §3.4–§3.5 token poll until the user
// authorises (returns Token), the issuer denies it (returns
// ErrAuthorizationDenied), the device code expires (returns
// ErrDeviceCodeExpired), or ctx is cancelled.
//
// authorization_pending → continue at the current interval.
// slow_down → bump the interval per the issuer's value (or +5s).
// expired_token → ErrDeviceCodeExpired.
// access_denied → ErrAuthorizationDenied.
//
// Tests inject a fake clock by passing an *http.Client whose
// transport returns canned responses; the polling cadence is
// driven off PollEvery, which the test can shrink to milliseconds.
func Poll(ctx context.Context, cfg Config, dc *DeviceCode) (*Token, error) {
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, ErrNoClientID
	}
	if strings.TrimSpace(cfg.TokenEndpoint) == "" {
		return nil, fmt.Errorf("devicecode: TokenEndpoint is required")
	}
	if dc == nil {
		return nil, fmt.Errorf("devicecode: nil device code")
	}
	form := url.Values{
		"client_id":   {cfg.ClientID},
		"device_code": {dc.DeviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}
	for k, vs := range cfg.ExtraTokenFields {
		for _, v := range vs {
			form.Add(k, v)
		}
	}
	interval := dc.PollEvery
	floor := cfg.MinInterval
	if floor <= 0 {
		floor = MinPollInterval
	}
	if interval <= 0 {
		interval = floor
	}
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		if !dc.Expires.IsZero() && time.Now().After(dc.Expires) {
			return nil, ErrDeviceCodeExpired
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			cfg.TokenEndpoint, strings.NewReader(form.Encode()))
		if err != nil {
			return nil, fmt.Errorf("devicecode: build poll request: %w", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", cfg.userAgent())
		resp, err := cfg.httpClient().Do(req)
		if err != nil {
			return nil, fmt.Errorf("devicecode: poll request: %w", err)
		}
		var body struct {
			Token
			Error     string `json:"error"`
			ErrorDesc string `json:"error_description"`
			Interval  int    `json:"interval"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("devicecode: decode poll response: %w", err)
		}
		resp.Body.Close()
		if body.AccessToken != "" {
			t := body.Token
			return &t, nil
		}
		switch body.Error {
		case "authorization_pending":
			// User hasn't finished yet; keep polling at the
			// current interval.
		case "slow_down":
			// RFC 8628 §3.5: extend by the new interval if
			// the issuer suggests one, else add 5s.
			if body.Interval > 0 {
				interval = time.Duration(body.Interval) * time.Second
			} else {
				interval += 5 * time.Second
			}
		case "expired_token":
			return nil, ErrDeviceCodeExpired
		case "access_denied":
			return nil, ErrAuthorizationDenied
		case "":
			return nil, fmt.Errorf("devicecode: token endpoint returned neither token nor error (status %s)", resp.Status)
		default:
			return nil, fmt.Errorf("devicecode: token endpoint error %q: %s", body.Error, body.ErrorDesc)
		}
	}
}
