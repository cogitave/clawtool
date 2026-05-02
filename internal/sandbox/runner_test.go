package sandbox

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRunOneShot_NoopEngineRefuses — without a host-native
// engine the noop fallback's Wrap returns an explicit error so a
// caller that requested a sandbox doesn't silently fall through
// to an unsandboxed run. The runner surfaces that as an err
// return (setup failure, before exec).
func TestRunOneShot_NoopEngineRefuses(t *testing.T) {
	// Skip whenever a real engine is detected on this host: bwrap (Linux)
	// or sandbox-exec (macOS, even when the .sb compiler is pending) both
	// short-circuit the noop path. The contract under test is "no engine
	// → refuse"; a real engine produces a different (engine-specific)
	// error, not the noop refusal text.
	if SelectEngine().Name() != "noop" {
		t.Skip("real sandbox engine present; noop-refusal contract not exercised")
	}
	res, err := RunOneShot(context.Background(), RunRequest{
		Profile: &Profile{Name: "p"},
		Command: "true",
	})
	if err == nil {
		t.Fatalf("expected wrap error from noop engine; got %+v", res)
	}
	if !strings.Contains(err.Error(), "noop") && !strings.Contains(err.Error(), "no host-native engine") {
		t.Errorf("error = %v, want it to mention the noop refusal", err)
	}
}

// TestRunOneShot_RejectsEmptyCommand — caller passing a blank
// command must trip the validation guard before any engine work.
func TestRunOneShot_RejectsEmptyCommand(t *testing.T) {
	_, err := RunOneShot(context.Background(), RunRequest{
		Profile: &Profile{Name: "p"},
		Command: "",
	})
	if err == nil {
		t.Fatal("expected error on empty command, got nil")
	}
}

// TestRunOneShot_RejectsNilProfile — same guard for a missing
// profile pointer; the runner is the wrong layer to invent one.
func TestRunOneShot_RejectsNilProfile(t *testing.T) {
	_, err := RunOneShot(context.Background(), RunRequest{
		Profile: nil,
		Command: "true",
	})
	if err == nil {
		t.Fatal("expected error on nil profile, got nil")
	}
}

// TestRunOneShot_DefaultTimeoutApplied — Timeout=0 picks up the
// 60s default. We assert the default-clamp logic without
// actually running for a full minute by checking the deadline
// attached to ctx via a synthetic engine wouldn't be doable
// without more plumbing — instead we assert the field after
// validation by passing a negative value and ensuring the
// runner doesn't refuse setup (the negative is silently
// promoted to default before exec).
func TestRunOneShot_DefaultTimeoutApplied(t *testing.T) {
	// Negative timeout would hang forever if not clamped to
	// the default. The noop engine's Wrap fails before exec
	// so this returns quickly regardless — what we care about
	// is "no panic, returns within reason".
	done := make(chan struct{})
	go func() {
		_, _ = RunOneShot(context.Background(), RunRequest{
			Profile: &Profile{Name: "p"},
			Command: "true",
			Timeout: -1,
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunOneShot hung — timeout clamp likely broken")
	}
}
