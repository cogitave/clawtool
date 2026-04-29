// Package unattended implements ADR-023 phase 1: the --unattended
// flag, one-time per-repo disclosure, JSONL audit log, and the
// hard kill switch primitive.
//
// Why a separate package: unattended-mode state crosses the CLI
// (argument parsing, disclosure prompt) and the supervisor
// (banner header, audit emit on every dispatch). Centralising
// it here keeps both surfaces calling one canonical
// implementation — the trust file, the audit path resolver, the
// banner formatter — and makes the policy testable in isolation.
//
// What this package DOESN'T do (deferred to v1.1, per ADR-023):
//   - Self-paced wake-up scheduling (`ScheduleWakeup` integration)
//   - Watch-event resumption (PR merged, CI failed, file changed)
//   - The compounding-trust clamp around remote A2A peers — that
//     lands when ADR-024 phase 1 (Agent Card endpoint) ships
package unattended

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cogitave/clawtool/internal/atomicfile"
	"github.com/cogitave/clawtool/internal/xdg"
	"github.com/google/uuid"
)

// SessionState carries the live unattended-mode session. Every
// dispatch in unattended mode runs through one of these so the
// audit log + banner ride together without the supervisor having
// to thread state through opts.
type SessionState struct {
	ID        string    `json:"session_id"`
	StartedAt time.Time `json:"started_at"`
	RepoPath  string    `json:"repo_path"`
	AuditPath string    `json:"audit_path"`
	YOLOAlias bool      `json:"yolo_alias,omitempty"` // true when the operator invoked --yolo

	mu       sync.Mutex
	auditWtr *os.File
}

// Banner returns the persistent status line the supervisor renders
// on every dispatch result so callers downstream of the dispatch
// know the chain crossed an unattended boundary. Format mirrors
// ADR-023 §Behaviour.
func (s *SessionState) Banner() string {
	if s == nil {
		return ""
	}
	elapsed := time.Since(s.StartedAt).Round(time.Second)
	mark := "UNATTENDED"
	if s.YOLOAlias {
		mark = "YOLO"
	}
	return fmt.Sprintf("[%s · %s elapsed · audit at %s]",
		mark, elapsed, s.AuditPath)
}

