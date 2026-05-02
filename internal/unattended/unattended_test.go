package unattended

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withTempXDG points XDG_DATA_HOME at t.TempDir() so the trust file
// + audit logs land in an isolated location for the test, restored
// on cleanup. Also points XDG_CONFIG_HOME at the same dir so the
// BIAM identity (loaded by Begin to sign audit lines) lands inside
// the test sandbox instead of the operator's real ~/.config.
func withTempXDG(t *testing.T) string {
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
	return dir
}

func TestTrust_GrantRevokeRoundTrip(t *testing.T) {
	withTempXDG(t)

	if ok, err := IsTrusted("/repo/a"); err != nil || ok {
		t.Fatalf("fresh trust file should report false, got ok=%v err=%v", ok, err)
	}

	if err := Grant("/repo/a", "first grant"); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if ok, err := IsTrusted("/repo/a"); err != nil || !ok {
		t.Errorf("after Grant, IsTrusted should be true, got ok=%v err=%v", ok, err)
	}
	if ok, err := IsTrusted("/repo/b"); err != nil || ok {
		t.Errorf("unrelated repo should not be trusted, got ok=%v", ok)
	}

	// Re-grant is idempotent — no duplicate row.
	if err := Grant("/repo/a", "regrant"); err != nil {
		t.Fatalf("re-Grant: %v", err)
	}
	tf, _ := loadTrust()
	if len(tf.Trust) != 1 {
		t.Errorf("re-grant produced %d rows, want 1", len(tf.Trust))
	}
	if tf.Trust[0].Note != "regrant" {
		t.Errorf("re-grant didn't update note: %q", tf.Trust[0].Note)
	}

	// Revoke removes it.
	gone, err := Revoke("/repo/a")
	if err != nil || !gone {
		t.Errorf("Revoke: gone=%v err=%v", gone, err)
	}
	if ok, _ := IsTrusted("/repo/a"); ok {
		t.Error("after Revoke, IsTrusted should be false")
	}
}

func TestTrust_RevokeUnknownIsNoop(t *testing.T) {
	withTempXDG(t)
	gone, err := Revoke("/never/granted")
	if err != nil {
		t.Errorf("Revoke unknown: err=%v", err)
	}
	if gone {
		t.Error("Revoke unknown should return gone=false")
	}
}

func TestTrust_PathNormalisation(t *testing.T) {
	withTempXDG(t)
	if err := Grant("/repo/a/", "with-trailing-slash"); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	// IsTrusted with the unsuffixed form should still match via
	// filepath.Clean normalisation.
	if ok, _ := IsTrusted("/repo/a"); !ok {
		t.Error("IsTrusted should normalise trailing slash")
	}
}

