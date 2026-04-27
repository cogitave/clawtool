// Package cli — `clawtool portal` subcommand surface (ADR-018).
//
// Read-only + persistence operations land in v0.16.1. The interactive
// `ask` flow that drives Obscura over CDP arrives in v0.16.2; today
// it returns a clear "deferred" error so the surface is discoverable
// before the engine ships.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/portal"
	"github.com/cogitave/clawtool/internal/secrets"
)

const portalUsage = `Usage:
  clawtool portal list                  List configured portals + auth-readiness.
  clawtool portal which                 Show the sticky-default portal.
  clawtool portal use <name>            Set the sticky default for 'portal ask'.
  clawtool portal unset                 Clear the sticky default.
  clawtool portal add <name>            Interactive wizard: opens Chrome with a
                                        clean temp profile, you log in, clawtool
                                        captures cookies via the DevTools Protocol
                                        (Network.getAllCookies), you supply three
                                        CSS selectors + a "response done" template,
                                        result lands in config.toml + secrets.toml.
  clawtool portal add --manual <name>   Legacy editor-driven path: opens $EDITOR
                                        with a TOML template; result is appended
                                        to ~/.config/clawtool/config.toml.
  clawtool portal remove <name>         Remove the [portals.<name>] block.
  clawtool portal ask [<name>] "<prompt>"
                                        Drive the saved web-UI flow with the
                                        prompt and stream the response.
                                        (CDP driver lands in v0.16.2.)

Portals are a Tool surface (ADR-017). They live next to [agents.X] /
[sources.X] in config.toml; cookie material lives in secrets.toml under
[scopes."portal.<name>"]. See docs/portals.md for the chat.deepseek.com
worked example.
`

func (a *App) runPortal(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(a.Stderr, portalUsage)
		return 2
	}
	switch argv[0] {
	case "list":
		return a.dispatchPortalErr("list", a.PortalList())
	case "which":
		return a.dispatchPortalErr("which", a.PortalWhich())
	case "use":
		if len(argv) != 2 {
			fmt.Fprintln(a.Stderr, "usage: clawtool portal use <name>")
			return 2
		}
		return a.dispatchPortalErr("use", a.PortalUse(argv[1]))
	case "unset":
		return a.dispatchPortalErr("unset", a.PortalUnset())
	case "add":
		// Default flow: interactive wizard (Chrome+CDP, captures
		// cookies + selectors live). --manual flag falls back to
		// the v0.16.1 $EDITOR-driven TOML template.
		manual := false
		var name string
		for _, v := range argv[1:] {
			switch v {
			case "--manual":
				manual = true
			default:
				if name != "" {
					fmt.Fprintln(a.Stderr, "usage: clawtool portal add [--manual] <name>")
					return 2
				}
				name = v
			}
		}
		if name == "" {
			fmt.Fprintln(a.Stderr, "usage: clawtool portal add [--manual] <name>")
			return 2
		}
		if manual {
			return a.dispatchPortalErr("add", a.PortalAdd(name))
		}
		return a.dispatchPortalErr("add", a.runPortalAddWizard(name))
	case "remove":
		if len(argv) != 2 {
			fmt.Fprintln(a.Stderr, "usage: clawtool portal remove <name>")
			return 2
		}
		return a.dispatchPortalErr("remove", a.PortalRemove(argv[1]))
	case "ask":
		if err := a.PortalAsk(argv[1:]); err != nil {
			fmt.Fprintf(a.Stderr, "clawtool portal ask: %v\n", err)
			return 1
		}
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(a.Stdout, portalUsage)
		return 0
	default:
		fmt.Fprintf(a.Stderr, "clawtool portal: unknown subcommand %q\n\n%s", argv[0], portalUsage)
		return 2
	}
}

func (a *App) dispatchPortalErr(verb string, err error) int {
	if err == nil {
		return 0
	}
	fmt.Fprintf(a.Stderr, "clawtool portal %s: %v\n", verb, err)
	return 1
}

// loadPortals returns config.Portals (or nil) — used by every
// subcommand. We always go through config.LoadOrDefault so a
// missing config file produces an empty map, not a crash.
func (a *App) loadPortals() (map[string]config.PortalConfig, string, error) {
	path := config.DefaultPath()
	cfg, err := config.LoadOrDefault(path)
	if err != nil {
		return nil, path, err
	}
	return cfg.Portals, path, nil
}

