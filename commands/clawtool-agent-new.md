---
description: Scaffold a Claude Code subagent persona via clawtool. Asks for the agent name, description, allowed-tools, and optional default instance, then writes ~/.claude/agents/<name>.md.
allowed-tools: mcp__clawtool__AgentNew
---

Scaffold a Claude Code subagent persona for the user.

**Step 1** — Ask for the agent name (kebab-case, e.g. `deep-grep`,
`codex-rescue`, `release-notes-writer`).

**Step 2** — Ask for a one-paragraph description that tells the
parent agent WHEN to dispatch this subagent. Be concrete — vague
descriptions cause the agent to never (or always) fire.

**Step 3** — Ask which tools the subagent should be allowed to use.
Common starter sets:

- **Research / dispatcher**: `mcp__clawtool__SendMessage, mcp__clawtool__TaskNotify, mcp__clawtool__TaskGet, mcp__clawtool__WebSearch, mcp__clawtool__WebFetch, Read, Glob, Grep`
- **Code reviewer**: `mcp__clawtool__Read, mcp__clawtool__Grep, mcp__clawtool__Glob, mcp__clawtool__SemanticSearch`
- **Builder / patcher**: `mcp__clawtool__Read, mcp__clawtool__Edit, mcp__clawtool__Write, mcp__clawtool__Bash, mcp__clawtool__Verify`

Empty = inherit the parent agent's full toolset.

**Step 4** — Optionally ask for a default clawtool instance. If the
agent is meant to dispatch to a specific upstream (e.g. `codex` for
deep refactors, `gemini` for design specs, `opencode` for read-only
research), capture that — the body will include a `Default instance:`
line so the routing is explicit.

**Step 5** — Optionally ask for a model preference (`sonnet`,
`haiku`, or `opus`). `haiku` is right for fast deterministic search
chains; `sonnet` for most synthesis work; `opus` for deep
multi-perspective reasoning.

**Step 6** — Call `mcp__clawtool__AgentNew` with the gathered fields.
Default `location=user` writes to `~/.claude/agents/<name>.md`; pass
`location=local` for a project-scoped agent at `./.claude/agents/<name>.md`.

After the file lands, summarize for the user:
- The path written
- One-line reminder that the subagent is now invokable from any
  Claude Code session via the `Agent` tool (or `subagent_type: <name>`)
- That the body is a starting skeleton — they should edit it to
  refine the workflow and the When-to-fire heuristic
