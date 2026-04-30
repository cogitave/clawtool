// Package server — playbook layer structure guard.
//
// `playbooks/` is the markdown layer clawtool ships alongside the
// MCP source-server layer (per playbooks/README.md). It carries
// agent-readable recipes for tools that don't have an MCP server
// (or where the operator prefers a CLI / browser auth path over
// a service account).
//
// This test pins the directory layout so a refactor that moves
// or renames the foundation files trips CI loudly. The contract
// is intentionally minimal — just the foundation files. Each
// per-tool playbook ships in its own commit and is verified in
// the upcoming `clawtool playbook list` surface-drift test
// (future tick).
package server

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPlaybooksLayout asserts the foundation files exist at
// their canonical paths. New per-tool playbooks (e.g.
// playbooks/jira/setup.md) don't need a test entry here; the
// rule is that the README + meta + at least one example always
// exist.
func TestPlaybooksLayout(t *testing.T) {
	root := repoRoot(t)
	required := []string{
		"playbooks/README.md",
		"playbooks/add-new-tool.md",
		"playbooks/_personal/.gitkeep",
		// One concrete example so the README isn't pointing at
		// a vapor directory. github is the kickoff playbook.
		"playbooks/github/setup.md",
	}
	for _, rel := range required {
		path := filepath.Join(root, rel)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("required playbook layout file missing: %s — %v", rel, err)
			continue
		}
		if info.IsDir() {
			t.Errorf("expected file at %s, got directory", rel)
		}
		if info.Size() == 0 {
			t.Errorf("playbook file %s is empty — content is the contract, not just the path", rel)
		}
	}
}

// TestPlaybooksPersonalGitignored confirms `_personal/` is in
// .gitignore so user-private playbooks for internal tools don't
// accidentally land in the public repo. The convention is open
// source; the contents stay private.
func TestPlaybooksPersonalGitignored(t *testing.T) {
	root := repoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	src := string(body)
	// Must ignore everything under _personal/ but keep the
	// .gitkeep placeholder so a fresh clone has the directory.
	for _, want := range []string{
		"/playbooks/_personal/*",
		"!/playbooks/_personal/.gitkeep",
	} {
		if !contains(src, want) {
			t.Errorf(".gitignore missing line %q\n--- gitignore ---\n%s", want, src)
		}
	}
}

// contains is a tiny strings.Contains shim so we don't pull in
// the strings package just for one use — the test stays
// dependency-light.
func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
