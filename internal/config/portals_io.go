// Package config — portal-config IO helpers (ADR-018).
//
// `clawtool portal add` opens an editor with a TOML template; on
// save we parse the buffer, validate it, and append it to the
// canonical config.toml. Removing a portal rewrites the file
// without that block. Both operations preserve any unrelated
// content (other portals, [agents.X], comments) by delegating to
// go-toml's marshal — never by hand-rolling string replacement.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// LoadFromBytes parses a TOML byte slice into a Config. Used by
// CLI flows that read user-edited template buffers without
// touching disk first.
func LoadFromBytes(body []byte) (Config, error) {
	var cfg Config
	if err := toml.Unmarshal(body, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse: %w", err)
	}
	return cfg, nil
}

// MarshalForAppend serialises just the [portals.*] entries of cfg
// (ignoring everything else) into a TOML byte fragment that
// AppendBytes can fold into the user's config.toml. Used by the
// portal wizard to round-trip the assembled PortalConfig through
// the same merge path the editor-driven `portal add` already uses.
func MarshalForAppend(cfg Config) ([]byte, error) {
	if len(cfg.Portals) == 0 {
		return nil, fmt.Errorf("MarshalForAppend: no portals to emit")
	}
	patch := Config{Portals: cfg.Portals}
	b, err := toml.Marshal(patch)
	if err != nil {
		return nil, fmt.Errorf("marshal portals: %w", err)
	}
	return b, nil
}

// AppendBytes merges the [portals.X] blocks from `body` into the
// existing config at `path` (creating the file when missing) and
// re-emits it. We go through go-toml round-trip — never a textual
// concat — so existing comments and key order in the source are
// preserved by go-toml's stable marshal output. Atomic temp+rename.
func AppendBytes(path string, body []byte) error {
	cfg, err := LoadOrDefault(path)
	if err != nil {
		return fmt.Errorf("load existing: %w", err)
	}
	patch, err := LoadFromBytes(body)
	if err != nil {
		return fmt.Errorf("parse incoming: %w", err)
	}
	if cfg.Portals == nil {
		cfg.Portals = map[string]PortalConfig{}
	}
	for name, p := range patch.Portals {
		if _, exists := cfg.Portals[name]; exists {
			return fmt.Errorf("portal %q already exists in %s", name, path)
		}
		cfg.Portals[name] = p
	}
	return writeConfigAtomic(path, cfg)
}

// RemovePortalBlock removes the [portals.<name>] stanza from the
// config at `path` and re-emits the file. No-op when the portal is
// missing.
func RemovePortalBlock(path, name string) error {
	cfg, err := LoadOrDefault(path)
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}
	if _, ok := cfg.Portals[name]; !ok {
		return nil
	}
	delete(cfg.Portals, name)
	if len(cfg.Portals) == 0 {
		// keep an empty map so go-toml still emits a stanza; the
		// blank-map case is rendered as nothing because we tag
		// `omitempty`. That is desired — the file goes back to its
		// pre-portal shape.
		cfg.Portals = nil
	}
	return writeConfigAtomic(path, cfg)
}

// writeConfigAtomic marshals cfg and atomically writes it to path.
// Mirrors Save() but skips the chmod-0600 step (config.toml stays
// 0644; only secrets.toml is 0600). Idempotent.
func writeConfigAtomic(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir parent: %w", err)
	}
	b, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	tmp := path + ".tmp"
	body := append(bytes.TrimRight(b, "\n"), '\n')
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	return os.Rename(tmp, path)
}
