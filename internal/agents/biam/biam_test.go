package biam

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIdentity_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "id.ed25519")
	a, err := LoadOrCreateIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if a.HostID == "" || a.InstanceID == "" {
		t.Errorf("identity should default host/instance: %+v", a)
	}
	if len(a.Public) == 0 {
		t.Error("public key empty after create")
	}
	// Second load should return the same keypair.
	b, err := LoadOrCreateIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(a.Public) != string(b.Public) {
		t.Error("public key not stable across loads")
	}
}

func TestIdentity_RejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.ed25519")
	if err := os.WriteFile(path, []byte("not a valid identity\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateIdentity(path); err == nil {
		t.Error("expected error on corrupt identity file")
	}
}

func TestEnvelope_SignVerify(t *testing.T) {
	dir := t.TempDir()
	id, _ := LoadOrCreateIdentity(filepath.Join(dir, "id.ed25519"))
	from := Address{HostID: id.HostID, InstanceID: id.InstanceID}
	to := Address{HostID: id.HostID, InstanceID: "codex"}
	env := NewEnvelope(from, to, "", KindPrompt, Body{Text: "hello"})

	if err := env.Sign(id); err != nil {
		t.Fatal(err)
	}
	if env.Signature == "" {
		t.Error("signature not set after Sign")
	}
	if err := env.Verify(id.Public); err != nil {
		t.Errorf("Verify with sender key should succeed: %v", err)
	}

	// Tamper the body; verify should fail.
	env.Body.Text = "tampered"
	if err := env.Verify(id.Public); err == nil {
		t.Error("Verify should fail after body tamper")
	}
}

func TestEnvelope_HasCycle(t *testing.T) {
	env := NewEnvelope(Address{"a", "x"}, Address{"b", "y"}, "", KindPrompt, Body{})
	if env.HasCycle(Address{"b", "y"}) {
		t.Error("fresh envelope should not see target as cycle")
	}
	env.Trace = append(env.Trace, "b/y")
	if !env.HasCycle(Address{"b", "y"}) {
		t.Error("cycle detection failed")
	}
}

func TestEnvelope_HopLimit(t *testing.T) {
	env := NewEnvelope(Address{"a", "x"}, Address{"b", "y"}, "", KindPrompt, Body{})
	env.MaxHops = 2
	if err := env.Hop(Address{"b", "y"}); err != nil {
		t.Fatal(err)
	}
	if err := env.Hop(Address{"a", "x"}); err != nil {
		t.Fatal(err)
	}
	if err := env.Hop(Address{"c", "z"}); err == nil {
		t.Error("expected hop_count exceeded error")
	}
}

func TestStore_CreateGetList(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "biam.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.CreateTask(context.Background(), "task-1", "claude/me", "codex"); err != nil {
		t.Fatal(err)
	}
	t1, err := store.GetTask(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if t1 == nil || t1.Status != TaskPending {
		t.Errorf("created task wrong: %+v", t1)
	}
	if t1.Agent != "codex" {
		t.Errorf("agent: %q", t1.Agent)
	}
	tasks, err := store.ListTasks(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Errorf("expected 1 task; got %d", len(tasks))
	}
}

func TestStore_PutEnvelope_Dedupe(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "biam.db"))
	defer store.Close()

	id, _ := LoadOrCreateIdentity(filepath.Join(dir, "id"))
	env := NewEnvelope(Address{"a", "x"}, Address{"a", "y"}, "task-2", KindPrompt, Body{Text: "hi"})
	_ = env.Sign(id)

	_ = store.CreateTask(context.Background(), env.TaskID, "a/x", "y")
	if err := store.PutEnvelope(context.Background(), env, false); err != nil {
		t.Fatal(err)
	}
	// Second insert with same idempotency_key is a no-op.
	if err := store.PutEnvelope(context.Background(), env, false); err != nil {
		t.Fatal(err)
	}
	msgs, err := store.MessagesFor(context.Background(), env.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Errorf("dedupe failed; got %d msgs", len(msgs))
	}
}

func TestStore_SetStatus_Terminal(t *testing.T) {
	dir := t.TempDir()
	store, _ := OpenStore(filepath.Join(dir, "biam.db"))
	defer store.Close()
	_ = store.CreateTask(context.Background(), "task-3", "me", "codex")
	if err := store.SetTaskStatus(context.Background(), "task-3", TaskDone, "summary line"); err != nil {
		t.Fatal(err)
	}
	t3, _ := store.GetTask(context.Background(), "task-3")
	if t3.Status != TaskDone {
		t.Errorf("status: %q", t3.Status)
	}
	if t3.ClosedAt == nil {
		t.Error("closed_at should be set on terminal status")
	}
	if t3.LastMessage != "summary line" {
		t.Errorf("last_message: %q", t3.LastMessage)
	}
}

// fakeSend returns a streaming reader with deterministic content so
// the runner has something to drain.
type fakeSend struct {
	body string
	err  error
}

func (f fakeSend) call(ctx context.Context, instance, prompt string, opts map[string]any) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(strings.NewReader(f.body)), nil
}

func TestRunner_Submit_HappyPath(t *testing.T) {
	dir := t.TempDir()
	id, _ := LoadOrCreateIdentity(filepath.Join(dir, "id"))
	store, _ := OpenStore(filepath.Join(dir, "biam.db"))
	defer store.Close()

	send := fakeSend{body: "agent reply"}
	r := NewRunner(store, id, send.call)

	taskID, err := r.Submit(context.Background(), "codex", "ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t1, err := store.WaitForTerminal(ctx, taskID, 50*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if t1.Status != TaskDone {
		t.Errorf("status: %q", t1.Status)
	}
	msgs, _ := store.MessagesFor(context.Background(), taskID)
	if len(msgs) != 2 {
		t.Errorf("expected 2 envelopes (prompt+result); got %d", len(msgs))
	}
	gotResult := false
	for _, m := range msgs {
		if m.Kind == KindResult && strings.Contains(m.Body.Text, "agent reply") {
			gotResult = true
		}
	}
	if !gotResult {
		t.Error("result envelope missing or body wrong")
	}
}

func TestRunner_Submit_Failure(t *testing.T) {
	dir := t.TempDir()
	id, _ := LoadOrCreateIdentity(filepath.Join(dir, "id"))
	store, _ := OpenStore(filepath.Join(dir, "biam.db"))
	defer store.Close()
	send := fakeSend{err: errors.New("synthetic failure")}
	r := NewRunner(store, id, send.call)
	taskID, err := r.Submit(context.Background(), "codex", "ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	t1, _ := store.WaitForTerminal(ctx, taskID, 50*time.Millisecond)
	if t1.Status != TaskFailed {
		t.Errorf("expected failed; got %q", t1.Status)
	}
}

func TestStore_OpenIdempotent(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "biam.db"))
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	store2, err := OpenStore(filepath.Join(dir, "biam.db"))
	if err != nil {
		t.Errorf("re-open should work; got %v", err)
	}
	store2.Close()
}

func TestParsePublicKey(t *testing.T) {
	id, _ := LoadOrCreateIdentity(filepath.Join(t.TempDir(), "id"))
	encoded := id.PublicKeyB64()
	pk, err := ParsePublicKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(pk) != string(id.Public) {
		t.Error("round-trip public key mismatch")
	}
	if _, err := ParsePublicKey("notvalid"); err == nil {
		t.Error("expected error on missing prefix")
	}
}
