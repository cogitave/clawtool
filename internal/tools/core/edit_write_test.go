package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ── Edit ─────────────────────────────────────────────────────────────────

func TestEdit_UniqueOccurrenceReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("hello world\nbye world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := executeEdit(path, "hello", "HELLO", false)
	if res.ErrorReason != "" {
		t.Fatalf("unexpected error: %s", res.ErrorReason)
	}
	if !res.Replaced || res.OccurrencesReplaced != 1 {
		t.Errorf("got replaced=%v occ=%d, want true/1", res.Replaced, res.OccurrencesReplaced)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "HELLO world\nbye world\n" {
		t.Errorf("content = %q, want HELLO swapped in", got)
	}
}

func TestEdit_RefusesAmbiguous(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(path, []byte("dup\nDup\ndup\n"), 0o644)

	res := executeEdit(path, "dup", "X", false)
	if res.Replaced {
		t.Error("must not replace ambiguous match")
	}
	if !strings.Contains(res.ErrorReason, "appears 2 times") {
		t.Errorf("error should report duplicate count, got: %q", res.ErrorReason)
	}
}

func TestEdit_ReplaceAllOptIn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(path, []byte("aaa bbb aaa ccc aaa\n"), 0o644)

	res := executeEdit(path, "aaa", "Z", true)
	if !res.Replaced || res.OccurrencesReplaced != 3 {
		t.Errorf("got replaced=%v occ=%d, want true/3", res.Replaced, res.OccurrencesReplaced)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "Z bbb Z ccc Z\n" {
		t.Errorf("content = %q", got)
	}
}

func TestEdit_NoMatchErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(path, []byte("foo\n"), 0o644)
	res := executeEdit(path, "zzz", "x", false)
	if res.Replaced {
		t.Error("expected no replacement when match absent")
	}
	if !strings.Contains(res.ErrorReason, "not found") {
		t.Errorf("error should explain absence: %q", res.ErrorReason)
	}
}

func TestEdit_RefusesDirectory(t *testing.T) {
	dir := t.TempDir()
	res := executeEdit(dir, "x", "y", false)
	if !strings.Contains(res.ErrorReason, "directory") {
		t.Errorf("expected directory rejection, got %q", res.ErrorReason)
	}
}

func TestEdit_RefusesBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	_ = os.WriteFile(path, []byte{'a', 0x00, 'b'}, 0o644)
	res := executeEdit(path, "a", "x", false)
	if !strings.Contains(res.ErrorReason, "binary") {
		t.Errorf("expected binary refusal, got %q", res.ErrorReason)
	}
}

func TestEdit_NoOpReplacementErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(path, []byte("x\n"), 0o644)
	res := executeEdit(path, "x", "x", false)
	if !strings.Contains(res.ErrorReason, "no change") {
		t.Errorf("expected no-op error, got %q", res.ErrorReason)
	}
}

func TestEdit_PreservesCRLF(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "win.txt")
	_ = os.WriteFile(path, []byte("hello\r\nworld\r\n"), 0o644)

	res := executeEdit(path, "hello", "HI", false)
	if res.ErrorReason != "" {
		t.Fatalf("error: %s", res.ErrorReason)
	}
	if res.LineEndings != "crlf" {
		t.Errorf("line_endings = %q, want crlf", res.LineEndings)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "HI\r\nworld\r\n" {
		t.Errorf("content = %q, CRLF lost during edit", got)
	}
}

// ── Write ────────────────────────────────────────────────────────────────

func TestWrite_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")
	res := executeWrite(path, "fresh\n", true, true, "")
	if res.ErrorReason != "" {
		t.Fatalf("error: %s", res.ErrorReason)
	}
	if !res.Created {
		t.Error("created should be true for fresh file")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "fresh\n" {
		t.Errorf("content = %q, want fresh\\n", got)
	}
	if res.LineEndings != "lf" {
		t.Errorf("line_endings = %q, want lf for new file", res.LineEndings)
	}
}

func TestWrite_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.txt")
	_ = os.WriteFile(path, []byte("old\n"), 0o644)
	res := executeWrite(path, "new\n", true, true, "")
	if res.Created {
		t.Error("created should be false for overwrite")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new\n" {
		t.Errorf("content = %q, want new\\n", got)
	}
}

