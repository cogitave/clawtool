// Package rules — TOML loader. Reads .clawtool/rules.toml (or a
// caller-supplied path) into a []Rule slice. Validation runs at
// load time so a malformed rule file fails fast with a line
// reference rather than silently dropping rules at evaluation
// time.
//
// Default lookup order matches the rest of clawtool's project-
// scope conventions (skill discovery, sandbox profile resolve):
//   1. ./.clawtool/rules.toml (project-local, highest precedence)
//   2. ~/.config/clawtool/rules.toml (user-global, XDG)
// First match wins; we don't merge across roots.

package rules

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// File is the on-disk shape — the [[rule]] array hosts the actual
// rules; future top-level metadata (version, comment) goes here.
type File struct {
	Rule []Rule `toml:"rule"`
}

// Load reads the TOML file at path, validates each rule, and
// pre-parses each condition so Evaluate doesn't re-parse on every
// fire.
func Load(path string) ([]Rule, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseBytes(body)
}

// ParseBytes is the test seam — same as Load but takes the body
// directly. Useful for ad-hoc rule strings in tests.
func ParseBytes(body []byte) ([]Rule, error) {
	var f File
	if err := toml.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("rules: parse toml: %w", err)
	}
	for i := range f.Rule {
		if f.Rule[i].Severity == "" {
			f.Rule[i].Severity = SeverityWarn
		}
	}
	for i, r := range f.Rule {
		if err := validateRule(r); err != nil {
			return nil, fmt.Errorf("rules: rule[%d] %q: %w", i, r.Name, err)
		}
		parsed, err := parseExpr(r.Condition)
		if err != nil {
			return nil, fmt.Errorf("rules: rule[%d] %q condition: %w", i, r.Name, err)
		}
		f.Rule[i].parsed = parsed
	}
	return f.Rule, nil
}

func validateRule(r Rule) error {
	if strings.TrimSpace(r.Name) == "" {
		return errors.New("name is required")
	}
	if !IsValidEvent(r.When) {
		return fmt.Errorf("invalid 'when': %q (allowed: pre_commit, post_edit, session_end, pre_send, pre_unattended)", r.When)
	}
	if !IsValidSeverity(r.Severity) {
		return fmt.Errorf("invalid 'severity': %q (allowed: off, warn, block)", r.Severity)
	}
	if strings.TrimSpace(r.Condition) == "" {
		return errors.New("condition is required")
	}
	return nil
}

// DefaultRoots returns the search roots for rules.toml. Project-
// local takes precedence over user-global, same convention skill /
// sandbox discovery uses.
func DefaultRoots() []string {
	roots := []string{filepath.Join(".clawtool", "rules.toml")}
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		roots = append(roots, filepath.Join(x, "clawtool", "rules.toml"))
	} else if home, err := os.UserHomeDir(); err == nil && home != "" {
		roots = append(roots, filepath.Join(home, ".config", "clawtool", "rules.toml"))
	}
	return roots
}

// LoadDefault tries each root in DefaultRoots order; returns the
// first that exists. ok=false when no rules file is configured;
// callers should treat that as "no rules to enforce" (clawtool's
// default mode is permissive — rules are opt-in).
func LoadDefault() ([]Rule, string, bool, error) {
	for _, p := range DefaultRoots() {
		if _, err := os.Stat(p); err == nil {
			rules, err := Load(p)
			if err != nil {
				return nil, p, true, err
			}
			return rules, p, true, nil
		}
	}
	return nil, "", false, nil
}
