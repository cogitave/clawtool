// Package cli — `clawtool doctor` is the one-command diagnostic.
// It surveys binary / agents / sources / recipes and prints a
// colour-coded checklist with suggested fix commands. Pure
// composition of existing internal helpers — no new deps.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/daemon"
	"github.com/cogitave/clawtool/internal/secrets"
	"github.com/cogitave/clawtool/internal/setup"
	"github.com/cogitave/clawtool/internal/version"
)

// doctorReport accumulates per-section findings so the final
// summary line can count warnings + criticals without re-walking
// the printed output.
type doctorReport struct {
	warnings int
	critical int
}

func (r *doctorReport) ok(w io.Writer, msg string)   { fmt.Fprintf(w, "  ✓ %s\n", msg) }
func (r *doctorReport) info(w io.Writer, msg string) { fmt.Fprintf(w, "  · %s\n", msg) }
func (r *doctorReport) warn(w io.Writer, msg, fix string) {
	r.warnings++
	fmt.Fprintf(w, "  ⚠ %s\n", msg)
	if fix != "" {
		fmt.Fprintf(w, "      → %s\n", fix)
	}
}
func (r *doctorReport) fail(w io.Writer, msg, fix string) {
	r.critical++
	fmt.Fprintf(w, "  ✗ %s\n", msg)
	if fix != "" {
		fmt.Fprintf(w, "      → %s\n", fix)
	}
}

// runDoctor is the dispatcher entry. Invoked from cli.go's Run as
// `case "doctor":`. argv carries any flags (none defined yet —
// reserved for `--json` and `--quiet` in v0.10.x).
func (a *App) runDoctor(_ []string) int {
	rep := &doctorReport{}
	w := a.Stdout

	fmt.Fprintf(w, "clawtool doctor — %s\n\n", version.Version)

	a.doctorBinary(w, rep)
	a.doctorConfig(w, rep)
	a.doctorDaemon(w, rep)
	a.doctorAgents(w, rep)
	a.doctorSources(w, rep)
	a.doctorRecipes(w, rep)

	a.doctorSummary(w, rep)
	if rep.critical > 0 {
		return 1
	}
	return 0
}

func (a *App) doctorBinary(w io.Writer, rep *doctorReport) {
	fmt.Fprintln(w, "[binary]")
	exe, err := os.Executable()
	if err == nil {
		rep.ok(w, fmt.Sprintf("running from %s (version %s)", exe, version.Version))
	} else {
		rep.warn(w, "could not resolve own executable path: "+err.Error(), "")
	}
	// Surface a pending upstream release if we know about one.
	// Quiet on failure: pre-release projects + offline runs both
	// hit non-OK paths; an unprompted user-facing dump is noise.
	upd := version.CheckForUpdate(context.Background())
	switch {
	case upd.HasUpdate:
		rep.warn(w,
			fmt.Sprintf("new release available: %s (you have %s)", upd.Latest, upd.Current),
			"curl -sSL https://raw.githubusercontent.com/cogitave/clawtool/main/install.sh | sh")
	case upd.Latest != "":
		rep.ok(w, fmt.Sprintf("up to date (latest release: %s)", upd.Latest))
	}
	fmt.Fprintln(w)
}

func (a *App) doctorConfig(w io.Writer, rep *doctorReport) {
	fmt.Fprintln(w, "[config]")
	cfgPath := a.Path()
	if _, err := os.Stat(cfgPath); err != nil {
		if os.IsNotExist(err) {
			rep.info(w, fmt.Sprintf("%s not found (defaults in use)", cfgPath))
		} else {
			rep.warn(w, fmt.Sprintf("stat %s: %v", cfgPath, err), "")
		}
	} else {
		rep.ok(w, fmt.Sprintf("%s present", cfgPath))
	}

	secPath := a.SecretsPath()
	if info, err := os.Stat(secPath); err == nil {
		mode := info.Mode().Perm()
		if mode != 0o600 {
			rep.warn(w,
				fmt.Sprintf("%s has mode %o (should be 0600)", secPath, mode),
				fmt.Sprintf("chmod 600 %s", secPath))
		} else {
			rep.ok(w, fmt.Sprintf("%s present (mode 0600)", secPath))
		}
	} else if os.IsNotExist(err) {
		rep.info(w, fmt.Sprintf("%s not found (no secrets configured)", secPath))
	} else {
		rep.warn(w, fmt.Sprintf("stat %s: %v", secPath, err), "")
	}
	fmt.Fprintln(w)
}

// doctorDaemon surfaces the persistent shared-MCP daemon's state
// (audit/UX gap from #193). The daemon backs every host's MCP claim
// in shared-http mode; if it's stale or missing, every codex/gemini
// dispatch breaks and the operator gets opaque MCP errors.
func (a *App) doctorDaemon(w io.Writer, rep *doctorReport) {
	fmt.Fprintln(w, "[daemon]")
	st, err := daemon.ReadState()
	if err != nil {
		rep.warn(w, "read daemon state: "+err.Error(), "")
		fmt.Fprintln(w)
		return
	}
	if st == nil {
		rep.info(w, "not running (no state file)")
		fmt.Fprintln(w, "      → clawtool daemon start")
		fmt.Fprintln(w)
		return
	}
	if daemon.IsRunning(st) {
		rep.ok(w, fmt.Sprintf("running pid %d at %s", st.PID, st.URL()))
	} else {
		rep.warn(w,
			fmt.Sprintf("state file claims pid %d / port %d but probe failed (stale)", st.PID, st.Port),
			"clawtool daemon restart",
		)
	}
	fmt.Fprintln(w)
}

