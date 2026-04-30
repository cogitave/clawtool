package rules

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// installFakeRtk writes a stub `rtk` (or `rtk.bat` on Windows) into
// a fresh tempdir, prepends it to PATH, and returns a cleanup func
// that restores the original PATH plus invalidates the memoized
// LookPath result. Used by tests that toggle rtk's presence to
// exercise both branches of RewriteBashCommand.
func installFakeRtk(t *testing.T) func() {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "rtk")
	body := "#!/bin/sh\nexec \"$@\"\n"
	if runtime.GOOS == "windows" {
		bin = filepath.Join(dir, "rtk.bat")
		body = "@echo off\r\n%*\r\n"
	}
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake rtk: %v", err)
	}
	orig := os.Getenv("PATH")
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+orig); err != nil {
		t.Fatalf("setenv PATH: %v", err)
	}
	resetRtkLookupForTest()
	return func() {
		_ = os.Setenv("PATH", orig)
		resetRtkLookupForTest()
	}
}

// hideRtk wipes PATH so exec.LookPath("rtk") fails. Required for
// the no-op-when-rtk-missing case — on a host where the operator
// already has rtk installed, the default PATH would mask the test.
func hideRtk(t *testing.T) func() {
	t.Helper()
	orig := os.Getenv("PATH")
	if err := os.Setenv("PATH", t.TempDir()); err != nil {
		t.Fatalf("setenv PATH: %v", err)
	}
	resetRtkLookupForTest()
	return func() {
		_ = os.Setenv("PATH", orig)
		resetRtkLookupForTest()
	}
}

func TestRewriteBashCommand_RewritesAllowlistedWhenRtkPresent(t *testing.T) {
	cleanup := installFakeRtk(t)
	defer cleanup()

	cases := map[string]string{
		"git status":         "rtk git status",
		"ls -R":              "rtk ls -R",
		"grep -r foo .":      "rtk grep -r foo .",
		"cat README.md":      "rtk cat README.md",
		"  find . -name x":   "rtk   find . -name x", // leading whitespace preserved beyond rtk prefix
		"git\tlog --oneline": "rtk git\tlog --oneline",
	}
	for in, want := range cases {
		got := RewriteBashCommand(in)
		if got != want {
			t.Errorf("RewriteBashCommand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRewriteBashCommand_NoOpWhenRtkMissing(t *testing.T) {
	cleanup := hideRtk(t)
	defer cleanup()

	// Allowlisted command — but rtk isn't on PATH. Helper must
	// return the input unchanged: the rule never blocks dispatch
	// just because the optimisation isn't available.
	in := "git status"
	if got := RewriteBashCommand(in); got != in {
		t.Errorf("RewriteBashCommand(%q) with rtk missing = %q, want unchanged", in, got)
	}
}

func TestRewriteBashCommand_NoOpForNonAllowlisted(t *testing.T) {
	cleanup := installFakeRtk(t)
	defer cleanup()

	// Even with rtk on PATH, non-allowlisted commands pass through.
	// curl / docker / ssh / npx are interactive or stream-producing
	// and would break under a buffering proxy.
	cases := []string{
		"curl https://example.com",
		"docker ps",
		"npx foo",
		"ssh host",
		"",       // empty
		"   ",    // whitespace only
		"rtk ls", // already rewritten — idempotent
	}
	for _, in := range cases {
		if got := RewriteBashCommand(in); got != in {
			t.Errorf("RewriteBashCommand(%q) = %q, want unchanged", in, got)
		}
	}
}
