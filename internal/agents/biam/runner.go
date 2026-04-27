package biam

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/cogitave/clawtool/internal/hooks"
)

// SendStream is the function shape the runner expects from Supervisor:
// invoke `instance` with `prompt` + `opts`, return a streaming
// io.ReadCloser. Matches Supervisor.Send so we can swap in tests.
type SendStream func(ctx context.Context, instance, prompt string, opts map[string]any) (io.ReadCloser, error)

// Runner glues the BIAM store to the supervisor's dispatch surface:
// async submissions land in the store as `prompt` envelopes; a
// goroutine drains the upstream stream and persists `result` (or
// `error`) envelopes; tasks transition through pending → active →
// done|failed.
type Runner struct {
	mu       sync.Mutex
	store    *Store
	identity *Identity
	send     SendStream
}

// NewRunner wires the runner. Identity + store are mandatory; send is
// the supervisor's dispatch func.
func NewRunner(store *Store, id *Identity, send SendStream) *Runner {
	return &Runner{store: store, identity: id, send: send}
}

// Submit enqueues an async dispatch. Returns the new task_id
// immediately; the goroutine streams the response into the store and
// transitions the task on completion. Cancel via `Cancel(taskID)`.
func (r *Runner) Submit(ctx context.Context, instance, prompt string, opts map[string]any) (string, error) {
	if r == nil || r.store == nil || r.identity == nil || r.send == nil {
		return "", errors.New("biam: runner not initialised")
	}
	to := Address{HostID: r.identity.HostID, InstanceID: instance}
	from := Address{HostID: r.identity.HostID, InstanceID: r.identity.InstanceID}

	env := NewEnvelope(from, to, "", KindPrompt, Body{Text: prompt})
	if err := env.Sign(r.identity); err != nil {
		return "", err
	}
	if err := r.store.CreateTask(ctx, env.TaskID, from.String(), instance); err != nil {
		return "", fmt.Errorf("biam: create task: %w", err)
	}
	if err := r.store.PutEnvelope(ctx, env, false); err != nil {
		return "", fmt.Errorf("biam: persist prompt: %w", err)
	}

	// Detached background dispatch — uses context.Background so the
	// caller's ctx cancellation doesn't kill the in-flight upstream
	// when they only meant to stop waiting. Cancel via Cancel().
	go r.run(env, instance, prompt, opts)

	return env.TaskID, nil
}

// run drains the upstream stream into the store and finalises the
// task. Body of the result envelope carries the (capped) full text;
// large outputs truncate so SQLite stays bounded.
func (r *Runner) run(prompt *Envelope, instance, promptText string, opts map[string]any) {
	bg := context.Background()
	_ = r.store.SetTaskStatus(bg, prompt.TaskID, TaskActive, "")

	rc, err := r.send(bg, instance, promptText, opts)
	if err != nil {
		r.recordResult(prompt, KindError, fmt.Sprintf("send error: %v", err), TaskFailed)
		return
	}
	defer rc.Close()

	// Buffer up to 4 MiB; anything beyond gets truncated with a marker.
	buf, truncated := readCapped(rc, 4*1024*1024)
	body := buf.String()
	if truncated {
		body += "\n\n…[truncated by clawtool BIAM at 4 MiB]"
	}
	r.recordResult(prompt, KindResult, body, TaskDone)
}

// recordResult writes the terminal envelope + flips the task row.
func (r *Runner) recordResult(prompt *Envelope, kind EnvelopeKind, body string, terminal TaskStatus) {
	bg := context.Background()
	from := Address{HostID: r.identity.HostID, InstanceID: prompt.To.InstanceID} // sender = the upstream agent
	to := Address{HostID: r.identity.HostID, InstanceID: r.identity.InstanceID}  // recipient = us
	reply := NewEnvelope(from, to, prompt.TaskID, kind, Body{Text: body})
	reply.ParentID = prompt.MessageID
	_ = reply.Sign(r.identity)

	if err := r.store.PutEnvelope(bg, reply, true); err != nil {
		// Best-effort: even if persist fails, still flip the task so
		// callers don't block forever. The task row's last_message
		// records the body via SetTaskStatus.
		_ = r.store.SetTaskStatus(bg, prompt.TaskID, terminal, summary(body))
	} else {
		_ = r.store.SetTaskStatus(bg, prompt.TaskID, terminal, summary(body))
	}
	// on_task_complete hook (F3) fires after the task row settles so
	// user scripts read a stable snapshot. The hook can't fail the
	// task — it's already terminal — but errors surface via the hook
	// manager's log path.
	if mgr := hooks.Get(); mgr != nil {
		_ = mgr.Emit(bg, hooks.EventOnTaskComplete, map[string]any{
			"task_id": prompt.TaskID,
			"agent":   prompt.To.InstanceID,
			"kind":    string(kind),
			"status":  string(terminal),
		})
	}
}

// summary trims the body to a one-line summary stored on the task row.
// Long bodies live in the messages table; the task summary is the
// glanceable headline.
func summary(s string) string {
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if i := indexNewline(s); i >= 0 {
		return s[:i]
	}
	return s
}

func indexNewline(s string) int {
	for i, r := range s {
		if r == '\n' {
			return i
		}
	}
	return -1
}

// readCapped consumes r up to `cap` bytes; reports truncation when
// the upstream had more.
func readCapped(r io.Reader, cap int) (*bytes.Buffer, bool) {
	buf := &bytes.Buffer{}
	tmp := make([]byte, 32*1024)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			if buf.Len()+n > cap {
				take := cap - buf.Len()
				if take > 0 {
					buf.Write(tmp[:take])
				}
				return buf, true
			}
			buf.Write(tmp[:n])
		}
		if err != nil {
			return buf, false
		}
	}
}

// WaitForTerminal proxies to the store with a default poll interval.
func (r *Runner) WaitForTerminal(ctx context.Context, taskID string, poll time.Duration) (*Task, error) {
	return r.store.WaitForTerminal(ctx, taskID, poll)
}
