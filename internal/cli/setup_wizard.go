// Package cli — Phase 2 setup state machine. Collapses onboard +
// init into one huh form with a single per-feature opt-in matrix.
// Per ADR-027: probe → matrix → required options → apply → verify.
//
// Phase 2 v1 ships the matrix for: bridge installs, MCP host
// claims, daemon up, BIAM identity, secrets store init, telemetry,
// AND the subset of recipes that are Stable + don't require any
// caller-supplied options. Recipes with required options (license,
// codeowners, …) still flow through `clawtool init`'s per-recipe
// prompts since the matrix can't ask for option values inline.
//
// `clawtool setup --legacy` falls back to the Phase 1 chain
// (onboard → init) for operators who prefer the old verb shape.
package cli

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/cogitave/clawtool/internal/setup"
)

// matrixItem is one row in the unified opt-in matrix. Stable
// identifier (the action key) is what the dispatcher uses; label is
// what the operator reads.
type matrixItem struct {
	key      string // unique within the form
	label    string
	category matrixCategory
	// core is true when this row is part of clawtool's curated
	// default install (recipe Meta.Core). The wizard uses this
	// to pre-check the row regardless of category.
	core  bool
	apply func(*App, context.Context, string) error
}

type matrixCategory string

const (
	matrixHost      matrixCategory = "host"
	matrixDaemon    matrixCategory = "daemon"
	matrixRecipe    matrixCategory = "recipe"
	matrixGuardrail matrixCategory = "guardrail"
)

// runSetupV2 is Phase 2 of ADR-027. Builds the matrix dynamically
// (host gaps + recipe gaps), shows ONE multi-select, dispatches in
// dependency order. --yes / non-TTY skips the matrix entirely and
// falls through to Phase 1's chain so unattended setup still works.
func (a *App) runSetupV2(argv []string, cwd string) int {
	for _, arg := range argv {
		if arg == "--legacy" {
			return a.runSetupLegacy(argv, cwd)
		}
	}

	items := buildSetupMatrix(a, cwd)
	if len(items) == 0 {
		fmt.Fprintln(a.Stdout, "✓ everything detectable is already set up. Run `clawtool overview` to confirm.")
		return 0
	}

	options := make([]huh.Option[string], 0, len(items))
	defaults := make([]string, 0, len(items))
	for _, it := range items {
		options = append(options, huh.NewOption(it.label, it.key))
		// Default-select host + daemon items, plus any recipe
		// row whose Meta.Core flag is set. Core recipes are the
		// curated default install — operator opt-OUT, not opt-IN.
		// Non-Core recipes stay unchecked so the wizard doesn't
		// spam unwanted scaffolding on first launch.
		if it.category == matrixHost || it.category == matrixDaemon || it.core {
			defaults = append(defaults, it.key)
		}
	}

	chosen := append([]string{}, defaults...)
	form := huh.NewForm(huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("clawtool setup — pick what to enable").
			Description("One screen, one matrix. Toggle with <space>; <enter> applies the selection. Recipes that require options (license holder, codeowners, …) still flow through `clawtool init`.").
			Options(options...).
			Value(&chosen),
	))
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(a.Stdout, "clawtool setup: aborted; nothing changed.")
			return 0
		}
		fmt.Fprintf(a.Stderr, "clawtool setup: %v\n", err)
		return 1
	}
	if len(chosen) == 0 {
		fmt.Fprintln(a.Stdout, "Nothing selected. Done.")
		return 0
	}

	chosenSet := map[string]bool{}
	for _, k := range chosen {
		chosenSet[k] = true
	}

	// Apply in matrix order (which is dependency order — daemon
	// before host claims, identity before async dispatches,
	// recipes last). Item dispatch is per-key so we never apply
	// a deselected item.
	ctx := context.Background()
	for _, it := range items {
		if !chosenSet[it.key] {
			continue
		}
		if err := it.apply(a, ctx, cwd); err != nil {
			fmt.Fprintf(a.Stdout, "  ✘ %s — %v\n", it.label, err)
			continue
		}
		fmt.Fprintf(a.Stdout, "  ✓ %s\n", it.label)
	}

	fmt.Fprintln(a.Stdout, "")
	fmt.Fprintln(a.Stdout, "── verify ───────────────────────────────────")
	a.runOverview(nil)
	return 0
}

