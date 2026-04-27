// Package portal — Ask orchestrator (ADR-018, v0.16.2).
//
// Spawns `obscura serve --port :0`, opens a fresh CDP browser
// context, seeds cookies + extra headers, navigates to the portal's
// start_url, runs login_check / ready_predicate, fills the input
// selector with the prompt, clicks submit, and polls the
// response_done_predicate until it resolves truthy. The final
// response selector's innerText streams back to the caller.
//
// Per ADR-007 we wrap Obscura's CDP server — never re-implement
// page rendering — and per ADR-014 the supervisor stays untouched:
// portals are a Tool surface (ADR-017).
package portal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/sysproc"
)

// AskOptions wraps the inputs an external caller (CLI / MCP /
// HTTP) needs to drive a saved portal flow.
type AskOptions struct {
	Cookies    []Cookie
	ObscuraBin string        // "obscura" → resolved via PATH if empty
	PollEvery  time.Duration // default 250ms
	Stdout     io.Writer     // optional: progress stream (one line per phase). nil → silent
}

// Ask drives the portal `p` with `prompt` and returns the captured
// response text. Idempotent in the sense that each call spins a
// fresh browser context (no shared state).
func Ask(ctx context.Context, p config.PortalConfig, prompt string, opts AskOptions) (string, error) {
	if err := Validate(p.Name, p); err != nil {
		return "", err
	}
	Defaults(&p)

	timeout := time.Duration(p.TimeoutMs) * time.Millisecond
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	bin := opts.ObscuraBin
	if bin == "" {
		bin = "obscura"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return "", fmt.Errorf("portal: %q binary not on PATH (see docs/browser-tools.md for install)", bin)
	}

	progress := opts.Stdout

	// 1. Spawn obscura serve --port 0 so the OS picks a free port;
	// scan stderr for the "listening on ws://..." line.
	cmd := exec.CommandContext(ctx, bin, "serve", "--port", "0")
	if p.Browser.Stealth {
		cmd.Args = append(cmd.Args, "--stealth")
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("portal: stderr pipe: %w", err)
	}
	sysproc.ApplyGroup(cmd) // ctx cancel + KillGroup reaps the child tree
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("portal: start obscura serve: %w", err)
	}
	defer func() {
		sysproc.KillGroup(cmd)
		_ = cmd.Wait()
	}()

	wsURL, err := readObscuraWS(stderr, 10*time.Second)
	if err != nil {
		return "", err
	}
	if progress != nil {
		fmt.Fprintf(progress, "portal: obscura listening at %s\n", wsURL)
	}

	// 2. Connect CDP, isolate browser context.
	browser, err := DialCDP(ctx, wsURL)
	if err != nil {
		return "", err
	}
	defer browser.Close()

	browserCtxID, err := browser.CreateBrowserContext(ctx)
	if err != nil {
		return "", err
	}
	targetID, err := browser.CreateTarget(ctx, "about:blank", browserCtxID,
		p.Browser.ViewportWidth, p.Browser.ViewportHeight)
	if err != nil {
		return "", err
	}
	sessionID, err := browser.AttachToTarget(ctx, targetID)
	if err != nil {
		return "", err
	}
	browser.SessionID = sessionID

	if err := browser.EnableNetwork(ctx); err != nil {
		return "", fmt.Errorf("portal: Network.enable: %w", err)
	}
	if err := browser.EnablePage(ctx); err != nil {
		return "", fmt.Errorf("portal: Page.enable: %w", err)
	}

	// 3. Seed auth state + headers BEFORE navigation so the first
	// request to the portal is already authenticated.
	if err := AssertAuthCookies(opts.Cookies, p.AuthCookieNames); err != nil {
		return "", err
	}
	if err := browser.SetCookies(ctx, opts.Cookies); err != nil {
		return "", fmt.Errorf("portal: setCookies: %w", err)
	}
	if err := browser.SetExtraHTTPHeaders(ctx, p.Headers); err != nil {
		return "", fmt.Errorf("portal: setExtraHTTPHeaders: %w", err)
	}

	// 4. Navigate.
	startURL := p.StartURL
	if startURL == "" {
		startURL = p.BaseURL
	}
	if err := browser.Navigate(ctx, startURL); err != nil {
		return "", fmt.Errorf("portal: navigate %s: %w", startURL, err)
	}
	if progress != nil {
		fmt.Fprintf(progress, "portal: navigated to %s\n", startURL)
	}

	pollEvery := opts.PollEvery
	if pollEvery <= 0 {
		pollEvery = 250 * time.Millisecond
	}

	// 5. Login check + ready predicate.
	if p.LoginCheck.Type != "" {
		if err := waitForPredicate(ctx, browser, p.LoginCheck, pollEvery, "login_check"); err != nil {
			return "", err
		}
	}
	if p.ReadyPredicate.Type != "" {
		if err := waitForPredicate(ctx, browser, p.ReadyPredicate, pollEvery, "ready_predicate"); err != nil {
			return "", err
		}
	}

	// 6. Type the prompt and submit. We bypass synthetic key events
	// (clean for every chat UI we've tested) and write directly into
	// the textarea + dispatch React's expected `input` event so
	// frameworks register the change.
	if err := typeAndSubmit(ctx, browser, p.Selectors.Input, p.Selectors.Submit, prompt); err != nil {
		return "", err
	}
	if progress != nil {
		fmt.Fprintln(progress, "portal: prompt submitted; waiting for response_done_predicate")
	}

	// 7. Wait for response done.
	if err := waitForPredicate(ctx, browser, p.ResponseDonePredicate, pollEvery, "response_done_predicate"); err != nil {
		return "", err
	}

	// 8. Extract the last response selector's innerText.
	respSelector := p.Selectors.Response
	if respSelector == "" {
		// Fall back to body innerText so the operator at least
		// gets *something* back even on a misconfigured portal.
		respSelector = "body"
	}
	expr := fmt.Sprintf(
		`(() => { const els = document.querySelectorAll(%s); const last = els[els.length-1]; return last ? last.innerText : ""; })()`,
		jsString(respSelector),
	)
	text, err := browser.EvaluateString(ctx, expr)
	if err != nil {
		return "", fmt.Errorf("portal: extract response: %w", err)
	}
	return text, nil
}

