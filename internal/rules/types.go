// Package rules — predicate-based rule engine for clawtool. Rules
// gate operator-defined invariants ("README must be updated when
// shipping a feature", "no Co-Authored-By in commits", "skill
// routing-map row required when adding a core tool"). Each rule
// fires on an event (pre_commit / post_edit / session_end / pre_send)
// and produces a Result the caller surfaces to the agent or operator.
//
// Why a new package, not BIAM hooks: internal/hooks fires SHELL
// COMMANDS for every event. This engine is in-process Go evaluation
// against a structured Context — no shell roundtrip, no JSON
// encoding to stdin, full type safety on conditions and predicates.
// The two compose: a hook entry can call `clawtool rules check`
// to invoke this engine, but most callers (the future Commit tool,
// the unattended-mode supervisor) should call rules.Evaluate
// directly.
//
// Design notes:
//   - Rules are PURE: given a Context, the same rule produces the
//     same Result. No I/O inside Eval; all state is on the Context.
//   - Conditions are a tiny DSL (changed(glob), commit_message_contains(s),
//     tool_call_count(name) > N) parsed once at load time.
//   - Severity is a 3-tier ladder (off / warn / block); a "block"
//     result is the caller's signal to refuse the action.
//
// This file declares the public types; eval.go implements the
// evaluator; loader.go reads .clawtool/rules.toml.
package rules

import "time"

// Severity ladders the operator's response to a violation.
type Severity string

const (
	// SeverityOff — rule defined but disabled. Useful for
	// staging a new rule without flipping it on yet.
	SeverityOff Severity = "off"
	// SeverityWarn — surface the violation in the result
	// payload so the agent / operator sees it, but don't block.
	SeverityWarn Severity = "warn"
	// SeverityBlock — refuse the action. Callers MUST treat
	// a block result as a hard stop.
	SeverityBlock Severity = "block"
)

// IsValidSeverity is the loader's allowlist guard. Empty severity
// in TOML defaults to "warn" — most operators want notification,
// not a hard block, when first wiring a rule.
func IsValidSeverity(s Severity) bool {
	switch s {
	case SeverityOff, SeverityWarn, SeverityBlock:
		return true
	}
	return false
}

// Event names the lifecycle hook a rule binds to. The set is
// fixed at v1; new events are additive, never renamed (same
// stability promise as internal/hooks).
type Event string

const (
	// EventPreCommit fires before the Commit core tool finalises
	// a commit. Rules here gate message format, file scope, etc.
	EventPreCommit Event = "pre_commit"
	// EventPostEdit fires after Edit / Write succeed. Rules here
	// track "you edited X, now you must edit Y" pairings.
	EventPostEdit Event = "post_edit"
	// EventSessionEnd fires when the BIAM task / agent loop
	// terminates. Last-chance gate: "did you update the README?"
	EventSessionEnd Event = "session_end"
	// EventPreSend fires before SendMessage dispatches. Rules
	// here gate routing (e.g. "code-writing tasks never go to
	// opencode" — operator's memory feedback, codified).
	EventPreSend Event = "pre_send"
	// EventPreUnattended fires when --unattended is about to
	// activate. Rules here are the safety brake before the
	// agent loop runs without operator presence.
	EventPreUnattended Event = "pre_unattended"
	// EventPreToolUse fires before a tool dispatch (Bash, Read,
	// etc.) is handed to the underlying tool. Rules here gate
	// or rewrite tool invocations — e.g. the rtk token-filter
	// rule prepends `rtk ` to allowlisted Bash commands so the
	// proxy compresses output before it reaches the agent.
	EventPreToolUse Event = "pre_tool_use"
)

// IsValidEvent guards against typos in TOML.
func IsValidEvent(e Event) bool {
	switch e {
	case EventPreCommit, EventPostEdit, EventSessionEnd,
		EventPreSend, EventPreUnattended, EventPreToolUse:
		return true
	}
	return false
}

// Rule is one operator-declared invariant. Loaded from
// .clawtool/rules.toml and evaluated against a Context at the
// matching Event.
type Rule struct {
	Name        string   `toml:"name"`
	Description string   `toml:"description,omitempty"`
	When        Event    `toml:"when"`
	Condition   string   `toml:"condition"`
	Severity    Severity `toml:"severity"`
	Hint        string   `toml:"hint,omitempty"`

	// parsed is the compiled condition AST. Populated by
	// loader.go; Evaluate uses this rather than re-parsing.
	parsed expr
}

// Context is what conditions evaluate against. The caller
// populates the fields relevant to the firing event; unset fields
// behave as their zero value (empty slices, zero counts).
//
// Fields are intentionally named to match the predicate vocabulary
// (e.g. ChangedPaths backs `changed(glob)`, CommitMessage backs
// `commit_message_contains(s)`).
type Context struct {
	// Event is the lifecycle stage producing the evaluation. A
	// rule whose `when` doesn't match Event is skipped without
	// being parsed.
	Event Event

	// ChangedPaths lists the files modified in the current
	// session / commit / edit. Forward-slash paths relative to
	// the repo root. Backs `changed(glob)` and `any_change(glob)`.
	ChangedPaths []string

	// CommitMessage is the proposed commit message body (incl.
	// trailers). Empty when Event != EventPreCommit. Backs
	// `commit_message_contains(s)`.
	CommitMessage string

	// ToolCalls counts tool invocations in the current session
	// keyed by tool name. Backs `tool_call_count(name) > N`.
	ToolCalls map[string]int

	// Now is injected so tests can pin time. Loader-built
	// contexts default to time.Now().
	Now time.Time

	// Args carries free-form key→string values — escape hatch
	// for predicates that don't deserve a typed field yet
	// (e.g. SendMessage's target instance for EventPreSend).
	// Backs `arg(key) == value`.
	Args map[string]string
}

// Result is one rule's verdict against one Context.
type Result struct {
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Passed   bool     `json:"passed"`
	// Reason is the human-readable justification. Empty when
	// Passed is true — passing rules are silent.
	Reason string `json:"reason,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

// Verdict aggregates the result of evaluating every applicable rule
// against one Context. Callers act on Blocked first (hard stop);
// Warnings are non-fatal but should be surfaced.
type Verdict struct {
	Event    Event    `json:"event"`
	Results  []Result `json:"results"`
	Warnings []Result `json:"warnings,omitempty"`
	Blocked  []Result `json:"blocked,omitempty"`
}

// Blocked reports whether at least one block-severity rule failed.
// Callers MUST consult this before proceeding with the action the
// rules guarded.
func (v Verdict) IsBlocked() bool { return len(v.Blocked) > 0 }
