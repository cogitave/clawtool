// Package github — GitHub OAuth Device Flow + tiny REST helpers
// scoped to clawtool's needs. Today: device-code authorisation +
// `PUT /user/starred/{owner}/{repo}` for the star feature. More
// will land as engagement / source-management features need them.
//
// Why Device Flow over web-redirect OAuth: clawtool is a CLI; we
// have no http server to receive a callback. Device Flow is
// designed exactly for this — we POST a device-code request,
// show the user a `user_code` and a verification URL, the user
// authorises in their browser, we poll the token endpoint until
// they finish. No redirect URI, no localhost listener, no port
// collision.
//
// Token storage: handled by the caller via internal/secrets, not
// here. This package is the wire-protocol shim and stays
// stateless so tests can drive it with httptest fixtures.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/auth/devicecode"
)

// ClientID is the GitHub OAuth App client_id used by clawtool's
// CLI surface. Public-by-design (Device Flow doesn't use a client
// secret; the user-code + browser confirmation IS the security
// boundary). Empty when the operator hasn't registered an OAuth
// app yet — the device flow then errors out cleanly via
// ErrNoClientID instead of crashing.
//
// To wire this in: create a GitHub OAuth App at
// github.com/settings/developers, set Device flow enabled, copy
// the resulting client_id into the build via -ldflags
// '-X github.com/cogitave/clawtool/internal/github.ClientID=<id>'
// or hard-code below at release time.
var ClientID = ""

// ErrNoClientID surfaces the "we don't have an OAuth app
// registered yet" state cleanly so the caller can fall back to
// a browser-redirect-to-action-page flow.
var ErrNoClientID = errors.New("github: clawtool's GitHub OAuth client_id is not configured")

// DefaultBaseURL is github.com's well-known endpoint. Overridable
// in tests (httptest fixture) by setting BaseURL on the Client.
const DefaultBaseURL = "https://github.com"

// DefaultAPIBaseURL is api.github.com's REST root. Same override
// shape as DefaultBaseURL.
const DefaultAPIBaseURL = "https://api.github.com"

// Client wraps an *http.Client with the URLs and credentials the
// clawtool→GitHub flows need. Construct via NewClient() and
// override fields for tests.
type Client struct {
	HTTP        *http.Client
	BaseURL     string // for /login/device/code + /login/oauth/access_token
	APIBaseURL  string // for REST endpoints
	UserAgent   string // GitHub asks every API call to set a UA
	ClientIDStr string // override for tests; falls back to package ClientID
}

// NewClient returns a Client with sane defaults. 30s overall
// timeout protects against a hung github.com from stranding the
// CLI; the per-call ctx the caller passes may impose a tighter
// budget for individual phases.
func NewClient() *Client {
	return &Client{
		HTTP:        &http.Client{Timeout: 30 * time.Second},
		BaseURL:     DefaultBaseURL,
		APIBaseURL:  DefaultAPIBaseURL,
		UserAgent:   "clawtool/1.x (+https://github.com/cogitave/clawtool)",
		ClientIDStr: "",
	}
}

func (c *Client) clientID() string {
	if c.ClientIDStr != "" {
		return c.ClientIDStr
	}
	return ClientID
}

// DeviceCode is the response from the device authorisation
// endpoint. The CLI shows VerificationURI + UserCode to the
// operator (and ideally OpenBrowser's the URI), then polls
// /login/oauth/access_token using DeviceCodeStr until the user
// authorises or the code expires.
type DeviceCode struct {
	DeviceCodeStr   string        `json:"device_code"`
	UserCode        string        `json:"user_code"`
	VerificationURI string        `json:"verification_uri"`
	ExpiresIn       int           `json:"expires_in"` // seconds
	Interval        int           `json:"interval"`   // poll interval, seconds
	Expires         time.Time     `json:"-"`          // computed
	PollEvery       time.Duration `json:"-"`          // computed
}

// RequestDeviceCode kicks off the device flow with the given
// space-separated scope list (e.g. "public_repo" for starring
// public repos). Returns the device code envelope or an error.
func (c *Client) RequestDeviceCode(ctx context.Context, scopes string) (*DeviceCode, error) {
	cid := c.clientID()
	if cid == "" {
		return nil, ErrNoClientID
	}
	form := url.Values{
		"client_id": {cid},
		"scope":     {scopes},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/login/device/code",
		strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("github: build device-code request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: device-code request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: device-code endpoint returned %s", resp.Status)
	}
	var dc DeviceCode
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return nil, fmt.Errorf("github: decode device-code response: %w", err)
	}
	dc.Expires = time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	dc.PollEvery = time.Duration(dc.Interval) * time.Second
	if dc.PollEvery < 5*time.Second {
		dc.PollEvery = 5 * time.Second // GitHub's documented floor
	}
	return &dc, nil
}

