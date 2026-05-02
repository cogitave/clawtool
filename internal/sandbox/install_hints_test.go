package sandbox

import (
	"strings"
	"testing"
)

// TestInstallHint_LinuxBwrap ensures the Linux+bwrap path lists
// the three major package managers operators reach for first
// (apt-get / dnf / pacman). Pinned by literal so a future copy
// edit doesn't accidentally drop one.
func TestInstallHint_LinuxBwrap(t *testing.T) {
	hint := InstallHint("linux", "bwrap")
	if hint == "" {
		t.Fatal("expected a non-empty hint for (linux, bwrap)")
	}
	for _, want := range []string{
		"apt-get install bubblewrap",
		"dnf install bubblewrap",
		"pacman -S bubblewrap",
	} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint missing %q\n--- hint ---\n%s", want, hint)
		}
	}
}

// TestInstallHint_MacosSandboxExec confirms the macOS+sandbox-exec
// hint explains the engine is built-in (no install) AND surfaces
// the .sb compiler status — operators must know "detected but
// pending" is not the same as "missing".
func TestInstallHint_MacosSandboxExec(t *testing.T) {
	hint := InstallHint("darwin", "sandbox-exec")
	if hint == "" {
		t.Fatal("expected a non-empty hint for (darwin, sandbox-exec)")
	}
	for _, want := range []string{
		"built into macOS",
		"no install",
		// .sb compiler status — phrasing matches sandbox_exec_darwin.go
		// so doc-grep "compiler pending" lands on both spots.
		"compiler",
	} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint missing %q\n--- hint ---\n%s", want, hint)
		}
	}
}

// TestInstallHint_WindowsUnsupported pins the Windows-only-Docker
// rule: bwrap and sandbox-exec are physically impossible on
// Windows; the Docker hint must point at Docker Desktop.
func TestInstallHint_WindowsUnsupported(t *testing.T) {
	dockerHint := InstallHint("windows", "docker")
	if dockerHint == "" {
		t.Fatal("expected a non-empty hint for (windows, docker)")
	}
	if !strings.Contains(dockerHint, "Docker Desktop") {
		t.Errorf("Windows docker hint should mention Docker Desktop\n--- hint ---\n%s", dockerHint)
	}
	// bwrap on Windows must redirect to WSL2 / Docker Desktop
	// instead of pretending bubblewrap is installable.
	bwrapWin := InstallHint("windows", "bwrap")
	if !strings.Contains(bwrapWin, "Linux-only") {
		t.Errorf("Windows bwrap hint should explain it is Linux-only; got: %s", bwrapWin)
	}
	if !strings.Contains(bwrapWin, "Docker Desktop") && !strings.Contains(bwrapWin, "WSL2") {
		t.Errorf("Windows bwrap hint should redirect to Docker Desktop or WSL2; got: %s", bwrapWin)
	}
	// sandbox-exec on Windows is meaningless — the hint must
	// say so plainly so operators don't waste time searching.
	seWin := InstallHint("windows", "sandbox-exec")
	if !strings.Contains(seWin, "macOS-only") {
		t.Errorf("Windows sandbox-exec hint should say it is macOS-only; got: %s", seWin)
	}
}

// TestInstallHint_NoopReturnsEmpty pins the contract: the noop
// engine is intrinsic, never missing, never has an install path.
// Doctor flow uses an empty hint as the "skip the append" signal.
func TestInstallHint_NoopReturnsEmpty(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		if got := InstallHint(goos, "noop"); got != "" {
			t.Errorf("InstallHint(%q, \"noop\") = %q; want empty", goos, got)
		}
	}
}

// TestInstallHint_UnknownReturnsEmpty pins the safe-by-default
// behaviour: an engine name we don't recognise yields "" rather
// than a misleading hint. Future engines must wire themselves in
// explicitly.
func TestInstallHint_UnknownReturnsEmpty(t *testing.T) {
	cases := []struct {
		goos, engine string
	}{
		{"linux", "firejail"}, // not yet supported
		{"plan9", "bwrap"},    // unrecognised goos
		{"", ""},
		{"linux", ""},
	}
	for _, c := range cases {
		if got := InstallHint(c.goos, c.engine); got != "" {
			t.Errorf("InstallHint(%q, %q) = %q; want empty", c.goos, c.engine, got)
		}
	}
}

// TestInstallHint_NoSudoDriving confirms the function never
// emits a command that would silently install something on the
// operator's behalf. Hints SHOW commands (operator runs them);
// they don't present a "we'll do it for you" surface. ADR-020
// §Resolved gate.
func TestInstallHint_NoSudoDriving(t *testing.T) {
	// Forbidden: any phrasing that suggests clawtool will run
	// the install itself. Hints describe what the operator does.
	forbidden := []string{
		"clawtool will install",
		"automatically installing",
		"running sudo for you",
		"auto-install",
	}
	cases := []struct{ goos, engine string }{
		{"linux", "bwrap"},
		{"linux", "docker"},
		{"darwin", "sandbox-exec"},
		{"darwin", "docker"},
		{"windows", "docker"},
	}
	for _, c := range cases {
		hint := InstallHint(c.goos, c.engine)
		for _, bad := range forbidden {
			if strings.Contains(strings.ToLower(hint), strings.ToLower(bad)) {
				t.Errorf("(%s, %s) hint contains forbidden phrase %q\n--- hint ---\n%s",
					c.goos, c.engine, bad, hint)
			}
		}
	}
}

// TestInstallHint_CaseInsensitive lets callers pass runtime.GOOS
// (always lowercase) OR an operator-typed engine name in any
// case without surprising matches. Pin the contract.
func TestInstallHint_CaseInsensitive(t *testing.T) {
	a := InstallHint("Linux", "BWRAP")
	b := InstallHint("linux", "bwrap")
	if a == "" || b == "" {
		t.Fatalf("expected non-empty hints; a=%q b=%q", a, b)
	}
	if a != b {
		t.Errorf("case-folding mismatch:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
}
