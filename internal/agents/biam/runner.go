package biam

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/cogitave/clawtool/internal/hooks"
	"github.com/cogitave/clawtool/internal/telemetry"
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
	// inflight tracks the per-task cancel func of an active
	// dispatch goroutine. Populated in Submit, cleared in run on
	// terminal. Cancel(taskID) looks up + invokes the func to
	// unblock the upstream stream + propagate via the
	// context-aware Send chain (which SIGINTs the child via
	// streamingProcess.Close on ctx.Done).
	inflight map[string]context.CancelFunc
}

// NewRunner wires the runner. Identity + store are mandatory; send is
// the supervisor's dispatch func.
func NewRunner(store *Store, id *Identity, send SendStream) *Runner {
	return &Runner{store: store, identity: id, send: send, inflight: map[string]context.CancelFunc{}}
}

// Submit enqueues an async dispatch. Returns the new task_id
// immediately; the goroutine streams the response into the store and
// transitions the task on completion. Cancel via `Cancel(taskID)`.
//
// `opts["from_instance"]` overrides the default `from` address. Cross-
// host bidi: when codex / gemini / opencode dispatch back to claude
// through the shared daemon, they pass their own family name so the
// resulting envelope's `from` reflects the actual sender, not the
// daemon's own identity. Without this, every BIAM thread looked like
// it originated from one centralised initiator and downstream
// reply-tracking ambiguated.
func (r *Runner) Submit(ctx context.Context, instance, prompt string, opts map[string]any) (string, error) {
	if r == nil || r.store == nil || r.identity == nil || r.send == nil {
		return "", errors.New("biam: runner not initialised")
	}
	to := Address{HostID: r.identity.HostID, InstanceID: instance}
	from := Address{HostID: r.identity.HostID, InstanceID: r.identity.InstanceID}
	if v, ok := opts["from_instance"]; ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			from.InstanceID = strings.TrimSpace(s)
		}
	}

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

	// Detached background dispatch with its OWN context so
	// Cancel(taskID) can unblock the upstream stream without
	// killing every in-flight dispatch. Caller's ctx is for
	// envelope persistence only — once Submit returns, the
	// goroutine owns its lifecycle.
	runCtx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.inflight[env.TaskID] = cancel
	r.mu.Unlock()
	go r.run(runCtx, env, instance, prompt, opts)

	return env.TaskID, nil
}

// Cancel propagates a cancellation request to the dispatch goroutine
// for taskID. Idempotent: returns nil for unknown / already-terminal
// tasks. The actual upstream process kill happens in
// streamingProcess.Close on ctx.Done — the runner's responsibility
// here is just to flip the row and wake the goroutine.
func (r *Runner) Cancel(ctx context.Context, taskID string) error {
	if r == nil || r.store == nil {
		return errors.New("biam: runner not initialised")
	}
	r.mu.Lock()
	cancelFn, ok := r.inflight[taskID]
	r.mu.Unlock()
	if !ok {
		// Task already terminal or unknown — best-effort flip the
		// row to TaskCancelled if it's still pending/active. Soft
		// failure if the row doesn't exist.
		if t, err := r.store.GetTask(ctx, taskID); err == nil && t != nil {
			if t.Status == TaskPending || t.Status == TaskActive {
				_ = r.store.SetTaskStatus(ctx, taskID, TaskCancelled, "cancelled by operator")
				Notifier.Publish(Task{TaskID: taskID, Status: TaskCancelled, Agent: t.Agent})
			}
		}
		return nil
	}
	cancelFn()
	return nil
}

// run drains the upstream stream into the store and finalises the
// task. Body of the result envelope carries the (capped) full text;
// large outputs truncate so SQLite stays bounded.
func (r *Runner) run(ctx context.Context, prompt *Envelope, instance, promptText string, opts map[string]any) {
	defer func() {
		// Always release the inflight cancel slot, even on early
		// return so Cancel becomes idempotent post-terminal.
		r.mu.Lock()
		delete(r.inflight, prompt.TaskID)
		r.mu.Unlock()
	}()
	bg := context.Background()
	_ = r.store.SetTaskStatus(bg, prompt.TaskID, TaskActive, "")

	rc, err := r.send(ctx, instance, promptText, opts)
	if err != nil {
		// Distinguish operator cancel from a genuine send failure
		// so the task row reflects intent.
		if ctx.Err() != nil {
			r.recordResult(prompt, KindError, "cancelled by operator before dispatch started", TaskCancelled)
			return
		}
		r.recordResult(prompt, KindError, fmt.Sprintf("send error: %v", err), TaskFailed)
		return
	}

	// Buffer up to 4 MiB AND broadcast every line to the WatchHub
	// as it arrives so the orchestrator / dashboard panes can show
	// live stdout. Body is rebuilt from the same scanned stream so
	// the persisted result envelope is byte-identical to the old
	// readCapped path.
	body, truncated := readCappedBroadcast(rc, 4*1024*1024, prompt.TaskID, instance)
	if truncated {
		body += "\n\n…[truncated by clawtool BIAM at 4 MiB]"
	}

	// Two failure signals matter:
	//   1. Process-level: streamingProcess.Close() returns ExitError
	//      when the upstream CLI exited non-zero. Easy case.
	//   2. Stream-level: every modern coding-agent CLI emits NDJSON
	//      events with a final {"type":"turn.failed"} or
	//      {"type":"error"} when the run aborts mid-flight (codex's
	//      content-policy flag, claude's tool-loop overflow, etc.)
	//      while still exiting 0. Without scanning the tail we record
	//      these as TaskDone with a useless transcript and downstream
	//      pollers wait forever for an answer that never comes.
	closeErr := rc.Close()
	streamFail := detectStreamFailure(body)
	terminal := TaskDone
	kind := KindResult
	switch {
	case closeErr != nil:
		terminal = TaskFailed
		kind = KindError
		if body != "" {
			body += "\n\n"
		}
		body += fmt.Sprintf("upstream exited non-zero: %v", closeErr)
	case streamFail != "":
		terminal = TaskFailed
		kind = KindError
		if body != "" {
			body += "\n\n"
		}
		body += "upstream stream reported failure: " + streamFail
	}
	r.recordResult(prompt, kind, body, terminal)
}