// PortalList prints the configured portals one per line — same
// shape as `clawtool send --list` so the operator sees both
// surfaces consistently.
func (a *App) PortalList() error {
	portals, _, err := a.loadPortals()
	if err != nil {
		return err
	}
	if len(portals) == 0 {
		fmt.Fprintln(a.Stdout, "(no portals configured — run `clawtool portal add <name>` to add one)")
		return nil
	}
	cfg := config.Config{Portals: portals}
	fmt.Fprintf(a.Stdout, "%-22s %-46s %s\n", "NAME", "BASE URL", "AUTH COOKIES")
	for _, name := range portal.Names(cfg) {
		p := portals[name]
		auth := strings.Join(p.AuthCookieNames, ",")
		if auth == "" {
			auth = "(none declared)"
		}
		fmt.Fprintf(a.Stdout, "%-22s %-46s %s\n", name, p.BaseURL, auth)
	}
	return nil
}

// PortalWhich resolves the sticky-default portal. Same precedence
// chain as the agent sticky default (env > sticky file > single-
// configured fallback).
func (a *App) PortalWhich() error {
	portals, _, err := a.loadPortals()
	if err != nil {
		return err
	}
	if len(portals) == 0 {
		return errors.New("no portals configured")
	}
	if env := strings.TrimSpace(os.Getenv("CLAWTOOL_PORTAL")); env != "" {
		if _, ok := portals[env]; !ok {
			return fmt.Errorf("CLAWTOOL_PORTAL=%q not in registry", env)
		}
		fmt.Fprintf(a.Stdout, "%s (env)\n", env)
		return nil
	}
	if name := readPortalSticky(); name != "" {
		if _, ok := portals[name]; !ok {
			return fmt.Errorf("sticky portal %q is not in registry; run `clawtool portal use <name>` to refresh", name)
		}
		fmt.Fprintf(a.Stdout, "%s (sticky)\n", name)
		return nil
	}
	if len(portals) == 1 {
		for n := range portals {
			fmt.Fprintf(a.Stdout, "%s (single configured)\n", n)
			return nil
		}
	}
	return errors.New("portal ambiguous — run `clawtool portal use <name>` or set CLAWTOOL_PORTAL")
}

// PortalUse persists the sticky default for `clawtool portal ask`.
func (a *App) PortalUse(name string) error {
	name = strings.TrimSpace(name)
	portals, _, err := a.loadPortals()
	if err != nil {
		return err
	}
	if _, ok := portals[name]; !ok {
		return fmt.Errorf("portal %q not in registry — run `clawtool portal list`", name)
	}
	if err := writePortalSticky(name); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "✓ active portal → %s\n", name)
	return nil
}

// PortalUnset removes the sticky-default file. Idempotent.
func (a *App) PortalUnset() error {
	if err := clearPortalSticky(); err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, "✓ sticky portal cleared")
	return nil
}

// PortalAdd opens $EDITOR with a TOML template for the named
// portal. On save we validate the parsed stanza and append it to
// config.toml. The validation refuses anything that wouldn't drive
// an Ask flow successfully, so a fat-finger landing in config never
// reaches the dispatch path.
func (a *App) PortalAdd(name string) error {
	if err := assertPortalName(name); err != nil {
		return err
	}
	portals, cfgPath, err := a.loadPortals()
	if err != nil {
		return err
	}
	if _, ok := portals[name]; ok {
		return fmt.Errorf("portal %q already exists in %s — `clawtool portal remove %s` first", name, cfgPath, name)
	}

	tmpl := portalTemplate(name)
	tmp, err := os.CreateTemp("", "clawtool-portal-*.toml")
	if err != nil {
		return fmt.Errorf("scratch file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(tmpl); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	if err := openInEditor(tmp.Name()); err != nil {
		return fmt.Errorf("$EDITOR: %w", err)
	}

	body, err := os.ReadFile(tmp.Name())
	if err != nil {
		return err
	}
	parsed, err := config.LoadFromBytes(body)
	if err != nil {
		return fmt.Errorf("parse edited template: %w", err)
	}
	if len(parsed.Portals) == 0 {
		return errors.New("no [portals.<name>] block found in the edited template; aborting")
	}
	for n, p := range parsed.Portals {
		if n != name {
			return fmt.Errorf("template defined portal %q but you ran add %q — pick one", n, name)
		}
		if err := portal.Validate(n, p); err != nil {
			return err
		}
	}
	if err := config.AppendBytes(cfgPath, body); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "✓ portal %s added in %s\n", name, cfgPath)
	fmt.Fprintf(a.Stdout, "  next: store cookies under [scopes.%q] in secrets.toml — see docs/portals.md\n", portal.SecretsScopePrefix+name)
	return nil
}