func TestWrite_AutoCreateParents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "file.txt")
	res := executeWrite(path, "x\n", true, true, "")
	if res.ErrorReason != "" {
		t.Fatalf("error: %s", res.ErrorReason)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}

func TestWrite_RefusesParentWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing", "f.txt")
	res := executeWrite(path, "x", false, true, "")
	if res.ErrorReason == "" || !strings.Contains(res.ErrorReason, "create_parents") {
		t.Errorf("expected parent-missing error, got: %q", res.ErrorReason)
	}
}

func TestWrite_PreservesCRLFOnOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "win.txt")
	_ = os.WriteFile(path, []byte("a\r\nb\r\n"), 0o644)

	res := executeWrite(path, "x\ny\n", true, true, "")
	if res.LineEndings != "crlf" {
		t.Errorf("line_endings = %q, want crlf (preserved)", res.LineEndings)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "x\r\ny\r\n" {
		t.Errorf("content = %q, CRLF not re-applied", got)
	}
}

func TestWrite_ForcedLineEndings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	_ = os.WriteFile(path, []byte("a\nb\n"), 0o644)

	res := executeWrite(path, "x\ny\n", true, true, LineEndings("crlf"))
	if res.ErrorReason != "" {
		t.Fatalf("error: %s", res.ErrorReason)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "x\r\ny\r\n" {
		t.Errorf("content = %q, want crlf-applied via override", got)
	}
}

func TestWrite_InvalidLineEndingsErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	res := executeWrite(path, "x", true, true, LineEndings("nope"))
	if !strings.Contains(res.ErrorReason, "invalid line_endings") {
		t.Errorf("expected invalid endings error, got %q", res.ErrorReason)
	}
}

func TestWrite_RefusesDirectoryPath(t *testing.T) {
	dir := t.TempDir()
	res := executeWrite(dir, "x", true, true, "")
	if !strings.Contains(res.ErrorReason, "directory") {
		t.Errorf("expected directory rejection, got %q", res.ErrorReason)
	}
}

func TestWrite_PreservesUTF8BOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bom.txt")
	bom := []byte{0xEF, 0xBB, 0xBF}
	_ = os.WriteFile(path, append(bom, []byte("old\n")...), 0o644)

	res := executeWrite(path, "new\n", true, true, "")
	if res.ErrorReason != "" {
		t.Fatalf("error: %s", res.ErrorReason)
	}
	got, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(got), string(bom)) {
		t.Errorf("BOM lost on overwrite: %q", got)
	}
	if !strings.Contains(string(got), "new\n") {
		t.Errorf("new content missing: %q", got)
	}
}

// ── Atomic helpers ───────────────────────────────────────────────────────