// buildSetupMatrix probes the host + repo and returns one item per
// actionable gap. Order is dependency-order: daemon → identity →
// secrets → MCP claims → bridge installs → recipes.
func buildSetupMatrix(a *App, cwd string) []matrixItem {
	out := []matrixItem{}

	// Stage A — daemon-side prerequisites.
	out = append(out,
		matrixItem{
			key: "daemon", category: matrixDaemon,
			label: "Start the persistent daemon (`clawtool serve --listen --mcp-http`).",
			apply: func(a *App, ctx context.Context, _ string) error {
				return ensureDaemonForSetup(ctx)
			},
		},
		matrixItem{
			key: "identity", category: matrixDaemon,
			label: "Generate the BIAM identity (Ed25519 keypair, mode 0600).",
			apply: func(a *App, ctx context.Context, _ string) error {
				return ensureIdentityForSetup()
			},
		},
		matrixItem{
			key: "secrets", category: matrixDaemon,
			label: "Initialise the secrets store (~/.config/clawtool/secrets.toml, mode 0600).",
			apply: func(a *App, ctx context.Context, _ string) error {
				return ensureSecretsStoreForSetup(a)
			},
		},
		// Checkpoint Guard — opt-in defense-in-depth atop ADR-021
		// Read-before-Write. Off by default (per the package doc
		// in internal/setup/checkpoint.go); the operator chooses
		// explicitly. The apply path threads the wizard step
		// through Validate → Persist so a malformed answer surfaces
		// before the config write rather than as a load error on
		// the next daemon boot.
		matrixItem{
			key: "checkpoint-guard", category: matrixGuardrail,
			label: setup.CheckpointGuardPromptTitle + " — " + setup.CheckpointGuardPromptDescription,
			apply: func(a *App, ctx context.Context, _ string) error {
				step := setup.DefaultCheckpointGuardStep()
				step.Enabled = true
				if err := setup.ValidateCheckpointGuardStep(step); err != nil {
					return err
				}
				_, err := setup.PersistCheckpointGuard("", step)
				return err
			},
		},
	)

	// Stage B — host wiring (one item per detected host that we
	// can claim). detectHost lives in onboard.go.
	state := detectHost(func(bin string) error {
		_, err := lookPathOrStub(bin)
		return err
	})
	for _, host := range state.MCPClaimable {
		host := host
		out = append(out, matrixItem{
			key: "claim:" + host, category: matrixHost,
			label: fmt.Sprintf("Register clawtool as an MCP server in %s.", host),
			apply: func(a *App, ctx context.Context, _ string) error {
				return claimHostForSetup(ctx, host)
			},
		})
	}
	for _, fam := range state.MissingBridges {
		fam := fam
		out = append(out, matrixItem{
			key: "bridge:" + fam, category: matrixHost,
			label: fmt.Sprintf("Install the %s bridge.", fam),
			apply: func(a *App, ctx context.Context, _ string) error {
				return a.BridgeAdd(fam)
			},
		})
	}

	// Stage C — recipe gaps. Stable recipes are always candidates;
	// Beta recipes ride along ONLY when Core (operator wants Beta
	// defaults to ship by default — see RecipeMeta.Core). Recipes
	// with required options are excluded; the operator picks them
	// via `clawtool init`.
	type recipeRow struct {
		key   string
		label string
		name  string
		core  bool
	}
	var rows []recipeRow
	for _, cat := range setup.Categories() {
		for _, r := range setup.InCategory(cat) {
			m := r.Meta()
			stable := m.Stability == setup.StabilityStable || m.Stability == ""
			if !stable && !m.Core {
				continue
			}
			if needsRequiredOptions(m.Name) {
				continue
			}
			status, _, _ := r.Detect(context.Background(), cwd)
			if status != setup.StatusAbsent {
				continue
			}
			rows = append(rows, recipeRow{
				key:   "recipe:" + m.Name,
				label: fmt.Sprintf("[%s] %s — %s", cat, m.Name, m.Description),
				name:  m.Name,
				core:  m.Core,
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].label < rows[j].label })
	for _, row := range rows {
		row := row
		out = append(out, matrixItem{
			key: row.key, category: matrixRecipe,
			core:  row.core,
			label: row.label,
			apply: func(a *App, ctx context.Context, cwd string) error {
				r := setup.Lookup(row.name)
				if r == nil {
					return fmt.Errorf("recipe %q vanished from registry", row.name)
				}
				_, err := setup.Apply(ctx, r, setup.ApplyOptions{
					Repo:     cwd,
					Prompter: setup.AlwaysSkip{},
				})
				return err
			},
		})
	}

	return out
}

// runSetupLegacy chains onboard → init (Phase 1 behaviour).
// Operators who hit a v2 bug or want the old prompts pass --legacy.
func (a *App) runSetupLegacy(argv []string, _ string) int {
	fmt.Fprintln(a.Stdout, "── stage 1/2 — clawtool onboard ─────────────")
	if rc := a.runOnboard(nil); rc != 0 {
		fmt.Fprintln(a.Stderr, "clawtool setup --legacy: onboard failed; stopping.")
		return rc
	}
	fmt.Fprintln(a.Stdout, "")
	fmt.Fprintln(a.Stdout, "── stage 2/2 — clawtool init (this repo) ────")
	// Strip --legacy before passing through to init.
	rest := make([]string, 0, len(argv))
	for _, a := range argv {
		if a != "--legacy" {
			rest = append(rest, a)
		}
	}
	return a.runInit(rest)
}

// lookPathOrStub mirrors exec.LookPath but lives here to avoid
// dragging os/exec into the matrix builder's signature. In tests
// the real check still works because we never stub it out.
func lookPathOrStub(bin string) (string, error) {
	return resolvePATH(bin)
}

// ── per-action helpers (thin so the dispatcher reads cleanly) ──────

func ensureDaemonForSetup(ctx context.Context) error {
	// Reuse onboard's helper through the public daemon package.
	return runDaemonEnsure(ctx)
}

func ensureIdentityForSetup() error {
	return runIdentityEnsure()
}

func ensureSecretsStoreForSetup(a *App) error {
	return runSecretsStoreEnsure(a)
}

func claimHostForSetup(ctx context.Context, host string) error {
	return runMCPClaim(ctx, host)
}

// Wrapper indirection so we can keep this file decoupled from the
// daemon/agents/biam imports onboard.go already pulls in. The real
// implementations live in setup_wizard_helpers.go alongside the
// onboard production callbacks.
var (
	resolvePATH           = func(bin string) (string, error) { return "", fmt.Errorf("resolvePATH not wired") }
	runDaemonEnsure       = func(ctx context.Context) error { return fmt.Errorf("runDaemonEnsure not wired") }
	runIdentityEnsure     = func() error { return fmt.Errorf("runIdentityEnsure not wired") }
	runSecretsStoreEnsure = func(a *App) error { return fmt.Errorf("runSecretsStoreEnsure not wired") }
	runMCPClaim           = func(ctx context.Context, host string) error { return fmt.Errorf("runMCPClaim not wired") }
)

// _ keeps strings imported even when the matrix builds without
// touching strings directly (defensive against future trims).
var _ = strings.TrimSpace
