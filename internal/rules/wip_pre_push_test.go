package rules

import (
	"os"
	"path/filepath"
	"testing"
)

// wipRuleCondition matches the `condition` field of the
// wip-on-protected-branch rule shipped in .clawtool/rules.toml.
// Kept literal here (not loaded from disk) so the unit test
// stays hermetic and the test file double-acts as the canonical
// description of what the operator policy means.
const wipRuleCondition = `not (arg("head_subject") ^= "wip!:" and (arg("branch") == "main" or arg("branch") == "master" or arg("branch") == "develop" or arg("branch") ~~ "release/*"))`

func wipRule() Rule {
	return Rule{
		Name:      "wip-on-protected-branch",
		When:      EventPrePush,
		Condition: wipRuleCondition,
		Severity:  SeverityBlock,
		Hint:      "test",
	}
}

func TestRulesCheck_BlocksWipOnProtectedBranch(t *testing.T) {
	cases := []struct {
		name        string
		branch      string
		headSubject string
		wantBlocked bool
	}{
		// Blocked: wip!: + protected branch.
		{"wip on main", "main", "wip!: draft", true},
		{"wip on master", "master", "wip!: draft", true},
		{"wip on develop", "develop", "wip!: tweak", true},
		{"wip on release/v1.2", "release/v1.2", "wip!: hot", true},
		{"wip on release/2026-Q2", "release/2026-Q2", "wip!: prep", true},

		// Allowed: real subject on protected branch.
		{"feat on main", "main", "feat: ship", false},
		{"fix on master", "master", "fix: bug", false},
		{"docs on release/x", "release/x", "docs: changelog", false},

		// Allowed: wip!: on feature branch.
		{"wip on feature", "feat/x", "wip!: draft", false},
		{"wip on autodev", "autodev/checkpoint", "wip!: tweak", false},
		{"wip on bugfix", "bugfix/leak", "wip!: trial", false},

		// Edge: head_subject contains wip!: but doesn't START with it
		// (operator's policy is prefix-only — `^=`).
		{"feat that mentions wip!: in body", "main", "feat: drop wip!: prefix support", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := Context{
				Event: EventPrePush,
				Args: map[string]string{
					"branch":       tc.branch,
					"head_subject": tc.headSubject,
				},
			}
			v := Evaluate([]Rule{wipRule()}, ctx)
			gotBlocked := v.IsBlocked()
			if gotBlocked != tc.wantBlocked {
				t.Errorf("blocked = %v, want %v (branch=%q subject=%q reasons=%+v)",
					gotBlocked, tc.wantBlocked, tc.branch, tc.headSubject, v.Results)
			}
		})
	}
}

func TestWipRule_LoadsFromShippedRulesFile(t *testing.T) {
	// Verify the rule literal in .clawtool/rules.toml parses
	// cleanly through the standard loader path. Catches the
	// case where someone hand-edits the toml and breaks the
	// condition syntax — the unit test above only exercises the
	// in-Go literal copy.
	candidates := []string{
		".clawtool/rules.toml",
		"../../.clawtool/rules.toml",
	}
	var path string
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			if _, err := os.Stat(abs); err == nil {
				path = abs
				break
			}
		}
	}
	if path == "" {
		t.Skip("rules.toml not reachable from test working dir")
	}
	rs, err := Load(path)
	if err != nil {
		t.Fatalf("Load %s: %v", path, err)
	}
	var found *Rule
	for i := range rs {
		if rs[i].Name == "wip-on-protected-branch" {
			found = &rs[i]
			break
		}
	}
	if found == nil {
		t.Fatal("wip-on-protected-branch rule not present in rules.toml")
	}
	if found.When != EventPrePush {
		t.Errorf("rule when = %q, want %q", found.When, EventPrePush)
	}
	if found.Severity != SeverityBlock {
		t.Errorf("rule severity = %q, want %q", found.Severity, SeverityBlock)
	}

	// And evaluating against a clearly-bad context blocks.
	v := Evaluate([]Rule{*found}, Context{
		Event: EventPrePush,
		Args:  map[string]string{"branch": "main", "head_subject": "wip!: oops"},
	})
	if !v.IsBlocked() {
		t.Errorf("loaded rule did not block wip!: on main; results=%+v", v.Results)
	}
}