// PollAccessToken polls /login/oauth/access_token at the
// device-code's documented interval until either the user
// authorises (returns the access token), the code expires
// (returns ErrDeviceCodeExpired), or the user denies it
// (returns ErrAuthorizationDenied). ctx cancellation aborts
// the poll cleanly so a Ctrl-C in the CLI doesn't hang.
//
// ADR-036 Phase 1: this delegates to the issuer-agnostic
// internal/auth/devicecode poller so the wizard's DeviceCodeStep
// and clawtool star share one RFC 8628 §3.5 state machine.
// Errors are translated back to this package's sentinels so
// existing callers (star.go) keep their `errors.Is` shape.
func (c *Client) PollAccessToken(ctx context.Context, dc *DeviceCode) (string, error) {
	cid := c.clientID()
	if cid == "" {
		return "", ErrNoClientID
	}
	if dc == nil {
		return "", fmt.Errorf("github: nil device code")
	}
	cfg := devicecode.Config{
		HTTP:          c.HTTP,
		TokenEndpoint: c.BaseURL + "/login/oauth/access_token",
		ClientID:      cid,
		UserAgent:     c.UserAgent,
	}
	shared := &devicecode.DeviceCode{
		DeviceCode: dc.DeviceCodeStr,
		Expires:    dc.Expires,
		PollEvery:  dc.PollEvery,
	}
	tok, err := devicecode.Poll(ctx, cfg, shared)
	if err != nil {
		switch {
		case errors.Is(err, devicecode.ErrAuthorizationDenied):
			return "", ErrAuthorizationDenied
		case errors.Is(err, devicecode.ErrDeviceCodeExpired):
			return "", ErrDeviceCodeExpired
		case errors.Is(err, devicecode.ErrNoClientID):
			return "", ErrNoClientID
		default:
			// Re-wrap with the github: prefix the existing
			// error-message tests look for, while preserving
			// the underlying cause via %w.
			return "", fmt.Errorf("github: poll token endpoint: %w", err)
		}
	}
	return tok.AccessToken, nil
}

// ErrDeviceCodeExpired is returned by PollAccessToken when the
// device code's lifetime ran out before the user authorised.
// Callers typically restart the flow with a fresh code.
var ErrDeviceCodeExpired = errors.New("github: device code expired before authorisation")

// ErrAuthorizationDenied is returned when the user explicitly
// declined the consent screen.
var ErrAuthorizationDenied = errors.New("github: authorization denied by user")

// StarRepo calls `PUT /user/starred/{owner}/{repo}` on the
// authenticated user's behalf. token is the bearer from
// PollAccessToken. owner+repo identify the target. Returns nil
// on success (idempotent — already-starred returns 204 too).
func (c *Client) StarRepo(ctx context.Context, token, owner, repo string) error {
	if owner == "" || repo == "" {
		return fmt.Errorf("github: owner+repo required")
	}
	url := fmt.Sprintf("%s/user/starred/%s/%s", c.APIBaseURL, owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, nil)
	if err != nil {
		return fmt.Errorf("github: build star request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", c.UserAgent)
	// GitHub's PUT-with-no-body convention requires Content-Length
	// to be explicit (some intermediaries reject zero-length).
	req.Header.Set("Content-Length", "0")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("github: star request: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("github: star: 401 unauthorized — token rejected (re-run authorisation)")
	case http.StatusForbidden:
		return fmt.Errorf("github: star: 403 forbidden — token lacks scope (need public_repo) or rate-limited")
	case http.StatusNotFound:
		return fmt.Errorf("github: star: 404 not found — repo %s/%s does not exist or token can't see it", owner, repo)
	default:
		return fmt.Errorf("github: star: unexpected status %s", resp.Status)
	}
}

// StarPageURL returns the human-facing star page on github.com
// for the given owner/repo. Used as the OAuth-disabled fallback:
// open this in the user's browser and let them click Star
// themselves.
func StarPageURL(owner, repo string) string {
	return fmt.Sprintf("%s/%s/%s", DefaultBaseURL, owner, repo)
}
