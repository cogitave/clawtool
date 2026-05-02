// Package setup — checkpoint wizard step.
//
// Owns the small piece of onboarding that asks the operator
// whether they want the Guard middleware (defense-in-depth atop
// ADR-021 Read-before-Write) turned on. Default is OFF — Guard is
// for the small set of agent flows (autodev, fanout) that
// explicitly bypass RbW with `unsafe_overwrite_without_read=true`,
// not for everyday interactive editing.
//
// Why a dedicated file (not a Recipe): Guard isn't a project
// installable — it's a clawtool-level user preference, persisted
// to ~/.config/clawtool/config.toml under [checkpoint.guard]. The
// recipe framework (recipe.go, runner.go) is for repo-scoped
// scaffolding (LICENSE, CODEOWNERS, .pre-commit-config.yaml). A
// user-config toggle has different semantics:
//
//   - Recipes write into the target repo and are tracked by
//     .clawtool.toml's `applied` list.
//   - User toggles write into the global config and don't change
//     repo state.
//
// Mixing the two would force the recipe runner to know about
// non-repo writes, complicating its idempotency contract. The
// checkpoint wizard step exposes a CheckpointWizardStep struct
// that the CLI's `onboard` flow + the chat-side OnboardWizard
// MCP tool can both consume to render the prompt + persist the
// answer.
//
// Scope: this file owns the prompt copy + the persistence helper.
// The actual TUI rendering lives in internal/cli/onboard*.go;
// this package stays UI-agnostic so the prompt can be reused by
// any front-end (huh form, chat tool, JSON RPC).
package setup

import (
	"errors"
	"fmt"

	"github.com/cogitave/clawtool/internal/config"
)

// CheckpointGuardPromptTitle is the headline copy shown when the
// wizard surfaces the toggle. Plain language — no internal doc IDs
// (per operator memory feedback "no internal doc IDs in UX").
const CheckpointGuardPromptTitle = "Checkpoint Guard"

// CheckpointGuardPromptDescription explains what flipping the
// toggle to ON actually does, in operator-friendly terms. The
// front-end renders this verbatim under the title.
const CheckpointGuardPromptDescription = "" +
	"Cap how many file edits an agent can make before requiring " +
	"a checkpoint commit. Defense-in-depth: even if an agent " +
	"bypasses the Read-before-Write safety net, you can't end " +
	"up with N+ uncheckpointed mutations stacked up. Operator " +
	"unblocks by running `clawtool checkpoint save` (autocommit) " +
	"or by landing a real Conventional commit.\n\n" +
	"Default: off. Recommended: leave off for interactive " +
	"editing; turn on for autodev / fanout sessions where an " +
	"agent might run unattended for an hour."

// CheckpointGuardWizardStep is the value the front-end binds to a
// huh.NewConfirm widget (or its chat-tool equivalent). The struct
// stays tiny on purpose — adding fields here forces every UI
// surface to update in lockstep, which is what we want.
type CheckpointGuardWizardStep struct {
	// Enabled is the operator's answer. Default false — front-ends
	// MUST seed the widget value to false so a wizard the operator
	// blew through with default-yes doesn't accidentally turn the
	// gate on.
	Enabled bool
	// MaxEditsWithoutCheckpoint is the threshold. Front-ends may
	// expose a number-picker widget; when omitted (zero), the
	// persistence step writes the package default
	// (checkpoint.DefaultMaxEditsWithoutCheckpoint = 5) by leaving
	// the field at zero — the loader (server.go boot) substitutes
	// the default at runtime.
	MaxEditsWithoutCheckpoint int
}

// DefaultCheckpointGuardStep returns the wizard step pre-seeded
// with the safe defaults: gate off, threshold zero (loader
// substitutes 5). Front-ends call this to populate their form
// widgets so a single source of truth governs the defaults.
func DefaultCheckpointGuardStep() CheckpointGuardWizardStep {
	return CheckpointGuardWizardStep{
		Enabled:                   false,
		MaxEditsWithoutCheckpoint: 0,
	}
}

// PersistCheckpointGuard writes the wizard's answer into the
// global config.toml at cfgPath. Atomic via Config.Save (temp+
// rename); the existing [checkpoint.guard] section is overwritten
// in full so a previous answer doesn't bleed through.
//
// Returns the persisted GuardConfig on success — the caller can
// log / display the actual saved values (handy when the front-end
// passed a zero threshold and we substituted the default).
//
// cfgPath empty → use config.DefaultPath().
func PersistCheckpointGuard(cfgPath string, step CheckpointGuardWizardStep) (config.GuardConfig, error) {
	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}
	cfg, err := config.LoadOrDefault(cfgPath)
	if err != nil {
		return config.GuardConfig{}, fmt.Errorf("load config: %w", err)
	}
	cfg.Checkpoint.Guard = config.GuardConfig{
		Enabled:                   step.Enabled,
		MaxEditsWithoutCheckpoint: step.MaxEditsWithoutCheckpoint,
	}
	if err := cfg.Save(cfgPath); err != nil {
		return config.GuardConfig{}, fmt.Errorf("save config: %w", err)
	}
	return cfg.Checkpoint.Guard, nil
}

// ValidateCheckpointGuardStep checks the step's invariants before
// persistence. Front-ends call this after collecting the answer +
// before PersistCheckpointGuard so a malformed value surfaces as
// a re-prompt instead of an opaque save error.
//
// Today the only invariant is "MaxEditsWithoutCheckpoint must not
// be negative" — zero is fine (means "use default"). Larger
// invariants (e.g. cap at 1000) can land here without churning
// callers.
func ValidateCheckpointGuardStep(step CheckpointGuardWizardStep) error {
	if step.MaxEditsWithoutCheckpoint < 0 {
		return errors.New("checkpoint guard: max_edits_without_checkpoint must be >= 0 (zero means \"use default\")")
	}
	return nil
}
