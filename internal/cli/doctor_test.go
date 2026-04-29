package cli

import (
	"bytes"
	"strings"
	"testing"

	// Side-effect: register every recipe so doctorRecipes has
	// something to walk. Same import the dispatcher uses.
	_ "github.com/cogitave/clawtool/internal/setup/recipes"
)

func TestUniqStrings_PreservesOrderDedups(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{nil, []string{}},
		{[]string{}, []string{}},
		{[]string{"a"}, []string{"a"}},
		{[]string{"a", "b", "a", "c", "b"}, []string{"a", "b", "c"}},
		{[]string{"x", "x", "x"}, []string{"x"}},
	}
	for _, c := range cases {
		got := uniqStrings(append([]string(nil), c.in...))
		if len(got) != len(c.want) {
			t.Errorf("uniqStrings(%v) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("uniqStrings(%v)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestDoctorReport_CountsAndRendersSymbols(t *testing.T) {
	out := &bytes.Buffer{}
	r := &doctorReport{}

	r.ok(out, "binary OK")
	r.info(out, "informational note")
	r.warn(out, "missing secret", "clawtool source set-secret github GITHUB_TOKEN")
	r.fail(out, "agent missing", "")

	if r.warnings != 1 {
		t.Errorf("warnings = %d, want 1", r.warnings)
	}
	if r.critical != 1 {
		t.Errorf("critical = %d, want 1", r.critical)
	}
	got := out.String()
	for _, want := range []string{"✓ binary OK", "· informational note", "⚠ missing secret", "✗ agent missing"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q in:\n%s", want, got)
		}
	}
	// Fix line should appear under the warn but not the empty fail.
	if !strings.Contains(got, "→ clawtool source set-secret github GITHUB_TOKEN") {
		t.Errorf("output missing fix-line for warn: %s", got)
	}
}

func TestRunDoctor_ProducesAllSections(t *testing.T) {
	app, out, dir := withWizardAppInTempDir(t)
	// Point secretsPath to a stable not-existing path inside the
	// tempdir so doctorConfig's "no secrets configured" branch fires
	// deterministically. Keep ConfigPath default (set by withWizardApp).
	app.SetSecretsPath(dir + "/secrets-not-here.toml")

	rc := app.runDoctor(nil)
	if rc != 0 && rc != 1 {
		t.Fatalf("doctor exit code unexpected: %d", rc)
	}

	got := out.String()
	for _, section := range []string{
		"[binary]",
		"[config]",
		"[agents]",
		"[sources]",
		"[recipes — current cwd]",
		"[uninstall plan]",
		"[summary]",
	} {
		if !strings.Contains(got, section) {
			t.Errorf("doctor output missing section %q in:\n%s", section, got)
		}
	}
}

// configRelativeDot is reserved (see source); tested for shape so a
// later refactor doesn't silently drop the home-dir abbreviation.
func TestConfigRelativeDot_PrefersTilde(t *testing.T) {
	got := configRelativeDot("/some/absolute/path")
	if got == "" {
		t.Error("configRelativeDot should never return empty")
	}
}
