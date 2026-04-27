package unattended

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withTempXDG points XDG_DATA_HOME at t.TempDir() so the trust file
// + audit logs land in an isolated location for the test, restored
// on cleanup.
func withTempXDG(t *testing.T) string {
	t.Helper()
	prev := os.Getenv("XDG_DATA_HOME")
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	t.Cleanup(func() {
		os.Setenv("XDG_DATA_HOME", prev)
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

func TestParseTrust_RobustToWhitespace(t *testing.T) {
	body := `# header

[[trust]]
repo_path = "/a"
granted_at = "2026-04-27T15:00:00Z"
note = "first"

[[trust]]
   repo_path = "/b"
   granted_at = "2026-04-27T15:30:00Z"
`
	tf := parseTrust(body)
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
		var entry AuditEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Errorf("line[%d] not valid JSON: %v\n  body=%s", i, err, line)
		}
		if entry.Session != state.ID {
			t.Errorf("line[%d] session = %q, want %q", i, entry.Session, state.ID)
		}
		if entry.TS.IsZero() {
			t.Errorf("line[%d] ts is zero", i)
		}
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
