// Package hooks — user-defined shell-command hooks for clawtool
// lifecycle events (ADR-014 F3, Claude Code parity).
//
// Pattern: every clawtool call site that wants to expose a hook
// emits one event; hooks.Emit fans the event out to every configured
// HookEntry under the matching event name. Events carry structured
// JSON metadata that lands on the script's stdin, so user scripts
// stay free of argv parsing. Failures default to log-and-continue;
// `block_on_error = true` flips that for guard-rail hooks.
//
// Per ADR-007 we wrap stdlib (`os/exec` + `encoding/json`); we don't
// invent an event-bus or RPC.
package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cogitave/clawtool/internal/config"
)

// Event is the canonical name string. Locked at v0.15; new events
// are additive, never renamed.
type Event string

const (
	EventPreSend         Event = "pre_send"
	EventPostSend        Event = "post_send"
	EventOnTaskComplete  Event = "on_task_complete"
	EventPreEdit         Event = "pre_edit"
	EventPostEdit        Event = "post_edit"
	EventPreBridgeAdd    Event = "pre_bridge_add"
	EventPostRecipeApply Event = "post_recipe_apply"
	EventOnServerStart   Event = "on_server_start"
	EventOnServerStop    Event = "on_server_stop"
)

// Manager is the process-wide hooks dispatcher. One per clawtool
// process; SetGlobal registers it. Nil manager → Emit is a no-op.
type Manager struct {
	cfg     config.HooksConfig
	emitted atomic.Uint64 // count of fires (telemetry / tests)
}

// New wires a Manager from the config block. Nil-safe; an empty
// HooksConfig yields a Manager whose Emit is a no-op.
func New(cfg config.HooksConfig) *Manager {
	return &Manager{cfg: cfg}
}

var (
	globalMu sync.RWMutex
	global   *Manager
)

// SetGlobal registers the process-wide manager. Idempotent.
func SetGlobal(m *Manager) {
	globalMu.Lock()
	defer globalMu.Unlock()
	global = m
}

// Get returns the process-wide manager (or nil when none set).
func Get() *Manager {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global
}

// Emit fires `event` against every configured HookEntry. Returns nil
// for non-blocking hooks; only block_on_error entries propagate
// failure. Safe to call with a nil manager (no-op) and with
// unregistered events (no-op).
func (m *Manager) Emit(ctx context.Context, event Event, payload map[string]any) error {
	if m == nil || len(m.cfg.Events) == 0 {
		return nil
	}
	entries, ok := m.cfg.Events[string(event)]
	if !ok || len(entries) == 0 {
		return nil
	}
	m.emitted.Add(1)

	body, err := encodePayload(event, payload)
	if err != nil {
		return fmt.Errorf("hooks: encode payload: %w", err)
	}

	var firstBlocking error
	for _, e := range entries {
		if err := runEntry(ctx, e, body); err != nil && e.BlockOnErr && firstBlocking == nil {
			firstBlocking = fmt.Errorf("hooks/%s: %w", event, err)
		}
	}
	return firstBlocking
}

// EmitCount reports how many events have fired (regardless of
// per-entry success). Useful for tests and the future `clawtool
// hooks status` subcommand.
func (m *Manager) EmitCount() uint64 {
	if m == nil {
		return 0
	}
	return m.emitted.Load()
}

// runEntry exec's one HookEntry with `body` on stdin. Cmd is shell-
// evaluated; Argv runs as a literal exec (skipping the shell). Stderr
// + stdout are captured into the same buffer so the operator can tail
// failures via clawtool's standard logging.
//
// Timeout enforcement uses a wall-clock AfterFunc + Process.Kill
// instead of exec.CommandContext: the latter relies on stdin/stdout
// goroutines exiting before Wait returns, which can stall on WSL /
// containers when the child's stdio is still attached to a closed
// pipe. AfterFunc + Kill guarantees Run() returns within ~timeout.
func runEntry(ctx context.Context, e config.HookEntry, body []byte) error {
	timeout := time.Duration(e.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	var cmd *exec.Cmd
	switch {
	case len(e.Argv) > 0:
		cmd = exec.Command(e.Argv[0], e.Argv[1:]...)
	case e.Cmd != "":
		cmd = exec.Command("/bin/sh", "-c", e.Cmd)
	default:
		return fmt.Errorf("hook entry has neither cmd nor argv")
	}
	cmd.Stdin = bytes.NewReader(body)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("hook start: %w", err)
	}
	timedOut := false
	timer := time.AfterFunc(timeout, func() {
		timedOut = true
		_ = cmd.Process.Kill()
	})
	// Honour parent ctx cancellation too — kill the hook if the
	// originating operation already exited.
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Kill()
		case <-stop:
		}
	}()
	err := cmd.Wait()
	close(stop)
	timer.Stop()
	if timedOut {
		return fmt.Errorf("hook timeout after %s: %s", timeout, truncate(combined.String(), 256))
	}
	if err != nil {
		return fmt.Errorf("%w: %s", err, truncate(combined.String(), 256))
	}
	return nil
}

func encodePayload(event Event, payload map[string]any) ([]byte, error) {
	envelope := map[string]any{
		"event":   string(event),
		"payload": payload,
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
	}
	return json.Marshal(envelope)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Compile-time guard so io stays imported when we add a streaming
// hook in v0.16.
var _ = io.Discard
