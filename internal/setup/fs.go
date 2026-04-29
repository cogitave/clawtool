package setup

import (
	"bytes"
	"errors"
	"os"

	"github.com/cogitave/clawtool/internal/atomicfile"
)

// WriteAtomic writes content to path via temp+rename so a crash mid-
// write never leaves the user with a half-finished file. Recipes use
// this for every file mutation; mode is typically 0o644 for repo
// files, 0o755 for scripts. Thin wrapper over atomicfile.WriteFileMkdir
// so all 94 recipe callsites share the project-wide canonical helper —
// one place to tune crash-window invariants going forward.
func WriteAtomic(path string, content []byte, mode os.FileMode) error {
	return atomicfile.WriteFileMkdir(path, content, mode, 0o755)
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
