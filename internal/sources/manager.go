package sources

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/cogitave/clawtool/internal/config"
	"github.com/cogitave/clawtool/internal/secrets"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Manager owns the lifecycle of all configured source instances and routes
// tools/call invocations between clawtool's own MCP server and the right
// child. Created from a Config + Secrets pair; started once; stopped on
// clawtool shutdown.
type Manager struct {
	cfg       config.Config
	secrets   *secrets.Store
	instances map[string]*Instance // keyed by instance name
	mu        sync.RWMutex
	startErrs map[string]error // per-instance start failures, kept for surface
}

// NewManager builds an empty manager from config + secrets. No processes
// are spawned until Start is called.
func NewManager(cfg config.Config, sec *secrets.Store) *Manager {
	return &Manager{
		cfg:       cfg,
		secrets:   sec,
		instances: map[string]*Instance{},
		startErrs: map[string]error{},
	}
}

// Start spawns every configured source. A failure on one instance does
// NOT abort the whole manager — others continue starting. The combined
// error (if any) is returned for caller logging; callers may proceed
// regardless because failures are also recorded per-instance for the
// `source list` surface.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var firstErr error
	for name, src := range m.cfg.Sources {
		spec, missing := m.specFor(name, src)
		inst := newInstance(name, spec)
		m.instances[name] = inst

		if len(missing) > 0 {
			inst.setStatus(StatusUnauthenticated, "missing env: "+strings.Join(missing, ", "))
			err := fmt.Errorf("source %q: missing required env: %s", name, strings.Join(missing, ", "))
			m.startErrs[name] = err
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := inst.start(ctx); err != nil {
			m.startErrs[name] = err
			if firstErr == nil {
				firstErr = err
			}
			// instance status is already set by start()
			continue
		}
	}
	return firstErr
}

// Stop reaps every instance. Idempotent.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, inst := range m.instances {
		inst.stop()
	}
}

// SourceTool pairs a wire-form tool with its routing handler so that the
// caller can register both with the parent MCP server in one shot.
type SourceTool struct {
	Tool    mcp.Tool
	Handler server.ToolHandlerFunc
}

// AggregatedTools returns one SourceTool per (running instance × tool)
// combination. Names are wire-form `<instance>__<tool>` per ADR-006. The
// handler closes over (instance, original tool name) and routes the call.
//
// Tools from instances in non-Running state are silently omitted; the
// list is the source of truth for what clawtool advertises *now*.
func (m *Manager) AggregatedTools() []SourceTool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Stable order across runs makes tools/list output deterministic.
	names := make([]string, 0, len(m.instances))
	for n := range m.instances {
		names = append(names, n)
	}
	sort.Strings(names)

	var out []SourceTool
	for _, name := range names {
		inst := m.instances[name]
		if s, _ := inst.Status(); s != StatusRunning {
			continue
		}
		instanceName := name
		instanceRef := inst
		for _, t := range inst.Tools {
			tool := t // copy, then rename
			tool.Name = instanceName + "__" + t.Name
			origName := t.Name

			handler := func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				args, _ := req.Params.Arguments.(map[string]any)
				result, err := instanceRef.callTool(ctx, origName, args)
				if err != nil {
					return mcp.NewToolResultError(
						fmt.Sprintf("source %q tool %q failed: %v", instanceName, origName, err),
					), nil
				}
				return result, nil
			}
			out = append(out, SourceTool{Tool: tool, Handler: handler})
		}
	}
	return out
}

// HealthReport is a snapshot used by future surface (`clawtool source list
// --runtime`). Each row carries enough info to print the same way every
// time without exposing implementation internals.
type HealthReport struct {
	Name      string
	Status    Status
	Reason    string
	ToolCount int
	Package   string // best-effort: command[1] when command[0] is a runner
}

// Health returns sorted-by-name HealthReport snapshots for every managed
// instance. Safe to call from any goroutine; takes a read lock.
func (m *Manager) Health() []HealthReport {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.instances))
	for n := range m.instances {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]HealthReport, 0, len(names))
	for _, n := range names {
		inst := m.instances[n]
		s, reason := inst.Status()
		pkg := ""
		if cmd := m.cfg.Sources[n].Command; len(cmd) > 1 {
			pkg = cmd[1]
		}
		out = append(out, HealthReport{
			Name:      n,
			Status:    s,
			Reason:    reason,
			ToolCount: len(inst.Tools),
			Package:   pkg,
		})
	}
	return out
}

// specFor resolves the spec for one source — substituting secrets into
// both the env template AND command argv, reporting any unresolved keys.
//
// Argv substitution matters for catalog entries whose `args` reference
// the same env vars they list in `required_env`. Example: filesystem's
// catalog entry uses `args = ["${FILESYSTEM_ROOT}"]` so the path winds
// up as a CLI argument to the npx-spawned server. Without this we'd
// hand `${FILESYSTEM_ROOT}` to the child verbatim.
func (m *Manager) specFor(name string, src config.Source) (Spec, []string) {
	resolved, missingEnv := m.secrets.Resolve(name, src.Env)

	cmd := make([]string, len(src.Command))
	missingArgs := map[string]bool{}
	for i, arg := range src.Command {
		expanded, missing := m.secrets.Expand(name, arg)
		cmd[i] = expanded
		for _, k := range missing {
			missingArgs[k] = true
		}
	}

	// Merge missing keys (env first, then argv-only). Dedup against env.
	missing := append([]string{}, missingEnv...)
	envSet := map[string]bool{}
	for _, k := range missingEnv {
		envSet[k] = true
	}
	for k := range missingArgs {
		if !envSet[k] {
			missing = append(missing, k)
		}
	}

	return Spec{Command: cmd, Env: resolved}, missing
}

// SplitWireName parses a `<instance>__<tool>` selector back into its parts.
// Used by callers that already received an aggregated tool name and need to
// route. Per ADR-006 the separator is two underscores and instance names
// don't contain underscores.
func SplitWireName(wire string) (instance, tool string, ok bool) {
	idx := strings.Index(wire, "__")
	if idx <= 0 || idx+2 >= len(wire) {
		return "", "", false
	}
	return wire[:idx], wire[idx+2:], true
}

// Errors that callers care about by identity.
var ErrNoSuchInstance = errors.New("no such source instance")
