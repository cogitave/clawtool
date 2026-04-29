// Package portal — chromedp-backed CDP driver for portal wizard +
// runtime (ADR-018). Per ADR-007 we wrap chromedp/chromedp instead
// of rolling our own WebSocket-CDP client. chromedp is the canonical
// Go binding to the DevTools Protocol — used by GoReleaser, k6, and
// every Mailgun integration test.
//
// Two modes share the same code path:
//
//   - Wizard:  newExecBrowser(ctx) — spawns the user's Chrome /
//     Chromium / Brave / Edge with Headless(false) + a temp
//     --user-data-dir so the operator can log in interactively.
//   - Runtime: newRemoteBrowser(ctx, ws) — attaches to an already-
//     running `obscura serve` (or any CDP host) over the supplied
//     WebSocket URL.
//
// Both return a `*BrowserSession` whose helpers (Navigate, Cookies,
// SetCookies, Evaluate, …) cover the surface portal flows actually
// need. We deliberately do not re-export the chromedp action API
// — we surface a small portal-shaped Go API so callers don't have
// to reason about chromedp.Tasks vs chromedp.ActionFunc.
package portal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Browser is the structural subset of BrowserSession that the
// portal Ask flow uses. Carved out so tests inject a fake without
// spawning Chrome / Obscura. Production code passes a
// *BrowserSession directly via duck typing.
type Browser interface {
	Navigate(ctx context.Context, url string) error
	SetCookies(ctx context.Context, cookies []Cookie) error
	SetExtraHTTPHeaders(ctx context.Context, headers map[string]string) error
	Evaluate(ctx context.Context, expr string, out any) error
	EvaluateBool(ctx context.Context, expr string) (bool, error)
	EvaluateString(ctx context.Context, expr string) (string, error)
}

// Ensure BrowserSession satisfies the interface at compile time.
var _ Browser = (*BrowserSession)(nil)

// BrowserSession is the wizard / runtime handle. Wraps a chromedp
// context plus its allocator-cancel + browser-cancel funcs so
// Close() reaps cleanly.
type BrowserSession struct {
	ctx         context.Context
	cancelCtx   context.CancelFunc
	cancelAlloc context.CancelFunc
	allocator   string // "exec" | "remote" — surfaced for error messages
}

// NewExecBrowser launches Chrome locally with a temp profile and
// remote-debug port, returning a session the wizard drives. The
// supplied options pick headless vs headed and the start URL; we
// keep the rest sensible (no first-run, no default-browser check,
// silenced password leak detection so a fresh profile doesn't
// nag).
type ExecOptions struct {
	Binary   string // override; empty = chromedp auto-detects
	Headless bool   // wizard sets false; tests set true
	StartURL string // optional; defaults to about:blank
}

// NewExecBrowser spawns Chrome via chromedp's exec-allocator.
// Caller MUST call Close() — that cancels the chromedp context AND
// the allocator, which kills the browser process and removes the
// temp profile dir.
func NewExecBrowser(parent context.Context, opts ExecOptions) (*BrowserSession, error) {
	allocOpts := append([]chromedp.ExecAllocatorOption{},
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.DisableGPU,
		// PasswordLeakDetection nags on a fresh profile; Autofill
		// silenced so the wizard doesn't have to dismiss a dialog.
		chromedp.Flag("disable-features", "PasswordLeakDetection,AutofillServerCommunication"),
	)
	if opts.Binary != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(opts.Binary))
	}
	allocOpts = append(allocOpts, chromedp.Flag("headless", opts.Headless))
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(parent, allocOpts...)

	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	// chromedp doesn't actually launch until the first action; emit
	// a cheap action so failures (binary missing, profile dir not
	// writable) surface here instead of mid-flow.
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(context.Context) error { return nil })); err != nil {
		cancelCtx()
		cancelAlloc()
		return nil, fmt.Errorf("portal: launch chrome (no Chrome / Chromium / Brave / Edge on PATH? install one or pass --chrome <path>): %w", err)
	}
	if start := strings.TrimSpace(opts.StartURL); start != "" {
		if err := chromedp.Run(ctx, chromedp.Navigate(start)); err != nil {
			cancelCtx()
			cancelAlloc()
			return nil, fmt.Errorf("portal: navigate start URL: %w", err)
		}
	}
	return &BrowserSession{ctx: ctx, cancelCtx: cancelCtx, cancelAlloc: cancelAlloc, allocator: "exec"}, nil
}

// NewRemoteBrowser attaches to an already-running CDP server (e.g.
// `obscura serve`). The browser-level WS URL comes from the
// caller — we don't probe /json/version here because the caller
// (runtime path) gets the URL when it spawns Obscura.
func NewRemoteBrowser(parent context.Context, wsURL string) (*BrowserSession, error) {
	allocCtx, cancelAlloc := chromedp.NewRemoteAllocator(parent, wsURL)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(context.Context) error { return nil })); err != nil {
		cancelCtx()
		cancelAlloc()
		return nil, fmt.Errorf("portal: connect remote CDP at %s: %w", wsURL, err)
	}
	return &BrowserSession{ctx: ctx, cancelCtx: cancelCtx, cancelAlloc: cancelAlloc, allocator: "remote"}, nil
}

// Close reaps the chromedp context and (for exec mode) the spawned
// browser + temp profile. Idempotent.
func (s *BrowserSession) Close() {
	if s == nil {
		return
	}
	if s.cancelCtx != nil {
		s.cancelCtx()
	}
	if s.cancelAlloc != nil {
		s.cancelAlloc()
	}
}

// Navigate loads the URL and waits for the document to be ready.
func (s *BrowserSession) Navigate(ctx context.Context, url string) error {
	return s.run(ctx, chromedp.Navigate(url))
}

