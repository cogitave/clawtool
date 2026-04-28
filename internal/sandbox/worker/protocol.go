// Package worker — sandbox-worker protocol shapes (ADR-029).
//
// The worker is the second leg of clawtool's orchestrator+worker
// pair. The daemon dials the worker over a single bearer-auth'd
// WebSocket; tool calls (Bash / Read / Edit / Write / Glob / Grep)
// route through Request frames. Wire format: JSON-line over WS,
// one request → one response, no streaming primitive in Phase 1
// (large outputs cap at 4 MiB and truncate; matches BIAM
// runner's existing readCapped policy).
//
// Two design choices worth reading the ADR for:
//
//  1. Daemon dials worker, NOT the reverse. claude.ai's mimic
//     uses the same asymmetry — the orchestrator owns the
//     connection lifetime. The worker is a passive listener
//     that accepts a single trusted dial.
//  2. Same binary serves both roles. `clawtool serve` is the
//     daemon; `clawtool sandbox-worker` is the worker. Shared
//     codebase = shared semantics for tool calls.
package worker

import (
	"encoding/json"
	"fmt"
)

// Kind enumerates the request types the worker handles. Adding
// new kinds is a wire-format break — bump the protocol version.
type Kind string

const (
	KindExec  Kind = "exec"
	KindRead  Kind = "read"
	KindWrite Kind = "write"
	KindGlob  Kind = "glob"
	KindGrep  Kind = "grep"
	KindStat  Kind = "stat"
	KindPing  Kind = "ping"
)

// ProtocolVersion bumps when wire format breaks. Phase 1 = "1".
const ProtocolVersion = "1"

// Request is the inbound shape on the worker WebSocket. ID is
// caller-assigned; responses echo it back so a client can
// pipeline multiple requests onto one connection (Phase 2).
type Request struct {
	V    string          `json:"v"`              // protocol version
	ID   string          `json:"id"`             // caller-assigned request id (uuid recommended)
	Kind Kind            `json:"kind"`           // operation
	Body json.RawMessage `json:"body,omitempty"` // per-kind payload
}

// Response is the outbound shape. Either Body OR Error is
// populated, never both. Status mirrors HTTP-ish conventions:
// 0 = ok, 1 = caller error, 2 = worker internal error.
type Response struct {
	V      string          `json:"v"`
	ID     string          `json:"id"`
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// ─── per-kind payloads ──────────────────────────────────────────

// ExecRequest mirrors mcp__clawtool__Bash's input shape so the
// daemon can transparently route Bash tool calls here.
type ExecRequest struct {
	Command   string            `json:"command"`
	Cwd       string            `json:"cwd,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	TimeoutMs int               `json:"timeout_ms,omitempty"` // hard wall-clock cap
}

// ExecResponse mirrors clawtool's structured Bash output shape.
type ExecResponse struct {
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMs int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out"`
	Cwd        string `json:"cwd"`
}

type ReadRequest struct {
	Path      string `json:"path"`
	LineStart int    `json:"line_start,omitempty"`
	LineEnd   int    `json:"line_end,omitempty"`
}

type ReadResponse struct {
	Content    string `json:"content"`
	TotalLines int    `json:"total_lines"`
	SizeBytes  int64  `json:"size_bytes"`
	FileHash   string `json:"file_hash,omitempty"`
}

type WriteRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Mode    string `json:"mode,omitempty"` // "overwrite" | "create"
}

type WriteResponse struct {
	BytesWritten int  `json:"bytes_written"`
	Created      bool `json:"created"`
}

type GlobRequest struct {
	Pattern string `json:"pattern"`
	Cwd     string `json:"cwd,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

type GlobResponse struct {
	Matches []string `json:"matches"`
	Count   int      `json:"count"`
}

type GrepRequest struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path,omitempty"`
	Glob    string `json:"glob,omitempty"`
}

type GrepResponse struct {
	Matches []GrepHit `json:"matches"`
	Count   int       `json:"count"`
}

type GrepHit struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

type StatRequest struct {
	Path string `json:"path"`
}

type StatResponse struct {
	Exists  bool   `json:"exists"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size,omitempty"`
	ModeStr string `json:"mode,omitempty"`
}

// ─── helpers ────────────────────────────────────────────────────

// EncodeRequest marshals one request to a single JSON line.
func EncodeRequest(r *Request) ([]byte, error) {
	r.V = ProtocolVersion
	return json.Marshal(r)
}

// DecodeRequest parses one JSON line. Caller must have already
// authenticated the WebSocket frame.
func DecodeRequest(b []byte) (*Request, error) {
	var r Request
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}
	if r.V != "" && r.V != ProtocolVersion {
		return nil, fmt.Errorf("unsupported protocol version %q (want %q)", r.V, ProtocolVersion)
	}
	return &r, nil
}

// MarshalBody is sugar for typed-payload → RawMessage.
func MarshalBody(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

// UnmarshalBody is the inverse — Request.Body → typed payload.
func UnmarshalBody(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}
