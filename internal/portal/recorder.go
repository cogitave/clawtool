// Package portal — `clawtool portal record` recorder (ADR-018,
// v1.1 deferred work).
//
// Drives Obscura's CDP server in record mode: the operator
// completes a login flow in the headed browser, the recorder
// captures cookies (Network.getCookies) and a heuristic guess at
// the input + response selectors via in-page JS evaluation. The
// resulting Recording struct is what `internal/cli/portal_record.go`
// serialises to ~/.config/clawtool/portals/<name>.toml.
//
// Scope note (ADR-018 §Resolved 2026-05-02): the full CDP listener
// stack (Network.responseReceived + DOM.documentUpdated streaming
// for response-done predicate fingerprinting) is deferred. v1.1
// ships the capture verb + TOML persistence + a heuristic recorder
// that the operator can manually refine — no listener daemon. The
// existing wizard uses the same chromedp surface, so swapping in a
// streamed-events recorder later is additive.
package portal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/config"
)

// Recording is the captured-session payload `record` writes to
// disk. Mirrors PortalConfig closely so callers can convert with
// ToPortalConfig() — but we keep it as a separate type so future
// recorder fields (network log fingerprints, click trail, …) can
// land without churning PortalConfig's TOML schema.
type Recording struct {
	Name                  string
	URL                   string
	Cookies               []Cookie
	AuthCookieNames       []string
	Selectors             config.PortalSelectors
	ResponseDonePredicate config.PortalPredicate
	CapturedAt            time.Time
}

// RecordOptions wraps the recorder dependencies tests need to
// substitute. Production callers leave Browser nil and the
// recorder spawns a real chromedp ExecBrowser; tests inject a
// fake.
type RecordOptions struct {
	// Browser, when non-nil, replaces the headed-Chrome spawn
	// with a caller-supplied implementation. Used by record-verb
	// tests to drive the recorder without launching Chrome.
	Browser RecorderBrowser

	// ConfirmLogin blocks until the operator says "I'm logged
	// in" — the recorder reads cookies the moment this returns
	// nil. Production wires it to a stdin "press Enter when
	// ready" prompt; tests pass a no-op.
	ConfirmLogin func(ctx context.Context) error

	// Progress, when non-nil, gets one line per phase ("opened
	// browser", "captured N cookies", etc.). Mirrors
	// AskOptions.Stdout.
	Progress io.Writer
}

// RecorderBrowser is the structural subset of BrowserSession the
// recorder needs. Carved out so tests fake it without spawning
// Chrome — same pattern as Browser (used by Ask) and portalBrowser
// (used by the wizard).
type RecorderBrowser interface {
	Navigate(ctx context.Context, url string) error
	Cookies(ctx context.Context) ([]Cookie, error)
	EvaluateString(ctx context.Context, expr string) (string, error)
	Close()
}

// Ensure BrowserSession satisfies RecorderBrowser at compile time.
// The wizard's cookie-capture path already exercises the underlying
// chromedp calls in production; the recorder reuses them.
var _ RecorderBrowser = (*BrowserSession)(nil)