// AuditEntry is one line in the JSONL audit log. The schema is
// intentionally append-only: new fields are additive, never
// renamed, so an operator can grep across logs from older
// clawtool versions without a parser break.
type AuditEntry struct {
	TS       time.Time      `json:"ts"`
	Session  string         `json:"session_id"`
	Kind     string         `json:"kind"`            // "dispatch" | "result" | "rule_block" | "kill"
	Agent    string         `json:"agent,omitempty"` // instance name when relevant
	Family   string         `json:"family,omitempty"`
	Prompt   string         `json:"prompt,omitempty"` // truncated to ~256 chars
	Result   string         `json:"result,omitempty"` // truncated tail
	Error    string         `json:"error,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Emit appends one entry to the session's audit log. Failures
// silently log to stderr — losing an audit line shouldn't kill the
// dispatch, but operators should know the audit broke.
func (s *SessionState) Emit(e AuditEntry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.auditWtr == nil {
		// First write — open for append, create-if-missing. Mode
		// 0o600 because the JSONL log persists dispatched prompts
		// (truncated to ~256 chars) and result tails — both
		// routinely include API responses, secrets, and
		// session-derived tokens. World-readable would be a
		// textbook secret-in-readable-file leak.
		f, err := os.OpenFile(s.AuditPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "unattended: open audit log %s: %v\n", s.AuditPath, err)
			return
		}
		s.auditWtr = f
	}
	if e.TS.IsZero() {
		e.TS = time.Now().UTC()
	}
	e.Session = s.ID
	body, err := json.Marshal(e)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unattended: marshal audit entry: %v\n", err)
		return
	}
	body = append(body, '\n')
	if _, err := s.auditWtr.Write(body); err != nil {
		fmt.Fprintf(os.Stderr, "unattended: append to audit log: %v\n", err)
	}
}

// Close flushes and closes the audit file. Safe to call multiple
// times.
func (s *SessionState) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.auditWtr == nil {
		return nil
	}
	err := s.auditWtr.Close()
	s.auditWtr = nil
	return err
}

// ───── trust / disclosure ────────────────────────────────────────

// TrustEntry is one row in the per-repo trust file. The operator
// confirms once per repo path; subsequent unattended dispatches
// from the same path skip the disclosure.
type TrustEntry struct {
	RepoPath  string    `toml:"repo_path"`
	GrantedAt time.Time `toml:"granted_at"`
	Note      string    `toml:"note,omitempty"`
}

// trustFile is the on-disk shape — kept TOML-shaped though we
// emit/parse with a tiny hand-rolled writer so we don't pull in
// a new dependency just for two scalar fields per row.
type trustFile struct {
	Trust []TrustEntry
}

// TrustFilePath returns the canonical path: $XDG_DATA_HOME/clawtool/
// unattended-trust.toml, or ~/.local/share/clawtool/unattended-
// trust.toml when XDG isn't set.
func TrustFilePath() string {
	return filepath.Join(xdg.DataDir(), "unattended-trust.toml")
}

// IsTrusted reports whether the operator has previously granted
// unattended-mode trust to this repo path. Lookup is exact-match
// on RepoPath after filepath.Clean — symlinks NOT resolved (we
// trust the operator's CLI invocation path).
func IsTrusted(repoPath string) (bool, error) {
	tf, err := loadTrust()
	if err != nil {
		return false, err
	}
	want := filepath.Clean(repoPath)
	for _, e := range tf.Trust {
		if filepath.Clean(e.RepoPath) == want {
			return true, nil
		}
	}
	return false, nil
}

// Grant adds a trust row for repoPath. Idempotent — re-granting
// updates GrantedAt but doesn't duplicate.
func Grant(repoPath, note string) error {
	tf, err := loadTrust()
	if err != nil {
		return err
	}
	want := filepath.Clean(repoPath)
	now := time.Now().UTC()
	for i, e := range tf.Trust {
		if filepath.Clean(e.RepoPath) == want {
			tf.Trust[i].GrantedAt = now
			if note != "" {
				tf.Trust[i].Note = note
			}
			return saveTrust(tf)
		}
	}
	tf.Trust = append(tf.Trust, TrustEntry{
		RepoPath:  repoPath,
		GrantedAt: now,
		Note:      note,
	})
	return saveTrust(tf)
}

// Revoke removes the trust row. ok=false when the path wasn't in
// the file.
func Revoke(repoPath string) (bool, error) {
	tf, err := loadTrust()
	if err != nil {
		return false, err
	}
	want := filepath.Clean(repoPath)
	out := tf.Trust[:0]
	found := false
	for _, e := range tf.Trust {
		if filepath.Clean(e.RepoPath) == want {
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		return false, nil
	}
	tf.Trust = out
	return true, saveTrust(tf)
}

// loadTrust reads + parses the trust file. Missing file = empty
// trust list (not an error — operator hasn't granted anything yet).
// Tiny hand-rolled TOML parser: the file's grammar is two scalar
// fields per [[trust]] table, nothing more.
func loadTrust() (trustFile, error) {
	path := TrustFilePath()
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return trustFile{}, nil
		}
		return trustFile{}, fmt.Errorf("unattended: read trust file %s: %w", path, err)
	}
	return parseTrust(string(body)), nil
}

func parseTrust(body string) trustFile {
	var out trustFile
	var cur *TrustEntry
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == "[[trust]]" {
			if cur != nil {
				out.Trust = append(out.Trust, *cur)
			}
			cur = &TrustEntry{}
			continue
		}
		if cur == nil {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"`)
		switch key {
		case "repo_path":
			cur.RepoPath = val
		case "granted_at":
			if t, err := time.Parse(time.RFC3339, val); err == nil {
				cur.GrantedAt = t
			}
		case "note":
			cur.Note = val
		}
	}
	if cur != nil {
		out.Trust = append(out.Trust, *cur)
	}
	return out
}

