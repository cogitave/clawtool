// Package a2a — Agent2Agent protocol surface for clawtool
// (ADR-024). Phase 1 ships only the Agent Card serializer + the
// JSON shape the protocol wants advertised at
// /.well-known/agent-card.json. mDNS announce, HTTP server, peer
// discovery, and capability tier enforcement layer on top in
// phase 2+.
//
// We adopt Google A2A's wire format (now Linux Foundation;
// github.com/a2aproject/A2A) verbatim rather than inventing one,
// per ADR-007 (wrap, don't reinvent). The Card describes *what*
// this clawtool instance can do (capabilities + skills + auth);
// it deliberately does NOT enumerate every aggregated tool —
// per A2A's opacity model, peers see the agent's contract, not
// its internal toolset.
package a2a

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/version"
)

// Card is the canonical A2A Agent Card (Schema v0.2.x, the
// stable LF-A2A snapshot as of 2026-04). Field names match the
// spec verbatim — JSON-RPC clients consuming the card MUST be
// able to parse it without translation.
type Card struct {
	// Name is the human-readable agent name shown in registries
	// and peer dashboards. We use clawtool's instance name when
	// the operator has set one; otherwise the bare project
	// name.
	Name string `json:"name"`

	// Description is one paragraph of plain text describing what
	// this agent does. Peers may render it in discovery UIs.
	Description string `json:"description"`

	// URL is the JSON-RPC endpoint base. Empty in phase 1
	// (Card-only mode); populated when phase 2 lands the HTTP
	// server.
	URL string `json:"url,omitempty"`

	// Version is the agent's product version (clawtool's
	// internal/version.Resolved()). NOT the A2A protocol version;
	// that's protocolVersion below.
	Version string `json:"version"`

	// ProtocolVersion is the A2A spec the card conforms to.
	// Follow upstream as it evolves; pinning here lets peers
	// negotiate.
	ProtocolVersion string `json:"protocolVersion"`

	// Capabilities is the feature flag block. We surface only
	// the streaming + push-notifications primitives clawtool
	// can actually serve today; future phases flip more on as
	// the implementation lands.
	Capabilities Capabilities `json:"capabilities"`

	// Skills enumerates the high-level abilities this agent
	// advertises. We don't dump every internal tool — that
	// would leak the operator's private surface and overflow
	// the card. Skills are *coarse* groupings (research,
	// code-edit, dispatch) the peer chooses from.
	Skills []Skill `json:"skills"`

	// DefaultInputModes / DefaultOutputModes — A2A's MIME-typed
	// I/O contract. clawtool speaks plain text + JSON-RPC; we
	// don't ship audio / image modes today.
	DefaultInputModes  []string `json:"defaultInputModes"`
	DefaultOutputModes []string `json:"defaultOutputModes"`

	// SecuritySchemes describes the auth modes this agent
	// accepts. Phase 1 advertises an empty schemes block (peers
	// can read the card but can't authenticate yet). Phase 2+
	// adds Bearer / OAuth schemes per ADR-024 §Threat model.
	SecuritySchemes map[string]SecurityScheme `json:"securitySchemes,omitempty"`

	// PublishedAt is when this card snapshot was generated.
	// Useful for caches / freshness checks. Always UTC.
	PublishedAt time.Time `json:"publishedAt"`
}

// Capabilities is A2A's feature-flag block. We model only the
// flags clawtool currently cares about; A2A's spec allows
// additional vendor extensions.
type Capabilities struct {
	// Streaming: server-sent events for long-running tasks. We
	// have BIAM (TaskNotify) which is conceptually the same;
	// phase 2 wires the HTTP transport.
	Streaming bool `json:"streaming"`

	// PushNotifications: webhook delivery on task transitions.
	// Same primitive as TaskNotify but cross-network. Phase 3+.
	PushNotifications bool `json:"pushNotifications"`

	// StateTransitionHistory: peer can replay every task
	// state. BIAM stores envelope history; we'll expose it
	// when phase 2 ships the HTTP /tasks/{id} endpoint.
	StateTransitionHistory bool `json:"stateTransitionHistory"`
}

// Skill is one coarse ability the agent advertises. A2A treats
// skills as the discovery primitive — a peer scanning a roster
// of cards looks at skill IDs to decide who can help.
type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags,omitempty"`
	// Examples are short prompts a peer can use to test the
	// skill. Optional. Keep them representative, not
	// exhaustive — the card is a contract, not a tutorial.
	Examples []string `json:"examples,omitempty"`
}

// SecurityScheme mirrors A2A's auth-scheme block. We expose
// only the fields clawtool's phase 2 will actually populate;
// A2A's full spec covers OAuth 2.1, mTLS, API key, etc.
type SecurityScheme struct {
	Type        string `json:"type"`             // "http" | "oauth2" | "apiKey"
	Scheme      string `json:"scheme,omitempty"` // for http: "bearer" / "basic"
	Description string `json:"description,omitempty"`
}

// CurrentProtocolVersion is the A2A spec snapshot we conform to.
// Bump in lockstep with upstream as their stable snapshots advance.
const CurrentProtocolVersion = "0.2.0"

