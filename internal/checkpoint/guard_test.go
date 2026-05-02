package checkpoint

import (
	"errors"
	"testing"
)

// TestGuard_DisabledByDefault asserts the package-global Guard
// is a strict no-op when SetConfig was never called (or was
// called with enabled=false). This is the contract that lets
// us wire the OnEdit + Check pair into Edit/Write unconditionally
// without paying a config-toggle cost on the hot path.
func TestGuard_DisabledByDefault(t *testing.T) {
	g := Guard()
	t.Cleanup(g.Reset)
	g.Reset() // ensure clean slate even if a previous test forgot

	if g.Enabled() {
		t.Fatalf("Guard().Enabled() = true at zero state, want false")
	}
	// Pile on edits without ever calling SetConfig — Check must
	// stay nil because the gate's master switch is off.
	for i := 0; i < 100; i++ {
		if err := g.OnEdit("file.go"); err != nil {
			t.Fatalf("OnEdit returned error in disabled mode: %v", err)
		}
		if err := g.Check(); err != nil {
			t.Fatalf("Check returned error in disabled mode at iter %d: %v", i, err)
		}
	}
	// Counter MUST stay at zero — disabled mode doesn't even
	// increment, so the operator can flip the toggle on later
	// without inheriting accumulated edits from before.
	if c := g.Counter(); c != 0 {
		t.Errorf("Counter = %d in disabled mode, want 0", c)
	}
}

// TestGuard_EnabledRequiresRecentCheckpoint exercises the gate
// firing path: enable Guard with a small N, run N edits, expect
// the Nth Check to return ErrCheckpointRequired. The N+1 edit
// stays blocked too (the gate is sticky until OnCheckpoint resets).
func TestGuard_EnabledRequiresRecentCheckpoint(t *testing.T) {
	g := Guard()
	t.Cleanup(g.Reset)

	const max = 3
	g.SetConfig(true, max)

	if !g.Enabled() {
		t.Fatalf("Guard().Enabled() = false after SetConfig(true, %d)", max)
	}

	// Edits 1..max-1 must pass — the gate fires only when the
	// counter REACHES the threshold (post-increment Check).
	for i := 1; i < max; i++ {
		if err := g.OnEdit("a.go"); err != nil {
			t.Fatalf("OnEdit %d returned %v, want nil", i, err)
		}
		if err := g.Check(); err != nil {
			t.Fatalf("Check at counter=%d returned %v, want nil (under threshold)", i, err)
		}
	}

	// The Nth edit trips the gate.
	if err := g.OnEdit("a.go"); err != nil {
		t.Fatalf("OnEdit %d returned %v, want nil (OnEdit never errors)", max, err)
	}
	err := g.Check()
	if err == nil {
		t.Fatalf("Check at counter=%d returned nil, want ErrCheckpointRequired", max)
	}
	if !errors.Is(err, ErrCheckpointRequired) {
		t.Errorf("Check error = %v, want ErrCheckpointRequired in chain", err)
	}

	// The gate stays sticky on subsequent edits — Check keeps
	// returning the sentinel until OnCheckpoint resets the
	// counter. This is what protects an agent from ploughing
	// past a single \"please checkpoint\" stop on repeat tries.
	if err := g.OnEdit("b.go"); err != nil {
		t.Fatalf("OnEdit beyond threshold returned %v, want nil", err)
	}
	if err := g.Check(); !errors.Is(err, ErrCheckpointRequired) {
		t.Errorf("Check after stuck-state edit = %v, want ErrCheckpointRequired", err)
	}
}