func saveTrust(tf trustFile) error {
	path := TrustFilePath()
	// Mode 0o700 on the parent dir + 0o600 on the file — the
	// trust list is the gate for `--unattended` mode (skips
	// every permission prompt for the listed repos), so leaking
	// which repos are auto-trusted is a privilege-escalation
	// signal a local attacker would absolutely target.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("unattended: mkdir %s: %w", filepath.Dir(path), err)
	}
	var b strings.Builder
	b.WriteString("# clawtool unattended-mode trust file.\n")
	b.WriteString("# Each [[trust]] row records a per-repo grant.\n\n")
	for _, e := range tf.Trust {
		b.WriteString("[[trust]]\n")
		fmt.Fprintf(&b, "repo_path = %q\n", e.RepoPath)
		fmt.Fprintf(&b, "granted_at = %q\n", e.GrantedAt.UTC().Format(time.RFC3339))
		if e.Note != "" {
			fmt.Fprintf(&b, "note = %q\n", e.Note)
		}
		b.WriteByte('\n')
	}
	// Atomic publish via temp+rename so a partial write can't be
	// observed by a concurrent IsTrusted reader. Mode 0o600 —
	// see saveTrust dir-mode comment.
	return atomicfile.WriteFile(path, []byte(b.String()), 0o600)
}

// ───── session lifecycle ─────────────────────────────────────────

// AuditDir returns the per-session audit directory:
// $XDG_DATA_HOME/clawtool/sessions/<id>/, or
// ~/.local/share/clawtool/sessions/<id>/ when XDG isn't set.
func AuditDir(sessionID string) string {
	return filepath.Join(xdg.DataDir(), "sessions", sessionID)
}

// Begin creates a new SessionState with a fresh UUID and audit log
// path. Caller MUST defer Close on the returned state so the audit
// file flushes to disk on session end.
func Begin(repoPath string, yolo bool) (*SessionState, error) {
	repoPath = filepath.Clean(repoPath)
	sessionID := uuid.NewString()
	dir := AuditDir(sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("unattended: mkdir audit dir %s: %w", dir, err)
	}
	state := &SessionState{
		ID:        sessionID,
		StartedAt: time.Now().UTC(),
		RepoPath:  repoPath,
		AuditPath: filepath.Join(dir, "audit.jsonl"),
		YOLOAlias: yolo,
	}
	state.Emit(AuditEntry{
		Kind: "session_start",
		Metadata: map[string]any{
			"repo_path": repoPath,
			"yolo":      yolo,
		},
	})
	return state, nil
}

// ───── disclosure copy ───────────────────────────────────────────

// DisclosurePanel returns the operator-facing copy printed on the
// first --unattended invocation per repo. Lists every downstream
// flag clawtool will set so the operator confirms knowingly.
//
// Per ADR-023: the disclosure is the flag name + this panel + the
// audit log. We do NOT add modal popups inside long-running
// sessions; that's the author's anti-pattern call.
func DisclosurePanel(repoPath string) string {
	var b strings.Builder
	b.WriteString("┌──────────────────────────────────────────────────────────────┐\n")
	b.WriteString("│  clawtool — UNATTENDED MODE                                  │\n")
	b.WriteString("├──────────────────────────────────────────────────────────────┤\n")
	b.WriteString("│  You are about to dispatch agents WITHOUT permission         │\n")
	b.WriteString("│  prompts. clawtool will set every downstream flag below.     │\n")
	b.WriteString("├──────────────────────────────────────────────────────────────┤\n")
	b.WriteString("│  Claude Code    →  --dangerously-skip-permissions            │\n")
	b.WriteString("│  Codex CLI      →  default_tools_approval_mode = approve     │\n")
	b.WriteString("│  Aider          →  --yes-always, --auto-commits=false        │\n")
	b.WriteString("│  Plandex        →  at least --basic autonomy tier            │\n")
	b.WriteString("│  Hermes         →  --no-confirm (when supported)             │\n")
	b.WriteString("├──────────────────────────────────────────────────────────────┤\n")
	b.WriteString("│  Audit log:    ~/.local/share/clawtool/sessions/<id>/        │\n")
	b.WriteString("│                audit.jsonl  (append-only)                    │\n")
	b.WriteString("│  Kill switch:  clawtool supervise --stop  (or SIGINT)        │\n")
	b.WriteString("├──────────────────────────────────────────────────────────────┤\n")
	fmt.Fprintf(&b, "│  Repo:        %-46s │\n", truncate(repoPath, 46))
	b.WriteString("│  Trust file:  ~/.local/share/clawtool/unattended-trust.toml  │\n")
	b.WriteString("│                                                              │\n")
	b.WriteString("│  This grant persists for THIS REPO until you revoke it via   │\n")
	b.WriteString("│      clawtool unattended revoke <repo>                       │\n")
	b.WriteString("└──────────────────────────────────────────────────────────────┘\n")
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
