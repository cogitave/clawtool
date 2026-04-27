// Package portal — Ask orchestrator (ADR-018).
//
// Spawns Obscura's CDP server, attaches via chromedp's
// RemoteAllocator, seeds cookies + extra headers, navigates,
// runs the saved login_check / ready_predicate, fills the input
// selector with the prompt, clicks submit (or dispatches Enter),
// polls response_done_predicate, returns the response selector's
// innerText. Per ADR-007 the heavy lifting (CDP wire, page
// lifecycle, JS evaluation) is chromedp's job — we orchestrate.
package portal

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	// Browser, when non-nil, replaces the obscura spawn + chromedp
	// connect path. Used by tests to drive Ask against a fake
	// Browser implementation; production callers leave this nil.
	Browser Browser
}

// Ask drives the portal `p` with `prompt` and returns the captured
// response text. Idempotent in the sense that each call spins a
// fresh browser context (no shared state) — except when
// opts.Browser is supplied, in which case Ask uses the provided
// Browser directly and is responsible only for orchestration.
func Ask(ctx context.Context, p config.PortalConfig, prompt string, opts AskOptions) (string, error) {
	if err := Validate(p.Name, p); err != nil {
		return "", err
	}
	Defaults(&p)

	timeout := time.Duration(p.TimeoutMs) * time.Millisecond
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if opts.Browser != nil {
		return runAskOnBrowser(ctx, opts.Browser, p, prompt, opts)
	}

	bin := opts.ObscuraBin
	if bin == "" {
		bin = "obscura"
	}
	if _, err := exec.LookPath(bin); err != nil {
		return "", fmt.Errorf("portal: %q binary not on PATH (see docs/browser-tools.md for install)", bin)
	}

	progress := opts.Stdout
	srv, err := startObscuraServer(ctx, bin, p.Browser.Stealth)
	if err != nil {
		return "", err
	}
	defer srv.close()
	if progress != nil {
		fmt.Fprintf(progress, "portal: obscura listening at %s\n", srv.wsURL)
	}

	session, err := NewRemoteBrowser(ctx, srv.wsURL)
	if err != nil {
		return "", err
	}
	defer session.Close()

	return runAskOnBrowser(ctx, session, p, prompt, opts)
}

// runAskOnBrowser is the pure orchestration: assumes the Browser
// is already connected, drives cookies → headers → navigate →
// login_check → ready_predicate → fill+submit → response_done →
// extract. Caller manages the browser's lifecycle.
func runAskOnBrowser(ctx context.Context, b Browser, p config.PortalConfig, prompt string, opts AskOptions) (string, error) {
	progress := opts.Stdout

	if err := AssertAuthCookies(opts.Cookies, p.AuthCookieNames); err != nil {
		return "", err
	}
	if err := b.SetCookies(ctx, opts.Cookies); err != nil {
		return "", fmt.Errorf("portal: setCookies: %w", err)
	}
	if err := b.SetExtraHTTPHeaders(ctx, p.Headers); err != nil {
		return "", fmt.Errorf("portal: setExtraHTTPHeaders: %w", err)
	}

	startURL := p.StartURL
	if startURL == "" {
		startURL = p.BaseURL
	}
	if err := b.Navigate(ctx, startURL); err != nil {
		return "", fmt.Errorf("portal: navigate %s: %w", startURL, err)
	}
	if progress != nil {
		fmt.Fprintf(progress, "portal: navigated to %s\n", startURL)
	}

	pollEvery := opts.PollEvery
	if pollEvery <= 0 {
		pollEvery = 250 * time.Millisecond
	}

	if p.LoginCheck.Type != "" {
		if err := waitForPredicate(ctx, b, p.LoginCheck, pollEvery, "login_check"); err != nil {
			return "", err
		}
	}
	if p.ReadyPredicate.Type != "" {
		if err := waitForPredicate(ctx, b, p.ReadyPredicate, pollEvery, "ready_predicate"); err != nil {
			return "", err
		}
	}

	if err := typeAndSubmit(ctx, b, p.Selectors.Input, p.Selectors.Submit, prompt); err != nil {
		return "", err
	}
	if progress != nil {
		fmt.Fprintln(progress, "portal: prompt submitted; waiting for response_done_predicate")
	}

	if err := waitForPredicate(ctx, b, p.ResponseDonePredicate, pollEvery, "response_done_predicate"); err != nil {
		return "", err
	}

	respSelector := p.Selectors.Response
	if respSelector == "" {
		respSelector = "body"
	}
	expr := fmt.Sprintf(
		`(() => { const els = document.querySelectorAll(%s); const last = els[els.length-1]; return last ? last.innerText : ""; })()`,
		jsString(respSelector),
	)
	return b.EvaluateString(ctx, expr)
}

