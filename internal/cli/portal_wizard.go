// Package cli — `clawtool portal add` interactive wizard
// (ADR-018, v0.16.3).
//
// Rebuilt on top of the chromedp-backed BrowserSession (ADR-007).
// Spawns the user's installed Chrome with --headless=false + a temp
// profile, waits for them to log in (optionally with a copy/paste
// prompt for the Claude in Chrome side-panel), pulls cookies via
// Network.getAllCookies, collects the three CSS selectors + a
// "response done" predicate template, and writes config.toml +
// secrets.toml.
//
// Per ADR-017 we never wrap claude-in-chrome — the wizard generates
// a plain-text prompt the operator can paste. clawtool stays
// MCP-server-free for the wizard transport.
package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/portal"
	"github.com/cogitave/clawtool/internal/secrets"
)

// wizardDeps lets tests substitute the side-effecting pieces. Same
// pattern as internal/cli/onboard.go's onboardDeps.
type wizardDeps struct {
	openBrowser func(ctx context.Context, opts portal.ExecOptions) (portalBrowser, error)
	runForm     func(*huh.Form) error
	stdoutLn    func(string)
	stderrLn    func(string)
	saveConfig  func(name string, p config.PortalConfig) error
	saveCookies func(scope string, cookies []portal.Cookie) error
}

// portalBrowser is the wizard-shaped subset of portal.BrowserSession.
// Pulling it through an interface makes the wizard table-testable
// without a real Chrome binary.
type portalBrowser interface {
	Navigate(ctx context.Context, url string) error
	Cookies(ctx context.Context) ([]portal.Cookie, error)
	Close()
}

// runPortalAddWizard is the entry point invoked from
// `runPortal("add", argv)`. The legacy `--manual` flag bypasses the
// wizard for the editor-driven path.
func (a *App) runPortalAddWizard(name string) error {
	d := wizardDeps{
		openBrowser: func(ctx context.Context, opts portal.ExecOptions) (portalBrowser, error) {
			return portal.NewExecBrowser(ctx, opts)
		},
		runForm:  func(f *huh.Form) error { return f.Run() },
		stdoutLn: func(s string) { fmt.Fprintln(a.Stdout, s) },
		stderrLn: func(s string) { fmt.Fprintln(a.Stderr, s) },
		saveConfig: func(n string, p config.PortalConfig) error {
			return persistPortalConfig(n, p)
		},
		saveCookies: func(scope string, cookies []portal.Cookie) error {
			return persistPortalCookies(scope, cookies)
		},
	}
	return runPortalAddWizardWithDeps(context.Background(), name, d)
}

// wizardState is the running scratch buffer. Tests inspect this
// after a happy-path run to confirm the produced PortalConfig
// shape via assemblePortalConfig.
type wizardState struct {
	Name             string
	URL              string
	InputSelector    string
	SubmitSelector   string
	ResponseSelector string
	PredicateChoice  string
	UseStealth       bool
	OpenInChrome     bool
}

