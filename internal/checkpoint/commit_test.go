package checkpoint

import (
	"strings"
	"testing"
)

func TestValidateMessage_Conventional(t *testing.T) {
	good := []string{
		"feat: add hermes bridge",
		"fix(scope): typo in README",
		"docs(api): clarify auth flow",
		"feat(parser)!: drop trailing-comma support",
		"refactor: split server.go",
		"chore: bump deps",
		"build(ci): bump Go to 1.26",
	}
	for _, m := range good {
		if err := ValidateMessage(m, CommitOptions{RequireConventional: true, ForbidCoauthor: true}); err != nil {
			t.Errorf("expected pass for %q, got: %v", m, err)
		}
	}

	bad := map[string]string{
		"":                            "empty",
		"   \n  ":                     "whitespace-only",
		"updated stuff":               "no type prefix",
		"FIX: caps":                   "uppercase type",
		"feat":                        "no colon, no subject",
		"feat:":                       "missing subject",
		"feat: ":                      "empty subject",
		"random(scope): subject":      "unknown type",
	}
	for m, why := range bad {
		if err := ValidateMessage(m, CommitOptions{RequireConventional: true, ForbidCoauthor: true}); err == nil {
			t.Errorf("expected fail for %q (%s), got nil", m, why)
		}
	}
}

func TestValidateMessage_Coauthor(t *testing.T) {
	cases := []struct {
		msg     string
		shouldFail bool
	}{
		{"feat: x\n\nCo-Authored-By: Claude <noreply@anthropic.com>", true},
		{"fix: y\n\nCo-authored-by: claude", true},
		{"docs: z\n\nCO-AUTHORED-BY: bot", true}, // case-insensitive key
		{"feat: clean\n\nSigned-off-by: me", false},
		{"feat: clean", false},
	}
	for _, tc := range cases {
		err := ValidateMessage(tc.msg, CommitOptions{RequireConventional: true, ForbidCoauthor: true})
		if tc.shouldFail && err == nil {
			t.Errorf("expected coauthor block for %q, got nil", tc.msg)
		}
		if !tc.shouldFail && err != nil {
			t.Errorf("expected pass for %q, got: %v", tc.msg, err)
		}
	}
}

func TestValidateMessage_OptOut(t *testing.T) {
	// With both checks off, even the messiest message passes.
	err := ValidateMessage(
		"random text\n\nCo-Authored-By: bot",
		CommitOptions{RequireConventional: false, ForbidCoauthor: false},
	)
	if err != nil {
		t.Errorf("opt-out config should pass any non-empty message, got: %v", err)
	}
	// But empty still fails.
	if err := ValidateMessage("", CommitOptions{}); err == nil {
		t.Error("empty message must always fail")
	}
}

func TestValidateMessage_OnlyConventional(t *testing.T) {
	err := ValidateMessage(
		"feat: x\n\nCo-Authored-By: bot",
		CommitOptions{RequireConventional: true, ForbidCoauthor: false},
	)
	if err != nil {
		t.Errorf("conventional-only should pass message with coauthor when ForbidCoauthor=false, got: %v", err)
	}
}

func TestFirstLine(t *testing.T) {
	cases := map[string]string{
		"single":         "single",
		"first\nsecond":  "first",
		"\nleading":      "",
		"trail\n":        "trail",
	}
	for in, want := range cases {
		if got := firstLine(in); got != want {
			t.Errorf("firstLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConventionalRegexAnchoring(t *testing.T) {
	// The regex must anchor at start of line — a stray valid-looking
	// fragment late in the message shouldn't pass the first-line check.
	bad := "deploy notes\n\nfeat: this would have been valid"
	if err := ValidateMessage(bad, CommitOptions{RequireConventional: true}); err == nil {
		t.Error("expected fail when first line isn't conventional, despite a valid line later")
	}
	if !strings.Contains(bad, "feat:") {
		t.Fatal("test setup: expected 'feat:' marker in body")
	}
}