func TestDetectLineEndings(t *testing.T) {
	cases := map[string]LineEndings{
		"a\r\nb\r\n":     LineEndingsCRLF,
		"a\nb\n":         LineEndingsLF,
		"a\rb\r":         LineEndingsCR,
		"plain":          LineEndingsLF,
		"":               LineEndingsUnknown,
		"mix\r\nthen\rx": LineEndingsCRLF,
	}
	for in, want := range cases {
		if got := detectLineEndings([]byte(in)); got != want {
			t.Errorf("detectLineEndings(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectBOM(t *testing.T) {
	utf8 := []byte{0xEF, 0xBB, 0xBF, 'a', 'b'}
	bom, body := detectBOM(utf8)
	if string(bom) != "\xef\xbb\xbf" {
		t.Errorf("UTF-8 BOM not detected: %x", bom)
	}
	if string(body) != "ab" {
		t.Errorf("body without BOM = %q, want ab", body)
	}

	plain := []byte("noBOM")
	bom2, body2 := detectBOM(plain)
	if len(bom2) != 0 {
		t.Errorf("expected no BOM, got %x", bom2)
	}
	if string(body2) != "noBOM" {
		t.Errorf("body changed = %q", body2)
	}
}

// ── Edit Read-before-Write guard (ADR-021) ───────────────────────────────
//
// These tests exercise runEdit through the MCP request shape so the
// parity guarantee with Write is verified end-to-end. Mirrors the
// TestGuardReadBeforeWrite_* matrix in session_state_test.go.

// mkEditReq fabricates an MCP CallToolRequest for the Edit tool.
func mkEditReq(path, oldStr, newStr string, unsafe bool) mcp.CallToolRequest {
	args := map[string]any{
		"path":       path,
		"old_string": oldStr,
		"new_string": newStr,
	}
	if unsafe {
		args["unsafe_overwrite_without_read"] = true
	}
	return mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "Edit",
			Arguments: args,
		},
	}
}

// editResultOf extracts the EditResult out of an mcp.CallToolResult
// without poking at the JSON envelope shape.
func editResultOf(t *testing.T, res *mcp.CallToolResult) EditResult {
	t.Helper()
	if res == nil {
		t.Fatal("nil CallToolResult")
	}
	got, ok := res.StructuredContent.(EditResult)
	if !ok {
		t.Fatalf("StructuredContent is %T, want EditResult", res.StructuredContent)
	}
	return got
}

func TestEdit_GuardReadBeforeWrite_RejectsUnreadFile(t *testing.T) {
	ResetSessionsForTest()
	t.Cleanup(ResetSessionsForTest)

	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := runEdit(context.Background(), mkEditReq(path, "hello", "HI", false))
	if err != nil {
		t.Fatalf("runEdit returned err: %v", err)
	}
	got := editResultOf(t, res)
	if got.ErrorReason == "" {
		t.Fatal("expected guard rejection, got success")
	}
	if !strings.Contains(got.ErrorReason, "has not Read") {
		t.Errorf("expected Read-before-Write guard error, got: %q", got.ErrorReason)
	}
	// File must remain untouched.
	body, _ := os.ReadFile(path)
	if string(body) != "hello world\n" {
		t.Errorf("guarded edit still mutated file: %q", body)
	}
}

func TestEdit_GuardReadBeforeWrite_AllowsAfterRead(t *testing.T) {
	ResetSessionsForTest()
	t.Cleanup(ResetSessionsForTest)

	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := HashFile(path)
	if err != nil {
		t.Fatal(err)
	}
	Sessions.RecordRead(sessionAnonymous, ReadRecord{
		Path:     path,
		FileHash: hash,
		ReadAt:   time.Now(),
	})

	res, err := runEdit(context.Background(), mkEditReq(path, "hello", "HI", false))
	if err != nil {
		t.Fatalf("runEdit returned err: %v", err)
	}
	got := editResultOf(t, res)
	if got.ErrorReason != "" {
		t.Fatalf("unexpected guard rejection after Read: %q", got.ErrorReason)
	}
	if !got.Replaced {
		t.Error("expected Replaced=true after recorded Read")
	}
	body, _ := os.ReadFile(path)
	if string(body) != "HI world\n" {
		t.Errorf("post-edit content = %q, want HI world\\n", body)
	}
}

func TestEdit_GuardReadBeforeWrite_UnsafeBypass(t *testing.T) {
	ResetSessionsForTest()
	t.Cleanup(ResetSessionsForTest)

	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// No prior Read recorded — but unsafe=true should allow the
	// edit to proceed.
	res, err := runEdit(context.Background(), mkEditReq(path, "hello", "HI", true))
	if err != nil {
		t.Fatalf("runEdit returned err: %v", err)
	}
	got := editResultOf(t, res)
	if got.ErrorReason != "" {
		t.Fatalf("unsafe_overwrite_without_read should bypass, got: %q", got.ErrorReason)
	}
	if !got.Replaced {
		t.Error("expected Replaced=true with unsafe bypass")
	}
	body, _ := os.ReadFile(path)
	if string(body) != "HI world\n" {
		t.Errorf("post-edit content = %q, want HI world\\n", body)
	}
}

func TestEdit_GuardReadBeforeWrite_RejectsStaleRead(t *testing.T) {
	ResetSessionsForTest()
	t.Cleanup(ResetSessionsForTest)

	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Record a Read with a hash that doesn't match the current file.
	Sessions.RecordRead(sessionAnonymous, ReadRecord{
		Path:     path,
		FileHash: "stale-hash-not-matching",
		ReadAt:   time.Now(),
	})

	res, err := runEdit(context.Background(), mkEditReq(path, "hello", "HI", false))
	if err != nil {
		t.Fatalf("runEdit returned err: %v", err)
	}
	got := editResultOf(t, res)
	if got.ErrorReason == "" {
		t.Fatal("expected stale-hash rejection, got success")
	}
	if !strings.Contains(got.ErrorReason, "changed since this session") {
		t.Errorf("expected stale-hash guard error, got: %q", got.ErrorReason)
	}
}
