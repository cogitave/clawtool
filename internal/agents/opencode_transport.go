package agents

import (
	"context"
	"io"
)

// opencodeTransport wraps OpenCode's `opencode run` headless mode.
// Future iteration: speak ACP v1 to a long-running `opencode acp`
// daemon — the canonical extensibility surface used by Zed in
// production. Phase 1 keeps the simpler `run` shell-out so the
// dispatch path is end-to-end exercisable without re-implementing
// the ACP protocol up front.
type opencodeTransport struct{}

// OpencodeTransport returns the OpenCode transport.
func OpencodeTransport() Transport { return opencodeTransport{} }

func (opencodeTransport) Family() string { return "opencode" }

func (opencodeTransport) Send(ctx context.Context, prompt string, opts map[string]any) (io.ReadCloser, error) {
	o := ParseOptions(opts)
	args := []string{"run"}
	if o.SessionID != "" {
		args = append(args, "--session", o.SessionID)
	}
	args = append(args, joinModel(o.Model, "--model")...)
	if o.Format == "json" || o.Format == "stream-json" {
		args = append(args, "--format", "json")
	}
	if o.Unattended {
		// OpenCode's elevation flag — bypass interactive
		// confirmations. Operator opted in via
		// `clawtool send --unattended` (ADR-023).
		args = append(args, "--yolo")
	}
	args = append(args, o.ExtraArgs...)
	args = append(args, prompt)

	rc, err := startStreamingExecFull(ctx, "opencode", args, o.Cwd, o.Sandbox, o.Env)
	if err != nil {
		return nil, ErrBinaryMissing{Family: "opencode", Binary: "opencode"}
	}
	return rc, nil
}
