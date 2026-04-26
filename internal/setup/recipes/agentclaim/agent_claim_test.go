package agentclaim

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/setup"
)

// withTempClaudeCode redirects the claude-code adapter to a tempdir so
// tests don't touch the real ~/.claude. Returns a cleanup that restores
// the override.
func withTempClaudeCode(t *testing.T) func() {
	t.Helper()
	dir := t.TempDir()
	prev := ""
	// Find current override via a getter the agents package doesn't
	// expose; mirror what agents_test does and just stash empty.
	agents.SetClaudeCodeSettingsPath(filepath.Join(dir, "settings.json"))
	return func() {
		agents.SetClaudeCodeSettingsPath(prev)
	}
}

func TestAgentClaim_Registered(t *testing.T) {
	r := setup.Lookup("agent-claim")
	if r == nil {
		t.Fatal("agent-claim recipe should self-register via init()")
	}
	if r.Meta().Category != setup.CategoryAgents {
		t.Errorf("wrong category: %q", r.Meta().Category)
	}
	if r.Meta().Upstream == "" {
		t.Error("Upstream must be set")
	}
}

func TestAgentClaim_DetectAbsentBeforeApply(t *testing.T) {
	cleanup := withTempClaudeCode(t)
	defer cleanup()

	r := setup.Lookup("agent-claim")
	status, _, err := r.Detect(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	// In an empty tempdir-rooted ~/.claude, the adapter detects no
	// directory; statuses come back with Detected=false → recipe
	// reports Absent.
	if status != setup.StatusAbsent {
		t.Errorf("got %q, want %q", status, setup.StatusAbsent)
	}
}

func TestAgentClaim_ApplyClaimsAllDetected(t *testing.T) {
	cleanup := withTempClaudeCode(t)
	defer cleanup()

	// Create the ~/.claude directory so the adapter reports detected.
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	agents.SetClaudeCodeSettingsPath(settings)

	r := setup.Lookup("agent-claim")
	if err := r.Apply(context.Background(), t.TempDir(), nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if err := r.Verify(context.Background(), t.TempDir()); err != nil {
		t.Errorf("Verify after Apply: %v", err)
	}

	status, _, _ := r.Detect(context.Background(), t.TempDir())
	if status != setup.StatusApplied {
		t.Errorf("after Apply, Detect = %q, want %q", status, setup.StatusApplied)
	}
}

func TestAgentClaim_ApplyIsIdempotent(t *testing.T) {
	cleanup := withTempClaudeCode(t)
	defer cleanup()

	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	agents.SetClaudeCodeSettingsPath(settings)

	r := setup.Lookup("agent-claim")
	if err := r.Apply(context.Background(), t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), t.TempDir(), nil); err != nil {
		t.Errorf("re-Apply should succeed; got %v", err)
	}
}

func TestAgentClaim_ApplyUnknownAgentReportsError(t *testing.T) {
	cleanup := withTempClaudeCode(t)
	defer cleanup()

	r := setup.Lookup("agent-claim")
	err := r.Apply(context.Background(), t.TempDir(), setup.Options{
		"agents": []string{"not-a-real-agent"},
	})
	if err == nil {
		t.Fatal("Apply should error on unknown agent")
	}
}

func TestAgentClaim_VerifyFailsBeforeApply(t *testing.T) {
	cleanup := withTempClaudeCode(t)
	defer cleanup()

	r := setup.Lookup("agent-claim")
	if err := r.Verify(context.Background(), t.TempDir()); err == nil {
		t.Error("Verify should fail when no agent is claimed")
	}
}