// TestGuard_AutocommitResetsCounter verifies the OnCheckpoint
// hook clears the edit counter so a freshly-landed `wip!:` (or
// real) commit unblocks the agent. The autocommit primitive
// (Autocommit in autocommit.go) calls OnCheckpoint on success;
// here we call it directly because spinning up a real git repo
// adds a `git not on PATH` skip path that obscures the test's
// intent. The Autocommit→OnCheckpoint wire is exercised by
// TestAutocommit_PrependsWipPrefix's git-backed flow.
func TestGuard_AutocommitResetsCounter(t *testing.T) {
	g := Guard()
	t.Cleanup(g.Reset)

	const max = 5
	g.SetConfig(true, max)

	// Walk the counter to the threshold so Check returns the
	// gate sentinel, mimicking an agent that just ran out of
	// budget.
	for i := 0; i < max; i++ {
		_ = g.OnEdit("x.go")
	}
	if err := g.Check(); !errors.Is(err, ErrCheckpointRequired) {
		t.Fatalf("pre-checkpoint Check = %v, want ErrCheckpointRequired", err)
	}
	if c := g.Counter(); c != max {
		t.Fatalf("pre-checkpoint Counter = %d, want %d", c, max)
	}

	// Autocommit lands → counter resets → next Check is clean.
	g.OnCheckpoint()
	if c := g.Counter(); c != 0 {
		t.Errorf("post-checkpoint Counter = %d, want 0", c)
	}
	if err := g.Check(); err != nil {
		t.Errorf("post-checkpoint Check = %v, want nil (counter reset)", err)
	}

	// And the operator can keep editing past the reset point —
	// the counter walks again from zero, the gate fires on the
	// new Nth edit just like a fresh session.
	for i := 1; i < max; i++ {
		_ = g.OnEdit("x.go")
		if err := g.Check(); err != nil {
			t.Fatalf("post-reset Check at counter=%d = %v, want nil", i, err)
		}
	}
	_ = g.OnEdit("x.go")
	if err := g.Check(); !errors.Is(err, ErrCheckpointRequired) {
		t.Errorf("post-reset Check at threshold = %v, want ErrCheckpointRequired", err)
	}
}

// TestGuard_SetConfigSubstitutesDefault verifies that a zero or
// negative MaxEditsWithoutCheckpoint falls back to the package
// default (5). This is the contract that lets a config.toml
// literally `max_edits_without_checkpoint = 0` round-trip as
// "use default" rather than "disable" — the Enabled flag is the
// disable switch.
func TestGuard_SetConfigSubstitutesDefault(t *testing.T) {
	g := Guard()
	t.Cleanup(g.Reset)

	g.SetConfig(true, 0)
	// Walk to the package default; the (default-1)-th Check
	// must be clean, the default-th must trip.
	for i := 1; i < DefaultMaxEditsWithoutCheckpoint; i++ {
		_ = g.OnEdit("x.go")
		if err := g.Check(); err != nil {
			t.Fatalf("Check at counter=%d (default fallback) = %v, want nil", i, err)
		}
	}
	_ = g.OnEdit("x.go")
	if err := g.Check(); !errors.Is(err, ErrCheckpointRequired) {
		t.Errorf("Check at default threshold = %v, want ErrCheckpointRequired", err)
	}

	// Negative is also normalised to default.
	g.Reset()
	g.SetConfig(true, -1)
	for i := 0; i < DefaultMaxEditsWithoutCheckpoint; i++ {
		_ = g.OnEdit("x.go")
	}
	if err := g.Check(); !errors.Is(err, ErrCheckpointRequired) {
		t.Errorf("Check after negative-N config = %v, want ErrCheckpointRequired", err)
	}
}

// TestGuard_ResetClearsCounterOnReconfig asserts SetConfig itself
// resets the counter — so an operator who flips the toggle off
// then back on doesn't immediately trip the gate from a stale
// counter accumulated under the previous (or off) state.
func TestGuard_ResetClearsCounterOnReconfig(t *testing.T) {
	g := Guard()
	t.Cleanup(g.Reset)

	g.SetConfig(true, 3)
	_ = g.OnEdit("x.go")
	_ = g.OnEdit("x.go")
	if c := g.Counter(); c != 2 {
		t.Fatalf("pre-reconfig Counter = %d, want 2", c)
	}

	// Re-call SetConfig (could be a config reload) — counter
	// must zero out. The test pins this so a future change
	// that drops the counter-reset doesn't ship without
	// updating its callers.
	g.SetConfig(true, 3)
	if c := g.Counter(); c != 0 {
		t.Errorf("post-reconfig Counter = %d, want 0", c)
	}
}
