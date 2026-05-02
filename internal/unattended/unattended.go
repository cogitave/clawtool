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
	"bufio"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cogitave/clawtool/internal/agents/biam"
	"github.com/cogitave/clawtool/internal/atomicfile"
	"github.com/cogitave/clawtool/internal/xdg"
	"github.com/google/uuid"
	"github.com/pelletier/go-toml/v2"
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

	// signer is the BIAM Ed25519 identity loaded at Begin(). Every
	// AuditEntry is wrapped {event, sig} so a tail-mutating attacker
	// can't silently scrub a dispatched prompt — verify will flag
	// the line as invalid. ADR-023 §"JSONL audit log signing".
	signer *biam.Identity
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
	line, err := signedLine(s.signer, e)
	if err != nil {
		fmt.Fprintf(os.Stderr, "unattended: sign audit entry: %v\n", err)
		return
	}
	if _, err := s.auditWtr.Write(line); err != nil {
		fmt.Fprintf(os.Stderr, "unattended: append to audit log: %v\n", err)
	}
}

// signedAuditLine is the on-disk shape of one JSONL row. The
// underlying AuditEntry sits inside `event` so the schema's
// "additive only" contract holds: a reader that pre-dates signing
// can still pull the entry by ignoring the wrapper. `sig` is the
// `ed25519:<hex>` form — same encoding BIAM envelopes use, so an
// operator inspecting both surfaces sees one consistent shape.
type signedAuditLine struct {
	Event json.RawMessage `json:"event"`
	Sig   string          `json:"sig"`
}

// signedLine produces the wrapped, signed JSONL line for an entry.
// Returned bytes already include the trailing newline. Returns an
// error when the signer is nil — Emit catches it and surfaces on
// stderr without aborting the dispatch (we'd rather lose the line
// than the dispatch result, but operators should see it broke).
func signedLine(signer *biam.Identity, e AuditEntry) ([]byte, error) {
	if signer == nil {
		return nil, errors.New("unattended: audit signer not initialised")
	}
	canonical, err := json.Marshal(e)
	if err != nil {
		return nil, fmt.Errorf("marshal entry: %w", err)
	}
	sig := signer.Sign(canonical)
	if sig == nil {
		return nil, errors.New("unattended: signer returned nil signature (private key missing)")
	}
	wrapper := signedAuditLine{
		Event: json.RawMessage(canonical),
		Sig:   "ed25519:" + hex.EncodeToString(sig),
	}
	body, err := json.Marshal(wrapper)
	if err != nil {
		return nil, fmt.Errorf("marshal wrapper: %w", err)
	}
	return append(body, '\n'), nil
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

// trustFile is the on-disk shape. The struct tag uses the lowercase
// `trust` table name so go-toml round-trips [[trust]] correctly —
// the on-disk header stays "[[trust]]" exactly as the historical
// hand-rolled writer emitted, so existing trust files load without
// migration.
type trustFile struct {
	Trust []TrustEntry `toml:"trust"`
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
// Round-trips through go-toml so a repo path containing quotes,
// backslashes, or a non-RFC3339 timestamp from a future schema
// version surfaces as a parse error instead of silently truncating
// (the prior hand-rolled reader trimmed `"` blindly and dropped
// any line it couldn't `Cut` on `=`).
func loadTrust() (trustFile, error) {
	path := TrustFilePath()
	body, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return trustFile{}, nil
		}
		return trustFile{}, fmt.Errorf("unattended: read trust file %s: %w", path, err)
	}
	var tf trustFile
	if err := toml.Unmarshal(body, &tf); err != nil {
		return trustFile{}, fmt.Errorf("unattended: parse trust file %s: %w", path, err)
	}
	return tf, nil
}

// trustFileHeader is the comment block we prepend to every saved
// trust file so an operator running `cat ~/.local/share/clawtool/
// unattended-trust.toml` sees what the file is for. go-toml's
// Marshal doesn't emit comments, so we concat manually around the
// marshal output.
const trustFileHeader = "# clawtool unattended-mode trust file.\n" +
	"# Each [[trust]] row records a per-repo grant.\n\n"