// Record opens a headed browser at `url`, blocks on
// opts.ConfirmLogin while the operator completes the login flow,
// then captures cookies + heuristic selectors and returns the
// Recording. Caller persists.
func Record(ctx context.Context, name, url string, opts RecordOptions) (*Recording, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("portal record: name is required")
	}
	url = strings.TrimSpace(url)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("portal record: url must start with http:// or https:// (got %q)", url)
	}
	if opts.ConfirmLogin == nil {
		opts.ConfirmLogin = func(context.Context) error { return nil }
	}

	browser := opts.Browser
	if browser == nil {
		// Headed Chrome with a temp profile — same allocator the
		// wizard uses (internal/cli/portal_wizard.go). Recorder
		// path stays identical so a future streamed-events
		// upgrade lands in one place.
		bs, err := NewExecBrowser(ctx, ExecOptions{Headless: false, StartURL: url})
		if err != nil {
			return nil, err
		}
		defer bs.Close()
		browser = bs
	} else {
		if err := browser.Navigate(ctx, url); err != nil {
			return nil, fmt.Errorf("portal record: navigate %s: %w", url, err)
		}
	}
	progress := opts.Progress
	logf := func(format string, args ...any) {
		if progress != nil {
			fmt.Fprintf(progress, format+"\n", args...)
		}
	}
	logf("portal record: browser opened at %s — complete your login, then confirm to capture", url)

	if err := opts.ConfirmLogin(ctx); err != nil {
		return nil, err
	}

	cookies, err := browser.Cookies(ctx)
	if err != nil {
		return nil, fmt.Errorf("portal record: capture cookies: %w", err)
	}
	host := hostFromURLForRecord(url)
	cookies = filterCookiesForHostRecord(cookies, host)
	logf("portal record: captured %d cookies for %s", len(cookies), host)

	selectors := heuristicSelectors(ctx, browser)
	logf("portal record: heuristic selectors input=%q response=%q", selectors.Input, selectors.Response)

	rec := &Recording{
		Name:                  name,
		URL:                   url,
		Cookies:               cookies,
		AuthCookieNames:       autoDetectAuthCookieNamesRecord(cookies),
		Selectors:             selectors,
		ResponseDonePredicate: defaultResponseDonePredicate(),
		CapturedAt:            time.Now().UTC(),
	}
	return rec, nil
}

// ToPortalConfig converts a Recording into the canonical
// PortalConfig the rest of the portal package validates and runs.
// Single source of truth for the recorder→config mapping so the
// CLI verb, MCP surface, and any future re-export agree on the
// shape.
func (r *Recording) ToPortalConfig() config.PortalConfig {
	return config.PortalConfig{
		Name:                  r.Name,
		BaseURL:               r.URL,
		StartURL:              r.URL,
		SecretsScope:          SecretsScopePrefix + r.Name,
		AuthCookieNames:       r.AuthCookieNames,
		TimeoutMs:             DefaultTimeoutMs,
		LoginCheck:            config.PortalPredicate{Type: PredicateSelectorVisible, Value: r.Selectors.Input},
		ReadyPredicate:        config.PortalPredicate{Type: PredicateSelectorVisible, Value: r.Selectors.Input},
		Selectors:             r.Selectors,
		ResponseDonePredicate: r.ResponseDonePredicate,
		Browser: config.PortalBrowserSettings{
			Stealth:        true,
			ViewportWidth:  DefaultViewportWidth,
			ViewportHeight: DefaultViewportHeight,
			Locale:         DefaultLocale,
		},
	}
}

// heuristicSelectors guesses the three CSS selectors by querying
// the live DOM. Heuristic only — operators are expected to refine
// the result by hand. The recorder picks the largest visible
// textarea (the chat input on most webapps) and the most-recent
// node that looks like an assistant-message wrapper. Errors
// degrade silently to empty strings; the persistence path will
// still validate via portal.Validate when the operator runs
// `portal ask`.
func heuristicSelectors(ctx context.Context, b RecorderBrowser) config.PortalSelectors {
	input := evalOrEmpty(ctx, b, heuristicInputSelectorJS)
	submit := evalOrEmpty(ctx, b, heuristicSubmitSelectorJS)
	response := evalOrEmpty(ctx, b, heuristicResponseSelectorJS)
	if input == "" {
		// Always seed *something* so portal.Validate doesn't
		// reject the recording outright. Operator clearly sees
		// the placeholder and edits.
		input = "textarea"
	}
	return config.PortalSelectors{
		Input:    input,
		Submit:   submit,
		Response: response,
	}
}