func TestLoadTrust_RoundTripsViaGoToml(t *testing.T) {
	withTempXDG(t)
	body := `# header

[[trust]]
repo_path = "/a"
granted_at = 2026-04-27T15:00:00Z
note = "first"

[[trust]]
   repo_path = "/b"
   granted_at = 2026-04-27T15:30:00Z
`
	if err := os.MkdirAll(filepath.Dir(TrustFilePath()), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(TrustFilePath(), []byte(body), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tf, err := loadTrust()
	if err != nil {
		t.Fatalf("loadTrust: %v", err)
	}
	if len(tf.Trust) != 2 {
		t.Fatalf("got %d entries, want 2", len(tf.Trust))
	}
	if tf.Trust[0].RepoPath != "/a" || tf.Trust[1].RepoPath != "/b" {
		t.Errorf("paths off: %+v", tf.Trust)
	}
	if tf.Trust[0].Note != "first" {
		t.Errorf("note miss: %q", tf.Trust[0].Note)
	}
}

func TestBegin_CreatesSessionAndDir(t *testing.T) {
	xdg := withTempXDG(t)

	state, err := Begin("/repo/x", false)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer state.Close()

	if state.ID == "" {
		t.Error("session ID empty")
	}
	if !strings.HasPrefix(state.AuditPath, xdg) {
		t.Errorf("audit path %q not under XDG home %q", state.AuditPath, xdg)
	}
	// session_start audit row should already be on disk.
	state.Close() // flush
	body, err := os.ReadFile(state.AuditPath)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if !strings.Contains(string(body), `"kind":"session_start"`) {
		t.Errorf("audit log missing session_start: %s", body)
	}
}

func TestEmit_AppendsJSONL(t *testing.T) {
	withTempXDG(t)
	state, err := Begin("/repo", false)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer state.Close()

	state.Emit(AuditEntry{
		Kind:   "dispatch",
		Agent:  "codex",
		Family: "codex",
		Prompt: "audit me",
	})
	state.Emit(AuditEntry{
		Kind:   "result",
		Agent:  "codex",
		Result: "ok",
	})
	state.Close()

	body, err := os.ReadFile(state.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) != 3 { // session_start + 2 emits
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), body)
	}
	for i, line := range lines {
		// Each line is now {event, sig}; the entry sits inside event.
		var wrapper struct {
			Event json.RawMessage `json:"event"`
			Sig   string          `json:"sig"`
		}
		if err := json.Unmarshal([]byte(line), &wrapper); err != nil {
			t.Errorf("line[%d] not valid JSON: %v\n  body=%s", i, err, line)
			continue
		}
		if wrapper.Sig == "" {
			t.Errorf("line[%d] missing sig", i)
		}
		var entry AuditEntry
		if err := json.Unmarshal(wrapper.Event, &entry); err != nil {
			t.Errorf("line[%d] event not valid JSON: %v\n  body=%s", i, err, wrapper.Event)
			continue
		}
		if entry.Session != state.ID {
			t.Errorf("line[%d] session = %q, want %q", i, entry.Session, state.ID)
		}
		if entry.TS.IsZero() {
			t.Errorf("line[%d] ts is zero", i)
		}
	}
}

// TestEmit_SignsLine — every JSONL line carries a verifiable
// signature over the canonical-JSON encoding of `event`. The
// session's signer.Public is the verification key; tampering with
// any byte of `event` invalidates the line. Pin the
// `ed25519:<hex>` shape too, so a sig string drift surfaces fast.
func TestEmit_SignsLine(t *testing.T) {
	withTempXDG(t)
	state, err := Begin("/repo", false)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer state.Close()

	state.Emit(AuditEntry{
		Kind:   "dispatch",
		Agent:  "codex",
		Prompt: "sign me",
	})
	state.Close()

	body, err := os.ReadFile(state.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected ≥2 lines (session_start + dispatch), got %d", len(lines))
	}

	for i, line := range lines {
		var w signedAuditLine
		if err := json.Unmarshal([]byte(line), &w); err != nil {
			t.Fatalf("line[%d] parse: %v\n  body=%s", i, err, line)
		}
		if !strings.HasPrefix(w.Sig, "ed25519:") {
			t.Errorf("line[%d] sig %q missing ed25519: prefix", i, w.Sig)
		}
		hexPart := strings.TrimPrefix(w.Sig, "ed25519:")
		sigBytes, err := hex.DecodeString(hexPart)
		if err != nil {
			t.Errorf("line[%d] sig hex decode: %v", i, err)
			continue
		}
		if !ed25519.Verify(state.signer.Public, w.Event, sigBytes) {
			t.Errorf("line[%d] signature did NOT verify against session signer pubkey", i)
		}
	}
}

// TestVerify_ValidSession — round-trip the public path: emit N
// events, then VerifySession reports valid=N and zero invalid /
// malformed.
func TestVerify_ValidSession(t *testing.T) {
	withTempXDG(t)
	state, err := Begin("/repo", false)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer state.Close()

	const dispatches = 5
	for i := 0; i < dispatches; i++ {
		state.Emit(AuditEntry{Kind: "dispatch", Agent: "codex", Prompt: "p"})
	}
	state.Close()

	report, err := VerifySession(state.ID, nil)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	wantTotal := dispatches + 1 // +1 for session_start
	if report.Total != wantTotal {
		t.Errorf("Total = %d, want %d", report.Total, wantTotal)
	}
	if report.Valid != wantTotal {
		t.Errorf("Valid = %d, want %d", report.Valid, wantTotal)
	}
	if report.Invalid != 0 {
		t.Errorf("Invalid = %d, want 0", report.Invalid)
	}
	if report.Malformed != 0 {
		t.Errorf("Malformed = %d, want 0", report.Malformed)
	}
}

