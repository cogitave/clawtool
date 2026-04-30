package agents

import (
	"context"
	"io"
)

// aiderTransport wraps Aider's headless mode (`aider --message`).
// Aider (Aider-AI/aider, 44k★ as of 2026-04) is a repo-map-aware
// pair-programming CLI that lands diffs straight into the working
// tree. It earns BIAM peer slot #6 alongside claude / codex /
// opencode / gemini / hermes.
//
// Why a 6th peer when the auto-memory says "opencode is research-
// only — code-writing routes to codex/gemini/claude/hermes":
// Aider is *also* code-writing. It complements the four code-
// writing peers by being repo-map-aware out of the box (it parses
// the whole tree once and reasons over the map), which makes it
// the cheapest peer for "edit this codebase to do X" prompts that
// span many files. The routing memory's intent is preserved —
// research → opencode, code-writing → claude/codex/gemini/hermes/aider.
//
// Headless contract assumed:
//   - `aider --message "<prompt>"` runs single-shot non-interactive.
//   - `--yes-always` is Aider's elevation flag (auto-confirm every
//     edit / git operation prompt). Operator opts in via
//     `clawtool send --unattended` (ADR-023); the audit log already
//     records intent.
//   - `--no-stream` + `--no-pretty` disable TTY decoration so the
//     dispatch pipe gets a clean text stream the BIAM runner can
//     forward.
type aiderTransport struct{}

// AiderTransport returns the Aider transport.
func AiderTransport() Transport { return aiderTransport{} }

func (aiderTransport) Family() string { return "aider" }

func (aiderTransport) Send(ctx context.Context, prompt string, opts map[string]any) (io.ReadCloser, error) {
	o := ParseOptions(opts)

	// Build argv. `--message` runs Aider non-interactively;
	// the remaining flags neutralise TTY-only behaviours so
	// the dispatch stream stays parseable.
	args := []string{"--message", prompt, "--no-stream", "--no-pretty"}
	args = append(args, joinModel(o.Model, "--model")...)

	if o.Unattended {
		// Aider's elevation flag — auto-confirm edit / git
		// operation prompts. ADR-023's audit-log entry already
		// captures the operator's `--unattended` opt-in; this
		// just makes the dispatch actually non-blocking.
		args = append(args, "--yes-always")
	}

	// Aider has no native session-resume; SessionID is ignored
	// at the transport layer. Persistent context lives in the
	// `.aider.chat.history.md` file in the cwd, which Aider
	// reads automatically on subsequent runs in the same dir.

	args = append(args, o.ExtraArgs...)

	rc, err := startStreamingExecFull(ctx, "aider", args, o.Cwd, o.Sandbox, o.Env)
	if err != nil {
		return nil, ErrBinaryMissing{Family: "aider", Binary: "aider"}
	}
	return rc, nil
}
