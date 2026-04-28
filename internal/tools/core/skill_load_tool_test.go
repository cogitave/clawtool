package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withSkillsRoot points the lookup chain at a tempdir via the
// CLAWTOOL_SKILLS_DIR escape hatch. Returns cleanup that
// restores the prior env value.
func withSkillsRoot(t *testing.T, root string) func() {
	t.Helper()
	prev, hadPrev := os.LookupEnv("CLAWTOOL_SKILLS_DIR")
	t.Setenv("CLAWTOOL_SKILLS_DIR", root)
	return func() {
		if hadPrev {
			t.Setenv("CLAWTOOL_SKILLS_DIR", prev)
		} else {
			os.Unsetenv("CLAWTOOL_SKILLS_DIR")
		}
	}
}

// dropSkill writes a minimal SKILL.md with the given description
// into root/<name>/SKILL.md.
func dropSkill(t *testing.T, root, name, description string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `---
name: ` + name + `
description: ` + description + `
---

# ` + name + `

Body of the skill.
`
	p := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveSkill_FindsCatalogScope(t *testing.T) {
	root := t.TempDir()
	defer withSkillsRoot(t, root)()
	dropSkill(t, root, "docx", "Word document creation")

	scope, path, err := resolveSkill("docx")
	if err != nil {
		t.Fatalf("resolveSkill: %v", err)
	}
	if scope != "catalog" {
		t.Errorf("scope = %q, want catalog (only the catalog root has it)", scope)
	}
	if !strings.HasSuffix(path, "/docx/SKILL.md") {
		t.Errorf("path = %q, want suffix /docx/SKILL.md", path)
	}
}

func TestResolveSkill_RejectsUnknown(t *testing.T) {
	root := t.TempDir()
	defer withSkillsRoot(t, root)()

	_, _, err := resolveSkill("nope")
	if err == nil {
		t.Fatal("expected error for unknown skill")
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Errorf("error should say 'not installed'; got: %v", err)
	}
}

func TestEnumerateSkills_SortedDeduped(t *testing.T) {
	root := t.TempDir()
	defer withSkillsRoot(t, root)()
	dropSkill(t, root, "zeta", "z desc")
	dropSkill(t, root, "alpha", "a desc")
	dropSkill(t, root, "mid", "m desc")

	entries, err := enumerateSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 3 {
		t.Fatalf("expected at least 3 entries; got %d (%+v)", len(entries), entries)
	}
	// Lookup names from this test (production may have project /
	// user roots populated too; we just confirm OUR three appear
	// in sorted order relative to each other).
	var ours []string
	for _, e := range entries {
		switch e.Name {
		case "alpha", "mid", "zeta":
			ours = append(ours, e.Name)
		}
	}
	want := []string{"alpha", "mid", "zeta"}
	if len(ours) != 3 {
		t.Fatalf("missing expected skills: got %v", ours)
	}
	for i := range want {
		if ours[i] != want[i] {
			t.Errorf("sort order wrong: got %v, want %v", ours, want)
			break
		}
	}
}

func TestExtractSkillDescription_SingleLine(t *testing.T) {
	body := `---
name: docx
description: Create Word documents
---

body
`
	if got := extractSkillDescription(body); got != "Create Word documents" {
		t.Errorf("desc = %q, want %q", got, "Create Word documents")
	}
}

func TestExtractSkillDescription_BlockScalar(t *testing.T) {
	body := `---
name: docx
description: >
  When the user wants Word documents, prefer python-docx with the
  template at references/template.docx.
allowed-tools: Read Write
---

body
`
	got := extractSkillDescription(body)
	if !strings.Contains(got, "Word documents") {
		t.Errorf("block-scalar desc missing content: %q", got)
	}
	if !strings.Contains(got, "template.docx") {
		t.Errorf("block-scalar desc lost continuation: %q", got)
	}
}

func TestExtractSkillDescription_NoFrontmatter(t *testing.T) {
	body := `# regular markdown

no frontmatter here
`
	if got := extractSkillDescription(body); got != "" {
		t.Errorf("expected empty desc; got %q", got)
	}
}

func TestValidSkillName_RejectsPathTraversal(t *testing.T) {
	bad := []string{
		"../etc/passwd",
		"foo/bar",
		"FOO",
		"foo bar",
		"",
	}
	for _, n := range bad {
		if validSkillName(n) {
			t.Errorf("validSkillName(%q) = true; want false (defense against path traversal)", n)
		}
	}
	for _, n := range []string{"docx", "frontend-design", "x", "skill-with-digits-123"} {
		if !validSkillName(n) {
			t.Errorf("validSkillName(%q) = false; want true", n)
		}
	}
}
