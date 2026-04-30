package portal

import (
	"context"
	"errors"
	"testing"
)

// TestBifrostDriver_Registered confirms the package init() seeded
// the driver registry with the bifrost stub. Discovery via
// PortalList depends on this — if the driver fails to register,
// `clawtool portal list` won't surface bifrost, and operators
// won't see the integration path.
func TestBifrostDriver_Registered(t *testing.T) {
	d := LookupDriver(BifrostDriverName)
	if d == nil {
		t.Fatal("bifrost driver should self-register at package init")
	}
	if d.Name() != BifrostDriverName {
		t.Errorf("Name() = %q, want %q", d.Name(), BifrostDriverName)
	}
	if d.Status() != "deferred" {
		t.Errorf("Status() = %q, want \"deferred\" for phase-1 stub", d.Status())
	}
	if d.Description() == "" {
		t.Error("Description() must be non-empty (surfaced in PortalList)")
	}
}

// TestBifrostDriver_AppearsInDriversList asserts the bifrost driver
// shows up in the sorted Drivers() iteration — that's the listing
// PortalList walks to render the merged config + drivers table.
func TestBifrostDriver_AppearsInDriversList(t *testing.T) {
	var found bool
	for _, d := range Drivers() {
		if d.Name() == BifrostDriverName {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Drivers() should include bifrost; got %d entries", len(Drivers()))
	}
}

// TestBifrostDriver_AskReturnsDeferred is the load-bearing assertion
// for phase 1: Ask must NOT silently succeed. It returns the typed
// ErrBifrostDeferred sentinel so CLI / MCP callers can match via
// errors.Is and surface a uniform "phase 2 not landed yet" message
// instead of pretending the gateway worked.
func TestBifrostDriver_AskReturnsDeferred(t *testing.T) {
	d := LookupDriver(BifrostDriverName)
	if d == nil {
		t.Fatal("bifrost driver missing")
	}
	resp, err := d.Ask(context.Background(), "any prompt")
	if err == nil {
		t.Fatal("Ask should return ErrBifrostDeferred, got nil error")
	}
	if !errors.Is(err, ErrBifrostDeferred) {
		t.Errorf("Ask should return ErrBifrostDeferred (errors.Is); got %v", err)
	}
	if resp != "" {
		t.Errorf("deferred Ask should return empty response; got %q", resp)
	}
}

// TestRegisterDriver_PanicsOnDuplicate guards the boot-time fail-
// fast: a second registration of the same name (typo / accidental
// re-import) should panic at process start, not silently shadow
// the first registration.
func TestRegisterDriver_PanicsOnDuplicate(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate driver registration")
		}
	}()
	// bifrost is already registered via init(); re-registering it
	// should panic.
	RegisterDriver(bifrostDriver{})
}

// TestRegisterDriver_PanicsOnNil pins the nil-safety contract.
func TestRegisterDriver_PanicsOnNil(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil driver")
		}
	}()
	RegisterDriver(nil)
}