// CardOptions carries the per-instance fields we don't want
// hard-coded into NewCard so a future supervisor can customise
// per dispatch (e.g. emit different skills depending on which
// instance is calling).
type CardOptions struct {
	// Name overrides the default ("clawtool"); empty keeps default.
	Name string
	// URL is the JSON-RPC endpoint. Empty until phase 2 lands.
	URL string
	// ExtraSkills appends to the canonical skill list. Empty
	// gives just the canonical set.
	ExtraSkills []Skill
}

// NewCard builds the Card snapshot for this clawtool instance.
// Pure function — fields come from CardOptions + version.Resolved()
// + a static skill list. Caller serializes via json.Marshal.
func NewCard(opts CardOptions) Card {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "clawtool"
	}
	skills := canonicalSkills()
	skills = append(skills, opts.ExtraSkills...)

	return Card{
		Name: name,
		Description: "Canonical tool layer + multi-agent supervisor for AI " +
			"coding agents. Wires Claude Code / Codex / Gemini / Opencode / " +
			"Hermes onto one timeout-safe, structured-output surface.",
		URL:             strings.TrimSpace(opts.URL),
		Version:         version.Resolved(),
		ProtocolVersion: CurrentProtocolVersion,
		Capabilities: Capabilities{
			// Phase 1: card-only. No live endpoint, no streaming, no push.
			// The flags advertise truthfully — peers won't try to use
			// what we can't deliver.
			Streaming:              false,
			PushNotifications:      false,
			StateTransitionHistory: false,
		},
		Skills:             skills,
		DefaultInputModes:  []string{"text/plain", "application/json"},
		DefaultOutputModes: []string{"text/plain", "application/json"},
		SecuritySchemes:    map[string]SecurityScheme{}, // empty in phase 1
		PublishedAt:        time.Now().UTC(),
	}
}

// canonicalSkills returns the static list of skill groups every
// clawtool instance advertises by default. We model FIVE coarse
// abilities — fewer is better (peers should pick a skill quickly,
// not page through dozens of tools).
func canonicalSkills() []Skill {
	return []Skill{
		{
			ID:          "research",
			Name:        "Research",
			Description: "Run web searches, fetch URLs, summarise documents, and synthesise multi-source findings.",
			Tags:        []string{"web", "search", "fetch", "summarise"},
			Examples: []string{
				"Find the latest A2A spec changes",
				"Summarise the top 5 results for 'OAuth 2.1 dynamic client registration'",
			},
		},
		{
			ID:          "code-read",
			Name:        "Code reading",
			Description: "Read and search source code with format-aware tooling (PDF / docx / xlsx / Jupyter / HTML / JSON / YAML / TOML / XML).",
			Tags:        []string{"read", "grep", "glob", "semantic-search"},
			Examples: []string{
				"Where do we handle token rotation?",
				"Find every callsite of `ParseOptions`",
			},
		},
		{
			ID:          "code-edit",
			Name:        "Code editing",
			Description: "Edit, write, and commit files with atomic temp+rename, line-ending preserve, Read-before-Write enforcement, and Conventional Commits validation.",
			Tags:        []string{"edit", "write", "commit", "conventional-commits"},
			Examples: []string{
				"Add a unit test for `parseExpr`",
				"Commit the staged changes with a `feat:` prefix",
			},
		},
		{
			ID:          "agent-dispatch",
			Name:        "Multi-agent dispatch",
			Description: "Forward prompts to other coding-agent CLIs (Claude Code, Codex, Gemini, Opencode, Hermes) via async BIAM and synthesise their responses.",
			Tags:        []string{"send-message", "biam", "supervisor", "fan-out"},
			Examples: []string{
				"Get a second opinion from Codex on this refactor",
				"Fan out to Gemini + Claude in parallel and pick the first finisher",
			},
		},
		{
			ID:          "shell",
			Name:        "Shell execution",
			Description: "Run shell commands with timeout safety, structured output, and optional sandbox isolation (bwrap / sandbox-exec / docker).",
			Tags:        []string{"bash", "verify", "sandbox"},
			Examples: []string{
				"Run the test suite and report pass/fail per check",
				"Execute `make build` inside a network-disabled sandbox",
			},
		},
	}
}

// MarshalJSON serializes the card to compact JSON — what an HTTP
// handler would write to /.well-known/agent-card.json. We use
// MarshalIndent for the CLI surface (`clawtool a2a card`) so
// humans can read it; the server-side path uses bare json.Marshal.
func (c Card) MarshalJSON() ([]byte, error) {
	type alias Card // avoid recursion on Card.MarshalJSON
	return json.Marshal(alias(c))
}

// MarshalIndented is the human-readable form for CLI output.
// Two-space indent matches GitHub Actions' workflow YAML
// convention so an operator copy-pasting the output into a
// gist / issue gets a readable layout.
func (c Card) MarshalIndented() ([]byte, error) {
	type alias Card
	return json.MarshalIndent(alias(c), "", "  ")
}
