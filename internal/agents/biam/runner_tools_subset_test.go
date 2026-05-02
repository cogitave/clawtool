package biam

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSendMessage_ToolsSubset_ForwardsToUpstream proves the in-band
// wiring half of ADR-014 §Resolved (2026-05-02) Phase 4: when the
// SendMessage caller passes a curated `tools` subset, the resulting
// BIAM prompt envelope carries it on body.extras.tools_subset so
// the upstream-peer-side filtering layer (per-bridge wiring lands
// progressively) AND the audit trail can both read the operator's
// intent off the same envelope.
//
// Stub upstream: we don't care what the dispatch actually does; the
// envelope is persisted before the goroutine even wakes. We poll
// the store once the task lands, then assert.
func TestSendMessage_ToolsSubset_ForwardsToUpstream(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "biam.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	id, err := LoadOrCreateIdentity(filepath.Join(dir, "identity.ed25519"))
	if err != nil {
		t.Fatal(err)
	}

	// Capture opts the runner threads to the upstream so we can
	// also assert the supervisor side sees the subset (some bridges
	// may consume it via opts before the envelope round-trip).
	var capturedOpts map[string]any
	send := func(_ context.Context, _ string, _ string, opts map[string]any) (io.ReadCloser, error) {
		capturedOpts = opts
		return io.NopCloser(strings.NewReader("ok")), nil
	}
	r := NewRunner(store, id, send)

	subset := []string{"Bash", "Read", "Grep"}
	ctx := t.Context()
	taskID, err := r.Submit(ctx, "claude", "ping", map[string]any{
		"tools_subset": subset,
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Wait for terminal so the goroutine has captured opts.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tk, err := store.GetTask(ctx, taskID)
		if err == nil && tk != nil && tk.Status.IsTerminal() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	msgs, err := store.MessagesFor(ctx, taskID)
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatalf("expected at least one envelope, got 0")
	}
	prompt := msgs[0]
	if prompt.Kind != KindPrompt {
		t.Fatalf("first envelope kind = %q, want %q", prompt.Kind, KindPrompt)
	}
	if prompt.Body.Extras == nil {
		t.Fatalf("envelope.body.extras is nil — tools_subset never attached")
	}
	raw, ok := prompt.Body.Extras["tools_subset"]
	if !ok {
		t.Fatalf("envelope.body.extras has no tools_subset key: %v", prompt.Body.Extras)
	}
	// Envelopes round-trip through JSON, which decodes []string as
	// []any. Accept either shape so the test stays happy whether
	// the runner stores typed or the store re-marshals through
	// MessagesFor's JSON path.
	got := normalizeStringSlice(raw)
	if len(got) != len(subset) {
		t.Fatalf("tools_subset length: got %v, want %v", got, subset)
	}
	for i, want := range subset {
		if got[i] != want {
			t.Errorf("tools_subset[%d] = %q, want %q", i, got[i], want)
		}
	}

	// Belt-and-braces: the supervisor (`send` closure) also saw
	// the subset on the opts map. Lets a downstream bridge
	// read it directly without unpacking the envelope.
	if capturedOpts == nil {
		t.Fatal("send was never invoked — opts capture is empty")
	}
	if _, ok := capturedOpts["tools_subset"]; !ok {
		t.Errorf("supervisor opts dropped tools_subset: %v", capturedOpts)
	}
}

// TestSendMessage_ToolsSubset_EmptyDefaultsToAllTools_RunnerSide is
// the runner-half back-compat lock: when the caller doesn't supply
// `tools_subset` (or supplies an empty slice), the prompt envelope
// MUST NOT carry a body.extras.tools_subset key. Anything else
// would be observably different from the pre-Phase-4 wire shape and
// every consumer that does a key-presence check (per-bridge filter
// wiring; future audit dashboards) would mis-classify the dispatch
// as restricted.
func TestSendMessage_ToolsSubset_EmptyDefaultsToAllTools_RunnerSide(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "biam.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	id, err := LoadOrCreateIdentity(filepath.Join(dir, "identity.ed25519"))
	if err != nil {
		t.Fatal(err)
	}

	send := func(_ context.Context, _ string, _ string, _ map[string]any) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader("ok")), nil
	}
	r := NewRunner(store, id, send)

	cases := []struct {
		name string
		opts map[string]any
	}{
		{"absent", map[string]any{}},
		{"empty []string", map[string]any{"tools_subset": []string{}}},
		{"empty []any", map[string]any{"tools_subset": []any{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			taskID, err := r.Submit(ctx, "claude", "ping", tc.opts)
			if err != nil {
				t.Fatalf("submit: %v", err)
			}
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				tk, err := store.GetTask(ctx, taskID)
				if err == nil && tk != nil && tk.Status.IsTerminal() {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			msgs, err := store.MessagesFor(ctx, taskID)
			if err != nil {
				t.Fatalf("messages: %v", err)
			}
			if len(msgs) == 0 {
				t.Fatalf("expected at least one envelope, got 0")
			}
			prompt := msgs[0]
			if prompt.Body.Extras == nil {
				return // good: no extras at all
			}
			if _, ok := prompt.Body.Extras["tools_subset"]; ok {
				t.Errorf("envelope.body.extras.tools_subset must be ABSENT for back-compat; got %v",
					prompt.Body.Extras)
			}
		})
	}
}

// normalizeStringSlice coerces a body.extras value that came back
// from the store into a []string regardless of whether the round
// trip went through JSON ([]any of strings) or kept the typed
// slice. Helper for the assertions above.
func normalizeStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return append([]string(nil), s...)
	case []any:
		out := make([]string, 0, len(s))
		for _, x := range s {
			if str, ok := x.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}
