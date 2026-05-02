package checkpoint

import (
	"fmt"
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
		"":                       "empty",
		"   \n  ":                "whitespace-only",
		"updated stuff":          "no type prefix",
		"FIX: caps":              "uppercase type",
		"feat":                   "no colon, no subject",
		"feat:":                  "missing subject",
		"feat: ":                 "empty subject",
		"random(scope): subject": "unknown type",
	}
	for m, why := range bad {
		if err := ValidateMessage(m, CommitOptions{RequireConventional: true, ForbidCoauthor: true}); err == nil {
			t.Errorf("expected fail for %q (%s), got nil", m, why)
		}
	}
}

func TestValidateMessage_Coauthor(t *testing.T) {
	cases := []struct {
		msg        string
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
		"single":        "single",
		"first\nsecond": "first",
		"\nleading":     "",
		"trail\n":       "trail",
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

// TestCheckpointCommit_RespectsGitConfigGpgsign locks in ADR-022
// §Resolved (2026-05-02): the checkpoint commit path propagates
// the operator's `git config commit.gpgsign` / `tag.gpgsign`
// preference when CommitOptions.Sign is left as nil. We do NOT
// shell out to a real git here — the gitConfigGetter package var
// is the seam tests stub, mirroring how the production code only
// invokes `git config --get <key>` from the resolver.
//
// Each row asserts the boolean the resolver returns; that boolean
// is wired directly into the `if … { args = append(args, "-S") }`
// branch in Run, so a true return is a one-to-one proof that `-S`
// would be passed to `git commit` (or `-s` to `git tag` for the
// tag.gpgsign key).
func TestCheckpointCommit_RespectsGitConfigGpgsign(t *testing.T) {
	type configRow struct {
		key   string
		value string // value `git config --get <key>` would print; "" means unset
		err   error  // simulated error from the git invocation
	}
	cases := []struct {
		name     string
		override *bool
		row      configRow
		key      string
		want     bool
	}{
		{
			name: "commit.gpgsign=true, no override → sign",
			row:  configRow{key: "commit.gpgsign", value: "true"},
			key:  "commit.gpgsign",
			want: true,
		},
		{
			name: "commit.gpgsign=false, no override → unsigned",
			row:  configRow{key: "commit.gpgsign", value: "false"},
			key:  "commit.gpgsign",
			want: false,
		},
		{
			name: "commit.gpgsign unset, no override → unsigned",
			row:  configRow{key: "commit.gpgsign", value: ""},
			key:  "commit.gpgsign",
			want: false,
		},
		{
			name: "commit.gpgsign=TRUE (case-insensitive) → sign",
			row:  configRow{key: "commit.gpgsign", value: "TRUE"},
			key:  "commit.gpgsign",
			want: true,
		},
		{
			name: "tag.gpgsign=true, no override → -s on tag",
			row:  configRow{key: "tag.gpgsign", value: "true"},
			key:  "tag.gpgsign",
			want: true,
		},
		{
			name:     "explicit override true beats config=false",
			override: BoolPtr(true),
			row:      configRow{key: "commit.gpgsign", value: "false"},
			key:      "commit.gpgsign",
			want:     true,
		},
		{
			name:     "explicit override false beats config=true",
			override: BoolPtr(false),
			row:      configRow{key: "commit.gpgsign", value: "true"},
			key:      "commit.gpgsign",
			want:     false,
		},
		{
			name: "git config errors out → unsigned, no panic",
			row:  configRow{key: "commit.gpgsign", err: fmt.Errorf("simulated boom")},
			key:  "commit.gpgsign",
			want: false,
		},
	}

	saved := gitConfigGetter
	t.Cleanup(func() { gitConfigGetter = saved })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var sawCwd, sawKey string
			gitConfigGetter = func(cwd, key string) (string, error) {
				sawCwd, sawKey = cwd, key
				if tc.row.err != nil {
					return "", tc.row.err
				}
				if key != tc.row.key {
					// Resolver asked for a key we didn't stub —
					// behave like git would and report unset.
					return "", nil
				}
				return tc.row.value, nil
			}
			got := resolveSignFromGitConfig("/fake/repo", tc.override, tc.key)
			if got != tc.want {
				t.Fatalf("resolveSignFromGitConfig(%q, %v, %q) = %v, want %v",
					"/fake/repo", tc.override, tc.key, got, tc.want)
			}
			// When the override was used, the getter should NOT
			// have been called — overrides short-circuit git.
			if tc.override != nil && (sawCwd != "" || sawKey != "") {
				t.Errorf("override path should bypass git config, but getter saw cwd=%q key=%q",
					sawCwd, sawKey)
			}
			// When the override was NOT used, the getter should
			// have been asked the right key for the right cwd.
			if tc.override == nil {
				if sawCwd != "/fake/repo" {
					t.Errorf("getter cwd = %q, want %q", sawCwd, "/fake/repo")
				}
				if sawKey != tc.key {
					t.Errorf("getter key = %q, want %q", sawKey, tc.key)
				}
			}
		})
	}
}
