// Package xdg — single source of truth for XDG Base Directory
// resolution. Pre-this package, ~17 call sites reimplemented the
// same fallback chain (XDG_X_HOME → ~/.{config,local/state,…} →
// last-ditch literal). Drift was real: secrets used the long form,
// daemon had a private configDir(), tools/core inlined yet another
// variant. Audit on 2026-04-29 collected them under one roof so
// the next operator who needs $XDG_RUNTIME_DIR doesn't add an
// 18th flavour.
//
// All four helpers honour the spec's escape hatch: when the env
// var is set AND non-empty, it wins outright; otherwise we fall
// back to $HOME/<spec-default>; if $HOME isn't resolvable either
// (containers, hermetic test sandboxes) the last-ditch literal
// keeps callers from panicking on a startup race.
//
// Naming: ConfigDir / StateDir / DataDir / CacheDir return the
// per-app subdirectory ("clawtool"); the bare X_HOME variants are
// not exported because no caller wants the raw user-level dir.
package xdg

import (
	"os"
	"path/filepath"
)

// appName is the per-app subdirectory every helper appends. Kept
// private so callers can't shadow the canonical "clawtool" prefix
// with a one-off (auditor's nightmare: half the code under
// /clawtool/, half under /clawtools/).
const appName = "clawtool"

// ConfigDir returns ~/.config/clawtool (XDG-aware). Used for
// config.toml, daemon.json, listener-token, peers.json, etc. —
// state that survives across runs and the operator may want to
// `git add .config/clawtool`.
func ConfigDir() string {
	return resolve("XDG_CONFIG_HOME", ".config")
}

// StateDir returns ~/.local/state/clawtool (XDG-aware). Used for
// daemon.log, task-watch.sock, the BIAM SQLite file — state that's
// runtime-volatile and the operator should NOT version-control.
func StateDir() string {
	return resolve("XDG_STATE_HOME", filepath.Join(".local", "state"))
}

// DataDir returns ~/.local/share/clawtool (XDG-aware). Used for
// data the app generates that survives but isn't config (telemetry
// state, cache snapshots that benefit from persistence).
func DataDir() string {
	return resolve("XDG_DATA_HOME", filepath.Join(".local", "share"))
}

// CacheDir returns ~/.cache/clawtool (XDG-aware). Used for
// regenerable artifacts: download caches, worktree scratch,
// embedding indexes. Anything here can be deleted without
// breaking the next run.
func CacheDir() string {
	return resolve("XDG_CACHE_HOME", ".cache")
}

// resolve is the shared fallback chain. Empty env var falls
// through to $HOME/<defaultRel>/clawtool. Empty home falls
// through to <defaultRel>/clawtool relative to cwd — keeps
// init-time code from panicking when neither is set (rare:
// minimal Docker bases without /etc/passwd).
func resolve(envKey, defaultRel string) string {
	if v := os.Getenv(envKey); v != "" {
		return filepath.Join(v, appName)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, defaultRel, appName)
	}
	return filepath.Join(defaultRel, appName)
}

// CacheDirOrTemp returns CacheDir() when $XDG_CACHE_HOME or $HOME
// is resolvable, else falls back to filepath.Join(os.TempDir(),
// "clawtool"). Differs from CacheDir() only in the last-ditch fallback:
// CacheDir returns the cwd-relative literal "clawtool/" (callers
// inside the project tree get a real but surprising path);
// CacheDirOrTemp routes to /tmp where the path is at least
// world-writeable + non-colliding-with-source.
//
// Used by code paths that need a real, writeable, non-shared
// directory even on hosts without $HOME — worktrees (rare on
// production hosts but common in CI), update cache (shipped via
// scratch CI runners). Callers append their own leaf via
// filepath.Join — this only resolves the per-app root.
func CacheDirOrTemp() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return filepath.Join(v, appName)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".cache", appName)
	}
	return filepath.Join(os.TempDir(), appName)
}

// ConfigDirIfHome / DataDirIfHome / CacheDirIfHome return the
// per-app directory when $XDG_X_HOME or $HOME is resolvable,
// else return the empty string. The empty-sentinel signals
// "skip this path" — uninstall and other cleanup walkers iterate
// candidate directories and need to avoid stepping on cwd-relative
// literals, which would let `clawtool uninstall` walk into a
// stray ./clawtool directory in the operator's project tree.
//
// Use these instead of ConfigDir / DataDir / CacheDir whenever the
// caller would prefer to skip the path entirely over scanning a
// surprise cwd-relative match. Production callers that always
// want a real path (state writes, log files, identity) should
// keep using the literal-fallback variants.
func ConfigDirIfHome() string { return resolveIfHome("XDG_CONFIG_HOME", ".config") }
func DataDirIfHome() string {
	return resolveIfHome("XDG_DATA_HOME", filepath.Join(".local", "share"))
}
func CacheDirIfHome() string { return resolveIfHome("XDG_CACHE_HOME", ".cache") }

func resolveIfHome(envKey, defaultRel string) string {
	if v := os.Getenv(envKey); v != "" {
		return filepath.Join(v, appName)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, defaultRel, appName)
	}
	return ""
}
