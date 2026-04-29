package core

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestBash_Success verifies a clean run captures stdout + zero exit + accurate
// duration accounting and the cwd we asked for.
func TestBash_Success(t *testing.T) {
	res := executeBash(context.Background(), "printf hello", t.TempDir(), 5*time.Second)

	if res.Stdout != "hello" {
		t.Errorf("stdout = %q, want %q", res.Stdout, "hello")
	}
	if res.Stderr != "" {
		t.Errorf("stderr = %q, want empty", res.Stderr)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit_code = %d, want 0", res.ExitCode)
	}
	if res.TimedOut {
		t.Error("timed_out = true, want false")
	}
	if res.DurationMs < 0 || res.DurationMs > 5000 {
		t.Errorf("duration_ms = %d, want >=0 and <5000", res.DurationMs)
	}
}

// TestBash_NonZeroExit asserts that a failing command propagates its exit
// status and stderr to the caller without dropping stdout written before
// the failure.
func TestBash_NonZeroExit(t *testing.T) {
	res := executeBash(context.Background(), "echo first; echo bad >&2; exit 7", "", 5*time.Second)

	if !strings.Contains(res.Stdout, "first") {
		t.Errorf("stdout = %q, want it to contain 'first'", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "bad") {
		t.Errorf("stderr = %q, want it to contain 'bad'", res.Stderr)
	}
	if res.ExitCode != 7 {
		t.Errorf("exit_code = %d, want 7", res.ExitCode)
	}
	if res.TimedOut {
		t.Error("timed_out = true, want false (process exited cleanly)")
	}
}

// TestBash_TimeoutPreservesOutput is the headline ADR-005 quality-bar test:
// the user-facing timeout fires, the call returns near the deadline (not
// after the runaway child finishes), and stdout up to that moment is
// preserved.
func TestBash_TimeoutPreservesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process-group SIGKILL is unix-only; Windows uses default Cancel")
	}

	const timeout = 300 * time.Millisecond

	res := executeBash(context.Background(),
		"echo first; sleep 5; echo never",
		"", timeout)

	if !res.TimedOut {
		t.Errorf("timed_out = false, want true")
	}
	if !strings.Contains(res.Stdout, "first") {
		t.Errorf("stdout = %q, want it to contain 'first' (output before kill)", res.Stdout)
	}
	if strings.Contains(res.Stdout, "never") {
		t.Errorf("stdout = %q, must not contain 'never' (runs after timeout)", res.Stdout)
	}
	if res.ExitCode != -1 {
		t.Errorf("exit_code = %d, want -1 (killed before clean exit)", res.ExitCode)
	}
	// The whole point: duration must be near `timeout`, not anywhere near
	// the 5-second sleep. Race-detector + scheduler jitter can shave a
	// few ms off the measured duration vs. the context deadline, so
	// allow a 50ms tolerance below `timeout` rather than asserting a
	// strict floor (the test was previously flaky under -race when
	// the cancel signal raced the duration tick).
	tolerance := int64(50)
	if res.DurationMs < int64(timeout.Milliseconds())-tolerance {
		t.Errorf("duration_ms = %d, want >= %d (timeout - %dms tolerance)", res.DurationMs, timeout.Milliseconds(), tolerance)
	}
	if res.DurationMs > 2000 {
		t.Errorf("duration_ms = %d, want <2000 — runaway child should be reaped via process group", res.DurationMs)
	}
}

// TestBash_CwdDefault verifies that an empty cwd is mapped to the user's
// home directory rather than inherited from the daemon.
func TestBash_CwdDefault(t *testing.T) {
	res := executeBash(context.Background(), "pwd", "", 5*time.Second)
	if res.ExitCode != 0 {
		t.Fatalf("pwd failed with exit %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	got := strings.TrimSpace(res.Stdout)
	want := homeDir()
	if got != want {
		t.Errorf("pwd = %q, want %q (home directory)", got, want)
	}
	if res.Cwd != want {
		t.Errorf("result.cwd = %q, want %q", res.Cwd, want)
	}
}

// TestBash_CwdOverride asserts the cwd argument is honored.
func TestBash_CwdOverride(t *testing.T) {
	tmp := t.TempDir()
	res := executeBash(context.Background(), "pwd", tmp, 5*time.Second)
	if res.ExitCode != 0 {
		t.Fatalf("pwd failed with exit %d, stderr=%q", res.ExitCode, res.Stderr)
	}
	got := strings.TrimSpace(res.Stdout)
	// macOS resolves /var to /private/var so allow a suffix match.
	if got != tmp && !strings.HasSuffix(got, tmp) {
		t.Errorf("pwd = %q, want %q (or suffix)", got, tmp)
	}
	if res.Cwd != tmp {
		t.Errorf("result.cwd = %q, want %q", res.Cwd, tmp)
	}
}
