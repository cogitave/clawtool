// Package core — Bash background-mode task registry (ADR-021
// phase B, Codex's "long-running" recommendation). Mirrors BIAM's
// task vocabulary (pending / active / done / failed / cancelled)
// without reusing the SQLite store: bash subprocess output is
// volatile, signing every stdout chunk via Ed25519 (which BIAM
// would do) is the wrong default. Process-local in-memory
// registry, lifetime = clawtool serve process.
package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/cogitave/clawtool/internal/secrets"
	"github.com/cogitave/clawtool/internal/sysproc"
	"github.com/google/uuid"
)

// BashTaskStatus mirrors BIAM's lifecycle so an agent that knows
// TaskGet's vocabulary doesn't need a second mental model.
type BashTaskStatus string

const (
	BashTaskActive    BashTaskStatus = "active"
	BashTaskDone      BashTaskStatus = "done"
	BashTaskFailed    BashTaskStatus = "failed"
	BashTaskCancelled BashTaskStatus = "cancelled"
)

// BashTask carries one background bash invocation's state. Output
// buffers grow without bound by design — the operator can always
// kill the task when the live tail gets noisy. We cap at 4 MiB
// per stream to match the BIAM body cap.
type BashTask struct {
	ID         string
	Command    string
	Cwd        string
	StartedAt  time.Time
	FinishedAt time.Time
	TimeoutMs  int

	mu       sync.Mutex
	status   BashTaskStatus
	stdout   bytes.Buffer
	stderr   bytes.Buffer
	exitCode int
	timedOut bool
	cancel   context.CancelFunc
	cmd      *exec.Cmd
}

const bashBgBufferCap = 4 * 1024 * 1024

// snapshot returns a read-only view safe to ship over MCP.
type BashTaskSnapshot struct {
	ID         string         `json:"task_id"`
	Command    string         `json:"command"`
	Cwd        string         `json:"cwd,omitempty"`
	Status     BashTaskStatus `json:"status"`
	Stdout     string         `json:"stdout"`
	Stderr     string         `json:"stderr"`
	ExitCode   int            `json:"exit_code"`
	TimedOut   bool           `json:"timed_out"`
	StartedAt  time.Time      `json:"started_at"`
	FinishedAt time.Time      `json:"finished_at,omitempty"`
}

// Snapshot returns the current state under the task's lock.
func (t *BashTask) Snapshot() BashTaskSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return BashTaskSnapshot{
		ID:         t.ID,
		Command:    t.Command,
		Cwd:        t.Cwd,
		Status:     t.status,
		Stdout:     t.stdout.String(),
		Stderr:     t.stderr.String(),
		ExitCode:   t.exitCode,
		TimedOut:   t.timedOut,
		StartedAt:  t.StartedAt,
		FinishedAt: t.FinishedAt,
	}
}

// BashTaskStore is the process-wide registry. Concurrent reads +
// writes are guarded by an RWMutex so TaskGet / TaskList stay
// fast under load.
type BashTaskStore struct {
	mu    sync.RWMutex
	tasks map[string]*BashTask
}

// BashTasks is the singleton. Tests use ResetBashTasksForTest.
var BashTasks = &BashTaskStore{tasks: map[string]*BashTask{}}

// ResetBashTasksForTest wipes the registry. Test-only.
func ResetBashTasksForTest() {
	BashTasks.mu.Lock()
	defer BashTasks.mu.Unlock()
	for _, t := range BashTasks.tasks {
		t.mu.Lock()
		if t.cancel != nil {
			t.cancel()
		}
		t.mu.Unlock()
	}
	BashTasks.tasks = map[string]*BashTask{}
}

