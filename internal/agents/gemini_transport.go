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
	args := []string{"-p", prompt}
	args = append(args, joinModel(o.Model, "--model")...)
	if o.Format != "" {
		args = append(args, "--output-format", o.Format)
	}
	args = append(args, o.ExtraArgs...)

	// Gemini has no native session-resume; SessionID is ignored at
	// the transport layer. A future polish iteration may synthesise
	// a transient GEMINI.md from prior turns when SessionID is set.

	rc, err := startStreamingExec(ctx, "gemini", args, o.Cwd)
	if err != nil {
		return nil, ErrBinaryMissing{Family: "gemini", Binary: "gemini"}
	}
	return rc, nil
}
