// Package core — session-scoped read tracking for the
// Read-before-Write guardrail (ADR-021). MCP session id is the
// key; we look it up via server.ClientSessionFromContext, never
// from a tool argument (Codex flagged this — model-supplied
// session ids can't be trusted).
package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/server"
)

// SessionKey is the trusted MCP session identifier. "anonymous"
// when the transport doesn't supply one (typical stdio).
type SessionKey string

const sessionAnonymous SessionKey = "anonymous"

// readFileForHash is a tiny indirection so tests can stub the
// disk read. Production reads via os.ReadFile.
var readFileForHash = func(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// ReadRecord captures what a Read tool call observed about a path
// at a single point in time. Edit + Write consult these to
// verify the agent has seen the file AND the file hasn't drifted
// since.
type ReadRecord struct {
	Path      string    `json:"path"`
	FileHash  string    `json:"file_hash"`            // SHA-256 of raw bytes
	RangeHash string    `json:"range_hash,omitempty"` // SHA-256 of returned line range
	LineStart int       `json:"line_start,omitempty"`
	LineEnd   int       `json:"line_end,omitempty"`
	ReadAt    time.Time `json:"read_at"`
}

// SessionState is the process-local read registry. Concurrent
// callers share one instance via Sessions.
type SessionState struct {
	mu    sync.Mutex
	reads map[SessionKey]map[string]ReadRecord
}

// Sessions is the process-wide singleton. Tests reset via
// ResetSessionsForTest.
var Sessions = &SessionState{
	reads: map[SessionKey]map[string]ReadRecord{},
}

// ResetSessionsForTest clears the registry. Test-only escape
// hatch matching the pattern in agents/supervisor.go.
func ResetSessionsForTest() {
	Sessions.mu.Lock()
	defer Sessions.mu.Unlock()
	Sessions.reads = map[SessionKey]map[string]ReadRecord{}
}

// SessionKeyFromContext extracts the trusted MCP session id from
// a tool handler's ctx. Falls back to "anonymous" so unit tests
// (and stdio sessions without a transport-supplied id) still get
// a meaningful key.
func SessionKeyFromContext(ctx context.Context) SessionKey {
	sess := server.ClientSessionFromContext(ctx)
	if sess == nil {
		return sessionAnonymous
	}
	id := sess.SessionID()
	if id == "" {
		return sessionAnonymous
	}
	return SessionKey(id)
}

// RecordRead stores a Read observation. Idempotent — re-reading
// the same path overwrites the prior record.
func (s *SessionState) RecordRead(sid SessionKey, r ReadRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reads[sid] == nil {
		s.reads[sid] = map[string]ReadRecord{}
	}
	s.reads[sid][r.Path] = r
}

// ReadOf returns the latest record for (session, path).
func (s *SessionState) ReadOf(sid SessionKey, path string) (ReadRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reads[sid] == nil {
		return ReadRecord{}, false
	}
	r, ok := s.reads[sid][path]
	return r, ok
}

// HashFile returns SHA-256 of the file's raw bytes as hex.
// Helper used by Read / Write / Edit; centralised so the format
// stays consistent across tools.
func HashFile(path string) (string, error) {
	body, err := readFileForHash(path)
	if err != nil {
		return "", err
	}
	return hashBytes(body), nil
}

// HashString computes SHA-256 of a string. Used for range_hash
// after format-aware decoding (PDF / DOCX / XLSX) so the hash
// captures the canonical text we returned to the agent, not the
// raw bytes.
func HashString(s string) string { return hashBytes([]byte(s)) }

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
