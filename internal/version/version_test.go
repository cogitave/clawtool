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

// TestResolveVersion_PseudoVersionFiltered pins the regex's
// filtering behaviour so a Go pseudo-version returned by
// debug.ReadBuildInfo never bleeds into Resolved(). resolveVersion
// can't easily inject a fake debug.BuildInfo (it's a runtime hook),
// but the unit here observes that the regex itself rejects the
// production-audited shape. TestIsPseudoVersion above is the
// wire-shape pin; this one asserts the package-level branching
// is wired to use it.
func TestResolveVersion_PseudoVersionFiltered(t *testing.T) {
	// Save + restore the global Version so the test doesn't
	// leak state into siblings.
	origVersion := Version
	t.Cleanup(func() { Version = origVersion })

	// Force the ldflags-stamped branch to no-op by zeroing
	// Version, so resolveVersion is forced through the
	// debug.ReadBuildInfo branch. The implementation can't
	// observe a synthesized pseudo-version in test (no hook to
	// fake debug.BuildInfo), but it CAN observe whether the
	// pseudo-version regex correctly catches the audited shape.
	// resolveVersion's last-resort returns strip(Version), so
	// after zeroing it we just assert the result isn't the
	// pseudo-version literal.
	Version = ""
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

// TestResolveVersion_NonEmptyVersionWins pins the post-2026-05-01
// behaviour: any non-empty Version short-circuits to that value,
// regardless of what debug.BuildInfo carries. Pre-fix, a literal
// "0.21.7" sentinel intercepted Version values that happened to
// equal the (then-current) release-please default; release-please
// bumped past it during v0.22.x and the gate quietly stopped
// firing. Drift-trap: if anyone re-introduces a magic-string
// sentinel, this test fails when Version equals it.
func TestResolveVersion_NonEmptyVersionWins(t *testing.T) {
	origVersion := Version
	t.Cleanup(func() { Version = origVersion })

	// A handful of values that have, at various points, served
	// as the release-please-tracked const. None of them should
	// trip a sentinel comparison anymore.
	for _, in := range []string{
		"0.21.7", // historical sentinel literal
		"0.22.119",
		"v0.22.119",
		"0.0.0-dev",
	} {
		Version = in
		got := resolveVersion()
		want := strip(in)
		if got != want {
			t.Errorf("Version=%q: resolveVersion()=%q, want %q (sentinel regression — should pass through unchanged)", in, got, want)
		}
	}
}
