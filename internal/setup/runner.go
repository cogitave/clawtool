package setup

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/cogitave/clawtool/internal/telemetry"
)

// emitRecipeApplyEvent fires after every recipe Apply terminates.
// Allow-listed shape: recipe name (public catalog), duration,
// outcome (success / error / skipped). Verify-failed counts as
// "verify_failed" outcome so the dashboard can split.
func emitRecipeApplyEvent(name string, start time.Time, res *ApplyResult) {
	tc := telemetry.Get()
	if tc == nil || !tc.Enabled() {
		return
	}
	outcome := "success"
	switch {
	case res.Skipped:
		outcome = "skipped"
	case res.VerifyErr != nil:
		outcome = "verify_failed"
	}
	tc.Track("recipe.apply", map[string]any{
		"recipe":      name,
		"duration_ms": time.Since(start).Milliseconds(),
		"outcome":     outcome,
	})
}

// CurrentPlatform returns the host's Platform. Recipes consult this
// when picking install commands; runtime/setup callers use it to
// route prereq install offers.
func CurrentPlatform() Platform {
	switch runtime.GOOS {
	case "linux":
		return PlatformLinux
	case "darwin":
		return PlatformDarwin
	case "windows":
		return PlatformWindows
	default:
		return Platform(runtime.GOOS)
	}
}

// PrereqOutcome is the per-prereq state after a check.
type PrereqOutcome struct {
	Prereq    Prereq
	Satisfied bool
	Err       error
}

// PrereqCheck runs Check() against every prereq and returns the
// outcome list in declared order. Never returns an error itself —
// individual failures land in Outcome.Err.
func PrereqCheck(ctx context.Context, recipe Recipe) []PrereqOutcome {
	prereqs := recipe.Prereqs()
	out := make([]PrereqOutcome, 0, len(prereqs))
	for _, p := range prereqs {
		outc := PrereqOutcome{Prereq: p}
		if p.Check == nil {
			outc.Satisfied = true
			out = append(out, outc)
			continue
		}
		if err := p.Check(ctx); err != nil {
			outc.Err = err
		} else {
			outc.Satisfied = true
		}
		out = append(out, outc)
	}
	return out
}

// AllSatisfied is the convenience predicate over PrereqCheck output.
func AllSatisfied(outcomes []PrereqOutcome) bool {
	for _, o := range outcomes {
		if !o.Satisfied {
			return false
		}
	}
	return true
}

// PromptDecision is what an interactive prompter (TTY wizard or
// MCP-driven Claude) returns for one prerequisite.
type PromptDecision int

const (
	// PromptInstall: caller offered to install, user accepted.
	// Runner should run Prereq.Install for the current platform.
	PromptInstall PromptDecision = iota
	// PromptManual: user wants to install themselves; runner skips
	// the auto-install and emits ManualHint.
	PromptManual
	// PromptSkip: user said skip this recipe entirely.
	PromptSkip
)

// Prompter is the abstraction over wizard / Claude / non-interactive
// auto modes. The runner calls it once per missing prereq.
//
// PromptDefault is what `clawtool init --yes` and the MCP code path
// use: returns PromptInstall on every prereq.
type Prompter interface {
	OnMissingPrereq(ctx context.Context, recipe Recipe, p Prereq, checkErr error) (PromptDecision, error)
}

// AlwaysInstall is the non-interactive Prompter. Used by
// `clawtool init --yes` and by the MCP recipe_apply tool when
// the caller passed `auto_install: true`.
type AlwaysInstall struct{}

func (AlwaysInstall) OnMissingPrereq(context.Context, Recipe, Prereq, error) (PromptDecision, error) {
	return PromptInstall, nil
}

// AlwaysSkip refuses installation. The MCP path uses it as a default
// safety guard when `auto_install` is false: a missing prereq
// surfaces as an error to Claude rather than silently shelling out
// `apt install`.
type AlwaysSkip struct{}

func (AlwaysSkip) OnMissingPrereq(context.Context, Recipe, Prereq, error) (PromptDecision, error) {
	return PromptSkip, nil
}

// CommandRunner is the abstraction for executing prereq install
// commands. Real callers pass an exec.Command-backed implementation;
// tests pass a recording fake. Returning an error aborts the recipe.
type CommandRunner interface {
	Run(ctx context.Context, command []string) error
}

// ApplyResult captures what a single recipe Apply call did. Used by
// the wizard's summary screen and by recipe_apply MCP output.
type ApplyResult struct {
	Recipe       string
	Category     Category
	Skipped      bool
	SkipReason   string
	Installed    []string // prereq names that were auto-installed
	ManualHints  []string // prereq names where the user picked manual
	UpstreamUsed string
	VerifyOK     bool
	VerifyErr    error
}

