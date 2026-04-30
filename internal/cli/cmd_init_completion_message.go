// Package cli — short markdown completion message for init runs.
//
// ChatRender produces a 3-line markdown block aimed at a chat
// session: a parent LLM context driving clawtool over MCP wants to
// surface a digest of what just happened without re-rendering the
// full TTY output. The format is deliberately rigid so downstream
// consumers can pattern-match it:
//
//	✓ N recipes applied: a, b, c
//	○ M already present (idempotent skip)
//	→ Suggested next: <first NextStep>; <second NextStep>
//
// Lines are emitted unconditionally (zeros render as "0"). When
// NextSteps is empty the third line is omitted. Lives in its own
// file so the chat-onboard branch can import this single helper
// without pulling the whole init wizard's dependency closure.
package cli

import (
	"fmt"
	"strings"
)

// ChatRender returns the markdown completion paragraph. Stable
// output: pinned by TestInitSummary_ChatRender. The chat-onboard
// branch (parallel) imports this verbatim.
func (s InitSummary) ChatRender() string {
	var b strings.Builder

	applied := s.AppliedNames()
	fmt.Fprintf(&b, "✓ %d recipes applied", len(applied))
	if len(applied) > 0 {
		fmt.Fprintf(&b, ": %s", strings.Join(applied, ", "))
	}
	b.WriteString("\n")

	fmt.Fprintf(&b, "○ %d already present (idempotent skip)\n", s.AlreadyPresentCount())

	if len(s.NextSteps) > 0 {
		first := s.NextSteps
		if len(first) > 2 {
			first = first[:2]
		}
		fmt.Fprintf(&b, "→ Suggested next: %s", strings.Join(first, "; "))
	}
	return strings.TrimRight(b.String(), "\n")
}
