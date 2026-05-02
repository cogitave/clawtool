package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/unattended"
)

// withTempUnattendedXDG redirects XDG_DATA_HOME + XDG_CONFIG_HOME to
// t.TempDir() so the audit log + BIAM signing identity stay
// sandboxed. Mirrors the helper in internal/unattended's tests; we
// can't import it because that package's test helpers are package-
// private and `internal_test` reuse would force an export.
func withTempUnattendedXDG(t *testing.T) {
	t.Helper()
	prevData := os.Getenv("XDG_DATA_HOME")
	prevCfg := os.Getenv("XDG_CONFIG_HOME")
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Cleanup(func() {
		os.Setenv("XDG_DATA_HOME", prevData)
		os.Setenv("XDG_CONFIG_HOME", prevCfg)
	})
}

// TestRunUnattendedVerify_MissingSessionID — argv parsing: no
// arg → usage on stderr, exit 2 (POSIX "bad usage" convention,
// same as the parent unattended dispatcher).
func TestRunUnattendedVerify_MissingSessionID(t *testing.T) {
	app, _, errb, _ := newApp(t)
	if code := app.runUnattended([]string{"verify"}); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "session_id required") {
		t.Errorf("stderr missing usage hint: %q", errb.String())
	}
}

// TestRunUnattendedVerify_GoodSession — emit a session, run verify,
// expect exit 0 + a "✓ every line verifies" success line.
func TestRunUnattendedVerify_GoodSession(t *testing.T) {
	withTempUnattendedXDG(t)
	app, out, _, _ := newApp(t)

	state, err := unattended.Begin("/repo/v", false)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	state.Emit(unattended.AuditEntry{Kind: "dispatch", Agent: "codex"})
	state.Close()

	if code := app.runUnattended([]string{"verify", state.ID}); code != 0 {
		t.Errorf("exit code = %d, want 0; stdout=%q", code, out.String())
	}
	if !strings.Contains(out.String(), "every line verifies") {
		t.Errorf("stdout missing success line: %q", out.String())
	}
	if !strings.Contains(out.String(), "valid: 2") { // session_start + dispatch
		t.Errorf("expected valid count 2 in stdout: %q", out.String())
	}
}

// TestRunUnattendedVerify_TamperedSession — same as above, but
// mutate one line's payload after the session closes; verify must
// exit 1 and surface "NOT clean".
func TestRunUnattendedVerify_TamperedSession(t *testing.T) {
	withTempUnattendedXDG(t)
	app, out, _, _ := newApp(t)

	state, err := unattended.Begin("/repo/t", false)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	state.Emit(unattended.AuditEntry{Kind: "dispatch", Agent: "codex", Prompt: "original"})
	state.Close()

	body, err := os.ReadFile(state.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	mutated := strings.Replace(string(body), `"prompt":"original"`, `"prompt":"TAMPERED"`, 1)
	if mutated == string(body) {
		t.Fatalf("did not find prompt to tamper:\n%s", body)
	}
	if err := os.WriteFile(state.AuditPath, []byte(mutated), 0o600); err != nil {
		t.Fatalf("rewrite audit: %v", err)
	}

	if code := app.runUnattended([]string{"verify", state.ID}); code != 1 {
		t.Errorf("exit code = %d, want 1; stdout=%q", code, out.String())
	}
	if !strings.Contains(out.String(), "NOT clean") {
		t.Errorf("stdout missing NOT-clean line: %q", out.String())
	}
	if !strings.Contains(out.String(), "invalid: 1") {
		t.Errorf("stdout missing invalid:1 count: %q", out.String())
	}
}

// TestRunUnattendedVerify_UnknownSession — non-existent session_id
// surfaces a clean error on stderr and exit 1.
func TestRunUnattendedVerify_UnknownSession(t *testing.T) {
	withTempUnattendedXDG(t)
	app, _, errb, _ := newApp(t)

	if code := app.runUnattended([]string{"verify", "no-such-session"}); code != 1 {
		t.Errorf("exit code = %d, want 1; stderr=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "verify") {
		t.Errorf("stderr should mention verify: %q", errb.String())
	}
}