// PortalRemove rewrites config.toml without the [portals.<name>]
// stanza. Cookies in secrets.toml are left in place so a temporary
// remove-then-re-add doesn't lose the export. Operators clean
// secrets manually when they want a true uninstall.
func (a *App) PortalRemove(name string) error {
	portals, cfgPath, err := a.loadPortals()
	if err != nil {
		return err
	}
	if _, ok := portals[name]; !ok {
		return fmt.Errorf("portal %q not found", name)
	}
	if err := config.RemovePortalBlock(cfgPath, name); err != nil {
		return err
	}
	fmt.Fprintf(a.Stdout, "✓ portal %s removed (cookies under [scopes.%q] left in secrets.toml — clean manually if no longer needed)\n", name, portal.SecretsScopePrefix+name)
	return nil
}

// PortalAsk is the deferred-feature placeholder. Validates the
// resolved portal so the operator gets the same diagnostics they
// will get in v0.16.2, then surfaces the deferred error.
func (a *App) PortalAsk(argv []string) error {
	if len(argv) == 0 {
		return errors.New(`usage: clawtool portal ask [<name>] "<prompt>"`)
	}
	var name, prompt string
	if len(argv) == 1 {
		prompt = argv[0]
	} else {
		name = argv[0]
		prompt = strings.Join(argv[1:], " ")
	}
	if name == "" {
		if env := strings.TrimSpace(os.Getenv("CLAWTOOL_PORTAL")); env != "" {
			name = env
		} else if s := readPortalSticky(); s != "" {
			name = s
		}
	}
	portals, _, err := a.loadPortals()
	if err != nil {
		return err
	}
	if name == "" {
		if len(portals) == 1 {
			for n := range portals {
				name = n
				break
			}
		} else {
			return errors.New("portal ambiguous — pass a <name> or run `clawtool portal use <name>`")
		}
	}
	p, ok := portals[name]
	if !ok {
		return fmt.Errorf("portal %q not in registry", name)
	}
	if err := portal.Validate(name, p); err != nil {
		return err
	}
	store, err := secrets.LoadOrEmpty(secrets.DefaultPath())
	if err != nil {
		return fmt.Errorf("portal ask: load secrets: %w", err)
	}
	rawCookies, _ := store.Get(p.SecretsScope, "cookies_json")
	cookies, err := portal.ParseCookies(rawCookies)
	if err != nil {
		return fmt.Errorf("portal ask: %w", err)
	}
	resp, err := portal.Ask(context.Background(), p, prompt, portal.AskOptions{
		Cookies: cookies,
		Stdout:  a.Stderr, // progress lines on stderr; the answer goes to stdout
	})
	if err != nil {
		return err
	}
	fmt.Fprintln(a.Stdout, resp)
	return nil
}

// ── helpers ────────────────────────────────────────────────────────

func assertPortalName(n string) error {
	n = strings.TrimSpace(n)
	if n == "" {
		return errors.New("portal name is required")
	}
	for _, r := range n {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return fmt.Errorf("portal name %q must match [a-z0-9_-]+", n)
		}
	}
	return nil
}

func portalTemplate(name string) string {
	return fmt.Sprintf(`# clawtool portal stanza — see ADR-018 + docs/portals.md.
#
# Save this file in the editor when you're done; clawtool validates
# the result and appends it to ~/.config/clawtool/config.toml.

[portals.%s]
name = "%s"
base_url = "https://example.com/"
start_url = "https://example.com/"
secrets_scope = "portal.%s"
auth_cookie_names = ["sessionid"]
timeout_ms = 180000

[portals.%s.login_check]
type = "selector_exists"
value = "textarea"

[portals.%s.ready_predicate]
type = "selector_visible"
value = "textarea"

[portals.%s.selectors]
input = "textarea"
submit = "button[type='submit']"
response = "div[class*='message']"

[portals.%s.response_done_predicate]
type = "eval_truthy"
value = """
(() => {
  const stop = document.querySelector('button[aria-label*="Stop"], button[data-testid*="stop"]');
  return !stop;
})()
"""

[portals.%s.headers]
Accept-Language = "en-US,en;q=0.9"

[portals.%s.browser]
stealth = true
viewport_width = 1440
viewport_height = 1000
locale = "en-US"
`, name, name, name, name, name, name, name, name, name)
}

func openInEditor(path string) error {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// portalStickyFile resolves the path; honors XDG_CONFIG_HOME like
// the agent sticky default does.
func portalStickyFile() string {
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(x, "clawtool", "active_portal")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "active_portal"
	}
	return filepath.Join(home, ".config", "clawtool", "active_portal")
}

func readPortalSticky() string {
	b, err := os.ReadFile(portalStickyFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func writePortalSticky(name string) error {
	path := portalStickyFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.TrimSpace(name)+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func clearPortalSticky() error {
	err := os.Remove(portalStickyFile())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
