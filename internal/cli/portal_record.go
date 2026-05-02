// Package cli — `clawtool portal record <name>` (ADR-018, v1.1).
//
// Drives portal.Record (CDP-backed via the existing chromedp
// wrapper) and persists the captured Recording to a per-portal
// TOML file under ~/.config/clawtool/portals/<name>.toml. The
// per-portal-file shape is new in v1.1 — the legacy wizard writes
// stanzas into config.toml and that path keeps working; record
// uses its own directory so a recorded session can be inspected,
// edited, or deleted without rewriting the main config.
//
// Scope (per ADR-018 §Resolved 2026-05-02): the heuristic recorder
// + TOML persistence ship in v1.1; the streamed
// Network.responseReceived + DOM.documentUpdated listener stack
// for fingerprinted response-done predicates is deferred. Operator
// edits the captured TOML by hand to refine the predicate, same
// way the wizard's "custom" choice flows.
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/cogitave/clawtool/internal/atomicfile"
	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/portal"
	"github.com/cogitave/clawtool/internal/secrets"
	"github.com/cogitave/clawtool/internal/xdg"
)

// recordDeps mirrors wizardDeps — lets tests substitute the
// browser-spawn + stdin-confirm path without driving real Chrome.
type recordDeps struct {
	record       func(ctx context.Context, name, url string, opts portal.RecordOptions) (*portal.Recording, error)
	confirmLogin func(ctx context.Context) error
	saveCookies  func(scope string, cookies []portal.Cookie) error
	portalsDir   func() string
	stdoutLn     func(string)
	stderrLn     func(string)
}

// runPortalRecord parses `record <name> [--url <url>] [--force]`
// and dispatches into the dependency-injected recorder. Same
// argv-parse pattern as `add` and `remove` above.
func (a *App) runPortalRecord(argv []string) int {
	var name, url string
	force := false
	for i := 0; i < len(argv); i++ {
		v := argv[i]
		switch {
		case v == "--force":
			force = true
		case v == "--url":
			if i+1 >= len(argv) {
				fmt.Fprintln(a.Stderr, "usage: clawtool portal record <name> [--url <url>] [--force]")
				return 2
			}
			i++
			url = argv[i]
		case strings.HasPrefix(v, "--url="):
			url = strings.TrimPrefix(v, "--url=")
		default:
			if name != "" {
				fmt.Fprintln(a.Stderr, "usage: clawtool portal record <name> [--url <url>] [--force]")
				return 2
			}
			name = v
		}
	}
	if name == "" {
		fmt.Fprintln(a.Stderr, "usage: clawtool portal record <name> [--url <url>] [--force]")
		return 2
	}
	if url == "" {
		// Without a URL we have nothing to navigate to. Operator
		// may rerun with --url <…> or set CLAWTOOL_PORTAL_URL in
		// scripted contexts.
		if env := strings.TrimSpace(os.Getenv("CLAWTOOL_PORTAL_URL")); env != "" {
			url = env
		} else {
			fmt.Fprintln(a.Stderr, "clawtool portal record: --url <url> is required (or set CLAWTOOL_PORTAL_URL)")
			return 2
		}
	}
	d := defaultRecordDeps(a)
	return a.dispatchPortalErr("record", a.runPortalRecordWithDeps(context.Background(), name, url, force, d))
}

// defaultRecordDeps wires production dependencies: real
// portal.Record (which spawns chromedp), stdin Enter prompt, real
// secrets store path, real ~/.config/clawtool/portals dir.
func defaultRecordDeps(a *App) recordDeps {
	return recordDeps{
		record: func(ctx context.Context, name, url string, opts portal.RecordOptions) (*portal.Recording, error) {
			return portal.Record(ctx, name, url, opts)
		},
		confirmLogin: func(ctx context.Context) error {
			fmt.Fprintln(a.Stdout, "")
			fmt.Fprintln(a.Stdout, "Complete your login in the opened browser, then press Enter to capture cookies + selectors.")
			r := bufio.NewReader(os.Stdin)
			_, err := r.ReadString('\n')
			if err != nil {
				return fmt.Errorf("read stdin: %w", err)
			}
			return nil
		},
		saveCookies: func(scope string, cookies []portal.Cookie) error {
			return persistPortalCookies(scope, cookies)
		},
		portalsDir: defaultPortalsDir,
		stdoutLn:   func(s string) { fmt.Fprintln(a.Stdout, s) },
		stderrLn:   func(s string) { fmt.Fprintln(a.Stderr, s) },
	}
}

// defaultPortalsDir returns ~/.config/clawtool/portals — the v1.1
// per-portal-recording home. Honors XDG_CONFIG_HOME via xdg.ConfigDir().
func defaultPortalsDir() string {
	return filepath.Join(xdg.ConfigDir(), "portals")
}

// runPortalRecordWithDeps is the test-friendly entry point. Returns
// nil on success; error on validation / collision / Record failure.
func (a *App) runPortalRecordWithDeps(ctx context.Context, name, url string, force bool, d recordDeps) error {
	if err := assertPortalName(name); err != nil {
		return err
	}
	dir := d.portalsDir()
	dest := filepath.Join(dir, name+".toml")
	if !force {
		if _, err := os.Stat(dest); err == nil {
			return fmt.Errorf("portal %q already recorded at %s — re-run with --force to overwrite", name, dest)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat %s: %w", dest, err)
		}
	}

	// Run the recorder. confirmLogin is wired through opts so
	// tests can pass a no-op without leaking stdin into the
	// test harness.
	d.stdoutLn(fmt.Sprintf("▶ Recording portal %q from %s …", name, url))
	rec, err := d.record(ctx, name, url, portal.RecordOptions{
		ConfirmLogin: d.confirmLogin,
	})
	if err != nil {
		return fmt.Errorf("record: %w", err)
	}

	cfg := rec.ToPortalConfig()
	if err := portal.Validate(name, cfg); err != nil {
		return fmt.Errorf("captured recording invalid: %w", err)
	}

	if err := writeRecordingTOML(dest, name, cfg); err != nil {
		return fmt.Errorf("write %s: %w", dest, err)
	}

	if d.saveCookies != nil && len(rec.Cookies) > 0 {
		if err := d.saveCookies(cfg.SecretsScope, rec.Cookies); err != nil {
			return fmt.Errorf("save cookies: %w", err)
		}
	}

	d.stdoutLn(fmt.Sprintf("✓ portal %q recorded → %s", name, dest))
	d.stdoutLn(fmt.Sprintf("  cookies stored under [scopes.%q] in %s", cfg.SecretsScope, secrets.DefaultPath()))
	d.stdoutLn("  edit the TOML to refine selectors / response_done_predicate before `clawtool portal ask`.")
	return nil
}

// writeRecordingTOML serialises a single PortalConfig to the
// per-portal file. We wrap the stanza in a Config{Portals:{…}} so
// the file can be merged into the canonical config.toml later via
// `clawtool portal add --manual` — same TOML shape as the
// wizard-produced template.
func writeRecordingTOML(path, name string, p config.PortalConfig) error {
	patch := config.Config{
		Portals: map[string]config.PortalConfig{name: p},
	}
	body, err := toml.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	// 0o600 because portal headers can carry tokens; mirrors the
	// secrets.toml + config.toml mode (writeConfigAtomic).
	return atomicfile.WriteFileMkdir(path, body, 0o600, 0o700)
}
