package biam

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// TaskStatus enumerates the per-task lifecycle ADR-015 §"State machine"
// locks at v1.
type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskActive    TaskStatus = "active"
	TaskDone      TaskStatus = "done"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
	TaskExpired   TaskStatus = "expired"
)

// IsTerminal reports whether a status closes the task.
func (s TaskStatus) IsTerminal() bool {
	switch s {
	case TaskDone, TaskFailed, TaskCancelled, TaskExpired:
		return true
	}
	return false
}

// Task is the BIAM-level row for a multi-message thread.
type Task struct {
	TaskID       string     `json:"task_id"`
	Status       TaskStatus `json:"status"`
	InitiatedBy  string     `json:"initiated_by"` // who started it; empty for inbound
	Agent        string     `json:"agent"`        // agent instance the dispatch hit
	CreatedAt    time.Time  `json:"created_at"`
	ClosedAt     *time.Time `json:"closed_at,omitempty"`
	LastMessage  string     `json:"last_message,omitempty"` // tail of the latest result
	MessageCount int        `json:"message_count"`
}

// Store wraps the per-instance SQLite file. Methods are safe for
// concurrent calls — the underlying connection pool serialises
// writes; readers fan out via WAL.
type Store struct {
	mu sync.Mutex
	db *sql.DB
}

