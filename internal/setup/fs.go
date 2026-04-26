package setup

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteAtomic writes content to path via temp+rename so a crash mid-
// write never leaves the user with a half-finished file. Recipes use
// this for every file mutation; mode is typically 0o644 for repo
// files, 0o755 for scripts.
func WriteAtomic(path string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir parent of %s: %w", path, err)
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, content, mode); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// FileExists is the boolean predicate. Returns (false, err) on
// fs errors that aren't IsNotExist so callers can surface them.
func FileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// ReadIfExists returns nil bytes + nil error when the file is
// absent. Recipes use this in Detect to fingerprint existing
// content without juggling not-found errors.
func ReadIfExists(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return b, nil
}

// HasMarker reports whether content includes the given marker
// substring. Recipes use this to label generated files
// ("managed-by: clawtool") and refuse to overwrite anything
// without the marker.
func HasMarker(content []byte, marker string) bool {
	return bytes.Contains(content, []byte(marker))
}

// ManagedByMarker is the canonical marker every clawtool-generated
// file embeds (typically as a YAML/TOML/HTML comment). Detect and
// Apply both check for it before refusing to touch unmanaged files.
const ManagedByMarker = "managed-by: clawtool"
