package agents

import (
	"context"
	"io"
)

// codexTransport wraps Codex's published headless mode (`codex exec`).
// Phase 1 ships the shell-out form; a future iteration will speak
// JSON-RPC to `codex app-server` directly (the same surface
// openai/codex-plugin-cc uses internally), keyed off Transport's
// stable interface so callers don't change.
type codexTransport struct{}

// CodexTransport returns the Codex transport. Exposed as a constructor
// so the supervisor can wire one in without depending on the unexported
// type name.
func CodexTransport() Transport { return codexTransport{} }

func (codexTransport) Family() string { return "codex" }

func (codexTransport) Send(ctx context.Context, prompt string, opts map[string]any) (io.ReadCloser, error) {
	o := ParseOptions(opts)
	args := []string{"exec"}
	args = append(args, joinModel(o.Model, "--model")...)
	if o.SessionID != "" {
		// `codex exec resume <sid> "<prompt>"` per developers.openai.com/codex/cli/features
		args = []string{"exec", "resume", o.SessionID}
	}

	// --skip-git-repo-check: codex refuses to run in any directory it
	// hasn't been invited to trust ("Not inside a trusted directory"
	// safeguard) — same IDE-style guard Gemini ships and the same
	// reasoning applies here: in the headless dispatch path the
	// operator has explicitly chosen to run `clawtool send`, so the
	// guard is redundant. Operators who need it can pass
	// `extra_args = ["--no-skip-git-repo-check"]` per call.
	args = append(args, "--skip-git-repo-check")
	args = append(args, "--json") // stream-json equivalent for codex exec
	args = append(args, o.ExtraArgs...)
	args = append(args, prompt)

	rc, err := startStreamingExecWith(ctx, "codex", args, o.Cwd, o.Sandbox)
	if err != nil {
		return nil, ErrBinaryMissing{Family: "codex", Binary: "codex"}
	}
	return rc, nil
}
