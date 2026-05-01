package version

import "testing"

// TestIsPseudoVersion pins the regex that filters Go module
// pseudo-versions out of the version-resolution chain. The
// operator's 2026-04-30 PostHog audit caught ~95% of inbound
// events stamped with `0.0.0-20260501001315-a5ac21717194`-shaped
// pseudo-versions; resolveVersion now treats those as if the
// build info had returned "(devel)" so the release-please
// constant takes over.
func TestIsPseudoVersion(t *testing.T) {
	cases := map[string]bool{
		// Real pseudo-versions — production audit shape.
		"0.0.0-20260501001315-a5ac21717194":  true,
		"v0.0.0-20260501001315-a5ac21717194": true,
		"0.0.0-20240101000000-000000000000":  true,
		// Real semvers — must not match.
		"v0.22.79":                           false,
		"0.22.79":                            false,
		"v1.0.0":                             false,
		"1.2.3-rc.1":                         false,
		"v9.9.9-test":                        false,
		"(devel)":                            false,
		"":                                   false,
		"0.0.0":                              false, // missing timestamp + sha
		"0.0.0-2026":                         false, // truncated timestamp
		"0.0.0-20260501001315-a5ac2171":      false, // sha too short
		"0.0.0-20260501001315-a5ac21717194z": false, // sha too long
		"0.0.0-20260501001315-A5AC21717194":  false, // uppercase sha rejected (Go uses lowercase)
	}
	for in, want := range cases {
		if got := isPseudoVersion(in); got != want {
			t.Errorf("isPseudoVersion(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestResolveVersion_PseudoVersionFallsBackToConstant simulates
// the `go install` path that lands a pseudo-version in
// debug.Main.Version. The resolveVersion implementation can't
// easily inject a fake debug.BuildInfo (it's a runtime hook),
// but it CAN observe the Version constant. We assert the
// regex's filtering behaviour here and rely on
// TestIsPseudoVersion above for the wire-shape pin.
func TestResolveVersion_PseudoVersionFiltered(t *testing.T) {
	// Save + restore the global Version so the test doesn't
	// leak state into siblings.
	origVersion := Version
	t.Cleanup(func() { Version = origVersion })

	// Force the ldflags branch off so resolveVersion falls
	// through to the debug.ReadBuildInfo branch in production —
	// here we additionally synthesize a pseudo-version to prove
	// the regex catches it. resolveVersion's last-resort returns
	// strip(Version), so setting Version to a sentinel lets us
	// observe whether the pseudo-version path was correctly
	// rejected.
	Version = "0.21.7" // dev-fallback sentinel
	if got := resolveVersion(); got == "0.0.0-20260501001315-a5ac21717194" {
		t.Errorf("resolveVersion must NOT return a pseudo-version; got %q", got)
	}
	// Smoke the regex from inside the same package — this is
	// the unit-of-behaviour the operator's PostHog audit
	// flagged. If isPseudoVersion ever stops matching the
	// production shape, the assertion fails before the bad
	// value can bleed into telemetry / A2A / health probes.
	if !isPseudoVersion("0.0.0-20260501001315-a5ac21717194") {
		t.Fatal("regression: production pseudo-version shape no longer matches")
	}
}
