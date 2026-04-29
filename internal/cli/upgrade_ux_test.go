package cli

import (
	"bytes"
	"strings"
	"testing"
)

// upgradeUX renders to whatever io.Writer the caller passes. A
// bytes.Buffer always falls into the "not a *os.File" branch, so
// these tests exercise the plain-text path — predictable, no
// ANSI noise to assert around. Colour rendering through a real
// TTY is covered in real upgrades and the CLAWTOOL_E2E_DOCKER
// container test.

func TestUpgradeUX_HeaderDelta_PlainShape(t *testing.T) {
	buf := &bytes.Buffer{}
	ux := newUpgradeUX(buf)
	ux.HeaderDelta("v0.22.34", "v0.22.35")
	got := buf.String()
	for _, want := range []string{"clawtool upgrade", "v0.22.34 -> v0.22.35"} {
		if !strings.Contains(got, want) {
			t.Fatalf("plain header missing %q:\n%s", want, got)
		}
	}
}

func TestUpgradeUX_PhaseFlow(t *testing.T) {
	buf := &bytes.Buffer{}
	ux := newUpgradeUX(buf)
	ux.PhaseStart("Downloading binary")
	ux.PhaseDone("clawtool_0.22.35_linux_amd64.tar.gz · 12.4 MB")
	got := buf.String()
	if !strings.Contains(got, "-> Downloading binary") {
		t.Fatalf("PhaseStart shape missing: %s", got)
	}
	if !strings.Contains(got, "OK Downloading binary") {
		t.Fatalf("PhaseDone success marker missing: %s", got)
	}
	if !strings.Contains(got, "clawtool_0.22.35_linux_amd64.tar.gz") {
		t.Fatalf("detail line lost: %s", got)
	}
}

func TestUpgradeUX_PhaseFailIncludesHint(t *testing.T) {
	buf := &bytes.Buffer{}
	ux := newUpgradeUX(buf)
	ux.PhaseStart("Replacing binary")
	ux.PhaseFail("permission denied", "re-run with sudo")
	got := buf.String()
	for _, want := range []string{
		"FAIL Replacing binary",
		"permission denied",
		"re-run with sudo",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("PhaseFail missing %q:\n%s", want, got)
		}
	}
}

func TestUpgradeUX_SectionAndNextSteps(t *testing.T) {
	buf := &bytes.Buffer{}
	ux := newUpgradeUX(buf)
	ux.Section("Daemon restart")
	ux.NextSteps([]string{
		"clawtool overview     check the live state",
		"clawtool changelog    full release notes",
	})
	got := buf.String()
	if !strings.Contains(got, "Daemon restart") {
		t.Fatalf("section title missing: %s", got)
	}
	if !strings.Contains(got, "Next steps") {
		t.Fatalf("next-steps section missing: %s", got)
	}
	if !strings.Contains(got, "clawtool overview") {
		t.Fatalf("first next-step lost: %s", got)
	}
	if !strings.Contains(got, "clawtool changelog") {
		t.Fatalf("second next-step lost: %s", got)
	}
}

func TestUpgradeUX_ReleaseNotesSkipsEmptyBody(t *testing.T) {
	buf := &bytes.Buffer{}
	ux := newUpgradeUX(buf)
	ux.ReleaseNotes("", 8)
	if got := buf.String(); got != "" {
		t.Fatalf("empty notes should not render anything; got: %q", got)
	}

	ux.ReleaseNotes("  \n  \t\n", 8) // whitespace-only also no-op
	if got := buf.String(); got != "" {
		t.Fatalf("whitespace-only notes should not render anything; got: %q", got)
	}
}

func TestUpgradeUX_ReleaseNotesTruncatesAtMaxLines(t *testing.T) {
	buf := &bytes.Buffer{}
	ux := newUpgradeUX(buf)
	body := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	ux.ReleaseNotes(body, 3)
	got := buf.String()
	if !strings.Contains(got, "line 1") {
		t.Fatalf("first line missing: %s", got)
	}
	if !strings.Contains(got, "line 3") {
		t.Fatalf("third line missing: %s", got)
	}
	if strings.Contains(got, "line 4") {
		t.Fatalf("truncation failed — line 4 leaked: %s", got)
	}
	if !strings.Contains(got, "…") {
		t.Fatalf("truncation marker '…' missing: %s", got)
	}
}

func TestHumanBytes_BoundaryCases(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{42, "42 B"},
		{1024, "1.0 KB"},
		{1500, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{12 * 1024 * 1024, "12.0 MB"},
	}
	for _, c := range cases {
		got := humanBytes(c.in)
		if got != c.want {
			t.Errorf("humanBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
