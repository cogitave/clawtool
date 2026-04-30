package setup

import "testing"

// TestMeta_CoreField pins the existence of the Core boolean on
// RecipeMeta. Zero-value must be false so unset / experimental
// recipes stay opt-in. Setting it explicitly must round-trip to the
// struct without coercion.
func TestMeta_CoreField(t *testing.T) {
	zero := RecipeMeta{}
	if zero.Core {
		t.Errorf("zero-value RecipeMeta.Core = true, want false (default opt-in must be opt-OUT-only)")
	}

	withCore := RecipeMeta{
		Name:        "x",
		Category:    CategoryGovernance,
		Description: "x",
		Upstream:    "https://example.com",
		Stability:   StabilityBeta,
		Core:        true,
	}
	if !withCore.Core {
		t.Errorf("RecipeMeta{Core: true}.Core = false, want true (field must round-trip)")
	}
	// Setting Core true with Beta stability is the headline use
	// case (operator wants Beta defaults to ship). Pin that the
	// combination is legal — i.e. nothing in the type system
	// forbids it.
	if withCore.Stability != StabilityBeta {
		t.Errorf("Stability lost: got %q", withCore.Stability)
	}
}