func evalOrEmpty(ctx context.Context, b RecorderBrowser, expr string) string {
	out, err := b.EvaluateString(ctx, expr)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// heuristicInputSelectorJS picks the largest visible <textarea> on
// the page — chat UIs almost always render the prompt input as a
// dominant textarea. Falls back to the first textarea if none have
// non-zero geometry yet (some SPAs layout-late).
const heuristicInputSelectorJS = `(() => {
  const tas = Array.from(document.querySelectorAll('textarea'));
  if (!tas.length) return '';
  const visible = tas.filter(el => el.offsetParent !== null);
  const pool = visible.length ? visible : tas;
  pool.sort((a,b) => (b.offsetWidth*b.offsetHeight) - (a.offsetWidth*a.offsetHeight));
  const el = pool[0];
  if (!el) return 'textarea';
  if (el.id) return '#' + CSS.escape(el.id);
  if (el.name) return 'textarea[name="' + el.name + '"]';
  return 'textarea';
})()`

// heuristicSubmitSelectorJS looks for a submit-shaped button near
// the chosen textarea. Empty result is fine — the Ask flow falls
// back to an Enter dispatch.
const heuristicSubmitSelectorJS = `(() => {
  const candidates = document.querySelectorAll('button[type="submit"], button[aria-label*="Send" i], button[data-testid*="send" i]');
  for (const b of candidates) {
    if (b.offsetParent !== null) {
      if (b.id) return '#' + CSS.escape(b.id);
      if (b.getAttribute('data-testid')) return 'button[data-testid="' + b.getAttribute('data-testid') + '"]';
      return 'button[type="submit"]';
    }
  }
  return '';
})()`

// heuristicResponseSelectorJS picks an assistant-message wrapper
// by walking common attribute conventions ([data-role], [data-
// message-author-role], aria-roledescription). Returns ” when no
// match — operator fills in by Inspect → Copy → Selector.
const heuristicResponseSelectorJS = `(() => {
  const probes = [
    '[data-message-author-role="assistant"]',
    '[data-role="assistant"]',
    '[data-author="assistant"]',
    '[aria-roledescription*="assistant" i]',
    'div[class*="message"]',
  ];
  for (const sel of probes) {
    if (document.querySelector(sel)) return sel;
  }
  return '';
})()`

// defaultResponseDonePredicate returns the same "stop button gone"
// predicate the wizard's "stop_gone" choice produces. Recorder
// uses it as the v1.1 default until the deferred CDP-listener
// path lands a fingerprinted predicate.
func defaultResponseDonePredicate() config.PortalPredicate {
	return config.PortalPredicate{
		Type:  PredicateEvalTruthy,
		Value: `(() => { const stop = document.querySelector('button[aria-label*="Stop"], button[data-testid*="stop"]'); return !stop; })()`,
	}
}

// ── helpers ────────────────────────────────────────────────────────
//
// Recorder duplicates a few host/cookie helpers from
// internal/cli/portal_wizard.go to keep the package boundary clean
// — wizard helpers are unexported and live in the cli package, but
// the recorder is a portal-package primitive (it can be invoked
// from MCP surfaces too). Keeping the heuristic logic here avoids
// a wizard→recorder reverse import.

func hostFromURLForRecord(u string) string {
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	if i := strings.IndexAny(u, "/?#"); i > 0 {
		u = u[:i]
	}
	return strings.ToLower(u)
}

func filterCookiesForHostRecord(in []Cookie, host string) []Cookie {
	host = strings.TrimPrefix(strings.ToLower(host), ".")
	out := make([]Cookie, 0, len(in))
	for _, c := range in {
		d := strings.TrimPrefix(strings.ToLower(c.Domain), ".")
		if d == "" {
			out = append(out, c)
			continue
		}
		if d == host || strings.HasSuffix(host, "."+d) || strings.HasSuffix(d, "."+host) {
			out = append(out, c)
		}
	}
	return out
}

func autoDetectAuthCookieNamesRecord(cookies []Cookie) []string {
	var out []string
	for _, c := range cookies {
		if !c.HTTPOnly {
			continue
		}
		low := strings.ToLower(c.Name)
		if strings.Contains(low, "session") ||
			strings.Contains(low, "auth") ||
			strings.HasSuffix(low, "_token") ||
			strings.HasPrefix(low, "sid") ||
			strings.HasPrefix(low, "csrf") {
			out = append(out, c.Name)
		}
	}
	return out
}