func saveTrust(tf trustFile) error {
	path := TrustFilePath()
	body, err := toml.Marshal(tf)
	if err != nil {
		return fmt.Errorf("unattended: marshal trust file: %w", err)
	}
	// Mode 0o700 on the parent dir + 0o600 on the file — the
	// trust list is the gate for `--unattended` mode (skips
	// every permission prompt for the listed repos), so leaking
	// which repos are auto-trusted is a privilege-escalation
	// signal a local attacker would absolutely target.
	out := append([]byte(trustFileHeader), body...)
	return atomicfile.WriteFileMkdir(path, out, 0o600, 0o700)
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
//
// Loads the BIAM Ed25519 identity (creating it on first launch) so
// every Emit() can wrap the entry as `{event, sig}`. ADR-023
// resolved this as unconditional: an unattended audit log that
// isn't tamper-evident isn't actually an audit log. A failure to
// load / create the identity therefore aborts session start —
// dispatching without signing would silently downgrade the
// security promise the operator just opted into.
func Begin(repoPath string, yolo bool) (*SessionState, error) {
	repoPath = filepath.Clean(repoPath)
	sessionID := uuid.NewString()
	dir := AuditDir(sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("unattended: mkdir audit dir %s: %w", dir, err)
	}
	signer, err := biam.LoadOrCreateIdentity("")
	if err != nil {
		return nil, fmt.Errorf("unattended: load audit signer: %w", err)
	}
	state := &SessionState{
		ID:        sessionID,
		StartedAt: time.Now().UTC(),
		RepoPath:  repoPath,
		AuditPath: filepath.Join(dir, "audit.jsonl"),
		YOLOAlias: yolo,
		signer:    signer,
	}
	state.Emit(AuditEntry{
		Kind: "session_start",
		Metadata: map[string]any{
			"repo_path": repoPath,
			"yolo":      yolo,
			// Stamp the signer's public key so a verifier can
			// distinguish "wrong key" from "tampered line" without
			// needing a separate index file.
			"signer_pub": signer.PublicKeyB64(),
		},
	})
	return state, nil
}

// ───── verify walker ─────────────────────────────────────────────

// VerifyReport summarises a `clawtool unattended verify <session>`
// pass over a JSONL audit log. Counts add up to Total — every line
// lands in exactly one bucket.
type VerifyReport struct {
	SessionID string `json:"session_id"`
	AuditPath string `json:"audit_path"`
	Total     int    `json:"total"`     // lines read
	Valid     int    `json:"valid"`     // signature checked + matched
	Invalid   int    `json:"invalid"`   // signature present but didn't verify (tampered)
	Malformed int    `json:"malformed"` // not JSON, missing event/sig, bad sig hex, etc.
}

// VerifySession walks the audit log for sessionID, re-canonicalises
// each event, and verifies the sig against `pub`. When pub is nil
// the local BIAM identity's public key is used — the common case
// (operator running verify on the same host that signed). One
// malformed / invalid line does NOT abort the walk: the report
// surfaces the count, and the operator decides whether to escalate.
//
// Append-only contract: every line is independent. A torn write at
// the file tail bumps Malformed by one but every preceding Valid
// stays Valid — that's the whole point of per-line signing.
func VerifySession(sessionID string, pub ed25519.PublicKey) (VerifyReport, error) {
	report := VerifyReport{SessionID: sessionID}
	if sessionID == "" {
		return report, errors.New("unattended: verify session_id empty")
	}
	report.AuditPath = filepath.Join(AuditDir(sessionID), "audit.jsonl")

	if pub == nil {
		id, err := biam.LoadOrCreateIdentity("")
		if err != nil {
			return report, fmt.Errorf("unattended: load verifier identity: %w", err)
		}
		pub = id.Public
	}

	f, err := os.Open(report.AuditPath)
	if err != nil {
		return report, fmt.Errorf("unattended: open audit log %s: %w", report.AuditPath, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Audit lines can carry truncated prompts / result tails up
	// to ~256 chars each; the wrapped JSON sits comfortably under
	// 64 KiB, but bump the buffer so a generous future schema
	// (longer truncation cap, richer metadata) doesn't strand
	// older logs at parse time.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue // tolerate stray blank lines
		}
		report.Total++
		var line signedAuditLine
		if err := json.Unmarshal(raw, &line); err != nil || len(line.Event) == 0 || line.Sig == "" {
			report.Malformed++
			continue
		}
		const prefix = "ed25519:"
		if !strings.HasPrefix(line.Sig, prefix) {
			report.Malformed++
			continue
		}
		sig, err := hex.DecodeString(line.Sig[len(prefix):])
		if err != nil {
			report.Malformed++
			continue
		}
		if biam.Verify(pub, line.Event, sig) {
			report.Valid++
		} else {
			report.Invalid++
		}
	}
	if err := scanner.Err(); err != nil {
		return report, fmt.Errorf("unattended: scan audit log: %w", err)
	}
	return report, nil
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
