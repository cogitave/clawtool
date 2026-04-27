package bridges

import (
	"context"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/setup"
)

func TestBridgesRegistered(t *testing.T) {
	want := map[string]bool{"codex": false, "opencode": false, "gemini": false, "hermes": false}
	for _, fam := range Families() {
		if _, ok := want[fam]; ok {
			want[fam] = true
		}
	}
	for fam, found := range want {
		if !found {
			t.Errorf("expected bridge family %q registered", fam)
		}
	}
}

func TestLookupByFamily_KnownAndUnknown(t *testing.T) {
	for _, fam := range []string{"codex", "opencode", "gemini", "hermes"} {
		r := LookupByFamily(fam)
		if r == nil {
			t.Errorf("LookupByFamily(%q) = nil", fam)
			continue
		}
		m := r.Meta()
		if m.Category != setup.CategoryAgents {
			t.Errorf("bridge %q category = %q, want agents", fam, m.Category)
		}
		if m.Upstream == "" {
			t.Errorf("bridge %q has empty Upstream", fam)
		}
	}
	if LookupByFamily("ghost") != nil {
		t.Error("LookupByFamily(\"ghost\") should be nil")
	}
}

func TestLookupByFamily_TrimAndLowercase(t *testing.T) {
	if r := LookupByFamily("  CODEX  "); r == nil {
		t.Error("LookupByFamily should be case-insensitive and trim whitespace")
	}
}

func TestBridgeMeta_DescriptionsAreNonEmpty(t *testing.T) {
	for _, fam := range Families() {
		r := LookupByFamily(fam)
		if r == nil {
			continue
		}
		m := r.Meta()
		if strings.TrimSpace(m.Description) == "" {
			t.Errorf("bridge %q has empty description", fam)
		}
		if !strings.Contains(strings.ToLower(m.Description), fam) {
			t.Errorf("bridge %q description should mention the family name; got %q", fam, m.Description)
		}
	}
}

// TestOpencodeBridge_BinaryOnly verifies that the opencode bridge's
// Detect path looks at PATH (not at `claude plugin list`), since
// opencode acp ships in the upstream binary itself.
func TestOpencodeBridge_BinaryOnly(t *testing.T) {
	r := LookupByFamily("opencode")
	if r == nil {
		t.Fatal("opencode bridge missing")
	}
	// Detect should NOT call `claude plugin list` for opencode; if
	// it tried to and `claude` is missing, Detect would return Error.
	// We don't assert the exact status (depends on whether
	// `opencode` happens to be on PATH on the test machine), only
	// that we don't error out via the claude path.
	_, _, err := r.Detect(context.Background(), "")
	if err != nil {
		t.Errorf("opencode bridge Detect should not error on missing claude; got %v", err)
	}
}
