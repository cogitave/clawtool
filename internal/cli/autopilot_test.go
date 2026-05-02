package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// withTmpAutopilotConfig redirects $XDG_CONFIG_HOME so the autopilot
// queue lands in a tmpdir for the duration of the test. The default
// queue path is $XDG_CONFIG_HOME/clawtool/autopilot/queue.toml so a
// single env override isolates every operation.
func withTmpAutopilotConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
}

func newAutopilotApp(t *testing.T) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	errb := &bytes.Buffer{}
	return &App{Stdout: out, Stderr: errb}, out, errb
}

// TestAutopilot_AddNextDoneLoop confirms the canonical agent loop:
// add an item → next dequeues it → done closes it → next returns
// silent on empty. This is the "devam edebilme yeteneği" (non-stalling
// continuation) primitive's whole reason to exist.
func TestAutopilot_AddNextDoneLoop(t *testing.T) {
	withTmpAutopilotConfig(t)

	app, out, errb := newAutopilotApp(t)
	if rc := app.Run([]string{"autopilot", "add", "ship the autopilot primitive"}); rc != 0 {
		t.Fatalf("add rc=%d, stderr=%s", rc, errb.String())
	}
	id := strings.TrimSpace(out.String())
	if id == "" {
		t.Fatalf("add did not print an id")
	}

	// next: emit JSON so we can assert the dequeued id.
	out.Reset()
	errb.Reset()
	if rc := app.Run([]string{"autopilot", "next", "--format", "json"}); rc != 0 {
		t.Fatalf("next rc=%d, stderr=%s", rc, errb.String())
	}
	var item struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(out.Bytes(), &item); err != nil {
		t.Fatalf("next json: %v\n%s", err, out.String())
	}
	if item.ID != id {
		t.Fatalf("next returned id %q, want %q", item.ID, id)
	}
	if item.Status != "in_progress" {
		t.Fatalf("next returned status %q, want in_progress", item.Status)
	}

	// done by id.
	out.Reset()
	errb.Reset()
	if rc := app.Run([]string{"autopilot", "done", id}); rc != 0 {
		t.Fatalf("done rc=%d, stderr=%s", rc, errb.String())
	}

	// next on empty queue: text format silent + rc=0.
	out.Reset()
	errb.Reset()
	if rc := app.Run([]string{"autopilot", "next"}); rc != 0 {
		t.Fatalf("next-empty rc=%d, stderr=%s", rc, errb.String())
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("next-empty wrote stdout %q, want silent", out.String())
	}

	// next on empty queue: json format prints {} so a shell loop
	// can detect drainage with `[ "$item" = "{}" ] && break`.
	out.Reset()
	errb.Reset()
	if rc := app.Run([]string{"autopilot", "next", "--format", "json"}); rc != 0 {
		t.Fatalf("next-empty-json rc=%d, stderr=%s", rc, errb.String())
	}
	if strings.TrimSpace(out.String()) != "{}" {
		t.Fatalf("next-empty-json wrote %q, want '{}'", out.String())
	}
}

// TestAutopilot_StatusJSON confirms the histogram surfaces every
// state. Drives the wire contract for shell pipelines.
func TestAutopilot_StatusJSON(t *testing.T) {
	withTmpAutopilotConfig(t)
	app, out, _ := newAutopilotApp(t)

	for i := 0; i < 3; i++ {
		out.Reset()
		if rc := app.Run([]string{"autopilot", "add", "task"}); rc != 0 {
			t.Fatalf("add rc=%d", rc)
		}
	}

	out.Reset()
	if rc := app.Run([]string{"autopilot", "status", "--format", "json"}); rc != 0 {
		t.Fatalf("status rc=%d", rc)
	}
	var c struct {
		Pending    int `json:"pending"`
		InProgress int `json:"in_progress"`
		Done       int `json:"done"`
		Skipped    int `json:"skipped"`
		Total      int `json:"total"`
	}
	if err := json.Unmarshal(out.Bytes(), &c); err != nil {
		t.Fatalf("status json: %v\n%s", err, out.String())
	}
	if c.Pending != 3 || c.Total != 3 {
		t.Fatalf("status json mismatch: %+v", c)
	}
}

