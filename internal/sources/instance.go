// Package sources spawns configured source MCP servers as child processes
// and proxies tools/list + tools/call between them and clawtool's own MCP
// server. Per ADR-006 each child's tools are exposed as
// `<instance>__<tool>` so multi-instance setups (two GitHubs etc.) cannot
// collide. Per ADR-007 we wrap mark3labs/mcp-go's client rather than
// reimplement MCP transport.
package sources

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// Status is the lifecycle state of a single source instance.
type Status string

const (
	StatusStarting        Status = "starting"
	StatusRunning         Status = "running"
	StatusDown            Status = "down"
	StatusUnauthenticated Status = "unauthenticated"
)

// Instance is one spawned source MCP server.
//
// Lifetime model:
//   - Created via Manager.startInstance(); Initialize + ListTools cache the
//     advertised tool surface.
//   - Concurrent CallTool calls are mediated by mark3labs/mcp-go's client
//     which is itself goroutine-safe over a single stdio transport.
//   - Stop closes the client which kills the child process.
type Instance struct {
	Name      string         // kebab-case instance name (selector form)
	Spec      Spec           // immutable spawn spec
	Client    *client.Client // nil when status != Running
	Tools     []mcp.Tool     // snapshot from ListTools at start
	StartedAt time.Time
	statusMu  sync.RWMutex
	status    Status
	statusErr string
}

// Spec is the resolved spawn input for one source. The config + secrets
// layers produce this; the manager consumes it.
type Spec struct {
	Command []string          // argv, command[0] is the binary
	Env     map[string]string // resolved env (already merged with required_env)
}

func newInstance(name string, spec Spec) *Instance {
	return &Instance{
		Name:   name,
		Spec:   spec,
		status: StatusStarting,
	}
}

// Status returns the current lifecycle state along with any non-empty
// reason string explaining a non-Running state.
func (i *Instance) Status() (Status, string) {
	i.statusMu.RLock()
	defer i.statusMu.RUnlock()
	return i.status, i.statusErr
}

func (i *Instance) setStatus(s Status, reason string) {
	i.statusMu.Lock()
	defer i.statusMu.Unlock()
	i.status = s
	i.statusErr = reason
}

// start spawns the child process, completes the MCP handshake, and caches
// the child's tools. Errors transition the instance to Down with the
// reason string preserved for surface in `source list`.
func (i *Instance) start(ctx context.Context) error {
	if len(i.Spec.Command) == 0 {
		i.setStatus(StatusDown, "empty command")
		return errors.New("empty command")
	}
	command := i.Spec.Command[0]
	args := i.Spec.Command[1:]

	// Compose env: process env, then overlay the resolved per-source env.
	// NewStdioMCPClient takes []string of "KEY=VALUE".
	env := append([]string{}, os.Environ()...)
	for k, v := range i.Spec.Env {
		env = append(env, k+"="+v)
	}

	c, err := client.NewStdioMCPClient(command, env, args...)
	if err != nil {
		i.setStatus(StatusDown, err.Error())
		return fmt.Errorf("spawn %s: %w", command, err)
	}

	initCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{
		Name:    "clawtool",
		Version: "0.4.0-dev",
	}
	if _, err := c.Initialize(initCtx, initReq); err != nil {
		_ = c.Close()
		// Distinguish auth failures (vague but useful at this layer).
		reason := err.Error()
		if isAuthError(reason) {
			i.setStatus(StatusUnauthenticated, reason)
			return fmt.Errorf("initialize %s (auth?): %w", i.Name, err)
		}
		i.setStatus(StatusDown, reason)
		return fmt.Errorf("initialize %s: %w", i.Name, err)
	}

	listCtx, cancel2 := context.WithTimeout(ctx, 10*time.Second)
	defer cancel2()
	tools, err := c.ListTools(listCtx, mcp.ListToolsRequest{})
	if err != nil {
		_ = c.Close()
		i.setStatus(StatusDown, "list_tools: "+err.Error())
		return fmt.Errorf("list tools %s: %w", i.Name, err)
	}

	i.Client = c
	i.Tools = tools.Tools
	i.StartedAt = time.Now()
	i.setStatus(StatusRunning, "")
	return nil
}

// callTool forwards a tools/call to the child instance using the *child's*
// (un-prefixed) tool name and arguments. Returns the child's CallToolResult
// untouched; caller wraps for clawtool's own MCP server.
func (i *Instance) callTool(ctx context.Context, toolName string, args map[string]any) (*mcp.CallToolResult, error) {
	if i.Client == nil {
		return nil, fmt.Errorf("instance %q is not running", i.Name)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = args
	return i.Client.CallTool(ctx, req)
}

// stop closes the client which terminates the child process.
func (i *Instance) stop() {
	if i.Client != nil {
		_ = i.Client.Close()
		i.Client = nil
	}
	i.setStatus(StatusDown, "stopped")
}

// isAuthError is a string-sniffing heuristic. The MCP protocol does not
// (yet) standardize auth failure codes at initialize time; child servers
// typically return error messages mentioning "auth", "credential",
// "token", or "401". We only use this to colour the source-list output;
// it does not affect routing.
func isAuthError(s string) bool {
	keywords := []string{"unauthorized", "401", "auth", "credential", "token", "permission"}
	low := toLower(s)
	for _, k := range keywords {
		if contains(low, k) {
			return true
		}
	}
	return false
}

// toLower / contains kept as small helpers to avoid pulling in `strings` in
// the inner hot path. (Tiny micro-opt; safe to switch later.)
func toLower(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
