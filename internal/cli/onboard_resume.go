// internal/cli/onboard_resume.go — wizard progress persistence so
// `clawtool onboard` can survive mid-flow interruption (Ctrl-C,
// terminal close, accidental crash) and pick up where it left off
// instead of starting from step 1 each time.
//
// Wire:
//   - State file: $XDG_CONFIG_HOME/clawtool/.onboard-progress.json
//     (mode 0600 — same conventions as the rest of the config tree).
//   - Saved after every wizard step completion (step index + the
//     onboardState snapshot at that point).
//   - Cleared after a successful finish so the next `clawtool
//     onboard` either starts fresh (if .onboarded marker absent)
//     or hits the "already onboarded → redo?" guard.
//
// Re-entry behaviour:
//   - .onboarded marker present, no progress file → "Already
//     onboarded. Re-run the wizard?"
//   - Progress file present → "Resume from step X?" (No = wipe
//     progress + start fresh).
//   - Neither → fresh wizard, no extra prompt.
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/cogitave/clawtool/internal/xdg"
)

// onboardProgress is the on-disk shape of a paused wizard. JSON for
// human-greppability — operators occasionally want to inspect or
// hand-edit (e.g. flip a Telemetry decision) before resuming.
type onboardProgress struct {
	// SchemaVersion lets us migrate the file shape across releases
	// without crashing old clients on new fields. Bump when the
	// onboardState shape changes incompatibly.
	SchemaVersion int          `json:"schema_version"`
	StepIdx       int          `json:"step_idx"`
	State         onboardState `json:"state"`
	SavedAt       time.Time    `json:"saved_at"`
	// CLawtoolVersion stamps the binary that wrote the file so we
	// can refuse to resume if the operator upgraded between
	// sessions and the wizard layout changed.
	ClawtoolVersion string `json:"clawtool_version"`
}

// onboardProgressSchema is the current schema version. Increment on
// any incompatible change to onboardState's JSON shape.
const onboardProgressSchema = 1

// onboardProgressPath returns the absolute path of the progress
// file. Lives alongside .onboarded under $XDG_CONFIG_HOME/clawtool.
func onboardProgressPath() string {
	return filepath.Join(xdg.ConfigDir(), ".onboard-progress.json")
}

// saveOnboardProgress writes the wizard's current step + state to
// disk atomically. Best-effort: a write failure is logged via the
// passed callback but doesn't abort the wizard (the operator can
// re-onboard from scratch if persistence is broken).
func saveOnboardProgress(stepIdx int, state *onboardState, version string) error {
	p := onboardProgress{
		SchemaVersion:   onboardProgressSchema,
		StepIdx:         stepIdx,
		State:           *state,
		SavedAt:         time.Now().UTC(),
		ClawtoolVersion: version,
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	path := onboardProgressPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Atomic temp+rename so a partial write can never leave a
	// corrupted progress file that the next session refuses to
	// parse.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// loadOnboardProgress reads the progress file. Returns nil + nil
// when the file is absent (clean state, not an error). Returns nil
// + error for any other read/parse failure so the caller can
// surface a "couldn't resume; starting fresh" warning.
func loadOnboardProgress() (*onboardProgress, error) {
	path := onboardProgressPath()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var p onboardProgress
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if p.SchemaVersion != onboardProgressSchema {
		// Incompatible schema — refuse to resume rather than
		// risk crashing partway through. Caller treats this as
		// "no progress" and starts fresh.
		return nil, fmt.Errorf("progress schema %d != %d (wizard layout changed; starting fresh)",
			p.SchemaVersion, onboardProgressSchema)
	}
	return &p, nil
}

// clearOnboardProgress removes the progress file. Idempotent.
// Called on successful onboard finish + on operator choosing
// "start over" at the resume prompt.
func clearOnboardProgress() error {
	err := os.Remove(onboardProgressPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// promptResume asks the operator whether to resume an in-flight
// wizard or start over. Renders as a small huh.Form BEFORE the
// alt-screen TUI takes over so the operator can see context (the
// timestamp / version of their previous session) above the prompt.
//
// Returns one of: "resume" | "restart" | "cancel". The caller is
// responsible for clearing the progress file when the choice is
// "restart" + applying the loaded state when "resume".
func promptResume(p *onboardProgress, stdout, stderr interface{ Write([]byte) (int, error) }) (string, error) {
	human := p.SavedAt.Local().Format("2006-01-02 15:04:05")
	choice := "resume"
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title(fmt.Sprintf("Previous onboard session paused at step %d", p.StepIdx+1)).
			Description(fmt.Sprintf(
				"Saved %s by clawtool %s. Pick:\n\n"+
					"  • Resume — pick up at the step you left off, with your previous answers\n"+
					"  • Start over — wipe the saved progress and run the wizard from step 1\n"+
					"  • Cancel — exit; your saved progress stays on disk",
				human, p.ClawtoolVersion)).
			Options(
				huh.NewOption("Resume from where I left off", "resume"),
				huh.NewOption("Start over from step 1", "restart"),
				huh.NewOption("Cancel — keep my progress for later", "cancel"),
			).
			Value(&choice),
	))
	form.WithAccessible(false)
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "cancel", nil
		}
		return "", fmt.Errorf("resume prompt: %w", err)
	}
	return choice, nil
}

// promptAlreadyOnboarded asks whether to re-run the wizard when the
// .onboarded marker is present (no progress file). Two outcomes:
// "redo" | "cancel".
func promptAlreadyOnboarded(stdout, stderr interface{ Write([]byte) (int, error) }) (string, error) {
	choice := "cancel"
	form := huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("clawtool is already onboarded on this machine").
			Description(
				"You've already run the onboard wizard at least once. Pick:\n\n"+
					"  • Re-run — go through the wizard again (existing config + identity left as-is unless you change them)\n"+
					"  • Cancel — exit without changes\n\n"+
					"Tip: pass `--force` to wipe the onboarded marker + saved progress and start completely fresh.",
			).
			Options(
				huh.NewOption("Re-run the wizard", "redo"),
				huh.NewOption("Cancel — leave configuration alone", "cancel"),
			).
			Value(&choice),
	))
	form.WithAccessible(false)
	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "cancel", nil
		}
		return "", fmt.Errorf("re-entry prompt: %w", err)
	}
	return choice, nil
}