// TestAutopilot_Skip confirms skip removes from pending list.
func TestAutopilot_Skip(t *testing.T) {
	withTmpAutopilotConfig(t)
	app, out, errb := newAutopilotApp(t)

	if rc := app.Run([]string{"autopilot", "add", "first"}); rc != 0 {
		t.Fatalf("add rc=%d, stderr=%s", rc, errb.String())
	}
	id := strings.TrimSpace(out.String())

	out.Reset()
	errb.Reset()
	if rc := app.Run([]string{"autopilot", "skip", id, "--note", "drop"}); rc != 0 {
		t.Fatalf("skip rc=%d, stderr=%s", rc, errb.String())
	}
	if !strings.Contains(out.String(), "skipped") {
		t.Fatalf("skip wrote %q, want 'skipped'", out.String())
	}

	// next on a queue with nothing pending returns empty.
	out.Reset()
	errb.Reset()
	if rc := app.Run([]string{"autopilot", "next"}); rc != 0 {
		t.Fatalf("next-empty rc=%d, stderr=%s", rc, errb.String())
	}
	if strings.TrimSpace(out.String()) != "" {
		t.Fatalf("next-after-skip wrote %q, want silent", out.String())
	}
}

// TestAutopilot_ListFilter confirms the --status filter narrows the
// returned set and JSON output parses cleanly.
func TestAutopilot_ListFilter(t *testing.T) {
	withTmpAutopilotConfig(t)
	app, out, _ := newAutopilotApp(t)

	for _, prompt := range []string{"a", "b", "c"} {
		out.Reset()
		if rc := app.Run([]string{"autopilot", "add", prompt}); rc != 0 {
			t.Fatalf("add rc=%d", rc)
		}
	}
	// Claim one and complete it so we have one done + two pending.
	out.Reset()
	if rc := app.Run([]string{"autopilot", "next", "--format", "json"}); rc != 0 {
		t.Fatalf("next rc=%d", rc)
	}
	var got struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("next json: %v\n%s", err, out.String())
	}
	out.Reset()
	if rc := app.Run([]string{"autopilot", "done", got.ID}); rc != 0 {
		t.Fatalf("done rc=%d", rc)
	}

	// Filter pending → 2.
	out.Reset()
	if rc := app.Run([]string{"autopilot", "list", "--status", "pending", "--format", "json"}); rc != 0 {
		t.Fatalf("list rc=%d", rc)
	}
	var pending []map[string]any
	if err := json.Unmarshal(out.Bytes(), &pending); err != nil {
		t.Fatalf("list json: %v\n%s", err, out.String())
	}
	if len(pending) != 2 {
		t.Fatalf("pending count=%d, want 2\n%s", len(pending), out.String())
	}

	// Filter done → 1.
	out.Reset()
	if rc := app.Run([]string{"autopilot", "list", "--status", "done", "--format", "json"}); rc != 0 {
		t.Fatalf("list rc=%d", rc)
	}
	var done []map[string]any
	if err := json.Unmarshal(out.Bytes(), &done); err != nil {
		t.Fatalf("list json: %v\n%s", err, out.String())
	}
	if len(done) != 1 {
		t.Fatalf("done count=%d, want 1\n%s", len(done), out.String())
	}
}

// TestAutopilot_UnknownVerb returns rc=2 with usage on stderr, and
// the bare verb prints usage. Mirrors every other group dispatcher.
func TestAutopilot_UnknownVerb(t *testing.T) {
	withTmpAutopilotConfig(t)
	app, _, errb := newAutopilotApp(t)

	if rc := app.Run([]string{"autopilot"}); rc != 2 {
		t.Fatalf("bare autopilot rc=%d, want 2", rc)
	}
	if !strings.Contains(errb.String(), "Usage:") {
		t.Fatalf("bare autopilot stderr missing usage:\n%s", errb.String())
	}

	errb.Reset()
	if rc := app.Run([]string{"autopilot", "noop"}); rc != 2 {
		t.Fatalf("autopilot noop rc=%d, want 2", rc)
	}
	if !strings.Contains(errb.String(), "unknown subcommand") {
		t.Fatalf("autopilot noop stderr missing 'unknown subcommand':\n%s", errb.String())
	}
}

// TestAutopilot_DoneNotFound surfaces the typed error from the queue
// layer with rc=1 and a clean error line.
func TestAutopilot_DoneNotFound(t *testing.T) {
	withTmpAutopilotConfig(t)
	app, _, errb := newAutopilotApp(t)

	if rc := app.Run([]string{"autopilot", "done", "nope"}); rc != 1 {
		t.Fatalf("done unknown rc=%d, want 1", rc)
	}
	if !strings.Contains(errb.String(), "not found") {
		t.Fatalf("done unknown stderr missing 'not found':\n%s", errb.String())
	}
}