// TestVerify_TamperedLine — mutate one byte inside `event` and
// confirm verify reports invalid=1, valid=N-1. This is the
// load-bearing security property: silent edits break verification.
func TestVerify_TamperedLine(t *testing.T) {
	withTempXDG(t)
	state, err := Begin("/repo", false)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer state.Close()

	state.Emit(AuditEntry{Kind: "dispatch", Agent: "codex", Prompt: "original"})
	state.Emit(AuditEntry{Kind: "dispatch", Agent: "codex", Prompt: "second"})
	state.Close()

	body, err := os.ReadFile(state.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	// Replace "original" with "TAMPERED" inside the event payload.
	// The wrapper still parses fine, and the sig stays the same —
	// that's exactly the silent-edit attack signing defends against.
	mutated := strings.Replace(string(body), `"prompt":"original"`, `"prompt":"TAMPERED"`, 1)
	if mutated == string(body) {
		t.Fatalf("did not find prompt to tamper inside body:\n%s", body)
	}
	if err := os.WriteFile(state.AuditPath, []byte(mutated), 0o600); err != nil {
		t.Fatalf("rewrite audit: %v", err)
	}

	report, err := VerifySession(state.ID, nil)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if report.Invalid != 1 {
		t.Errorf("Invalid = %d, want 1", report.Invalid)
	}
	if report.Valid != report.Total-1 {
		t.Errorf("Valid = %d, want Total-1 = %d", report.Valid, report.Total-1)
	}
	if report.Malformed != 0 {
		t.Errorf("Malformed = %d, want 0 (the wrapper still parses)", report.Malformed)
	}
}

// TestVerify_MalformedLine — a non-JSON tail line counts as
// malformed, not invalid. Keeps the count semantics honest: a torn
// write (Malformed) is operationally different from an attacker
// rewrite (Invalid).
func TestVerify_MalformedLine(t *testing.T) {
	withTempXDG(t)
	state, err := Begin("/repo", false)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer state.Close()
	state.Emit(AuditEntry{Kind: "dispatch", Agent: "codex"})
	state.Close()

	f, err := os.OpenFile(state.AuditPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	if _, err := f.WriteString("not-json garbage\n"); err != nil {
		t.Fatalf("append: %v", err)
	}
	f.Close()

	report, err := VerifySession(state.ID, nil)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if report.Malformed != 1 {
		t.Errorf("Malformed = %d, want 1", report.Malformed)
	}
	if report.Invalid != 0 {
		t.Errorf("Invalid = %d, want 0", report.Invalid)
	}
}

func TestBanner_Format(t *testing.T) {
	state := &SessionState{
		ID:        "abc-123",
		StartedAt: time.Now().Add(-90 * time.Second),
		RepoPath:  "/repo",
		AuditPath: "/tmp/audit.jsonl",
	}
	got := state.Banner()
	for _, want := range []string{"UNATTENDED", "elapsed", "/tmp/audit.jsonl"} {
		if !strings.Contains(got, want) {
			t.Errorf("banner missing %q: %q", want, got)
		}
	}
	state.YOLOAlias = true
	if !strings.Contains(state.Banner(), "YOLO") {
		t.Error("YOLO alias should swap the marker")
	}
}

func TestDisclosurePanel_NamesEveryFlag(t *testing.T) {
	panel := DisclosurePanel("/some/repo")
	for _, want := range []string{
		"UNATTENDED MODE",
		"--dangerously-skip-permissions",
		"default_tools_approval_mode = approve",
		"--yes-always",
		"--basic",
		"--no-confirm",
		"audit.jsonl",
		"clawtool supervise --stop",
		"unattended-trust.toml",
		"/some/repo",
	} {
		if !strings.Contains(panel, want) {
			t.Errorf("disclosure panel missing %q", want)
		}
	}
}

func TestAuditDir_HonoursXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/custom/xdg")
	got := AuditDir("session-1")
	want := filepath.Join("/custom/xdg", "clawtool", "sessions", "session-1")
	if got != want {
		t.Errorf("AuditDir = %q, want %q", got, want)
	}
}