// typeAndSubmit fills the input selector with the prompt and either
// clicks the submit selector or — when none is configured — fires
// Enter via dispatchEvent. We inject value directly + dispatch
// 'input' so React/Vue/Svelte controlled components register the
// state change; otherwise our value gets clobbered on the next
// re-render.
func typeAndSubmit(ctx context.Context, browser *CDPClient, inputSel, submitSel, prompt string) error {
	tmpl := `
(() => {
  const el = document.querySelector(%s);
  if (!el) return { ok: false, reason: "input selector not found" };
  // Use the native setter so React's synthetic onChange picks it up.
  const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value')
    || Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
  if (setter) { setter.set.call(el, %s); }
  else { el.value = %s; }
  el.dispatchEvent(new Event('input', { bubbles: true }));
  el.dispatchEvent(new Event('change', { bubbles: true }));
  return { ok: true };
})()`
	res, err := browser.Evaluate(ctx, fmt.Sprintf(tmpl, jsString(inputSel), jsString(prompt), jsString(prompt)))
	if err != nil {
		return fmt.Errorf("portal: fill input: %w", err)
	}
	var fill struct {
		OK     bool   `json:"ok"`
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(res, &fill)
	if !fill.OK {
		return fmt.Errorf("portal: fill input: %s", fill.Reason)
	}

	if strings.TrimSpace(submitSel) != "" {
		clickTmpl := `(() => { const b = document.querySelector(%s); if (!b) return false; b.click(); return true; })()`
		ok, err := browser.EvaluateBool(ctx, fmt.Sprintf(clickTmpl, jsString(submitSel)))
		if err != nil {
			return fmt.Errorf("portal: click submit: %w", err)
		}
		if !ok {
			return fmt.Errorf("portal: submit selector %q did not match", submitSel)
		}
		return nil
	}

	// Fallback: dispatch Enter on the input. Works for textarea-only
	// chat UIs that submit via key handler without a button.
	enterTmpl := `(() => { const el = document.querySelector(%s); if (!el) return false; el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true })); return true; })()`
	if _, err := browser.EvaluateBool(ctx, fmt.Sprintf(enterTmpl, jsString(inputSel))); err != nil {
		return fmt.Errorf("portal: dispatch Enter: %w", err)
	}
	return nil
}

// waitForPredicate polls the given PortalPredicate until it returns
// truthy or ctx expires. Phase name is folded into the error so the
// operator sees which step failed.
func waitForPredicate(ctx context.Context, browser *CDPClient, pred config.PortalPredicate, every time.Duration, phase string) error {
	expr, err := predicateExpression(pred)
	if err != nil {
		return fmt.Errorf("portal: %s: %w", phase, err)
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		ok, err := browser.EvaluateBool(ctx, expr)
		if err == nil && ok {
			return nil
		}
		select {
		case <-ctx.Done():
			if err != nil {
				return fmt.Errorf("portal: %s timed out (last error: %v)", phase, err)
			}
			return fmt.Errorf("portal: %s timed out", phase)
		case <-t.C:
		}
	}
}

// predicateExpression converts a PortalPredicate into a JS expression
// the CDP Runtime.evaluate path can run. Bool coercion happens at the
// EvaluateBool wrapper.
func predicateExpression(p config.PortalPredicate) (string, error) {
	switch p.Type {
	case PredicateSelectorExists:
		return fmt.Sprintf(`!!document.querySelector(%s)`, jsString(p.Value)), nil
	case PredicateSelectorVisible:
		return fmt.Sprintf(`(() => { const el = document.querySelector(%s); return !!el && el.offsetParent !== null; })()`, jsString(p.Value)), nil
	case PredicateEvalTruthy:
		return p.Value, nil
	}
	return "", fmt.Errorf("unknown predicate type %q", p.Type)
}

// jsString returns a safe JS string literal for `s`. We only need
// to escape the four characters that break JSON strings since CDP
// frames embed JS source via Runtime.evaluate's "expression" field
// (already JSON-encoded by Send()).
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// obscuraWSPattern matches the "DevTools listening on ws://..." line
// that obscura prints to stderr at startup. We deliberately accept a
// few common phrasings (DevTools, listening, Listening) so a future
// Obscura release that tweaks the wording doesn't break us silently.
var obscuraWSPattern = regexp.MustCompile(`ws://\S+`)

// readObscuraWS scans stderr for the WebSocket URL with a hard
// deadline; falls back to the /json/version endpoint if the line
// pattern misses (some obscura builds suppress the banner).
func readObscuraWS(stderr io.ReadCloser, deadline time.Duration) (string, error) {
	type result struct {
		url string
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer stderr.Close()
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 64*1024), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if m := obscuraWSPattern.FindString(line); m != "" {
				ch <- result{url: m}
				return
			}
		}
		err := scanner.Err()
		if err == nil {
			err = errors.New("portal: obscura serve exited before printing a ws:// URL")
		}
		ch <- result{err: err}
	}()
	select {
	case r := <-ch:
		return r.url, r.err
	case <-time.After(deadline):
		return "", errors.New("portal: timed out waiting for obscura to print its ws:// URL — try `obscura serve --port 9222` manually to verify the binary works")
	}
}

// FetchWSFromVersionEndpoint is a fallback when obscura's stderr
// banner doesn't carry the URL — hits /json/version and pulls
// webSocketDebuggerUrl. Exposed for future use; not on the default
// path because the regex scan covers every Obscura release we've
// tested. The caller still needs a port; today we only call this
// when the operator uses --port <n> manually.
func FetchWSFromVersionEndpoint(ctx context.Context, host string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+host+"/json/version", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		WebSocketURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.WebSocketURL == "" {
		return "", errors.New("portal: /json/version returned no webSocketDebuggerUrl")
	}
	return body.WebSocketURL, nil
}
