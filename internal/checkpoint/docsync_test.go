package checkpoint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cogitave/clawtool/internal/rules"
)

// makeDocsyncRepo builds a temp directory with the listed files
// (relative paths, forward-slash) so CheckDocsync has something
// to stat. Returns the cwd (absolute).
func makeDocsyncRepo(t *testing.T, files ...string) string {
	t.Helper()
	cwd := t.TempDir()
	for _, rel := range files {
		abs := filepath.Join(cwd, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, []byte("// fixture\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", abs, err)
		}
	}
	return cwd
}

// docsyncRuleAt returns a Rule that fires the docsync_violation()
// predicate at the given severity.
func docsyncRuleAt(sev rules.Severity) rules.Rule {
	return rules.Rule{
		Name:      "docsync-pair",
		When:      rules.EventPreCommit,
		Condition: `not docsync_violation("go")`,
		Severity:  sev,
		Hint:      "update sibling *.md or stage it",
	}
}

// ---------------------------------------------------------------
// CheckDocsync — pure helper. Used as the predicate's data source.
// ---------------------------------------------------------------

func TestDocsync_RuleFiresOnGoChangeWithoutDocUpdate(t *testing.T) {
	// Setup: foo.go AND foo.md both exist on disk, but only foo.go
	// is in the changed set. Expect CheckDocsync to flag foo.go.
	cwd := makeDocsyncRepo(t,
		"internal/foo/foo.go",
		"internal/foo/foo.md",
	)
	violations := CheckDocsync(cwd, []string{"internal/foo/foo.go"})
	if len(violations) != 1 || violations[0] != "internal/foo/foo.go" {
		t.Fatalf("expected single violation for foo.go, got %v", violations)
	}

	// Co-landed change — both files in the set. Expect no
	// violation.
	violations = CheckDocsync(cwd, []string{
		"internal/foo/foo.go",
		"internal/foo/foo.md",
	})
	if len(violations) != 0 {
		t.Fatalf("expected no violations when doc is co-landed, got %v", violations)
	}

	// No sibling on disk — silent. The rule only enforces existing
	// doc pairings (operator opts in by creating the .md).
	cwdNoDoc := makeDocsyncRepo(t, "internal/bar/bar.go")
	violations = CheckDocsync(cwdNoDoc, []string{"internal/bar/bar.go"})
	if len(violations) != 0 {
		t.Fatalf("expected no violations when sibling missing, got %v", violations)
	}

	// _test.go and codegen suffixes are exempt.
	cwdMixed := makeDocsyncRepo(t,
		"pkg/zoo/zoo_test.go", "pkg/zoo/zoo_test.md",
		"pkg/zoo/api.pb.go", "pkg/zoo/api.pb.md",
		"pkg/zoo/handlers_gen.go", "pkg/zoo/handlers_gen.md",
		"pkg/zoo/zz_generated_deepcopy.go", "pkg/zoo/zz_generated_deepcopy.md",
	)
	violations = CheckDocsync(cwdMixed, []string{
		"pkg/zoo/zoo_test.go",
		"pkg/zoo/api.pb.go",
		"pkg/zoo/handlers_gen.go",
		"pkg/zoo/zz_generated_deepcopy.go",
	})
	if len(violations) != 0 {
		t.Fatalf("expected zero violations for test+codegen exempt files, got %v", violations)
	}
}

// ---------------------------------------------------------------
// Severity gradient — reuses rules.Severity verbatim per ADR-022
// §Resolved (2026-05-02). off / warn / block ladder.
// ---------------------------------------------------------------

func TestDocsync_SeverityOff_NoFire(t *testing.T) {
	// SeverityOff rules are skipped before evaluation runs at
	// all (rules.Evaluate filter). A docsync violation in the
	// Context must NOT show up in the verdict's Results,
	// Warnings, or Blocked slices.
	ctx := rules.Context{
		Event:             rules.EventPreCommit,
		ChangedPaths:      []string{"internal/foo/foo.go"},
		DocsyncViolations: []string{"internal/foo/foo.go"},
	}
	v := rules.Evaluate([]rules.Rule{docsyncRuleAt(rules.SeverityOff)}, ctx)
	if len(v.Results) != 0 {
		t.Errorf("severity=off: expected no Results, got %v", v.Results)
	}
	if len(v.Warnings) != 0 {
		t.Errorf("severity=off: expected no Warnings, got %v", v.Warnings)
	}
	if len(v.Blocked) != 0 {
		t.Errorf("severity=off: expected no Blocked, got %v", v.Blocked)
	}
	if v.IsBlocked() {
		t.Error("severity=off: IsBlocked() must be false")
	}
}

func TestDocsync_SeverityWarn_Reports(t *testing.T) {
	// SeverityWarn surfaces the violation in v.Warnings without
	// blocking. Verdict.IsBlocked() stays false; caller can
	// proceed but should display the warning.
	ctx := rules.Context{
		Event:             rules.EventPreCommit,
		ChangedPaths:      []string{"internal/foo/foo.go"},
		DocsyncViolations: []string{"internal/foo/foo.go"},
	}
	v := rules.Evaluate([]rules.Rule{docsyncRuleAt(rules.SeverityWarn)}, ctx)
	if len(v.Warnings) != 1 {
		t.Fatalf("severity=warn: expected 1 warning, got %d (%v)", len(v.Warnings), v.Warnings)
	}
	if v.Warnings[0].Rule != "docsync-pair" {
		t.Errorf("warning rule name = %q, want docsync-pair", v.Warnings[0].Rule)
	}
	if v.Warnings[0].Severity != rules.SeverityWarn {
		t.Errorf("warning severity = %q, want warn", v.Warnings[0].Severity)
	}
	if v.Warnings[0].Passed {
		t.Error("warning Passed must be false (violation reported)")
	}
	if v.IsBlocked() {
		t.Error("severity=warn: IsBlocked() must be false")
	}

	// Sanity: with no violation, the rule passes silently.
	clean := rules.Context{
		Event:        rules.EventPreCommit,
		ChangedPaths: []string{"internal/foo/foo.go", "internal/foo/foo.md"},
	}
	cv := rules.Evaluate([]rules.Rule{docsyncRuleAt(rules.SeverityWarn)}, clean)
	if len(cv.Warnings) != 0 || len(cv.Blocked) != 0 {
		t.Errorf("clean run: expected no warnings/blocks, got warn=%v block=%v", cv.Warnings, cv.Blocked)
	}
}

func TestDocsync_SeverityBlock_RefusesCommit(t *testing.T) {
	// SeverityBlock is the operator's hard-stop — Verdict.
	// IsBlocked() flips true and the violation lands in
	// v.Blocked. The Commit tool consults IsBlocked() and
	// refuses to call git commit.
	ctx := rules.Context{
		Event:             rules.EventPreCommit,
		ChangedPaths:      []string{"internal/foo/foo.go"},
		DocsyncViolations: []string{"internal/foo/foo.go"},
	}
	v := rules.Evaluate([]rules.Rule{docsyncRuleAt(rules.SeverityBlock)}, ctx)
	if len(v.Blocked) != 1 {
		t.Fatalf("severity=block: expected 1 blocked result, got %d (%v)", len(v.Blocked), v.Blocked)
	}
	if !v.IsBlocked() {
		t.Error("severity=block: IsBlocked() must be true (commit refused)")
	}
	if v.Blocked[0].Severity != rules.SeverityBlock {
		t.Errorf("blocked severity = %q, want block", v.Blocked[0].Severity)
	}
	if v.Blocked[0].Hint == "" {
		t.Error("blocked result must propagate the operator hint")
	}
}
