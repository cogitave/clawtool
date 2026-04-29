// Package worker — process-wide singleton client used by tool
// handlers (Bash / Read / Edit / Write) to route through the
// sandbox worker when configured.
//
// The lifecycle: server.go's buildMCPServer reads
// cfg.SandboxWorker at boot, calls SetGlobal once if Mode != "off",
// and tool handlers consult Global() per call. nil global = host
// fallback (legacy behaviour preserved).
package worker

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cogitave/clawtool/internal/xdg"
)

var (
	globalMu sync.RWMutex
	global   *Client
)

// SetGlobal registers the daemon-wide worker client. Pass nil to
// disable. Idempotent.
func SetGlobal(c *Client) {
	globalMu.Lock()
	global = c
	globalMu.Unlock()
}

// Global returns the registered client, or nil when worker mode
// is off / unconfigured. Tool handlers MUST handle nil by falling
// back to host execution — this is the contract that keeps
// `mode=off` backward-compatible.
func Global() *Client {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global
}

// DefaultTokenPath honours XDG conventions for the worker token
// file. Mirrors internal/cli/sandbox_worker.go's helper but
// duplicated here so daemon-side code doesn't import internal/cli
// (would create a cycle).
func DefaultTokenPath() string {
	return filepath.Join(xdg.ConfigDir(), "worker-token")
}

// LoadToken reads the bearer token from path with the same
// trimming rules the worker server uses on its end. Empty file
// or missing file returns ("", error).
func LoadToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", os.ErrInvalid
	}
	return tok, nil
}
