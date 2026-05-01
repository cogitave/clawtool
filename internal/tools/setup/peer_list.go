// Package setuptools — `PeerList` MCP tool.
//
// Today's MCP surface lets a calling agent dispatch / send messages
// (PeerSend, SendMessage, AutonomousRun) but has no way to *discover*
// what BIAM peers are currently registered. The CLI verb
// `clawtool peer list` covers the operator path; this tool covers
// the chat-driven path so an in-context agent can answer "what
// other Claude Code / Codex / Gemini sessions are reachable right
// now" without shelling out.
//
// Read-only. Talks to the local daemon via daemon.HTTPRequest —
// same auth + 5s timeout + XDG state path the rest of the peer
// surface uses. The handler does no filtering itself; everything
// is forwarded as a query param so the daemon's a2a.Registry.List
// is the single source of truth.
package setuptools

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/a2a"
	"github.com/cogitave/clawtool/internal/daemon"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// peerListResult mirrors the daemon's GET /v1/peers response so
// the structured-content payload matches what an operator would
// see via curl. Field order: peers first (the data the caller
// needs), count + as_of last (provenance the agent can echo back).
type peerListResult struct {
	Peers []a2a.Peer `json:"peers"`
	Count int        `json:"count"`
	AsOf  time.Time  `json:"as_of"`
}

// peerListHTTP is the indirection seam tests substitute. Default
// dials the local daemon via daemon.HTTPRequest; tests stub it to
// return a fixture without spinning up an httptest server. We keep
// the indirection at package level rather than inside Runtime so
// the existing `Register: func(s, _ Runtime)` shape in
// internal/tools/core/manifest.go stays untouched.
var peerListHTTP = func(method, path string, body *bytes.Reader, out any) error {
	return daemon.HTTPRequest(method, path, body, out)
}

// RegisterPeerList wires the PeerList MCP tool to s. Mirrors
// RegisterOnboardStatus's shape so the manifest row is a one-liner.
func RegisterPeerList(s *server.MCPServer) {
	s.AddTool(
		mcp.NewTool(
			"PeerList",
			mcp.WithDescription(
				"Snapshot of every BIAM peer (Claude Code / Codex / Gemini / OpenCode session, recipe-installed agent) currently registered + heartbeating with the local clawtool daemon. Returns the same shape as GET /v1/peers — peers + count + as_of. Read-only.",
			),
			mcp.WithString("circle",
				mcp.Description("Optional: narrow to peers in this circle (group). Empty returns every peer.")),
			mcp.WithString("backend",
				mcp.Description("Optional: narrow to peers with this backend (claude-code|codex|gemini|opencode|clawtool).")),
			mcp.WithString("status",
				mcp.Description("Optional: narrow to peers with this status (online|busy|offline).")),
		),
		runPeerList,
	)
}

// runPeerList is the handler. Pure read; never writes.
func runPeerList(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	q := url.Values{}
	if v := strings.TrimSpace(req.GetString("circle", "")); v != "" {
		q.Set("circle", v)
	}
	if v := strings.TrimSpace(req.GetString("backend", "")); v != "" {
		q.Set("backend", v)
	}
	if v := strings.TrimSpace(req.GetString("status", "")); v != "" {
		q.Set("status", v)
	}
	path := "/v1/peers"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}

	var out peerListResult
	if err := peerListHTTP(http.MethodGet, path, nil, &out); err != nil {
		return mcp.NewToolResultError("PeerList: " + err.Error()), nil
	}
	return resultOfJSON("PeerList", out)
}