func runPortalAddWizardWithDeps(ctx context.Context, name string, d wizardDeps) error {
	if err := assertPortalName(name); err != nil {
		return err
	}
	state := wizardState{Name: name, OpenInChrome: true, UseStealth: true}

	// ─ Step 1: URL + intro ───────────────────────────────────
	intro := huh.NewForm(huh.NewGroup(
		huh.NewNote().
			Title("clawtool portal add — interactive wizard").
			Description("This wizard opens your installed Chrome with a clean temp\n"+
				"profile so your normal login state stays untouched. After\n"+
				"Chrome opens, log in to the portal as you normally would.\n"+
				"clawtool watches via the DevTools Protocol and reads cookies\n"+
				"once you say you're done. Runtime requests use Obscura\n"+
				"headless. ADR-017 / ADR-018."),
		huh.NewInput().
			Title("Portal URL").
			Description("e.g. https://chat.deepseek.com/").
			Placeholder("https://...").
			Value(&state.URL).
			Validate(func(s string) error {
				s = strings.TrimSpace(s)
				if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
					return errors.New("URL must start with http:// or https://")
				}
				return nil
			}),
		huh.NewConfirm().
			Title("Open Chrome now?").
			Description("clawtool spawns Chrome with a temp profile. Log in normally; clawtool reads cookies via Network.getAllCookies after you confirm.").
			Affirmative("Yes, launch Chrome").
			Negative("Cancel").
			Value(&state.OpenInChrome),
	))
	if err := d.runForm(intro); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return errors.New("aborted")
		}
		return err
	}
	if !state.OpenInChrome {
		return errors.New("aborted before Chrome launch")
	}
	state.URL = strings.TrimSpace(state.URL)

	// ─ Step 2: launch Chrome (headless=false), navigate ──────
	d.stdoutLn("▶ Detecting Chrome / Chromium / Brave / Edge…")
	browser, err := d.openBrowser(ctx, portal.ExecOptions{Headless: false, StartURL: state.URL})
	if err != nil {
		return err
	}
	defer browser.Close()
	d.stdoutLn(fmt.Sprintf("▶ Chrome opened at %s.", state.URL))

	// ─ Step 3: claude-in-chrome assist prompt + login wait ───
	hint := buildClaudeInChromeHint(state.URL)
	d.stdoutLn("")
	d.stdoutLn("If you have the Claude in Chrome extension installed, paste the following")
	d.stdoutLn("into the side panel for assisted login + selector hints. Otherwise, log in")
	d.stdoutLn("manually in the Chrome window.")
	d.stdoutLn("")
	d.stdoutLn("─── Claude in Chrome prompt ───")
	d.stdoutLn(hint)
	d.stdoutLn("─── end ───")
	d.stdoutLn("")

	var loginConfirm bool
	loginGate := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Logged in?").
			Description("Confirm only when you can see the chat textarea — clawtool will read cookies the moment you say yes.").
			Affirmative("Yes, capture cookies").
			Negative("Cancel").
			Value(&loginConfirm),
	))
	if err := d.runForm(loginGate); err != nil {
		return err
	}
	if !loginConfirm {
		return errors.New("aborted before login")
	}

	// ─ Step 4: cookie capture + auth-name auto-detect ────────
	cookies, err := browser.Cookies(ctx)
	if err != nil {
		return fmt.Errorf("getAllCookies: %w", err)
	}
	host := hostFromURL(state.URL)
	cookies = filterCookiesForHost(cookies, host)
	if len(cookies) == 0 {
		return fmt.Errorf("no cookies captured for %s — did the login complete?", host)
	}
	authNames := autoDetectAuthCookieNames(cookies)
	d.stdoutLn(fmt.Sprintf("▶ Captured %d cookies; auto-detected auth names: %s", len(cookies), strings.Join(authNames, ", ")))

	// ─ Step 5: selectors + predicate ─────────────────────────
	selectors := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Input selector").
			Description("CSS selector for the message input. Right-click the textarea in Chrome → Inspect → Copy → Copy selector. (e.g. `textarea` works for many sites.)").
			Value(&state.InputSelector).
			Validate(nonEmpty),
		huh.NewInput().
			Title("Submit selector (optional)").
			Description("CSS selector for the send button. Leave empty to dispatch Enter on the input element instead.").
			Value(&state.SubmitSelector),
		huh.NewInput().
			Title("Response selector").
			Description("CSS selector that wraps assistant messages. Send a test message in Chrome, right-click the reply → Inspect → Copy → Copy selector. Match the LATEST reply when there are many.").
			Value(&state.ResponseSelector).
			Validate(nonEmpty),
		huh.NewSelect[string]().
			Title("How does the page tell you generation finished?").
			Options(
				huh.NewOption("Stop button disappears (most chat UIs)", "stop_gone"),
				huh.NewOption("Input becomes empty / re-enabled", "input_cleared"),
				huh.NewOption("Custom JS expression (edit later)", "custom"),
			).
			Value(&state.PredicateChoice),
	))
	if err := d.runForm(selectors); err != nil {
		return err
	}
	state.InputSelector = strings.TrimSpace(state.InputSelector)
	state.SubmitSelector = strings.TrimSpace(state.SubmitSelector)
	state.ResponseSelector = strings.TrimSpace(state.ResponseSelector)

	// ─ Step 6: assemble + persist ───────────────────────────
	cfg := assemblePortalConfig(state, authNames)
	if err := portal.Validate(state.Name, cfg); err != nil {
		return fmt.Errorf("assembled config invalid: %w", err)
	}
	if err := d.saveCookies(cfg.SecretsScope, cookies); err != nil {
		return fmt.Errorf("save cookies: %w", err)
	}
	if err := d.saveConfig(state.Name, cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	d.stdoutLn("")
	d.stdoutLn(fmt.Sprintf("✓ portal %q saved.", state.Name))
	d.stdoutLn(fmt.Sprintf("  config.toml: [portals.%s]", state.Name))
	d.stdoutLn(fmt.Sprintf("  secrets.toml: [scopes.%q] cookies_json=…", cfg.SecretsScope))
	d.stdoutLn("")
	d.stdoutLn(fmt.Sprintf("Next: clawtool portal ask %s \"hello\"", state.Name))
	d.stdoutLn("(Make sure obscura is installed — see docs/browser-tools.md.)")
	return nil
}

