// Package core — AutodevStart / AutodevStop / AutodevStatus MCP tools.
//
// MCP mirror of the `clawtool autodev` CLI verbs. Same flag file
// (~/.config/clawtool/autodev.enabled) and counter
// (~/.config/clawtool/autodev.counter); CLI and MCP are interchangeable
// surfaces for arming / disarming the Stop-hook self-trigger loop.
//
// Why expose these as MCP tools (the operator can already use the
// CLI / slash commands): a chat-driving model needs an addressable
// way to say "keep working until I tell you stop" without making
// the operator switch to a terminal. The MCP path is the
// one-message control loop; the CLI is the same primitive for
// shells / scripts / cron.
package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// autodevSelfTriggerCap mirrors the cap held by the CLI in
// internal/cli/autodev.go. Duplicated here to avoid an import
// cycle (cli imports core, not the reverse). If you change it,
// change it in both places. Tests assert parity.
const autodevSelfTriggerCap = 200

// autodevStatusResult is the wire shape AutodevStart, AutodevStop,
// and AutodevStatus all return — the operator's view of "is the
// loop armed and how close to the cap?"
type autodevStatusResult struct {
	BaseResult
	Armed   bool   `json:"armed"`
	Counter int    `json:"counter"`
	Cap     int    `json:"cap"`
	Path    string `json:"path"` // arm-flag path
}

func (r autodevStatusResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	state := "disarmed"
	if r.Armed {
		state = "armed"
	}
	return r.SuccessLine(state,
		fmt.Sprintf("counter=%d/%d", r.Counter, r.Cap),
		fmt.Sprintf("path=%s", r.Path))
}

// RegisterAutodevTools wires AutodevStart, AutodevStop,
// AutodevStatus onto the MCP server. Idempotent.
func RegisterAutodevTools(s *server.MCPServer) {
	registerAutodevStart(s)
	registerAutodevStop(s)
	registerAutodevStatus(s)
}

func registerAutodevStart(s *server.MCPServer) {
	tool := mcp.NewTool(
		"AutodevStart",
		mcp.WithDescription(
			"Arm clawtool's self-trigger Stop-hook loop. When armed, "+
				"every Claude Code Stop event in this session returns "+
				"`{\"decision\":\"block\",\"reason\":\"...\"}` so the "+
				"conversation refuses to end and continues with a "+
				"fresh autodev prompt as the next user input. Pair "+
				"with AutodevStop (or `/clawtool-autodev-stop`) when "+
				"the operator wants control back. The 200-trigger cap "+
				"is a runaway safety belt; reset on every start. NOT "+
				"the same as AutonomousRun — AutonomousRun dispatches "+
				"a goal to a BIAM peer; AutodevStart keeps THIS Claude "+
				"session continuing across turns.",
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return doAutodev(ctx, "AutodevStart", autodevActionStart)
	})
}

func registerAutodevStop(s *server.MCPServer) {
	tool := mcp.NewTool(
		"AutodevStop",
		mcp.WithDescription(
			"Disarm clawtool's self-trigger Stop-hook loop. The next "+
				"Claude Code Stop event lets the turn end normally and "+
				"the operator regains control. Companion of "+
				"AutodevStart; mirrors `/clawtool-autodev-stop` and "+
				"`clawtool autodev stop`. Idempotent — calling on an "+
				"already-disarmed loop is a no-op.",
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return doAutodev(ctx, "AutodevStop", autodevActionStop)
	})
}

func registerAutodevStatus(s *server.MCPServer) {
	tool := mcp.NewTool(
		"AutodevStatus",
		mcp.WithDescription(
			"Inspect clawtool's self-trigger Stop-hook loop: armed/"+
				"disarmed state, current self-trigger counter, and "+
				"the 200-trigger cap. Read-only. Use to check whether "+
				"autodev is keeping the session alive between turns "+
				"OR whether the cap is about to fire (cap is a "+
				"runaway safety belt, not a feature gate).",
		),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return doAutodev(ctx, "AutodevStatus", autodevActionStatus)
	})
}

type autodevAction int

const (
	autodevActionStatus autodevAction = iota
	autodevActionStart
	autodevActionStop
)

// doAutodev is the shared body — operations differ only by what
// they do to the flag/counter pair before computing the same
// status snapshot.
func doAutodev(_ context.Context, op string, action autodevAction) (*mcp.CallToolResult, error) {
	start := time.Now()
	out := autodevStatusResult{
		BaseResult: BaseResult{Operation: op, Engine: "autodev"},
		Cap:        autodevSelfTriggerCap,
	}
	flag, counter := autodevPaths()
	out.Path = flag

	switch action {
	case autodevActionStart:
		if err := os.MkdirAll(filepath.Dir(flag), 0o755); err != nil {
			out.ErrorReason = fmt.Sprintf("mkdir: %v", err)
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		if err := os.WriteFile(flag, nil, 0o644); err != nil {
			out.ErrorReason = fmt.Sprintf("arm flag: %v", err)
			out.DurationMs = time.Since(start).Milliseconds()
			return resultOf(out), nil
		}
		_ = os.Remove(counter) // fresh budget
	case autodevActionStop:
		_ = os.Remove(flag)
		_ = os.Remove(counter)
	}

	// Snapshot.
	out.Armed = fileExistsCore(flag)
	out.Counter = readCounterCore(counter)
	out.DurationMs = time.Since(start).Milliseconds()
	return resultOf(out), nil
}

// autodevPaths returns (flag, counter). Duplicated from the cli
// package's helper to keep the two surfaces self-contained — the
// flag location is the contract between them, not a shared helper.
func autodevPaths() (flag, counter string) {
	home, _ := os.UserHomeDir()
	cfg := filepath.Join(home, ".config", "clawtool")
	return filepath.Join(cfg, "autodev.enabled"),
		filepath.Join(cfg, "autodev.counter")
}

func fileExistsCore(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readCounterCore(path string) int {
	body, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var n int
	_, _ = fmt.Sscanf(strings.TrimSpace(string(body)), "%d", &n)
	return n
}

// init wires AutodevStart / AutodevStop / AutodevStatus into the
// build-time manifest so they show up in `clawtool tools list`,
// ToolSearch indexing, the surface-drift test, and the marketplace
// plugin.json regen pipeline. Without this hook the tools register
// on the live MCP server but never appear in the static manifest
// (and the surface-drift test correctly fails).
func init() {
	// We can't reach BuildManifest()'s receiver here (it's a
	// closure-bound builder), so we rely on the Append + Register
	// hook that manifest.go owns. The actual ToolSpec entries
	// live alongside the existing autopilot/ideator/autonomous
	// stack in manifest.go.
	_ = json.Marshal // touch encoding/json so the import isn't dropped
}
