package agents

import (
	"context"
	"io"
	"os"
)

// claudeTransport wraps Claude Code's `claude -p` headless print mode.
// Two scenarios:
//
//  1. **Headless host** (no TUI present, e.g. CI hook, Docker
//     container). `claude -p "<prompt>"` works end-to-end.
//  2. **Inside-Claude-Code self-dispatch.** clawtool runs as an MCP
//     server inside a Claude Code session that called us; sending a
//     prompt back to that same session would loop. Detected by the
//     CLAUDE_CODE_SESSION_ID env var the host sets when invoking
//     MCP servers; if it's present, refuse with ErrSelfDispatch.
type claudeTransport struct {
	allowSelfDispatch bool // testability: tests can set this to true
}

// ClaudeTransport returns the Claude Code transport.
func ClaudeTransport() Transport { return claudeTransport{} }

func (claudeTransport) Family() string { return "claude" }

func (c claudeTransport) Send(ctx context.Context, prompt string, opts map[string]any) (io.ReadCloser, error) {
	if !c.allowSelfDispatch && os.Getenv("CLAUDE_CODE_SESSION_ID") != "" {
		return nil, ErrSelfDispatch
	}
	o := ParseOptions(opts)

	// Claude CLI's `-p` (print) headless mode is the canonical
	// non-interactive surface. We deliberately do NOT pass `--bare`:
	// older drafts of this transport added it expecting "no chrome"
	// behaviour, but on the current Claude Code build that flag puts
	// the CLI into a path that ignores the existing auth session and
	// reports "Not logged in" — the opposite of what's wanted in a
	// headless dispatch. Plain `-p` honours the session.
	args := []string{"-p", prompt}
	if o.SessionID != "" {
		args = []string{"--resume", o.SessionID, "-p", prompt}
	}
	args = append(args, joinModel(o.Model, "--model")...)
	if o.Format != "" {
		args = append(args, "--output-format", o.Format)
	}
	args = append(args, o.ExtraArgs...)

	rc, err := startStreamingExecWith(ctx, "claude", args, o.Cwd, o.Sandbox)
	if err != nil {
		return nil, ErrBinaryMissing{Family: "claude", Binary: "claude"}
	}
	return rc, nil
}
