package portal

import (
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/config"
)

func validPortal() config.PortalConfig {
	return config.PortalConfig{
		Name:         "my-deepseek",
		BaseURL:      "https://chat.deepseek.com/",
		SecretsScope: "portal.my-deepseek",
		Selectors: config.PortalSelectors{
			Input:  "textarea",
			Submit: "button[type='submit']",
		},
		ResponseDonePredicate: config.PortalPredicate{
			Type:  PredicateEvalTruthy,
			Value: "document.querySelector('textarea')?.value === ''",
		},
	}
}

func TestValidate_OK(t *testing.T) {
	if err := Validate("my-deepseek", validPortal()); err != nil {
		t.Fatalf("expected valid portal, got %v", err)
	}
}

func TestValidate_RequiresBaseURL(t *testing.T) {
	p := validPortal()
	p.BaseURL = ""
	err := Validate("p", p)
	if err == nil || !strings.Contains(err.Error(), "base_url") {
		t.Fatalf("expected base_url error, got %v", err)
	}
}

func TestValidate_RejectsNonHTTP(t *testing.T) {
	p := validPortal()
	p.BaseURL = "ftp://nope"
	err := Validate("p", p)
	if err == nil || !strings.Contains(err.Error(), "http") {
		t.Fatalf("expected scheme error, got %v", err)
	}
}

func TestValidate_RequiresSecretsScopePrefix(t *testing.T) {
	p := validPortal()
	p.SecretsScope = "wrong-prefix"
	err := Validate("p", p)
	if err == nil || !strings.Contains(err.Error(), "portal.") {
		t.Fatalf("expected scope-prefix error, got %v", err)
	}
}

func TestValidate_RequiresInputSelector(t *testing.T) {
	p := validPortal()
	p.Selectors.Input = ""
	err := Validate("p", p)
	if err == nil || !strings.Contains(err.Error(), "selectors.input") {
		t.Fatalf("expected input-selector error, got %v", err)
	}
}

func TestValidate_RejectsBadPredicateType(t *testing.T) {
	p := validPortal()
	p.ResponseDonePredicate.Type = "what_even"
	err := Validate("p", p)
	if err == nil || !strings.Contains(err.Error(), "response_done_predicate.type") {
		t.Fatalf("expected predicate type error, got %v", err)
	}
}

func TestValidate_RequiresResponseDone(t *testing.T) {
	p := validPortal()
	p.ResponseDonePredicate.Type = ""
	err := Validate("p", p)
	if err == nil || !strings.Contains(err.Error(), "response_done_predicate") {
		t.Fatalf("expected response-done error, got %v", err)
	}
}

func TestDefaults_FillsHoles(t *testing.T) {
	p := validPortal()
	Defaults(&p)
	if p.StartURL != p.BaseURL {
		t.Errorf("StartURL should default to BaseURL, got %q", p.StartURL)
	}
	if p.TimeoutMs != DefaultTimeoutMs {
		t.Errorf("TimeoutMs default = %d, want %d", p.TimeoutMs, DefaultTimeoutMs)
	}
	if p.Browser.ViewportWidth != DefaultViewportWidth {
		t.Errorf("Viewport width default = %d", p.Browser.ViewportWidth)
	}
	if p.Browser.Locale != DefaultLocale {
		t.Errorf("Locale default = %q", p.Browser.Locale)
	}
}

func TestParseCookies_Array(t *testing.T) {
	raw := `[{"name":"sessionid","value":"abc","domain":".deepseek.com","secure":true,"httpOnly":true},
	         {"name":"cf_clearance","value":"def","domain":".deepseek.com"}]`
	got, err := ParseCookies(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "sessionid" || got[1].Name != "cf_clearance" {
		t.Fatalf("unexpected cookies: %+v", got)
	}
	if !got[0].HTTPOnly {
		t.Error("httpOnly flag should round-trip")
	}
}

func TestParseCookies_SingleObject(t *testing.T) {
	got, err := ParseCookies(`{"name":"only","value":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "only" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestParseCookies_Empty(t *testing.T) {
	got, err := ParseCookies("   ")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("empty input should yield nil cookies, got %+v", got)
	}
}

func TestParseCookies_BadShape(t *testing.T) {
	if _, err := ParseCookies("not json"); err == nil {
		t.Error("expected error on garbage input")
	}
}

func TestAssertAuthCookies_AllPresent(t *testing.T) {
	have := []Cookie{{Name: "sessionid"}, {Name: "cf_clearance"}}
	if err := AssertAuthCookies(have, []string{"sessionid", "cf_clearance"}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestAssertAuthCookies_Missing(t *testing.T) {
	have := []Cookie{{Name: "sessionid"}}
	err := AssertAuthCookies(have, []string{"sessionid", "cf_clearance"})
	if err == nil || !strings.Contains(err.Error(), "cf_clearance") {
		t.Fatalf("expected missing-name error, got %v", err)
	}
}

func TestNames_Sorted(t *testing.T) {
	cfg := config.Config{Portals: map[string]config.PortalConfig{
		"zebra":  {},
		"apple":  {},
		"mango":  {},
		"banana": {},
	}}
	got := Names(cfg)
	want := []string{"apple", "banana", "mango", "zebra"}
	for i, n := range want {
		if got[i] != n {
			t.Fatalf("Names()[%d]=%q want %q", i, got[i], n)
		}
	}
}
