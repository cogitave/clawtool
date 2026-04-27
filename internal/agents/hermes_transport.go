package agents

import (
	"context"
	"io"
)

// hermesTransport wraps NousResearch hermes-agent's `hermes chat -q`
// headless mode. Hermes is a self-improving agent with 47 built-in
// tools (web, terminal, git, file ops, skills) and supports 20+
// inference providers via BYOK (OpenRouter, Anthropic, Codex, Gemini,
// Bedrock, NIM, Ollama, ...). Per ADR-007 we wrap the published CLI
// instead of re-implementing the agent loop.
//
// Source: github.com/nousresearch/hermes-agent (MIT, 120K stars as
// of 2026-04-27). The `-q` flag is hermes's headless one-shot mode,
// equivalent to `claude -p` / `gemini -p` / `codex exec` in the rest
// of the bridge family.
//
// Plugin install path: hermes ships as a standalone CLI binary, not
// a Claude Code plugin. The bridge recipe (internal/setup/recipes/
// bridges) verifies the binary on PATH — same pattern OpenCode uses.
type hermesTransport struct{}

// HermesTransport returns the Hermes transport.
func HermesTransport() Transport { return hermesTransport{} }

func (hermesTransport) Family() string { return "hermes" }

func (hermesTransport) Send(ctx context.Context, prompt string, opts map[string]any) (io.ReadCloser, error) {
	o := ParseOptions(opts)

	// `hermes chat` is the conversation subcommand; `-q "<prompt>"`
	// runs a single non-interactive query. SessionID maps onto
	// hermes's `--session-id` for resume — verified against
	// `hermes chat --help` from upstream README.
	args := []string{"chat", "-q", prompt}

	// Hermes accepts both `--provider <name>` and `--model
	// "provider/model-id"`. We pass model as-is via `--model`; if
	// the operator wants a specific provider, they pass it through
	// extra_args. ExtraArgs catches anything model+provider can't.
	args = append(args, joinModel(o.Model, "--model")...)

	if o.SessionID != "" {
		args = append(args, "--session-id", o.SessionID)
	}

	// Hermes default output is JSON-shaped streaming; "text" forces
	// plain output. Match the rest of the family by honouring the
	// caller's Format when set.
	if o.Format == "json" || o.Format == "stream-json" {
		args = append(args, "--format", "json")
	} else if o.Format == "text" {
		args = append(args, "--format", "text")
	}

	if o.Unattended {
		// Hermes elevation flag — accept all tool calls without
		// prompting. Per upstream README the headless flag is
		// `--yolo`. Operator opted in via `clawtool send --unattended`.
		args = append(args, "--yolo")
	}

	args = append(args, o.ExtraArgs...)

	rc, err := startStreamingExecFull(ctx, "hermes", args, o.Cwd, o.Sandbox, o.Env)
	if err != nil {
		return nil, ErrBinaryMissing{Family: "hermes", Binary: "hermes"}
	}
	return rc, nil
}
