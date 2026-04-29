package core

import (
	"strings"
	"testing"
	"time"
)

func TestEditorContext_IsZero(t *testing.T) {
	if !(EditorContext{}).IsZero() {
		t.Error("zero value should report IsZero")
	}
	if (EditorContext{FilePath: "/tmp/x.go"}).IsZero() {
		t.Error("non-empty FilePath should not be IsZero")
	}
	if (EditorContext{StartLine: 1}).IsZero() {
		t.Error("non-zero StartLine should not be IsZero")
	}
	if (EditorContext{Intent: "refactor"}).IsZero() {
		t.Error("non-empty Intent should not be IsZero")
	}
}

func TestCurrentContext_DefaultSession(t *testing.T) {
	ResetContextsForTest()
	t.Cleanup(ResetContextsForTest)

	if !CurrentContext("").IsZero() {
		t.Error("empty session should yield empty context before any SetContext")
	}
	if !CurrentContext(defaultContextSession).IsZero() {
		t.Error("default session should yield empty context before any SetContext")
	}
}

func TestSetContextStore_MergeAndPersist(t *testing.T) {
	ResetContextsForTest()
	t.Cleanup(ResetContextsForTest)

	// Direct store mutation (mirroring what runSetContext does)
	// — covers the merge semantics without spinning up an MCP
	// server harness.
	contexts.mu.Lock()
	contexts.sessions["work"] = EditorContext{
		FilePath:  "/tmp/foo.go",
		StartLine: 10,
		Intent:    "first",
		UpdatedAt: time.Now(),
	}
	contexts.mu.Unlock()

	got := CurrentContext("work")
	if got.FilePath != "/tmp/foo.go" || got.StartLine != 10 || got.Intent != "first" {
		t.Fatalf("first write lost: %+v", got)
	}

	// Simulate a partial merge: update only Intent.
	contexts.mu.Lock()
	cur := contexts.sessions["work"]
	cur.Intent = "second"
	cur.UpdatedAt = time.Now()
	contexts.sessions["work"] = cur
	contexts.mu.Unlock()

	got = CurrentContext("work")
	if got.Intent != "second" {
		t.Errorf("Intent merge: want second, got %q", got.Intent)
	}
	if got.FilePath != "/tmp/foo.go" {
		t.Errorf("partial merge clobbered FilePath: %q", got.FilePath)
	}
	if got.StartLine != 10 {
		t.Errorf("partial merge clobbered StartLine: %d", got.StartLine)
	}
}

func TestSetContextStore_SessionsAreIsolated(t *testing.T) {
	ResetContextsForTest()
	t.Cleanup(ResetContextsForTest)

	contexts.mu.Lock()
	contexts.sessions["a"] = EditorContext{FilePath: "/a.go"}
	contexts.sessions["b"] = EditorContext{FilePath: "/b.go"}
	contexts.mu.Unlock()

	if CurrentContext("a").FilePath != "/a.go" {
		t.Errorf("session a leaked")
	}
	if CurrentContext("b").FilePath != "/b.go" {
		t.Errorf("session b leaked")
	}
	if CurrentContext("c").FilePath != "" {
		t.Errorf("unknown session should be empty, got %+v", CurrentContext("c"))
	}
}

func TestGetContextResult_RenderEmpty(t *testing.T) {
	r := getContextResult{
		BaseResult: BaseResult{Operation: "GetContext"},
		SessionID:  "default",
		Context:    EditorContext{},
	}
	out := r.Render()
	if !strings.Contains(out, "no context set") {
		t.Errorf("empty render missing hint: %q", out)
	}
}

func TestGetContextResult_RenderPopulated(t *testing.T) {
	r := getContextResult{
		BaseResult: BaseResult{Operation: "GetContext"},
		SessionID:  "work",
		Context: EditorContext{
			FilePath:  "/tmp/x.go",
			StartLine: 5,
			EndLine:   12,
			Intent:    "extract helper",
			UpdatedAt: time.Now().Add(-2 * time.Second),
			UpdatedBy: "claude",
		},
	}
	out := r.Render()
	for _, want := range []string{"work", "/tmp/x.go", "5–12", "extract helper", "claude"} {
		if !strings.Contains(out, want) {
			t.Errorf("populated render missing %q in:\n%s", want, out)
		}
	}
}

func TestSetContextResult_RenderShape(t *testing.T) {
	r := setContextResult{
		BaseResult: BaseResult{Operation: "SetContext"},
		SessionID:  "default",
		Context: EditorContext{
			FilePath: "/tmp/x.go",
			Intent:   "fix bug",
		},
	}
	out := r.Render()
	if !strings.Contains(out, "✓") {
		t.Errorf("success marker missing: %q", out)
	}
	if !strings.Contains(out, "fix bug") {
		t.Errorf("intent missing: %q", out)
	}
}
