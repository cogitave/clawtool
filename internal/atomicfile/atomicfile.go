// Package atomicfile — one canonical primitive for "write a file
// without leaving a half-written artifact on crash". Used by config
// stores, daemon state, agent identity, a2a inbox, secrets — every
// place where a partial write at the target path would corrupt
// downstream consumers.
//
// Strategy: write to a unique temp file in the *same directory* as
// the target, then rename(2). Same-filesystem rename is atomic on
// every platform clawtool supports — readers see either the old
// file or the new file, never a torn intermediate.
//
// We deliberately do not use a third-party "atomic write" library
// (per the project's design call): stdlib gives us the right
// guarantees when the temp lives in the target's directory.
package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile writes content to path via temp+rename.
//
// mode controls the final file permission. Pass 0 to preserve the
// existing file's mode (or fall back to 0o644 for a brand-new path).
//
// The caller is responsible for any parent-directory creation —
// MkdirAll-and-write doubles up too often (caller already knows the
// scope, e.g. 0o700 for ~/.config dirs vs 0o755 for repo dirs).
// Use WriteFileMkdir when the parent directory may not exist.
func WriteFile(path string, content []byte, mode os.FileMode) error {
	return write(path, content, mode, false, 0)
}

// WriteFileMkdir is WriteFile + MkdirAll(parent, dirMode) up front.
// Use when callers know the parent directory may be missing (most
// $XDG_CONFIG_HOME state files on first run).
func WriteFileMkdir(path string, content []byte, mode os.FileMode, dirMode os.FileMode) error {
	if dirMode == 0 {
		dirMode = 0o755
	}
	return write(path, content, mode, true, dirMode)
}

func write(path string, content []byte, mode os.FileMode, mkdir bool, dirMode os.FileMode) error {
	if path == "" {
		return errors.New("atomicfile: empty path")
	}
	dir := filepath.Dir(path)
	if mkdir {
		if err := os.MkdirAll(dir, dirMode); err != nil {
			return fmt.Errorf("atomicfile: mkdir %s: %w", dir, err)
		}
	}
	if mode == 0 {
		mode = 0o644
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
	}

	tmp, err := os.CreateTemp(dir, ".clawtool-atomic-*")
	if err != nil {
		return fmt.Errorf("atomicfile: create temp in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("atomicfile: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("atomicfile: close temp: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("atomicfile: chmod temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("atomicfile: rename %s -> %s: %w", tmpPath, path, err)
	}
	cleanup = false
	return nil
}
