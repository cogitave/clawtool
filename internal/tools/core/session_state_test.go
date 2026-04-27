package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHashBytes_Deterministic(t *testing.T) {
	a := hashBytes([]byte("hello world"))
	b := hashBytes([]byte("hello world"))
	if a != b {
		t.Errorf("same input must hash equal: %s vs %s", a, b)
	}
	if len(a) != 64 {
		t.Errorf("SHA-256 hex should be 64 chars, got %d", len(a))
	}
}

func TestHashFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := HashFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if got != hashBytes([]byte("hello")) {
		t.Errorf("HashFile and hashBytes disagree")
	}
}

func TestSessions_RecordAndLookup(t *testing.T) {
	ResetSessionsForTest()
	t.Cleanup(ResetSessionsForTest)

	rec := ReadRecord{
		Path:      "/tmp/foo.txt",
		FileHash:  "abc",
		RangeHash: "def",
		LineStart: 1,
		LineEnd:   10,
		ReadAt:    time.Now(),
	}
	Sessions.RecordRead("session-A", rec)

	got, ok := Sessions.ReadOf("session-A", "/tmp/foo.txt")
	if !ok {
		t.Fatal("expected record to round-trip")
	}
	if got.FileHash != "abc" {
		t.Errorf("FileHash mismatch: %q", got.FileHash)
	}

	if _, ok := Sessions.ReadOf("session-B", "/tmp/foo.txt"); ok {
		t.Error("records must not leak across sessions")
	}
	if _, ok := Sessions.ReadOf("session-A", "/tmp/other"); ok {
		t.Error("records must not leak across paths")
	}
}

func TestSessionKeyFromContext_AnonymousFallback(t *testing.T) {
	// Background ctx has no MCP session attached; we expect the
	// anonymous fallback so unit tests still work end-to-end.
	got := SessionKeyFromContext(context.Background())
	if got != sessionAnonymous {
		t.Errorf("expected anonymous fallback, got %q", got)
	}
}

func TestPrefixLineNumbers(t *testing.T) {
	got := prefixLineNumbers("alpha\nbeta\ngamma\n", 10)
	want := "  10 | alpha\n  11 | beta\n  12 | gamma\n"
	if got != want {
		t.Errorf("\n got %q\nwant %q", got, want)
	}
}

func TestPrefixLineNumbers_NoTrailingNewline(t *testing.T) {
	got := prefixLineNumbers("solo", 1)
	if !strings.Contains(got, "   1 | solo") {
		t.Errorf("got %q", got)
	}
}

func TestGuardReadBeforeWrite_RejectsExistingWithoutRead(t *testing.T) {
	ResetSessionsForTest()
	t.Cleanup(ResetSessionsForTest)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := guardReadBeforeWrite(context.Background(), path, "", false, false)
	if err == nil || !strings.Contains(err.Error(), "has not Read") {
		t.Fatalf("expected Read-before-Write rejection, got %v", err)
	}
}

func TestGuardReadBeforeWrite_AllowsAfterRead(t *testing.T) {
	ResetSessionsForTest()
	t.Cleanup(ResetSessionsForTest)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, _ := HashFile(path)
	Sessions.RecordRead(sessionAnonymous, ReadRecord{
		Path:     path,
		FileHash: hash,
		ReadAt:   time.Now(),
	})
	if err := guardReadBeforeWrite(context.Background(), path, "", false, false); err != nil {
		t.Fatalf("expected pass after recorded Read, got %v", err)
	}
}

func TestGuardReadBeforeWrite_RejectsStaleRead(t *testing.T) {
	ResetSessionsForTest()
	t.Cleanup(ResetSessionsForTest)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	Sessions.RecordRead(sessionAnonymous, ReadRecord{
		Path:     path,
		FileHash: "stale-hash-not-matching",
		ReadAt:   time.Now(),
	})
	err := guardReadBeforeWrite(context.Background(), path, "", false, false)
	if err == nil || !strings.Contains(err.Error(), "changed since this session") {
		t.Fatalf("expected stale-hash rejection, got %v", err)
	}
}

func TestGuardReadBeforeWrite_CreateModeRejectsExisting(t *testing.T) {
	ResetSessionsForTest()
	t.Cleanup(ResetSessionsForTest)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := guardReadBeforeWrite(context.Background(), path, "create", false, false)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected create-mode collision error, got %v", err)
	}
}

func TestGuardReadBeforeWrite_CreateModeAllowsNew(t *testing.T) {
	ResetSessionsForTest()
	t.Cleanup(ResetSessionsForTest)
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")
	if err := guardReadBeforeWrite(context.Background(), path, "create", false, false); err != nil {
		t.Fatalf("create mode should pass for missing path, got %v", err)
	}
}

func TestGuardReadBeforeWrite_UnsafeOverridesGuard(t *testing.T) {
	ResetSessionsForTest()
	t.Cleanup(ResetSessionsForTest)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := guardReadBeforeWrite(context.Background(), path, "", false, true); err != nil {
		t.Fatalf("unsafe_overwrite_without_read=true should bypass, got %v", err)
	}
}
