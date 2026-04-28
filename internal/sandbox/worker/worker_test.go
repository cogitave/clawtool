package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Phase 1 tests exercise the per-kind handlers directly. The
// WebSocket roundtrip (auth + framing + JSON-line transport) is
// covered by the daemon-side integration suite; here we want fast,
// hermetic checks that the worker's request → response semantics
// are correct.

func TestProtocol_RequestRoundTrip(t *testing.T) {
	body, err := MarshalBody(ExecRequest{Command: "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	req := &Request{ID: "abc", Kind: KindExec, Body: body}
	raw, err := EncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRequest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.V != ProtocolVersion || got.ID != "abc" || got.Kind != KindExec {
		t.Errorf("decoded request mismatched: %+v", got)
	}
	var inner ExecRequest
	if err := UnmarshalBody(got.Body, &inner); err != nil {
		t.Fatal(err)
	}
	if inner.Command != "echo hi" {
		t.Errorf("body command = %q, want %q", inner.Command, "echo hi")
	}
}

func TestProtocol_VersionMismatchRejected(t *testing.T) {
	raw := []byte(`{"v":"99","id":"x","kind":"ping"}`)
	if _, err := DecodeRequest(raw); err == nil {
		t.Fatal("expected version mismatch error")
	}
}

func TestHandleExec_RunsAndCaptures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/bash; non-windows only")
	}
	workdir := t.TempDir()
	body, status, err := handleExec(context.Background(),
		ExecRequest{Command: "echo merhaba"},
		ServerOptions{Workdir: workdir, MaxBytes: 4 * 1024})
	if err != nil || status != 0 {
		t.Fatalf("handleExec: status=%d err=%v", status, err)
	}
	var resp ExecResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0", resp.ExitCode)
	}
	if resp.Stdout != "merhaba\n" {
		t.Errorf("stdout = %q, want %q", resp.Stdout, "merhaba\n")
	}
}

func TestHandleExec_NonZeroExitSurfaces(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/bash; non-windows only")
	}
	workdir := t.TempDir()
	body, status, err := handleExec(context.Background(),
		ExecRequest{Command: "exit 7"},
		ServerOptions{Workdir: workdir, MaxBytes: 4 * 1024})
	if err != nil || status != 0 {
		t.Fatalf("handleExec: status=%d err=%v", status, err)
	}
	var resp ExecResponse
	_ = json.Unmarshal(body, &resp)
	if resp.ExitCode != 7 {
		t.Errorf("exit code = %d, want 7", resp.ExitCode)
	}
}

func TestHandleRead_RoundTrip(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "hi.txt"), []byte("merhaba\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, status, err := handleRead(
		ReadRequest{Path: "hi.txt"},
		ServerOptions{Workdir: workdir})
	if err != nil || status != 0 {
		t.Fatalf("handleRead: status=%d err=%v", status, err)
	}
	var resp ReadResponse
	_ = json.Unmarshal(body, &resp)
	if resp.Content != "merhaba\nworld\n" {
		t.Errorf("content = %q, want %q", resp.Content, "merhaba\nworld\n")
	}
}

func TestHandleWrite_CreatesInsideWorkdir(t *testing.T) {
	workdir := t.TempDir()
	body, status, err := handleWrite(
		WriteRequest{Path: "subdir/new.txt", Content: "fresh"},
		ServerOptions{Workdir: workdir})
	if err != nil || status != 0 {
		t.Fatalf("handleWrite: status=%d err=%v", status, err)
	}
	var resp WriteResponse
	_ = json.Unmarshal(body, &resp)
	if !resp.Created {
		t.Error("expected Created=true on first write")
	}
	if got, _ := os.ReadFile(filepath.Join(workdir, "subdir/new.txt")); string(got) != "fresh" {
		t.Errorf("file content = %q, want %q", got, "fresh")
	}
}

// resolveInside is the path-jail trick that prevents an attacker
// from escaping the worker's workdir via absolute-path tricks.
// claude.ai's /mnt/skills mount pattern depends on this jail; if
// this regresses, a model that tricks Read into "/etc/passwd"
// escapes the sandbox.
func TestResolveInside_TrapsAbsolutePaths(t *testing.T) {
	jailed := resolveInside("/workspace", "/etc/passwd")
	if jailed != "/workspace/etc/passwd" {
		t.Errorf("absolute path not jailed: got %q, want /workspace/etc/passwd", jailed)
	}

	rel := resolveInside("/workspace", "src/main.go")
	if rel != "/workspace/src/main.go" {
		t.Errorf("relative path resolution = %q, want /workspace/src/main.go", rel)
	}
}

func TestHandleStat_NonexistentReturnsExistsFalse(t *testing.T) {
	workdir := t.TempDir()
	body, status, err := handleStat(
		StatRequest{Path: "ghost.txt"},
		ServerOptions{Workdir: workdir})
	if err != nil || status != 0 {
		t.Fatalf("handleStat: status=%d err=%v", status, err)
	}
	var resp StatResponse
	_ = json.Unmarshal(body, &resp)
	if resp.Exists {
		t.Error("ghost file should not exist")
	}
}

func TestHandleWrite_CreateModeRefusesExisting(t *testing.T) {
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "exists.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, status, err := handleWrite(
		WriteRequest{Path: "exists.txt", Content: "new", Mode: "create"},
		ServerOptions{Workdir: workdir})
	if err == nil {
		t.Fatal("expected error for create-mode on existing file")
	}
	if status != 1 {
		t.Errorf("status = %d, want 1 (caller error)", status)
	}
}
