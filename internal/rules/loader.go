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

// findProjectRulesPath walks UP from the process working
// directory looking for an existing `.clawtool/rules.toml`,
// stopping at the filesystem root or 12 levels (whichever first).
// Returns "" when no ancestor has the file. Used by both
// DefaultRoots (read path) and LocalRulesPath (write path) so
// RulesCheck and RulesAdd target the same file no matter where
// the daemon was spawned from. Pre-fix DefaultRoots was cwd-only
// (RulesCheck returned `configured: false`) and LocalRulesPath
// was cwd-relative (RulesAdd silently wrote to the daemon's
// working directory's `.clawtool/rules.toml`, often $HOME).
func findProjectRulesPath() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := cwd
	for i := 0; i < 12; i++ {
		candidate := filepath.Join(dir, ".clawtool", "rules.toml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

// DefaultRoots returns the search roots for rules.toml. Project-
// local (walked up from cwd) takes precedence over user-global,
// same convention skill / sandbox discovery uses.
func DefaultRoots() []string {
	roots := []string{}
	if walked := findProjectRulesPath(); walked != "" {
		roots = append(roots, walked)
	}
	// Always include the relative form too — covers the case
	// where cwd resolution failed or the operator runs from a
	// non-walkable mount.
	roots = append(roots, filepath.Join(".clawtool", "rules.toml"))
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

// LocalRulesPath returns the project-scoped rules path. Prefers
// an existing `.clawtool/rules.toml` walked up from cwd (so
// RulesAdd from anywhere inside the project lands in the right
// file); falls back to creating one in the literal cwd when no
// ancestor is found (first rule in a fresh project).
func LocalRulesPath() string {
	if walked := findProjectRulesPath(); walked != "" {
		return walked
	}
	return filepath.Join(".clawtool", "rules.toml")
}

// UserRulesPath returns the user-scoped rules path:
// $XDG_CONFIG_HOME/clawtool/rules.toml (or ~/.config/...).
func UserRulesPath() string {
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(x, "clawtool", "rules.toml")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "clawtool", "rules.toml")
	}
	return filepath.Join("clawtool", "rules.toml")
}

// AppendRule writes one new rule to the file at path, creating
// the file (and parent dirs) when missing. Validates the rule's
// shape and condition syntax BEFORE persisting so a malformed
// add never corrupts the existing rules. Returns ErrDuplicate
// when a rule with the same Name already exists in the file.
func AppendRule(path string, r Rule) error {
	if err := validateRule(r); err != nil {
		return fmt.Errorf("rules: append %q: %w", r.Name, err)
	}
	if _, err := parseExpr(r.Condition); err != nil {
		return fmt.Errorf("rules: append %q condition: %w", r.Name, err)
	}
	// Read existing rules (if any) — we'll re-emit them all so
	// the file stays in canonical TOML shape (no dangling
	// fragments from hand-edits, ordering preserved).
	var existing []Rule
	if body, err := os.ReadFile(path); err == nil {
		existing, err = ParseBytes(body)
		if err != nil {
			return fmt.Errorf("rules: parse existing %s: %w", path, err)
		}
	}
	for _, e := range existing {
		if e.Name == r.Name {
			return fmt.Errorf("rules: append: rule %q already exists in %s", r.Name, path)
		}
	}
	all := append(existing, r)
	return saveRules(path, all)
}

// RemoveRule deletes the named rule from the file at path. Returns
// ok=false when no rule with that name exists; the file stays
// untouched.
func RemoveRule(path, name string) (bool, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	existing, err := ParseBytes(body)
	if err != nil {
		return false, fmt.Errorf("rules: parse %s: %w", path, err)
	}
	out := existing[:0]
	found := false
	for _, e := range existing {
		if e.Name == name {
			found = true
			continue
		}
		out = append(out, e)
	}
	if !found {
		return false, nil
	}
	return true, saveRules(path, out)
}

// saveRules emits the canonical TOML representation. Each rule
// becomes one [[rule]] block with name / description / when /
// condition / severity / hint fields written in a stable order.
// We hand-roll the writer to avoid pulling in a TOML encoder
// dependency just for one shape.
func saveRules(path string, rs []Rule) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("rules: mkdir %s: %w", filepath.Dir(path), err)
	}
	var b strings.Builder
	b.WriteString("# clawtool rules — predicate-based invariants enforced at\n")
	b.WriteString("# lifecycle events (pre_commit, post_edit, session_end,\n")
	b.WriteString("# pre_send, pre_unattended). See docs/rules.md for the schema.\n\n")
	for i, r := range rs {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("[[rule]]\n")
		fmt.Fprintf(&b, "name      = %q\n", r.Name)
		if r.Description != "" {
			fmt.Fprintf(&b, "description = %q\n", r.Description)
		}
		fmt.Fprintf(&b, "when      = %q\n", string(r.When))
		fmt.Fprintf(&b, "condition = %q\n", r.Condition)
		fmt.Fprintf(&b, "severity  = %q\n", string(r.Severity))
		if r.Hint != "" {
			fmt.Fprintf(&b, "hint      = %q\n", r.Hint)
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