// ── helpers ──────────────────────────────────────────────────────

func nonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("required")
	}
	return nil
}

func buildClaudeInChromeHint(url string) string {
	return fmt.Sprintf(`Open %s. If a login form appears, wait for me (the user) to type
my credentials manually — do NOT type passwords for me. Once I'm
logged in and the chat textarea is visible, do these three things:
  1. Click the message input box once.
  2. Tell me the unique CSS selector that matches it.
  3. Send the message "ping" once. After the assistant replies, tell
     me the CSS selector that wraps the assistant's reply (latest only).
Format the selectors in a single fenced block I can paste back to
the terminal.`, url)
}

func filterCookiesForHost(in []portal.Cookie, host string) []portal.Cookie {
	host = strings.TrimPrefix(strings.ToLower(host), ".")
	out := make([]portal.Cookie, 0, len(in))
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

func autoDetectAuthCookieNames(cookies []portal.Cookie) []string {
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

func hostFromURL(u string) string {
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	if i := strings.IndexAny(u, "/?#"); i > 0 {
		u = u[:i]
	}
	return strings.ToLower(u)
}

func assemblePortalConfig(s wizardState, authNames []string) config.PortalConfig {
	return config.PortalConfig{
		Name:            s.Name,
		BaseURL:         s.URL,
		StartURL:        s.URL,
		SecretsScope:    portal.SecretsScopePrefix + s.Name,
		AuthCookieNames: authNames,
		TimeoutMs:       portal.DefaultTimeoutMs,
		LoginCheck: config.PortalPredicate{
			Type:  portal.PredicateSelectorVisible,
			Value: s.InputSelector,
		},
		ReadyPredicate: config.PortalPredicate{
			Type:  portal.PredicateSelectorVisible,
			Value: s.InputSelector,
		},
		Selectors: config.PortalSelectors{
			Input:    s.InputSelector,
			Submit:   s.SubmitSelector,
			Response: s.ResponseSelector,
		},
		ResponseDonePredicate: predicateForChoice(s.PredicateChoice, s.InputSelector),
		Browser: config.PortalBrowserSettings{
			Stealth:        s.UseStealth,
			ViewportWidth:  portal.DefaultViewportWidth,
			ViewportHeight: portal.DefaultViewportHeight,
			Locale:         portal.DefaultLocale,
		},
	}
}

func predicateForChoice(choice, inputSelector string) config.PortalPredicate {
	switch choice {
	case "stop_gone":
		return config.PortalPredicate{
			Type:  portal.PredicateEvalTruthy,
			Value: `(() => { const stop = document.querySelector('button[aria-label*="Stop"], button[data-testid*="stop"]'); return !stop; })()`,
		}
	case "input_cleared":
		return config.PortalPredicate{
			Type: portal.PredicateEvalTruthy,
			Value: fmt.Sprintf(
				`(() => { const el = document.querySelector(%q); return el && !el.disabled && (el.value === '' || el.value == null); })()`,
				inputSelector),
		}
	}
	return config.PortalPredicate{
		Type:  portal.PredicateEvalTruthy,
		Value: `(() => { return !document.querySelector('button[aria-label*="Stop"], [data-testid*="stop"]'); })()`,
	}
}

func persistPortalConfig(name string, p config.PortalConfig) error {
	patch := config.Config{Portals: map[string]config.PortalConfig{name: p}}
	body, err := config.MarshalForAppend(patch)
	if err != nil {
		return err
	}
	return config.AppendBytes(config.DefaultPath(), body)
}

func persistPortalCookies(scope string, cookies []portal.Cookie) error {
	store, err := secrets.LoadOrEmpty(secrets.DefaultPath())
	if err != nil {
		return err
	}
	jsonBody, err := portal.MarshalCookies(cookies)
	if err != nil {
		return err
	}
	store.Set(scope, "cookies_json", jsonBody)
	return store.Save(secrets.DefaultPath())
}