// detectStreamFailure scans the tail of an NDJSON stream-json body for
// terminal failure events. Returns the failure detail (or empty string
// when the stream looks healthy). Supports the shapes claude / codex /
// gemini emit today: top-level {"type":"turn.failed",...},
// {"type":"error",...}, and codex's {"type":"item.completed","item":{
// "type":"command_execution","status":"failed"}} which we deliberately
// IGNORE (tool calls fail individually all the time without ending
// the turn).
func detectStreamFailure(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")
	// Walk from the tail — only the LAST terminal event matters.
	for i := len(lines) - 1; i >= 0 && i > len(lines)-12; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || line[0] != '{' {
			continue
		}
		var ev struct {
			Type    string          `json:"type"`
			Error   json.RawMessage `json:"error,omitempty"`
			Message string          `json:"message,omitempty"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "turn.failed", "error":
			if msg := strings.TrimSpace(ev.Message); msg != "" {
				return ev.Type + ": " + msg
			}
			if len(ev.Error) > 0 {
				var inner struct {
					Message string `json:"message"`
				}
				if json.Unmarshal(ev.Error, &inner) == nil && inner.Message != "" {
					return ev.Type + ": " + inner.Message
				}
				return ev.Type + ": " + string(ev.Error)
			}
			return ev.Type
		}
	}
	return ""
}

// recordResult writes the terminal envelope + flips the task row.
func (r *Runner) recordResult(prompt *Envelope, kind EnvelopeKind, body string, terminal TaskStatus) {
	bg := context.Background()
	from := Address{HostID: r.identity.HostID, InstanceID: prompt.To.InstanceID} // sender = the upstream agent
	to := Address{HostID: r.identity.HostID, InstanceID: r.identity.InstanceID}  // recipient = us
	reply := NewEnvelope(from, to, prompt.TaskID, kind, Body{Text: body})
	reply.ParentID = prompt.MessageID
	_ = reply.Sign(r.identity)

	// Best-effort persist of the reply envelope; failure is logged
	// implicitly via the discarded error but doesn't abort the task
	// flip — callers waiting on the terminal state must unblock
	// even if SQLite is temporarily wedged.
	_ = r.store.PutEnvelope(bg, reply, true)
	// Always flip the task row to terminal — the row's last_message
	// column carries the body via SetTaskStatus, so the persist
	// outcome above doesn't gate the user-visible state. (Pre-fix
	// this was an if/else where both arms called the same line; the
	// branching was a no-op with an empty success path that looked
	// like an unfinished refactor.)
	_ = r.store.SetTaskStatus(bg, prompt.TaskID, terminal, summary(body))
	// In-process completion push so TaskNotify callers wake the
	// instant a task settles, no SQLite poll. Done after the row
	// flip so subscribers see the terminal state if they re-query.
	// Best-effort GetTask: if it fails (DB transient) we still
	// publish a synthetic Task so the subscriber unblocks rather
	// than waiting on a poll that may never arrive.
	if t, err := r.store.GetTask(bg, prompt.TaskID); err == nil && t != nil {
		Notifier.Publish(*t)
	} else {
		Notifier.Publish(Task{
			TaskID: prompt.TaskID,
			Status: terminal,
			Agent:  prompt.To.InstanceID,
		})
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

	// Telemetry: BIAM task terminal. Family extracted from instance
	// label by trimming the trailing -<n> suffix that BridgeAdd
	// appends; stays anonymous (no instance-specific label leaks).
	if tc := telemetry.Get(); tc != nil && tc.Enabled() {
		duration := int64(0)
		if t, err := r.store.GetTask(bg, prompt.TaskID); err == nil && t != nil {
			if t.ClosedAt != nil {
				duration = t.ClosedAt.Sub(t.CreatedAt).Milliseconds()
			}
		}
		tc.Track("biam.task.terminal", map[string]any{
			"agent":       familyFromInstance(prompt.To.InstanceID),
			"outcome":     biamOutcome(terminal),
			"duration_ms": duration,
		})
	}
}

// familyFromInstance strips trailing -<n> suffixes that the bridge
// installer appends so the telemetry stays at family granularity
// only (claude / codex / gemini / opencode / hermes), never the
// per-instance label.
func familyFromInstance(inst string) string {
	for i := len(inst) - 1; i >= 0; i-- {
		c := inst[i]
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '-' && i < len(inst)-1 {
			return inst[:i]
		}
		break
	}
	if idx := strings.IndexByte(inst, '-'); idx > 0 {
		return inst[:idx]
	}
	return inst
}

func biamOutcome(s TaskStatus) string {
	switch s {
	case TaskDone:
		return "success"
	case TaskFailed:
		return "error"
	case TaskCancelled:
		return "cancelled"
	case TaskExpired:
		return "timeout"
	}
	return string(s)
}

// summary trims the body to a one-line summary stored on the task row.
// Long bodies live in the messages table; the task summary is the
// glanceable headline.
//
// NDJSON awareness: codex / gemini / opencode all emit
// newline-delimited JSON event streams. The very first line is
// usually `{"type":"thread.started","thread_id":"…"}` — a useless
// header. The actual reply lives in the LAST event of type
// `item.completed` with an inner `item.type == "agent_message"`.
// When we detect the NDJSON shape we walk the tail and lift the
// agent_message text instead of returning the meaningless header.
//
// Non-NDJSON outputs (plain text from claude -p, free-form bodies,
// error tails) fall through to the legacy first-line-up-to-200
// behaviour. Empty / unrecognised cases also fall through so the
// summary always has something visible.
func summary(s string) string {
	if v := summaryFromNDJSON(s); v != "" {
		return clipSummary(v)
	}
	return clipSummary(firstLine(s))
}

// summaryFromNDJSON walks lines of `s` for codex-style NDJSON
// events. Returns the last `agent_message` text when found, empty
// when the body is not NDJSON-shaped or no agent_message exists.
//
// Why walk forward rather than from the tail: events are sequential
// and we may have multiple `agent_message` items in a turn; the
// most-recent one is the right summary. Allocating a single decoder
// state and overwriting on each match keeps the function O(n) over
// body bytes.
func summaryFromNDJSON(s string) string {
	if len(s) == 0 || s[0] != '{' {
		return ""
	}
	var last string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var ev struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		if ev.Type == "item.completed" && ev.Item.Type == "agent_message" && strings.TrimSpace(ev.Item.Text) != "" {
			last = strings.TrimSpace(ev.Item.Text)
		}
	}
	return last
}

func firstLine(s string) string {
	if i := indexNewline(s); i >= 0 {
		return s[:i]
	}
	return s
}

func clipSummary(s string) string {
	if len(s) > 200 {
		return s[:200] + "…"
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

// readCappedBroadcast reads r line-by-line, buffers up to `cap` bytes
// for the persisted result body, AND fans every line as a StreamFrame
// to the WatchHub so live consumers (orchestrator, dashboard,
// `task watch`) can render the upstream agent's output as it arrives.
//
// Returns the assembled body string + a truncation flag. Lines past
// the cap stop being appended to the body but continue to broadcast
// — the live view stays accurate even when the persisted result hits
// the SQLite size limit.
func readCappedBroadcast(r io.Reader, capBytes int, taskID, instance string) (string, bool) {
	agent := familyFromInstance(instance)
	br := bufio.NewReaderSize(r, 64*1024)
	var body bytes.Buffer
	truncated := false
	first := true

	for {
		line, err := br.ReadString('\n')
		if line != "" {
			// Append to body up to the cap.
			if !truncated {
				if body.Len()+len(line) > capBytes {
					take := capBytes - body.Len()
					if take > 0 {
						body.WriteString(line[:take])
					}
					truncated = true
				} else {
					body.WriteString(line)
				}
			}
			// Trim the trailing newline for the broadcast — the
			// renderer adds its own line separator. Empty lines
			// pass through (operators see the agent's blank
			// lines too).
			emit := strings.TrimRight(line, "\n")
			if !first || emit != "" {
				Watch.BroadcastFrame(StreamFrame{
					TaskID: taskID,
					Agent:  agent,
					Line:   emit,
					Kind:   "stdout",
					TS:     time.Now().UTC(),
				})
			}
			first = false
		}
		if err != nil {
			return body.String(), truncated
		}
	}
}

// WaitForTerminal proxies to the store with a default poll interval.
func (r *Runner) WaitForTerminal(ctx context.Context, taskID string, poll time.Duration) (*Task, error) {
	return r.store.WaitForTerminal(ctx, taskID, poll)
}
