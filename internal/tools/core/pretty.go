package core

import (
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// BaseResult holds the fields and rendering helpers every tool
// result shares. Each tool's *Result struct embeds this so the
// JSON shape stays uniform (timing, error, engine surfaced the
// same way everywhere) and Render() implementations stay short
// — each one is just "header + body + footer" composed from
// BaseResult helpers and the tool-specific data.
//
// Operation is JSON-omitted; it's a presentation concern (the
// header verb), not a wire field.
type BaseResult struct {
	Operation   string `json:"-"`
	Engine      string `json:"engine,omitempty"`
	DurationMs  int64  `json:"duration_ms,omitempty"`
	ErrorReason string `json:"error_reason,omitempty"`
}

// IsError is the universal failure predicate.
func (b BaseResult) IsError() bool { return b.ErrorReason != "" }

// ErrorLine renders the canonical failure one-liner. Every tool
// that fails uses this — keeps "✗ <verb> — <reason>" consistent
// across the whole catalog. Reason is redacted for known secret
// shapes (API keys, bearer tokens, cookies) so an upstream error
// message that includes a credential doesn't leak to the peer.
// See internal/tools/core/redact.go for the canonical patterns.
func (b BaseResult) ErrorLine(target string) string {
	op := b.Operation
	if op == "" {
		op = "operation"
	}
	reason := redactSecrets(b.ErrorReason)
	if target != "" {
		return fmt.Sprintf("✗ %s %s — %s", op, target, reason)
	}
	return fmt.Sprintf("✗ %s — %s", op, reason)
}

// Pre-2026-04-30 we shipped a `MarshalJSON()` here that ran every
// envelope through `redactSecrets(ErrorReason)` before marshal —
// nicely safe by construction, but Go's interface promotion meant
// the outer tool result types (which embed BaseResult and add
// Stdout / ExitCode / Matches / …) inherited THIS MarshalJSON,
// shadowing every sibling field. The MCP wire structuredContent
// silently dropped to just `{duration_ms: N}` and the model lost
// access to bash output, search hits, agent rosters, …
//
// Restored: outer types use Go's default struct marshal which
// includes every embedded + sibling field. Redaction now lives in
// two places that already covered the actual leak vectors:
//
//   - ErrorLine() — runs every BaseResult.ErrorReason through
//     redactSecrets before rendering. content[].text (the channel
//     the chat UI shows the user, and the fallback the model reads)
//     is therefore safe.
//   - tools/core/redact.go's wire-level secret patterns (set/env
//     prefixes, Authorization headers, cookies) are still applied
//     by every tool that surfaces stderr / output; that work was
//     never tied to the BaseResult MarshalJSON path.
//
// The trade-off: structuredContent.error_reason exposes the raw
// err.Error() string, which is what the v0.21 wire shape did and
// what the existing e2e suite asserts. Worth it; the alternative
// (every outer type implementing its own MarshalJSON) is a 60-site
// migration with one missed site producing the same shadowing bug
// in reverse.

// SuccessLine is the canonical single-line success format used by
// stateless tools (Edit, Write). Variadic extras are joined with
// " · " and the duration is appended automatically.
func (b BaseResult) SuccessLine(target string, extras ...string) string {
	op := b.Operation
	if op == "" {
		op = "ok"
	}
	parts := []string{fmt.Sprintf("✓ %s %s", op, target)}
	if len(extras) > 0 {
		parts = append(parts, strings.Join(extras, " · "))
	}
	parts = append(parts, fmt.Sprintf("%dms", b.DurationMs))
	return strings.Join(parts, " — ")
}

// HeaderLine renders the canonical multi-line header used by tools
// that return content (Bash, Read, Grep). Engine — when set — is
// shown in brackets so the caller always knows which backend ran.
func (b BaseResult) HeaderLine(title string) string {
	if b.Engine == "" {
		return title
	}
	return fmt.Sprintf("%s [%s]", title, b.Engine)
}

// FooterLine joins extras with " · " and appends the duration. Used
// at the bottom of multi-line results (after content).
func (b BaseResult) FooterLine(extras ...string) string {
	parts := append([]string(nil), extras...)
	parts = append(parts, fmt.Sprintf("%dms", b.DurationMs))
	return strings.Join(parts, " · ")
}

// Renderer is the contract every tool result implements. The MCP
// dispatch helper below relies on it exclusively; tools that don't
// implement it would fall through to JSON marshaling, but every
// core tool overrides it.
type Renderer interface {
	Render() string
}

// resultOf is the single MCP-result builder every tool calls.
// Returns a CallToolResult with:
//
//   - StructuredContent: the original struct (model can field-access
//     stdout / exit_code / matches / etc. without parsing).
//   - Content[0].text: r.Render() — the human-readable view that
//     the chat UI shows the user. Per MCP 2025-06-18, the same
//     content[].text channel is what the model also reads as a
//     fallback when it can't introspect StructuredContent, so we
//     keep both informative and consistent.
func resultOf(r Renderer) *mcp.CallToolResult {
	return mcp.NewToolResultStructured(r, r.Render())
}

// humanBytes renders sizes as B/KiB/MiB. Used in pretty footers
// across multiple tools.
func humanBytes(n int64) string {
	const k = 1024
	switch {
	case n < k:
		return fmt.Sprintf("%dB", n)
	case n < k*k:
		return fmt.Sprintf("%.1fKiB", float64(n)/k)
	default:
		return fmt.Sprintf("%.1fMiB", float64(n)/(k*k))
	}
}
