package a2a

import "sync"

// Process-global registry handle. The HTTP daemon constructs one
// at boot and exposes it to tools / CLI verbs that need a read
// (the in-process MCP shortcut path) without having to thread the
// registry through every signature. Daemon teardown clears the
// pointer so a stale Get() doesn't return a closed registry.
//
// Concurrent Get / Set guarded by a small RWMutex; the registry
// itself has its own sync.RWMutex for table mutations, so two
// layers of locking are both load-bearing.
var (
	globalMu sync.RWMutex
	global   *Registry
)

// SetGlobal installs the process-wide registry. Caller — typically
// internal/server/server.go's buildMCPServer — does this once at
// boot. Passing nil clears it (used by daemon shutdown).
func SetGlobal(r *Registry) {
	globalMu.Lock()
	defer globalMu.Unlock()
	global = r
}

// GetGlobal returns the process-wide registry, or nil when no
// daemon has set one. Read-side callers (CLI tools, MCP handlers)
// should nil-check; the returned value is safe for concurrent
// access.
func GetGlobal() *Registry {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global
}