// Cookies returns every cookie the session holds. Wizard uses this
// after the operator confirms login; runtime never calls it (we
// inject + go).
func (s *BrowserSession) Cookies(ctx context.Context) ([]Cookie, error) {
	var cookies []*network.Cookie
	err := s.run(ctx, chromedp.ActionFunc(func(c context.Context) error {
		got, err := network.GetCookies().Do(c)
		if err != nil {
			return err
		}
		cookies = got
		return nil
	}))
	if err != nil {
		return nil, err
	}
	out := make([]Cookie, 0, len(cookies))
	for _, c := range cookies {
		out = append(out, Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HTTPOnly: c.HTTPOnly,
			SameSite: string(c.SameSite),
			Expires:  int64(c.Expires),
		})
	}
	return out, nil
}

// SetCookies seeds the session before navigation. Runtime portal Ask
// uses this to inject the saved auth state.
func (s *BrowserSession) SetCookies(ctx context.Context, cookies []Cookie) error {
	if len(cookies) == 0 {
		return nil
	}
	return s.run(ctx, chromedp.ActionFunc(func(c context.Context) error {
		params := make([]*network.CookieParam, 0, len(cookies))
		for _, ck := range cookies {
			p := &network.CookieParam{
				Name:     ck.Name,
				Value:    ck.Value,
				Domain:   ck.Domain,
				Path:     ck.Path,
				Secure:   ck.Secure,
				HTTPOnly: ck.HTTPOnly,
			}
			if ck.SameSite != "" {
				p.SameSite = network.CookieSameSite(ck.SameSite)
			}
			params = append(params, p)
		}
		return network.SetCookies(params).Do(c)
	}))
}

// SetExtraHTTPHeaders applies on every subsequent request from the
// session. Runtime path uses it for Accept-Language etc.
func (s *BrowserSession) SetExtraHTTPHeaders(ctx context.Context, headers map[string]string) error {
	if len(headers) == 0 {
		return nil
	}
	return s.run(ctx, chromedp.ActionFunc(func(c context.Context) error {
		raw := make(network.Headers, len(headers))
		for k, v := range headers {
			raw[k] = v
		}
		return network.SetExtraHTTPHeaders(raw).Do(c)
	}))
}

// Evaluate runs JS and decodes the result via json.Unmarshal into
// `out`. `out` must be a pointer (or nil to discard).
func (s *BrowserSession) Evaluate(ctx context.Context, expr string, out any) error {
	if out == nil {
		var ignored json.RawMessage
		return s.run(ctx, chromedp.Evaluate(expr, &ignored, withAwaitPromise))
	}
	return s.run(ctx, chromedp.Evaluate(expr, out, withAwaitPromise))
}

// withAwaitPromise tells chromedp to await any Promise the expression
// resolves to before reading the result. Required for predicates
// that involve async DOM mutations (response polling, etc.).
func withAwaitPromise(p *runtime.EvaluateParams) *runtime.EvaluateParams {
	return p.WithAwaitPromise(true)
}

// EvaluateBool returns the boolean coercion of `expr`. Used by the
// predicate poller.
func (s *BrowserSession) EvaluateBool(ctx context.Context, expr string) (bool, error) {
	var out bool
	if err := s.Evaluate(ctx, "Boolean("+expr+")", &out); err != nil {
		return false, err
	}
	return out, nil
}

// EvaluateString returns the string coercion of `expr`. Used to pull
// the rendered response selector's innerText.
func (s *BrowserSession) EvaluateString(ctx context.Context, expr string) (string, error) {
	var out string
	if err := s.Evaluate(ctx, expr, &out); err != nil {
		return "", err
	}
	return out, nil
}

// run threads the session ctx through chromedp.Run while honouring
// the caller's ctx — first to expire wins. We do this because
// chromedp.Run uses the session ctx by default, but our callers
// (Ask flow) wrap the call in an additional timeout.
func (s *BrowserSession) run(ctx context.Context, actions ...chromedp.Action) error {
	merged, cancel := mergeCtx(s.ctx, ctx)
	defer cancel()
	return chromedp.Run(merged, actions...)
}

// mergeCtx returns a context that fires when either parent fires.
// The returned cancel func releases the watcher goroutine
// immediately; if the caller forgets to call it, the goroutine
// still exits when either parent context is cancelled (`merged`
// inherits cancellation from `a`).
func mergeCtx(a, b context.Context) (context.Context, context.CancelFunc) {
	if b == nil {
		return a, func() {}
	}
	merged, cancel := context.WithCancel(a)
	stop := make(chan struct{})
	go func() {
		select {
		case <-b.Done():
			cancel()
		case <-merged.Done():
			// `a` cancelled or our cancel ran — either way we're done.
		case <-stop:
		}
	}()
	return merged, func() {
		close(stop)
		cancel()
	}
}

// ── runtime Ask flow (replaces the v0.16.2 hand-rolled cdp+ask) ──

// Spawning Obscura and parsing its ws:// banner is small enough to
// keep here rather than add a separate file. We deliberately keep
// the obscura process management in *one* place so the lifecycle
// (start, ws-discovery, kill on Close) is auditable.

type obscuraServer struct {
	closer io.Closer
	wsURL  string
}

// (Implementation detail: actual obscura spawn lives in
// obscura_runtime.go to keep this file's surface readable.)

// AskNotImplementedError is the shared sentinel CLI/MCP surfaces
// match against when the runtime path is unavailable. Kept here
// (not in portal.go) because the v0.16.2 sentinel was tied to the
// hand-rolled CDP swap; v0.16.3 keeps it for forward-compat with
// any caller that still detects it.
var ErrSessionContextDone = errors.New("portal: browser session context cancelled")
