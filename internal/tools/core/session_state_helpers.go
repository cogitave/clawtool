// Package core — file-IO helpers extracted to keep
// session_state.go pure. Mirrors the read.go IO surface but
// stays small enough to embed.
package core

import "os"

// readFileForHash is a tiny indirection so tests can stub the
// disk read. Production reads via os.ReadFile.
var readFileForHash = func(path string) ([]byte, error) {
	return os.ReadFile(path)
}
