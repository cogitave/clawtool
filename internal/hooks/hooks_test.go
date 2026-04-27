package hooks

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/config"
)

func TestEmit_NoManager_NoOp(t *testing.T) {
	var m *Manager
	if err := m.Emit(context.Background(), EventPreSend, map[string]any{}); err != nil {
		t.Errorf("nil manager Emit should be no-op; got %v", err)
	}
	if m.EmitCount() != 0 {
		t.Error("nil manager should report 0 emits")
	}
}

func TestEmit_EmptyConfig_NoOp(t *testing.T) {
	m := New(config.HooksConfig{})
	if err := m.Emit(context.Background(), EventPreSend, map[string]any{}); err != nil {
		t.Error(err)
	}
	if m.EmitCount() != 0 {
		t.Errorf("empty config should not increment emits; got %d", m.EmitCount())
	}
}

func TestEmit_RunsConfiguredEntry(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "flag")
	cfg := config.HooksConfig{
		Events: map[string][]config.HookEntry{
			"pre_send": {
				{Cmd: "touch " + flag},
			},
		},
	}
	m := New(cfg)
	if err := m.Emit(context.Background(), EventPreSend, map[string]any{"x": 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(flag); err != nil {
		t.Errorf("hook should have touched flag file: %v", err)
	}
	if m.EmitCount() != 1 {
		t.Errorf("EmitCount: got %d, want 1", m.EmitCount())
	}
}

func TestEmit_BlockOnError_PropagatesFailure(t *testing.T) {
	cfg := config.HooksConfig{
		Events: map[string][]config.HookEntry{
			"pre_send": {{Cmd: "exit 1", BlockOnErr: true}},
		},
	}
	m := New(cfg)
	err := m.Emit(context.Background(), EventPreSend, nil)
	if err == nil {
		t.Error("block_on_error hook failure should propagate")
	}
}

func TestEmit_NonBlocking_FailureSwallowed(t *testing.T) {
	cfg := config.HooksConfig{
		Events: map[string][]config.HookEntry{
			"pre_send": {{Cmd: "exit 1"}}, // no BlockOnErr
		},
	}
	m := New(cfg)
	if err := m.Emit(context.Background(), EventPreSend, nil); err != nil {
		t.Errorf("non-blocking failure should not propagate; got %v", err)
	}
}

func TestEmit_Argv_SkipsShell(t *testing.T) {
	dir := t.TempDir()
	flag := filepath.Join(dir, "argv-flag")
	cfg := config.HooksConfig{
		Events: map[string][]config.HookEntry{
			"pre_edit": {
				{Argv: []string{"touch", flag}},
			},
		},
	}
	m := New(cfg)
	if err := m.Emit(context.Background(), EventPreEdit, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(flag); err != nil {
		t.Errorf("argv hook should have touched flag: %v", err)
	}
}

func TestEmit_Timeout(t *testing.T) {
	// Linux process-group reaping for shell children is left to the
	// future ProcessGroup helper (the same one applyProcessGroup
	// uses for the Bash tool). This test exercises the immediate
	// path: a hook that returns non-zero before the timeout fires
	// surfaces as an error promptly, with no shell-stall risk.
	cfg := config.HooksConfig{
		Events: map[string][]config.HookEntry{
			"pre_send": {{Cmd: "exit 7", BlockOnErr: true, TimeoutMs: 1000}},
		},
	}
	m := New(cfg)
	start := time.Now()
	err := m.Emit(context.Background(), EventPreSend, nil)
	if err == nil {
		t.Fatal("expected error from non-zero hook")
	}
	if !strings.Contains(err.Error(), "exit") && !strings.Contains(err.Error(), "7") {
		t.Errorf("error should mention exit code or status: %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("non-zero hook should fail fast; took %v", elapsed)
	}
}

func TestEmit_PayloadOnStdin(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "payload.json")
	cfg := config.HooksConfig{
		Events: map[string][]config.HookEntry{
			"on_task_complete": {
				{Cmd: "cat > " + out},
			},
		},
	}
	m := New(cfg)
	payload := map[string]any{"task_id": "abc-123", "agent": "codex"}
	if err := m.Emit(context.Background(), EventOnTaskComplete, payload); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "abc-123") {
		t.Errorf("hook should have received payload on stdin: %s", body)
	}
	// Decode the envelope shape and verify event field is set.
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if env["event"] != "on_task_complete" {
		t.Errorf("envelope event field: %v", env["event"])
	}
}

func TestSetGlobal_GetGlobal(t *testing.T) {
	old := Get()
	t.Cleanup(func() { SetGlobal(old) })

	m := New(config.HooksConfig{Events: map[string][]config.HookEntry{
		"pre_send": {{Cmd: "true"}},
	}})
	SetGlobal(m)
	if got := Get(); got != m {
		t.Error("SetGlobal/Get round-trip mismatch")
	}
	SetGlobal(nil)
	if Get() != nil {
		t.Error("SetGlobal(nil) should clear")
	}
}