// SubmitBackgroundBash spawns the command, registers a task, and
// returns the task_id. The goroutine reading stdout/stderr keeps
// running after the call returns; consumers poll via TaskGet
// until status is terminal.
func SubmitBackgroundBash(parent context.Context, command, cwd string, timeoutMs int) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", errors.New("bash background: empty command")
	}
	cwd = defaultCwd(cwd)
	if timeoutMs <= 0 {
		timeoutMs = defaultTimeoutMs
	}
	if timeoutMs > maxTimeoutMs {
		timeoutMs = maxTimeoutMs
	}

	id := uuid.NewString()
	taskCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)

	cmd := exec.CommandContext(taskCtx, "/bin/bash", "-c", command)
	cmd.Dir = cwd
	// Octopus pattern: scrub secret-shaped env vars before they
	// reach the child shell. Same policy as the synchronous Bash
	// path in bash.go — a long-running background task is even
	// more likely to leak via a log file or rogue script, so
	// the rule applies equally.
	cmd.Env = secrets.ScrubEnv(os.Environ())
	sysproc.ApplyGroupWithCtxCancel(cmd)

	task := &BashTask{
		ID:        id,
		Command:   command,
		Cwd:       cwd,
		StartedAt: time.Now(),
		TimeoutMs: timeoutMs,
		status:    BashTaskActive,
		cancel:    cancel,
		cmd:       cmd,
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return "", fmt.Errorf("bash background: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return "", fmt.Errorf("bash background: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return "", fmt.Errorf("bash background: start: %w", err)
	}

	// Stream pipes into the task's buffers under the task lock.
	// Cap each stream at bashBgBufferCap so a misbehaving command
	// can't OOM the server. We deliberately drop tail bytes when
	// the cap hits — preferable to summary truncation because the
	// HEAD of the output usually carries the diagnostic banner.
	var drainWG sync.WaitGroup
	drainWG.Add(2)
	go drainPipe(task, stdoutPipe, &task.stdout, &drainWG)
	go drainPipe(task, stderrPipe, &task.stderr, &drainWG)

	// Wait for the process in a goroutine so Submit returns now.
	go func() {
		// Drain ordering matters. The os/exec docs are explicit:
		//   "Wait will close the pipe after seeing the command
		//    exit. It is incorrect to call Wait before all reads
		//    from the pipe have completed."
		// Calling cmd.Wait first races: under load (CI runners
		// with -race), Wait can close the parent pipe end before
		// drainPipe's last Read returns, dropping bytes. Symptom:
		// "stdout = ''" on commands that wrote output. drainWG
		// completes naturally when the child closes its stdout/
		// stderr fds at exit, so waiting on drainWG first is the
		// canonical pattern; cmd.Wait then just collects the
		// exit code without touching the pipes.
		drainWG.Wait()
		err := cmd.Wait()
		task.mu.Lock()
		task.FinishedAt = time.Now()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				task.exitCode = exitErr.ExitCode()
			} else {
				task.exitCode = -1
			}
			if taskCtx.Err() == context.DeadlineExceeded {
				task.timedOut = true
				task.status = BashTaskFailed
			} else if errors.Is(taskCtx.Err(), context.Canceled) {
				task.status = BashTaskCancelled
			} else {
				task.status = BashTaskFailed
			}
		} else {
			task.status = BashTaskDone
		}
		task.mu.Unlock()
		// Free the cancel ctx — we keep the entry so polls see
		// the final state, but the timer no longer needs to fire.
		cancel()
	}()
	_ = parent // ctx isn't used today; reserved for caller-driven cancel layering

	BashTasks.mu.Lock()
	BashTasks.tasks[id] = task
	BashTasks.mu.Unlock()
	return id, nil
}

// drainPipe streams an io.Reader into buf under the task's lock.
// Caps total bytes at bashBgBufferCap; once exceeded we silently
// drop the tail so the task's status field still reflects exit.
// wg.Done() fires when the pipe closes (process exit + write end
// closed) — the cmd.Wait goroutine joins on this so terminal
// status only flips after every byte has been buffered.
func drainPipe(task *BashTask, r interface {
	Read(p []byte) (int, error)
}, buf *bytes.Buffer, wg *sync.WaitGroup) {
	defer wg.Done()
	tmp := make([]byte, 32*1024)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			task.mu.Lock()
			room := bashBgBufferCap - buf.Len()
			if room > 0 {
				if n > room {
					n = room
				}
				buf.Write(tmp[:n])
			}
			task.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// GetBashTask returns the snapshot for id. ok=false when no task
// matches.
func GetBashTask(id string) (BashTaskSnapshot, bool) {
	BashTasks.mu.RLock()
	t, ok := BashTasks.tasks[id]
	BashTasks.mu.RUnlock()
	if !ok {
		return BashTaskSnapshot{}, false
	}
	return t.Snapshot(), true
}

// KillBashTask cancels the task's context, which propagates SIGKILL
// to the whole process group via ApplyGroupWithCtxCancel. No-op
// when the task is already terminal. Returns ok=false for unknown
// IDs.
func KillBashTask(id string) (BashTaskSnapshot, bool) {
	BashTasks.mu.RLock()
	t, ok := BashTasks.tasks[id]
	BashTasks.mu.RUnlock()
	if !ok {
		return BashTaskSnapshot{}, false
	}
	t.mu.Lock()
	if t.status == BashTaskActive && t.cancel != nil {
		t.cancel()
	}
	t.mu.Unlock()
	// Snapshot AFTER cancel so terminal status appears if the
	// goroutine raced to update it.
	return t.Snapshot(), true
}

// ListBashTasks returns every recorded task, newest first. Bounded
// by limit (0 = no cap).
func ListBashTasks(limit int) []BashTaskSnapshot {
	BashTasks.mu.RLock()
	out := make([]BashTaskSnapshot, 0, len(BashTasks.tasks))
	for _, t := range BashTasks.tasks {
		out = append(out, t.Snapshot())
	}
	BashTasks.mu.RUnlock()
	// Sort: newest StartedAt first.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].StartedAt.After(out[j-1].StartedAt); j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