// ApplyOptions bundles everything Apply needs that isn't recipe-
// specific Options.
type ApplyOptions struct {
	// Repo is the absolute path to the target repository.
	Repo string
	// RecipeOptions is the per-recipe parameter bag (vault path,
	// license SPDX id, etc.).
	RecipeOptions Options
	// Prompter handles missing prereqs. Required.
	Prompter Prompter
	// Runner executes install commands. Required if any prereq
	// has an Install entry.
	Runner CommandRunner
}

// ErrSkippedByUser is returned when the prompter votes PromptSkip
// for a missing prereq. Carries through ApplyResult.SkipReason.
var ErrSkippedByUser = errors.New("recipe skipped by user")

// Apply runs the full apply sequence for one recipe:
//   - Detect (skip if already StatusApplied and ApplyOptions.Force
//     is false; force is wired in v0.10).
//   - Prereqs: check each, prompt on missing, install if accepted.
//   - Recipe.Apply.
//   - Recipe.Verify (post-condition; non-fatal warning if it fails).
//
// Returns the ApplyResult either way; errors are returned alongside
// (Result.Skipped + non-nil err on user-skip; Result.VerifyErr +
// nil err on apply-ok-but-verify-failed).
func Apply(ctx context.Context, recipe Recipe, ao ApplyOptions) (ApplyResult, error) {
	start := time.Now()
	res := ApplyResult{
		Recipe:       recipe.Meta().Name,
		Category:     recipe.Meta().Category,
		UpstreamUsed: recipe.Meta().Upstream,
	}
	defer func() {
		emitRecipeApplyEvent(recipe.Meta().Name, start, &res)
	}()
	if ao.Prompter == nil {
		return res, errors.New("ApplyOptions.Prompter is required")
	}

	// Prereq pass.
	outcomes := PrereqCheck(ctx, recipe)
	for _, o := range outcomes {
		if o.Satisfied {
			continue
		}
		decision, err := ao.Prompter.OnMissingPrereq(ctx, recipe, o.Prereq, o.Err)
		if err != nil {
			return res, fmt.Errorf("prereq %q prompt: %w", o.Prereq.Name, err)
		}
		switch decision {
		case PromptSkip:
			res.Skipped = true
			res.SkipReason = fmt.Sprintf("prereq %q not satisfied: %v", o.Prereq.Name, o.Err)
			return res, ErrSkippedByUser
		case PromptManual:
			res.ManualHints = append(res.ManualHints, o.Prereq.Name)
			// Manual means user takes responsibility; we still
			// re-check after the prompt so the user can install
			// out-of-band and continue. Re-run Check.
			if o.Prereq.Check != nil {
				if err := o.Prereq.Check(ctx); err != nil {
					res.Skipped = true
					res.SkipReason = fmt.Sprintf("prereq %q still missing after manual hint", o.Prereq.Name)
					return res, ErrSkippedByUser
				}
			}
		case PromptInstall:
			cmd, ok := o.Prereq.Install[CurrentPlatform()]
			if !ok || len(cmd) == 0 {
				// No platform-canonical install — fall back to
				// manual hint and ask the user to handle it.
				res.ManualHints = append(res.ManualHints, o.Prereq.Name)
				if o.Prereq.Check != nil {
					if err := o.Prereq.Check(ctx); err != nil {
						res.Skipped = true
						res.SkipReason = fmt.Sprintf("prereq %q has no install command for %s", o.Prereq.Name, CurrentPlatform())
						return res, ErrSkippedByUser
					}
				}
				continue
			}
			if ao.Runner == nil {
				return res, fmt.Errorf("ApplyOptions.Runner is required to install prereq %q", o.Prereq.Name)
			}
			if err := ao.Runner.Run(ctx, cmd); err != nil {
				return res, fmt.Errorf("install prereq %q: %w", o.Prereq.Name, err)
			}
			res.Installed = append(res.Installed, o.Prereq.Name)
		}
	}

	// Apply.
	if err := recipe.Apply(ctx, ao.Repo, ao.RecipeOptions); err != nil {
		return res, fmt.Errorf("apply recipe %q: %w", recipe.Meta().Name, err)
	}

	// Verify (non-fatal — surface to caller, don't block).
	if err := recipe.Verify(ctx, ao.Repo); err != nil {
		res.VerifyErr = err
		return res, nil
	}
	res.VerifyOK = true
	return res, nil
}