// typeAndSubmit fills the input selector with the prompt then either
// clicks the submit selector or fires Enter via dispatchEvent.
// Native value setter + synthetic input/change events so React /
// Vue / Svelte controlled components register the change.
func typeAndSubmit(ctx context.Context, s Browser, inputSel, submitSel, prompt string) error {
	tmpl := `
(() => {
  const el = document.querySelector(%s);
  if (!el) return { ok: false, reason: "input selector not found" };
  const setter = Object.getOwnPropertyDescriptor(window.HTMLTextAreaElement.prototype, 'value')
    || Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value');
  if (setter) { setter.set.call(el, %s); }
  else { el.value = %s; }
  el.dispatchEvent(new Event('input', { bubbles: true }));
  el.dispatchEvent(new Event('change', { bubbles: true }));
  return { ok: true };
})()`
	var fill struct {
		OK     bool   `json:"ok"`
		Reason string `json:"reason"`
	}
	if err := s.Evaluate(ctx, fmt.Sprintf(tmpl, jsString(inputSel), jsString(prompt), jsString(prompt)), &fill); err != nil {
		return fmt.Errorf("portal: fill input: %w", err)
	}
	if !fill.OK {
		return fmt.Errorf("portal: fill input: %s", fill.Reason)
	}

	if strings.TrimSpace(submitSel) != "" {
		clickTmpl := `(() => { const b = document.querySelector(%s); if (!b) return false; b.click(); return true; })()`
		ok, err := s.EvaluateBool(ctx, fmt.Sprintf(clickTmpl, jsString(submitSel)))
		if err != nil {
			return fmt.Errorf("portal: click submit: %w", err)
		}
		if !ok {
			return fmt.Errorf("portal: submit selector %q did not match", submitSel)
		}
		return nil
	}

	enterTmpl := `(() => { const el = document.querySelector(%s); if (!el) return false; el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', bubbles: true })); return true; })()`
	if _, err := s.EvaluateBool(ctx, fmt.Sprintf(enterTmpl, jsString(inputSel))); err != nil {
		return fmt.Errorf("portal: dispatch Enter: %w", err)
	}
	return nil
}

func waitForPredicate(ctx context.Context, s Browser, pred config.PortalPredicate, every time.Duration, phase string) error {
	expr, err := predicateExpression(pred)
	if err != nil {
		return fmt.Errorf("portal: %s: %w", phase, err)
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		ok, evalErr := s.EvaluateBool(ctx, expr)
		if evalErr == nil && ok {
			return nil
		}
		select {
		case <-ctx.Done():
			if evalErr != nil {
				return fmt.Errorf("portal: %s timed out (last error: %v)", phase, evalErr)
			}
			return fmt.Errorf("portal: %s timed out", phase)
		case <-t.C:
		}
	}
}

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

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// ── obscura process management ────────────────────────────────────

type runningObscura struct {
	cmd   *exec.Cmd
	wsURL string
}

func (r *runningObscura) close() {
	if r == nil || r.cmd == nil {
		return
	}
	sysproc.KillGroup(r.cmd)
	_ = r.cmd.Wait()
}

func startObscuraServer(ctx context.Context, bin string, stealth bool) (*runningObscura, error) {
	args := []string{"serve", "--port", "0"}
	if stealth {
		args = append(args, "--stealth")
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("portal: stderr pipe: %w", err)
	}
	sysproc.ApplyGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("portal: start obscura serve: %w", err)
	}
	wsURL, err := readObscuraWS(stderr, 10*time.Second)
	if err != nil {
		sysproc.KillGroup(cmd)
		_ = cmd.Wait()
		return nil, err
	}
	return &runningObscura{cmd: cmd, wsURL: wsURL}, nil
}

var obscuraWSPattern = regexp.MustCompile(`ws://\S+`)

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
			if m := obscuraWSPattern.FindString(scanner.Text()); m != "" {
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
		return "", errors.New("portal: timed out waiting for obscura's ws:// URL — try `obscura serve --port 9222` manually to verify")
	}
}
