// Package core — atomic.go: shared file-mutation primitives for Edit and
// Write. These helpers guarantee no partial-write artifacts on crash and
// give us a single place to add safety polish (line-ending preservation,
// BOM handling) consistently across both tools.
//
// Per ADR-007 we do not use a third-party "atomic write" library — Go's
// stdlib (`os.WriteFile` + `os.Rename`) is enough when used correctly.
// The platform invariant we rely on: rename(2) on the same filesystem is
// atomic, so writers never see a half-written file at the target path.
package core

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LineEndings identifies the dominant line-ending convention of a file.
type LineEndings string

const (
	LineEndingsLF      LineEndings = "lf"
	LineEndingsCRLF    LineEndings = "crlf"
	LineEndingsCR      LineEndings = "cr" // ancient Mac
	LineEndingsUnknown LineEndings = "unknown"
)

// detectLineEndings inspects content (or the first chunk of it) and
// returns the dominant line-ending style. Detection rules:
//   - if "\r\n" appears at all, treat as CRLF (one shouldn't mix)
//   - else if "\r" appears without paired "\n", treat as CR
//   - else default LF
//
// On empty input: returns Unknown so callers can fall back to LF.
func detectLineEndings(b []byte) LineEndings {
	if len(b) == 0 {
		return LineEndingsUnknown
	}
	if bytes.Contains(b, []byte("\r\n")) {
		return LineEndingsCRLF
	}
	if bytes.IndexByte(b, '\r') >= 0 {
		return LineEndingsCR
	}
	return LineEndingsLF
}

// applyLineEndings rewrites raw content to match target. Idempotent: if
// content already matches target, returns the original bytes unchanged.
// Strategy: normalize all variants to LF first, then expand to target.
func applyLineEndings(content []byte, target LineEndings) []byte {
	normalized := bytes.ReplaceAll(content, []byte("\r\n"), []byte("\n"))
	normalized = bytes.ReplaceAll(normalized, []byte("\r"), []byte("\n"))
	switch target {
	case LineEndingsCRLF:
		return bytes.ReplaceAll(normalized, []byte("\n"), []byte("\r\n"))
	case LineEndingsCR:
		return bytes.ReplaceAll(normalized, []byte("\n"), []byte("\r"))
	case LineEndingsLF, LineEndingsUnknown:
		fallthrough
	default:
		return normalized
	}
}

// detectBOM returns any UTF-8 / UTF-16 BOM prefix present at the start of
// b, plus the body without it. clawtool preserves BOMs so re-writing a
// Windows-flavoured file doesn't corrupt downstream consumers.
func detectBOM(b []byte) (bom, body []byte) {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return b[:3], b[3:]
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return b[:2], b[2:]
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return b[:2], b[2:]
	}
	return nil, b
}

// looksBinary returns true when the byte slice contains a NUL byte. Same
// heuristic Read uses; agents shouldn't be writing into files we'd refuse
// to read back. Keeps the safety bar symmetric.
func looksBinary(b []byte) bool {
	return bytes.IndexByte(b, 0x00) >= 0
}

// writeAtomic writes content to path via temp+rename so callers never
// observe a partial file at path.
//
// On success: target file replaced with mode 0644 (or existing mode if
// the path already existed).
//
// We intentionally do NOT cross-filesystem rename — temp file lives in
// the same directory as the target so rename(2) stays a metadata-only
// operation.
func writeAtomic(path string, content []byte) error {
	if path == "" {
		return errors.New("empty path")
	}
	dir := filepath.Dir(path)

	// Preserve existing file mode where possible.
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(dir, ".clawtool-write-*")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmp.Name()
	// Don't leak temp on failure paths.
	cleanupTemp := true
	defer func() {
		if cleanupTemp {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("chmod temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp -> target: %w", err)
	}
	cleanupTemp = false
	return nil
}

// resolvePath joins cwd + path when path is relative. Empty cwd falls
// back to the user's home directory — same convention every other Read /
// Edit / Bash tool uses. Single helper keeps the rule consistent across
// tools.
func resolvePath(path, cwd string) string {
	if filepath.IsAbs(path) {
		return path
	}
	if cwd == "" {
		cwd = homeDir()
	}
	return filepath.Join(cwd, path)
}


