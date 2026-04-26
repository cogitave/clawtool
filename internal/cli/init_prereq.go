// Package cli — wizard-side install-prompt UX. Implements
// setup.Prompter (the abstraction the runner uses for missing
// prereqs) on top of huh + os/exec, plus the CommandRunner that
// actually shells out the per-platform install commands.
//
// Kept in a separate file from init_wizard.go so the prompter +
// runner pair can be swapped or wrapped (e.g. by a dry-run wrapper)
// without touching the rest of the wizard. Test files inject a
// recordingRunner and a scriptedPrompter against the same
// interfaces — the wizard surface stays huh-bound, but every
// install decision is exercised in unit tests.
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/cogitave/clawtool/internal/setup"
)

// interactiveInstallPrompter satisfies setup.Prompter by asking the
// user (via huh.Select) what to do when a recipe's Check fails.
//
// Three outcomes:
//   - Install: caller runs Prereq.Install for the current platform.
//   - Manual: caller prints ManualHint and re-runs Check.
//   - Skip:   caller short-circuits with ErrSkippedByUser.
//
// stdout is where install command output goes (so the user sees
// what's happening); stderr collects errors. Defaults to the
// process's streams; tests inject buffers.
type interactiveInstallPrompter struct {
	stdout io.Writer
	stderr io.Writer
}

// OnMissingPrereq is the setup.Prompter contract — render a small
// huh.Select with three labelled options and return the user's
// choice. We deliberately do NOT auto-pick Install: the user must
// say yes once per missing prereq, even with --yes (which short-
// circuits the wizard entirely before we get here).
func (p *interactiveInstallPrompter) OnMissingPrereq(_ context.Context, recipe setup.Recipe, pr setup.Prereq, checkErr error) (setup.PromptDecision, error) {
	platCmd, hasInstall := pr.Install[setup.CurrentPlatform()]
	cmdLine := ""
	if hasInstall && len(platCmd) > 0 {
		cmdLine = strings.Join(platCmd, " ")
	}

	hint := pr.ManualHint
	if hint == "" {
		hint = "(no manual install hint provided)"
	}

	var choice setup.PromptDecision
	choices := []huh.Option[setup.PromptDecision]{}
	if cmdLine != "" {
		choices = append(choices, huh.NewOption(
			fmt.Sprintf("Install for me — runs: %s", cmdLine),
			setup.PromptInstall,
		))
	}
	choices = append(choices,
		huh.NewOption("I'll install it myself (show the manual hint)", setup.PromptManual),
		huh.NewOption("Skip this recipe", setup.PromptSkip),
	)

	desc := fmt.Sprintf("Reason: %v\n%s", checkErr, hint)
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[setup.PromptDecision]().
			Title(fmt.Sprintf("[%s] missing prereq: %s", recipe.Meta().Name, pr.Name)).
			Description(desc).
			Options(choices...).
			Value(&choice),
	))
	if err := form.Run(); err != nil {
		// User aborted (Ctrl-C). Treat as a skip rather than blowing
		// up the whole wizard.
		return setup.PromptSkip, nil
	}
	if choice == setup.PromptManual {
		fmt.Fprintf(p.stdout, "  → %s\n", pr.ManualHint)
	}
	return choice, nil
}

// osCommandRunner satisfies setup.CommandRunner by exec'ing the
// install command directly. stdout/stderr are wired to the
// configured writers so the user sees apt/brew/winget output in
// real time. Errors propagate so the runner can surface a failure
// (e.g. apt's exit code) instead of silently retrying.
type osCommandRunner struct {
	stdout io.Writer
	stderr io.Writer
}

func (r *osCommandRunner) Run(ctx context.Context, command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("empty install command")
	}
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Stdout = r.stdout
	cmd.Stderr = r.stderr
	cmd.Stdin = os.Stdin // some installers prompt for sudo / EULA
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("install command %q failed: %w", strings.Join(command, " "), err)
	}
	return nil
}

// newWizardPrompter returns the (Prompter, CommandRunner) pair the
// interactive wizard wires into setup.Apply. Both share the same
// stdout/stderr so install output and prompts interleave cleanly.
func newWizardPrompter(stdout, stderr io.Writer) (setup.Prompter, setup.CommandRunner) {
	return &interactiveInstallPrompter{stdout: stdout, stderr: stderr},
		&osCommandRunner{stdout: stdout, stderr: stderr}
}
