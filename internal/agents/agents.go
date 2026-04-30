// Package agents bridges clawtool to the host AI coding agents that
// embed it (Claude Code, Codex, OpenCode, …). Per ADR-011 the role of
// this package is the **hard-replacement** of native tools — i.e.
// flipping the host agent's settings so clawtool's `mcp__clawtool__*`
// becomes the only Bash/Read/Edit/Write/Grep/Glob/WebFetch/WebSearch
// the model can see.
//
// Each host gets one Adapter implementation. The Adapter only needs to
// know how to read its settings, claim a known set of tools (atomic
// write + idempotent), release them, and report status. The CLI
// subcommand (internal/cli/agents.go) is a thin dispatcher over this
// interface.
package agents

import (
	"errors"
	"sort"
)

// Adapter describes one host AI coding agent. Implementations live in
// per-host files (claudecode.go for Claude Code, codex.go later, …).
//
// Implementations MUST:
//   - Be idempotent for Claim and Release (run twice = same result).
//   - Use atomic writes (no partial state observable on crash).
//   - Touch ONLY the fields/files they own. User customizations stay.
//   - Track ownership via a marker file so Release only undoes what
//     this clawtool installation added.
type Adapter interface {
	Name() string

	// Detected returns true when the host's settings file exists or
	// the host is otherwise configured on this machine. False means
	// the agent isn't installed; CLI commands should report
	// "not detected" instead of failing.
	Detected() bool

	// Claim disables the native tools clawtool replaces. Idempotent.
	Claim(opts Options) (Plan, error)

	// Release re-enables every tool clawtool's previous Claim
	// disabled (tracked via the per-adapter marker file). Idempotent.
	Release(opts Options) (Plan, error)

	// Status reports what's currently claimed.
	Status() (Status, error)
}

// Options carries per-call flags the CLI propagates.
type Options struct {
	DryRun bool
}

// Plan is what an adapter would do (or did, in non-dry-run). The CLI
// renders this for human consumption.
type Plan struct {
	Adapter      string   // "claude-code"
	Action       string   // "claim" | "release" | "noop"
	SettingsPath string   // file the adapter would mutate
	MarkerPath   string   // file the adapter would write to track ownership
	ToolsAdded   []string // tools claim is about to disable
	ToolsRemoved []string // tools release is about to re-enable
	WasNoop      bool     // already in the requested state
	DryRun       bool
}

// Status is a snapshot of the adapter's current claim state.
type Status struct {
	Adapter      string   `json:"adapter"`
	Detected     bool     `json:"detected"`
	SettingsPath string   `json:"settings_path,omitempty"`
	Claimed      bool     `json:"claimed"`                  // true when our marker file exists and lists tools
	DisabledByUs []string `json:"disabled_by_us,omitempty"` // tools we disabled (read from marker)
	Notes        string   `json:"notes,omitempty"`          // anything an adapter wants to surface (e.g. "settings file missing")
}

// Registry is the set of available adapters. Adapters self-register in
// their package's init() function so adding a host is one new file.
var Registry = []Adapter{}

// Register adds an adapter to the registry. Sorted by Name for stable
// CLI output across runs.
func Register(a Adapter) {
	Registry = append(Registry, a)
	sort.Slice(Registry, func(i, j int) bool { return Registry[i].Name() < Registry[j].Name() })
}

// Find returns the adapter matching name, or ErrUnknownAgent.
func Find(name string) (Adapter, error) {
	for _, a := range Registry {
		if a.Name() == name {
			return a, nil
		}
	}
	return nil, ErrUnknownAgent
}

// ErrUnknownAgent is returned by Find when the requested agent isn't in
// the Registry. CLI catches this to print the list of known agents.
var ErrUnknownAgent = errors.New("unknown agent")

// ClaimedToolsForClawtool is the canonical set of native tool names
// every adapter disables when claiming. Per ADR-011 this is exactly the
// 1:1 mapping with clawtool's core tools that have native equivalents.
//
// Stored here (not per-adapter) because the mapping is identical across
// hosts — Claude Code calls them `Bash`, `Read`, etc.; other hosts use
// the same names since they all converged on Claude Code's vocabulary.
// If Codex / OpenCode use different native names, override per adapter.
var ClaimedToolsForClawtool = []string{
	"Bash",
	"Edit",
	"Glob",
	"Grep",
	"Read",
	"WebFetch",
	"WebSearch",
	"Write",
}
