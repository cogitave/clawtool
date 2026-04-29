package core

import (
	"os/exec"
	"sync"
)

// Engine describes one detected upstream binary that clawtool wraps.
//
// Per ADR-007, clawtool curates and wraps best-in-class engines. Detection
// runs once on startup so wrapper tools can pick the best available engine
// without re-shelling-out for every call.
type Engine struct {
	Name string // canonical name, e.g. "ripgrep"
	Bin  string // resolved absolute path, empty if absent
}

var (
	engineOnce  sync.Once
	engineCache map[string]Engine
)

// detectEngines runs once and caches results. Tests can call ResetEngineCache
// for isolation.
func detectEngines() {
	engineCache = map[string]Engine{}
	for _, name := range []string{"rg", "grep", "pdftotext", "pandoc", "obscura"} {
		if path, err := exec.LookPath(name); err == nil {
			engineCache[name] = Engine{Name: name, Bin: path}
		} else {
			engineCache[name] = Engine{Name: name, Bin: ""}
		}
	}
}

// LookupEngine returns the cached engine entry for a given binary name.
// The Bin field is empty if the engine is not present on this system.
func LookupEngine(name string) Engine {
	engineOnce.Do(detectEngines)
	return engineCache[name]
}

// ResetEngineCache forces a re-detection on next LookupEngine call. Used by
// tests that manipulate $PATH.