func (a *App) doctorAgents(w io.Writer, rep *doctorReport) {
	fmt.Fprintln(w, "[agents]")
	if len(agents.Registry) == 0 {
		rep.info(w, "no agent adapters registered (build configuration issue)")
		fmt.Fprintln(w)
		return
	}
	for _, ad := range agents.Registry {
		s, err := ad.Status()
		if err != nil {
			rep.warn(w, fmt.Sprintf("%s: %v", ad.Name(), err), "")
			continue
		}
		switch {
		case s.Detected && s.Claimed:
			rep.ok(w, fmt.Sprintf("%s — detected, claimed (%d native tool(s) disabled)", ad.Name(), len(s.DisabledByUs)))
		case s.Detected && !s.Claimed:
			rep.info(w, fmt.Sprintf("%s — detected, NOT claimed", ad.Name()))
			fmt.Fprintf(w, "      → clawtool agents claim %s\n", ad.Name())
		default:
			rep.info(w, fmt.Sprintf("%s — not detected on this host", ad.Name()))
		}
	}
	fmt.Fprintln(w)
}

func (a *App) doctorSources(w io.Writer, rep *doctorReport) {
	fmt.Fprintln(w, "[sources]")
	cfg, err := config.LoadOrDefault(a.Path())
	if err != nil {
		rep.warn(w, "load config: "+err.Error(), "")
		fmt.Fprintln(w)
		return
	}
	if len(cfg.Sources) == 0 {
		rep.info(w, "no sources configured")
		fmt.Fprintln(w, "      → clawtool source add github   (or any catalog name)")
		fmt.Fprintln(w)
		return
	}
	store, err := secrets.LoadOrEmpty(a.SecretsPath())
	if err != nil {
		rep.warn(w, "load secrets: "+err.Error(), "")
		fmt.Fprintln(w)
		return
	}
	names := make([]string, 0, len(cfg.Sources))
	for n := range cfg.Sources {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		src := cfg.Sources[n]
		var missing []string
		for _, raw := range src.Env {
			_, miss := store.Expand(n, raw)
			missing = append(missing, miss...)
		}
		if len(missing) > 0 {
			rep.fail(w,
				fmt.Sprintf("%s — missing secrets: %s", n, strings.Join(uniqStrings(missing), ", ")),
				fmt.Sprintf("clawtool source set-secret %s %s", n, missing[0]))
		} else {
			rep.ok(w, fmt.Sprintf("%s — all credentials resolved", n))
		}
	}
	fmt.Fprintln(w)
}

func (a *App) doctorRecipes(w io.Writer, rep *doctorReport) {
	fmt.Fprintln(w, "[recipes — current cwd]")
	cwd, err := os.Getwd()
	if err != nil {
		rep.warn(w, "getwd: "+err.Error(), "")
		fmt.Fprintln(w)
		return
	}
	any := false
	for _, cat := range setup.Categories() {
		recipes := setup.InCategory(cat)
		if len(recipes) == 0 {
			continue
		}
		for _, r := range recipes {
			status, _, _ := r.Detect(context.Background(), cwd)
			any = true
			switch status {
			case setup.StatusApplied:
				rep.ok(w, fmt.Sprintf("%-26s applied", r.Meta().Name))
			case setup.StatusPartial:
				rep.warn(w,
					fmt.Sprintf("%-26s partial — file exists but not clawtool-managed", r.Meta().Name),
					fmt.Sprintf("clawtool recipe apply %s --force   (overwrite)", r.Meta().Name))
			case setup.StatusError:
				rep.warn(w, fmt.Sprintf("%s — Detect errored", r.Meta().Name), "")
				// StatusAbsent is the common case — `clawtool recipe list`
				// shows it explicitly; doctor stays focused on what's
				// applied or warning-worthy.
			}
		}
	}
	if !any {
		rep.info(w, "(no recipes registered — internal/setup/recipes/all.go missing?)")
	}
	fmt.Fprintln(w)
}

func (a *App) doctorSummary(w io.Writer, rep *doctorReport) {
	fmt.Fprintln(w, "[summary]")
	switch {
	case rep.critical > 0:
		fmt.Fprintf(w, "  ✗ %d critical issue(s), %d warning(s) — fix the ✗ rows above first.\n",
			rep.critical, rep.warnings)
	case rep.warnings > 0:
		fmt.Fprintf(w, "  ⚠ %d warning(s), no critical issues.\n", rep.warnings)
	default:
		fmt.Fprintln(w, "  ✓ everything healthy. clawtool init is ready to roll.")
	}
}

// uniqStrings dedups a slice while preserving order. Used by the
// sources section to compress repeat ${VAR} references in env
// templates into a single line.
func uniqStrings(xs []string) []string {
	seen := map[string]bool{}
	out := xs[:0]
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// configRelativeDot is the human-readable abbreviation for the
// user-config dir. Used in suggested-fix command lines so a user
// who doesn't read the full path knows what changed.
//
//nolint:unused // reserved for v0.10.x doctor refinements
func configRelativeDot(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return filepath.Clean(p)
}
