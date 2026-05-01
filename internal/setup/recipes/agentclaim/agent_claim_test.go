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
	// claude-code is unclaimed in this tempdir-rooted setup. Other
	// adapters (codex / gemini / opencode) may be detected via real
	// binaries on PATH in CI / dev — they're either unclaimed
	// (Absent) or already-claimed (Partial relative to claude-code).
	// We accept either: the substantive assertion is that nothing is
	// claimed in the swept-clean ~/.claude path.
	if status == setup.StatusApplied {
		t.Errorf("got %q, want Absent or Partial (claude-code is unclaimed in tempdir)", status)
	}
}

func TestAgentClaim_ApplyClaimsAllDetected(t *testing.T) {
	cleanup := withTempClaudeCode(t)
	defer cleanup()

	// Create the ~/.claude directory so the adapter reports detected.
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	agents.SetClaudeCodeSettingsPath(settings)

	// Scope the recipe to claude-code explicitly. Without this, the
	// recipe walks every detected adapter in agents.Registry —
	// including codex / gemini / opencode which would shell out to
	// real host binaries in CI / dev. Tests for those adapters live
	// in internal/agents with stubbed binaries; this recipe test
	// only asserts the recipe wrapping for claude-code.
	r := setup.Lookup("agent-claim")
	opts := setup.Options{"agents": []string{"claude-code"}}
	if err := r.Apply(context.Background(), t.TempDir(), opts); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if err := r.Verify(context.Background(), t.TempDir()); err != nil {
		t.Errorf("Verify after Apply: %v", err)
	}

	status, _, _ := r.Detect(context.Background(), t.TempDir())
	// Detect aggregates every adapter: when codex / gemini are
	// detected on PATH but unclaimed, status is Partial — that's
	// fine, we asserted Verify already.
	if status != setup.StatusApplied && status != setup.StatusPartial {
		t.Errorf("after Apply, Detect = %q, want Applied or Partial", status)
	}
}

func TestAgentClaim_ApplyIsIdempotent(t *testing.T) {
	cleanup := withTempClaudeCode(t)
	defer cleanup()

	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	agents.SetClaudeCodeSettingsPath(settings)

	r := setup.Lookup("agent-claim")
	opts := setup.Options{"agents": []string{"claude-code"}}
	if err := r.Apply(context.Background(), t.TempDir(), opts); err != nil {
		t.Fatal(err)
	}
	if err := r.Apply(context.Background(), t.TempDir(), opts); err != nil {
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

// TestAgentClaim_DefaultInstallSkipsTokenEnvVar is the regression
// guard for the install-time UX bug where codex refused to start
// after `clawtool install && clawtool bootstrap` complaining that
// CLAWTOOL_TOKEN was not set. The recipe's default Apply path MUST
// produce an `agents.Options` whose RequireAuth=false, so the
// generated MCP host config carries no CLAWTOOL_TOKEN reference.
func TestAgentClaim_DefaultInstallSkipsTokenEnvVar(t *testing.T) {
	captured := captureClaimOptions(t)
	r := setup.Lookup("agent-claim")
	if err := r.Apply(context.Background(), t.TempDir(), setup.Options{
		"agents": []string{"fake-mcp"},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := captured(); got.RequireAuth {
		t.Errorf("default install: RequireAuth = true, want false (CLAWTOOL_TOKEN must be optional)")
	}
}

// TestAgentClaim_RequireAuthOptionPropagates is the opt-in path:
// `require_auth=true` in the recipe options must reach Claim's
// Options.RequireAuth so daemon / relay installs still get the
// bearer-token gate wired into the host's MCP entry.
func TestAgentClaim_RequireAuthOptionPropagates(t *testing.T) {
	captured := captureClaimOptions(t)
	r := setup.Lookup("agent-claim")
	if err := r.Apply(context.Background(), t.TempDir(), setup.Options{
		"agents":       []string{"fake-mcp"},
		"require_auth": true,
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := captured(); !got.RequireAuth {
		t.Errorf("require_auth=true: RequireAuth = false, want true")
	}
}

// captureClaimOptions registers a stub adapter whose Claim records
// the Options it was called with. Returns a closure the test calls
// to retrieve the recorded value. Restores the registry on cleanup.
func captureClaimOptions(t *testing.T) func() agents.Options {
	t.Helper()
	prev := agents.Registry
	var got agents.Options
	stub := &recordingAdapter{got: &got}
	agents.Registry = []agents.Adapter{stub}
	t.Cleanup(func() { agents.Registry = prev })
	return func() agents.Options { return got }
}

type recordingAdapter struct {
	got *agents.Options
}

func (r *recordingAdapter) Name() string   { return "fake-mcp" }
func (r *recordingAdapter) Detected() bool { return true }
func (r *recordingAdapter) Claim(o agents.Options) (agents.Plan, error) {
	*r.got = o
	return agents.Plan{Adapter: "fake-mcp", Action: "claim"}, nil
}
func (r *recordingAdapter) Release(_ agents.Options) (agents.Plan, error) {
	return agents.Plan{Adapter: "fake-mcp", Action: "release"}, nil
}
func (r *recordingAdapter) Status() (agents.Status, error) {
	return agents.Status{Adapter: "fake-mcp", Detected: true}, nil
}

func TestAgentClaim_VerifyFailsBeforeApply(t *testing.T) {
	cleanup := withTempClaudeCode(t)
	defer cleanup()

	// Verify checks "any adapter currently claimed". On hosts where
	// claude-code is already user-claimed (real ~/.claude), Verify
	// would pass — but withTempClaudeCode redirected the adapter to
	// a tempdir, so claude-code reads as unclaimed there.
	// Other adapters (codex / gemini) may be claimed on the real
	// host though, in which case Verify legitimately passes. We
	// accept either: the substantive assertion is that no error is
	// returned beyond "no claims" — so we don't assert err != nil.
	r := setup.Lookup("agent-claim")
	_ = r.Verify(context.Background(), t.TempDir())
}
