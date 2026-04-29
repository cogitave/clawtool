package a2a

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewCard_DefaultName(t *testing.T) {
	c := NewCard(CardOptions{})
	if c.Name != "clawtool" {
		t.Errorf("default name = %q, want %q", c.Name, "clawtool")
	}
	if c.Version == "" {
		t.Error("Version is empty — should pick up from internal/version.Version")
	}
	if c.ProtocolVersion != CurrentProtocolVersion {
		t.Errorf("ProtocolVersion = %q, want %q", c.ProtocolVersion, CurrentProtocolVersion)
	}
	if len(c.Skills) != 5 {
		t.Errorf("default canonical skill count = %d, want 5", len(c.Skills))
	}
	if c.PublishedAt.IsZero() {
		t.Error("PublishedAt is zero — should be UTC now")
	}
	if c.PublishedAt.Location() != time.UTC {
		t.Errorf("PublishedAt location = %v, want UTC", c.PublishedAt.Location())
	}
}

func TestNewCard_NameOverride(t *testing.T) {
	c := NewCard(CardOptions{Name: "  my-instance  "})
	if c.Name != "my-instance" {
		t.Errorf("Name = %q, want trimmed 'my-instance'", c.Name)
	}
}

func TestNewCard_ExtraSkillsAppended(t *testing.T) {
	extra := Skill{ID: "weather", Name: "Weather", Description: "Forecast lookup."}
	c := NewCard(CardOptions{ExtraSkills: []Skill{extra}})
	if len(c.Skills) != 6 {
		t.Errorf("len = %d, want canonical(5)+extra(1)=6", len(c.Skills))
	}
	last := c.Skills[len(c.Skills)-1]
	if last.ID != "weather" {
		t.Errorf("extra skill not appended last; got %q", last.ID)
	}
}

func TestCard_PhaseOneCapabilities_AreAllFalse(t *testing.T) {
	c := NewCard(CardOptions{})
	// Phase 1 advertises ONLY card-only mode. Streaming /
	// PushNotifications / StateTransitionHistory all flip on
	// when phase 2+ ships an actual HTTP server. Until then
	// we MUST advertise false so peers don't try to use what
	// we can't deliver.
	if c.Capabilities.Streaming {
		t.Error("phase 1: Streaming must be false")
	}
	if c.Capabilities.PushNotifications {
		t.Error("phase 1: PushNotifications must be false")
	}
	if c.Capabilities.StateTransitionHistory {
		t.Error("phase 1: StateTransitionHistory must be false")
	}
}

func TestCard_DefaultModesAreTextAndJSON(t *testing.T) {
	c := NewCard(CardOptions{})
	for _, want := range []string{"text/plain", "application/json"} {
		found := false
		for _, m := range c.DefaultInputModes {
			if m == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("DefaultInputModes missing %q: %v", want, c.DefaultInputModes)
		}
	}
}

func TestCard_MarshalJSON_RoundTrips(t *testing.T) {
	c := NewCard(CardOptions{Name: "test", URL: "https://example.invalid/a2a"})
	body, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back Card
	if err := json.Unmarshal(body, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Name != "test" || back.URL != "https://example.invalid/a2a" {
		t.Errorf("round-trip lost fields: %+v", back)
	}
	if len(back.Skills) != len(c.Skills) {
		t.Errorf("skill count drift: %d vs %d", len(c.Skills), len(back.Skills))
	}
}

func TestCard_MarshalIndented_IsHumanReadable(t *testing.T) {
	c := NewCard(CardOptions{})
	body, err := c.MarshalIndented()
	if err != nil {
		t.Fatalf("MarshalIndented: %v", err)
	}
	src := string(body)
	if !strings.Contains(src, "\n  \"name\":") {
		t.Errorf("indented output should have 2-space indent: %s", src[:min(200, len(src))])
	}
}

func TestCanonicalSkills_HaveIDsAndDescriptions(t *testing.T) {
	skills := canonicalSkills()
	seen := map[string]bool{}
	for _, s := range skills {
		if s.ID == "" {
			t.Errorf("skill %q missing ID", s.Name)
		}
		if s.Name == "" || s.Description == "" {
			t.Errorf("skill %q has empty Name or Description", s.ID)
		}
		if seen[s.ID] {
			t.Errorf("duplicate skill ID: %q", s.ID)
		}
		seen[s.ID] = true
	}
	// Sanity: the five canonical IDs.
	expected := []string{"research", "code-read", "code-edit", "agent-dispatch", "shell"}
	for _, id := range expected {
		if !seen[id] {
			t.Errorf("canonical skill %q missing", id)
		}
	}
}