// OpenStore opens (creating if absent) the SQLite database at path.
// WAL mode + busy-timeout makes concurrent writers tolerant.
func OpenStore(path string) (*Store, error) {
	if path == "" {
		path = DefaultStorePath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("biam: mkdir store dir: %w", err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("biam: open sqlite: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// DefaultStorePath honours XDG_DATA_HOME, falls back to HOME.
func DefaultStorePath() string {
	if v := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); v != "" {
		return filepath.Join(v, "clawtool", "biam.db")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "share", "clawtool", "biam.db")
	}
	return "biam.db"
}

// Close flushes + closes the underlying database. Idempotent.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

// migrate creates the v1 schema on first open. Additive migrations
// land here in subsequent versions.
func (s *Store) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS tasks (
  task_id        TEXT PRIMARY KEY,
  status         TEXT NOT NULL,
  initiated_by   TEXT,
  agent          TEXT,
  created_at     TEXT NOT NULL,
  closed_at      TEXT,
  last_message   TEXT
);

CREATE TABLE IF NOT EXISTS messages (
  message_id      TEXT PRIMARY KEY,
  task_id         TEXT NOT NULL,
  parent_id       TEXT,
  correlation_id  TEXT,
  from_host       TEXT NOT NULL,
  from_instance   TEXT NOT NULL,
  to_host         TEXT NOT NULL,
  to_instance     TEXT NOT NULL,
  kind            TEXT NOT NULL,
  body            TEXT NOT NULL,
  hop_count       INTEGER NOT NULL,
  trace           TEXT NOT NULL,
  created_at      TEXT NOT NULL,
  ttl_seconds     INTEGER NOT NULL,
  idempotency_key TEXT NOT NULL,
  signature       TEXT,
  delivery_state  TEXT,
  inbound         INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_task ON messages(task_id, created_at);

CREATE TABLE IF NOT EXISTS dedupe_keys (
  idempotency_key TEXT PRIMARY KEY,
  seen_at         TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS peers (
  host_id     TEXT NOT NULL,
  instance_id TEXT NOT NULL,
  public_key  TEXT NOT NULL,
  url         TEXT,
  token       TEXT,
  PRIMARY KEY (host_id, instance_id)
);
`
	_, err := s.db.Exec(schema)
	return err
}

// CreateTask inserts a new task row and returns the row's task_id.
// Idempotent: an existing task_id returns nil error.
func (s *Store) CreateTask(ctx context.Context, taskID, initiatedBy, agent string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO tasks (task_id, status, initiated_by, agent, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, taskID, TaskPending, initiatedBy, agent, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// SetTaskStatus updates the task row + (when terminal) closed_at +
// last_message. Pass empty `lastMessage` to leave it untouched.
func (s *Store) SetTaskStatus(ctx context.Context, taskID string, status TaskStatus, lastMessage string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if status.IsTerminal() {
		_, err := s.db.ExecContext(ctx, `
			UPDATE tasks
			   SET status = ?, closed_at = ?, last_message = COALESCE(NULLIF(?, ''), last_message)
			 WHERE task_id = ?
		`, status, now, lastMessage, taskID)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		   SET status = ?, last_message = COALESCE(NULLIF(?, ''), last_message)
		 WHERE task_id = ?
	`, status, lastMessage, taskID)
	return err
}

// GetTask returns the row for the given task_id, plus the message
// count via a sub-query so the caller doesn't need a second round trip.
func (s *Store) GetTask(ctx context.Context, taskID string) (*Task, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT t.task_id, t.status, t.initiated_by, t.agent, t.created_at, t.closed_at, t.last_message,
		       (SELECT COUNT(*) FROM messages m WHERE m.task_id = t.task_id) AS msg_count
		  FROM tasks t
		 WHERE t.task_id = ?
	`, taskID)
	var t Task
	var closedAt, lastMessage, initiatedBy, agent sql.NullString
	var createdAt string
	if err := row.Scan(&t.TaskID, &t.Status, &initiatedBy, &agent, &createdAt, &closedAt, &lastMessage, &t.MessageCount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	t.InitiatedBy = initiatedBy.String
	t.Agent = agent.String
	t.LastMessage = lastMessage.String
	t.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if closedAt.Valid {
		ts, _ := time.Parse(time.RFC3339Nano, closedAt.String)
		t.ClosedAt = &ts
	}
	return &t, nil
}

// ListTasks returns the most-recent tasks (default limit 50, max 1000).
func (s *Store) ListTasks(ctx context.Context, limit int) ([]Task, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT t.task_id, t.status, t.initiated_by, t.agent, t.created_at, t.closed_at, t.last_message,
		       (SELECT COUNT(*) FROM messages m WHERE m.task_id = t.task_id)
		  FROM tasks t
	  ORDER BY t.created_at DESC
		 LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var t Task
		var createdAt string
		var closedAt, lastMessage, initiatedBy, agent sql.NullString
		if err := rows.Scan(&t.TaskID, &t.Status, &initiatedBy, &agent, &createdAt, &closedAt, &lastMessage, &t.MessageCount); err != nil {
			return nil, err
		}
		t.InitiatedBy = initiatedBy.String
		t.Agent = agent.String
		t.LastMessage = lastMessage.String
		t.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		if closedAt.Valid {
			ts, _ := time.Parse(time.RFC3339Nano, closedAt.String)
			t.ClosedAt = &ts
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// PutEnvelope inserts a message into the messages table. Inbound vs
// outbound is the caller's call. Dedupe via idempotency_key prevents
// double-inserts on retry.
func (s *Store) PutEnvelope(ctx context.Context, env *Envelope, inbound bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	bodyJSON, err := json.Marshal(env.Body)
	if err != nil {
		return fmt.Errorf("biam: marshal body: %w", err)
	}
	traceJSON, err := json.Marshal(env.Trace)
	if err != nil {
		return fmt.Errorf("biam: marshal trace: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Dedupe — silently drop a message we've already seen.
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM dedupe_keys WHERE idempotency_key = ?`, env.IdempotencyKey).Scan(&existing); err != nil {
		return fmt.Errorf("biam: dedupe lookup: %w", err)
	}
	if existing > 0 {
		return tx.Commit()
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO messages
		  (message_id, task_id, parent_id, correlation_id,
		   from_host, from_instance, to_host, to_instance,
		   kind, body, hop_count, trace, created_at,
		   ttl_seconds, idempotency_key, signature, inbound)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		env.MessageID, env.TaskID, nullString(env.ParentID), nullString(env.CorrelationID),
		env.From.HostID, env.From.InstanceID, env.To.HostID, env.To.InstanceID,
		env.Kind, string(bodyJSON), env.HopCount, string(traceJSON),
		env.CreatedAt.UTC().Format(time.RFC3339Nano),
		env.TTLSeconds, env.IdempotencyKey, env.Signature, boolToInt(inbound),
	); err != nil {
		return fmt.Errorf("biam: insert message: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO dedupe_keys (idempotency_key, seen_at) VALUES (?, ?)
	`, env.IdempotencyKey, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("biam: insert dedupe: %w", err)
	}
	return tx.Commit()
}

// MessagesFor returns every envelope persisted under task_id, oldest
// first. Snapshot — does not subscribe.
func (s *Store) MessagesFor(ctx context.Context, taskID string) ([]Envelope, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT message_id, parent_id, correlation_id,
		       from_host, from_instance, to_host, to_instance,
		       kind, body, hop_count, trace, created_at,
		       ttl_seconds, idempotency_key, signature
		  FROM messages
		 WHERE task_id = ?
	  ORDER BY created_at ASC
	`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Envelope
	for rows.Next() {
		var e Envelope
		var parentID, correlationID, signature sql.NullString
		var bodyJSON, traceJSON, createdAt string
		if err := rows.Scan(&e.MessageID, &parentID, &correlationID,
			&e.From.HostID, &e.From.InstanceID, &e.To.HostID, &e.To.InstanceID,
			&e.Kind, &bodyJSON, &e.HopCount, &traceJSON, &createdAt,
			&e.TTLSeconds, &e.IdempotencyKey, &signature,
		); err != nil {
			return nil, err
		}
		e.TaskID = taskID
		e.Version = "biam-v1"
		if parentID.Valid {
			e.ParentID = parentID.String
		}
		if correlationID.Valid {
			e.CorrelationID = correlationID.String
		}
		if signature.Valid {
			e.Signature = signature.String
		}
		_ = json.Unmarshal([]byte(bodyJSON), &e.Body)
		_ = json.Unmarshal([]byte(traceJSON), &e.Trace)
		e.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		out = append(out, e)
	}
	return out, rows.Err()
}

// WaitForTerminal polls (cheap) until the task reaches a terminal
// state or the context is cancelled. The caller usually wraps this in
// a timeout.
func (s *Store) WaitForTerminal(ctx context.Context, taskID string, poll time.Duration) (*Task, error) {
	if poll <= 0 {
		poll = 250 * time.Millisecond
	}
	for {
		t, err := s.GetTask(ctx, taskID)
		if err != nil {
			return nil, err
		}
		if t == nil {
			return nil, fmt.Errorf("biam: task %q not found", taskID)
		}
		if t.Status.IsTerminal() {
			return t, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(poll):
		}
	}
}

func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
