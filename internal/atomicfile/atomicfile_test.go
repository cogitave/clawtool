package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFile_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q, want %q", got, "hello")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestWriteFile_PreservesExistingMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "preserve.txt")
	if err := os.WriteFile(path, []byte("v1"), 0o640); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := WriteFile(path, []byte("v2"), 0); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640 (preserved)", info.Mode().Perm())
	}
}

func TestWriteFile_AtomicReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "replace.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := WriteFile(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Fatalf("content = %q, want %q", got, "new")
	}
	// No temp file left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" || filepath.Base(e.Name())[0] == '.' {
			t.Fatalf("leaked temp file: %s", e.Name())
		}
	}
}

func TestWriteFile_EmptyPath(t *testing.T) {
	if err := WriteFile("", []byte("x"), 0o600); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestWriteFileMkdir_CreatesParents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "leaf.txt")
	if err := WriteFileMkdir(path, []byte("deep"), 0o600, 0o700); err != nil {
		t.Fatalf("WriteFileMkdir: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "deep" {
		t.Fatalf("content = %q, want %q", got, "deep")
	}
	parent, _ := os.Stat(filepath.Dir(path))
	if parent.Mode().Perm() != 0o700 {
		t.Fatalf("parent dir mode = %v, want 0700", parent.Mode().Perm())
	}
}
