package agents

import (
	"context"
	"io"
)

// geminiTransport wraps Gemini CLI's `gemini -p` headless mode.
// Gemini has no first-party app-server / ACP surface as of 2026-04;
// the `abiswas97/gemini-plugin-cc` Claude Code bridge wraps the same
// `gemini` binary internally.
type geminiTransport struct{}

// GeminiTransport returns the Gemini transport.
func GeminiTransport() Transport { return geminiTransport{} }

func (geminiTransport) Family() string { return "gemini" }

func (geminiTransport) Send(ctx context.Context, prompt string, opts map[string]any) (io.ReadCloser, error) {
	o := ParseOptions(opts)

	// --skip-trust: Gemini CLI refuses to run in directories it hasn't
	// marked as trusted (exit 55 + a stderr hint pointing at
	// geminicli.com/docs/cli/trusted-folders). The trust check is an
	// IDE-style safeguard against accidentally executing untrusted
	// project config; in clawtool's relay path the operator has
	// explicitly chosen to dispatch via `clawtool send`, so the
	// safeguard is redundant and we suppress it. Operators who'd
	// rather opt back in can pass `extra_args = ["--no-skip-trust"]`
	// per call (Gemini accepts that flag — verified via `gemini --help`).
	args := []string{"-p", prompt, "--skip-trust"}
	args = append(args, joinModel(o.Model, "--model")...)

	// Gemini CLI silently swallows output in non-TTY contexts unless
	// --output-format is explicit. Default to "text" so the bare
	// `clawtool send --agent gemini "<prompt>"` flow returns
	// something. Caller can still override with --format.
	format := o.Format
	if format == "" {
		format = "text"
	}
	args = append(args, "--output-format", format)
	args = append(args, o.ExtraArgs...)

	// Gemini has no native session-resume; SessionID is ignored at
	// the transport layer. A future polish iteration may synthesise
	// a transient GEMINI.md from prior turns when SessionID is set.

	rc, err := startStreamingExecWith(ctx, "gemini", args, o.Cwd, o.Sandbox)
	if err != nil {
		return nil, ErrBinaryMissing{Family: "gemini", Binary: "gemini"}
	}
	return rc, nil
}
