// Package secrets stores per-source credentials separately from config.toml.
//
// Per ADR-008, secrets live at ~/.config/clawtool/secrets.toml (mode 0600).
// Keeping them out of config.toml means the latter can be safely committed
// to a repo / synced via dotfiles, while credentials stay machine-local.
//
// The store is structured as: scope (= source instance name) → key/value
// map. Scope "global" holds env that applies to every source.
//
// Resolve() interpolates ${VAR} references in a config-supplied env map
// against secrets first, then the process env, returning the env that
// should be set on a spawned source process.
package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Store is the in-memory representation of secrets.toml.
type Store struct {
	Scopes map[string]map[string]string `toml:"scopes,omitempty"`
}

// DefaultPath returns ~/.config/clawtool/secrets.toml (or the XDG variant).
// Mirrors config.DefaultPath but with the secrets.toml filename.
func DefaultPath() string {
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(x, "clawtool", "secrets.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "secrets.toml"
	}
	return filepath.Join(home, ".config", "clawtool", "secrets.toml")
}

// LoadOrEmpty reads the secrets file. A missing file is not an error; we
// return an empty store so callers can Set+Save without first running an
// init step.
func LoadOrEmpty(path string) (*Store, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Store{Scopes: map[string]map[string]string{}}, nil
		}
		return nil, err
	}
	var s Store
	if err := toml.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if s.Scopes == nil {
		s.Scopes = map[string]map[string]string{}
	}
	return &s, nil
}

// Save writes the store to path with mode 0600 (creating the parent dir
// with mode 0700 if necessary). Atomic via temp+rename so a crash never
// leaves a half-written secrets file.
func (s *Store) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}
	b, err := toml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// Set assigns a value to (scope, key). Scope "" maps to "global".
func (s *Store) Set(scope, key, value string) {
	if scope == "" {
		scope = "global"
	}
	if s.Scopes == nil {
		s.Scopes = map[string]map[string]string{}
	}
	if s.Scopes[scope] == nil {
		s.Scopes[scope] = map[string]string{}
	}
	s.Scopes[scope][key] = value
}

// Get returns the value for (scope, key). It checks the requested scope
// first, then the "global" scope, then returns ok=false. This lets the
// user define shared values once in [scopes.global] and override per
// instance only when needed.
func (s *Store) Get(scope, key string) (string, bool) {
	if scope == "" {
		scope = "global"
	}
	if v, ok := s.Scopes[scope][key]; ok {
		return v, true
	}
	if scope != "global" {
		if v, ok := s.Scopes["global"][key]; ok {
			return v, true
		}
	}
	return "", false
}

// Delete removes a key from a scope. Empty scopes are pruned to keep the
// on-disk file tidy.
func (s *Store) Delete(scope, key string) {
	if scope == "" {
		scope = "global"
	}
	delete(s.Scopes[scope], key)
	if len(s.Scopes[scope]) == 0 {
		delete(s.Scopes, scope)
	}
}

// Resolve takes the env map a catalog entry asks for (e.g.
// {GITHUB_TOKEN: "${GITHUB_TOKEN}"}) and returns the env that should be
// set on the spawned source. Each ${VAR} reference is filled in by:
//
//  1. The store at scope+key, then store global+key
//  2. The process env
//  3. Empty string (with `missing` populated so callers can warn)
//
// Plain (non-${...}) values are passed through unchanged.
func (s *Store) Resolve(scope string, template map[string]string) (resolved map[string]string, missing []string) {
	if len(template) == 0 {
		return nil, nil
	}
	resolved = make(map[string]string, len(template))
	for k, raw := range template {
		v, ok := s.expand(scope, raw)
		resolved[k] = v
		if !ok {
			missing = append(missing, k)
		}
	}
	return resolved, missing
}

var refRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Expand resolves every ${VAR} reference in v against the secrets scope
// first, then the "global" scope, then the process env, returning the
// expanded string plus the list of variable names that could not be
// resolved (in encounter order, deduplicated).
//
// A literal without any ${...} substring is returned verbatim with no
// missing entries — this is the hot-path callers depend on so they
// don't pay the regex cost on plain values.
func (s *Store) Expand(scope, v string) (string, []string) {
	if !strings.Contains(v, "${") {
		return v, nil
	}
	var missing []string
	seen := map[string]bool{}
	out := refRE.ReplaceAllStringFunc(v, func(match string) string {
		name := match[2 : len(match)-1]
		if val, ok := s.Get(scope, name); ok {
			return val
		}
		if env := os.Getenv(name); env != "" {
			return env
		}
		if !seen[name] {
			seen[name] = true
			missing = append(missing, name)
		}
		return ""
	})
	return out, missing
}

// expand is the bool-returning helper kept for backwards-compat with the
// internal Resolve flow. New callers should reach for Expand instead.
func (s *Store) expand(scope, v string) (string, bool) {
	out, missing := s.Expand(scope, v)
	return out, len(missing) == 0
}
