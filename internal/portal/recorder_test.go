package portal

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/config"
)

// fakeRecorderBrowser implements RecorderBrowser without spawning
// Chrome. Tracks navigation + returns canned cookies + selector
// hits keyed on the JS expression so the heuristic logic can be
// exercised offline.
type fakeRecorderBrowser struct {
	navigated string
	cookies   []Cookie
	evals     map[string]string
	closed    bool
}

func (f *fakeRecorderBrowser) Navigate(_ context.Context, url string) error {
	f.navigated = url
	return nil
}
func (f *fakeRecorderBrowser) Cookies(context.Context) ([]Cookie, error) {
	return f.cookies, nil
}
func (f *fakeRecorderBrowser) EvaluateString(_ context.Context, expr string) (string, error) {
	if v, ok := f.evals[expr]; ok {
		return v, nil
	}
	return "", nil
}
func (f *fakeRecorderBrowser) Close() { f.closed = true }

func TestRecord_HappyPath_PopulatesAllFields(t *testing.T) {
	b := &fakeRecorderBrowser{
		cookies: []Cookie{
			{Name: "sessionid", Value: "abc", Domain: ".example.com", HTTPOnly: true},
			{Name: "tracker", Value: "x", Domain: ".other.com", HTTPOnly: false},
		},
		evals: map[string]string{
			heuristicInputSelectorJS:    "textarea#prompt",
			heuristicSubmitSelectorJS:   "button[type=\"submit\"]",
			heuristicResponseSelectorJS: "[data-message-author-role=\"assistant\"]",
		},
	}
	rec, err := Record(context.Background(), "deepseek", "https://chat.example.com/", RecordOptions{
		Browser:      b,
		ConfirmLogin: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if rec.Name != "deepseek" || rec.URL != "https://chat.example.com/" {
		t.Errorf("name/url not propagated: %+v", rec)
	}
	if rec.Selectors.Input != "textarea#prompt" {
		t.Errorf("Input selector wrong: %q", rec.Selectors.Input)
	}
	if rec.Selectors.Response == "" {
		t.Errorf("Response selector empty")
	}
	if len(rec.Cookies) != 1 || rec.Cookies[0].Name != "sessionid" {
		t.Errorf("cookies not filtered to host: %+v", rec.Cookies)
	}
	if !contains(rec.AuthCookieNames, "sessionid") {
		t.Errorf("auth-name autodetect missed sessionid: %v", rec.AuthCookieNames)
	}

	// ToPortalConfig produces a Validate-clean config.
	cfg := rec.ToPortalConfig()
	if err := Validate(rec.Name, cfg); err != nil {
		t.Errorf("ToPortalConfig output rejected by Validate: %v", err)
	}
	if cfg.SecretsScope != SecretsScopePrefix+"deepseek" {
		t.Errorf("SecretsScope wrong: %q", cfg.SecretsScope)
	}
}

func TestRecord_RejectsBadURL(t *testing.T) {
	if _, err := Record(context.Background(), "x", "not-a-url", RecordOptions{
		Browser:      &fakeRecorderBrowser{},
		ConfirmLogin: func(context.Context) error { return nil },
	}); err == nil {
		t.Fatal("expected URL validation error")
	}
}

func TestRecord_PropagatesConfirmLoginError(t *testing.T) {
	want := errors.New("operator cancelled")
	_, err := Record(context.Background(), "x", "https://x.example/", RecordOptions{
		Browser:      &fakeRecorderBrowser{},
		ConfirmLogin: func(context.Context) error { return want },
	})
	if !errors.Is(err, want) {
		t.Errorf("expected %v, got %v", want, err)
	}
}

func TestRecord_FallsBackToTextareaWhenHeuristicEmpty(t *testing.T) {
	// When the page exposes nothing the heuristic likes, the
	// recorder must still produce a Validate-clean PortalConfig
	// — Selectors.Input is required, so we fall back to the
	// generic "textarea" sentinel.
	b := &fakeRecorderBrowser{} // empty evals → heuristics return ""
	rec, err := Record(context.Background(), "x", "https://x.example/", RecordOptions{
		Browser:      b,
		ConfirmLogin: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if rec.Selectors.Input != "textarea" {
		t.Errorf("expected fallback Input=textarea, got %q", rec.Selectors.Input)
	}
}

func TestFilterCookiesForHostRecord_KeepsHostOnly(t *testing.T) {
	in := []Cookie{
		{Name: "a", Domain: ".example.com"},
		{Name: "b", Domain: "chat.example.com"},
		{Name: "c", Domain: ".other.com"},
		{Name: "d", Domain: ""},
	}
	got := filterCookiesForHostRecord(in, "chat.example.com")
	names := []string{}
	for _, c := range got {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "a,b,d" {
		t.Errorf("filterCookiesForHostRecord wrong: %v", names)
	}
}

func TestAutoDetectAuthCookieNamesRecord_PicksHTTPOnlyAuthLike(t *testing.T) {
	in := []Cookie{
		{Name: "sessionid", HTTPOnly: true},
		{Name: "auth_token", HTTPOnly: true},
		{Name: "csrf", HTTPOnly: true},
		{Name: "ga_tracker", HTTPOnly: false}, // not httpOnly → drop
		{Name: "preferences", HTTPOnly: true}, // no auth keyword → drop
	}
	got := autoDetectAuthCookieNamesRecord(in)
	for _, want := range []string{"sessionid", "auth_token", "csrf"} {
		if !contains(got, want) {
			t.Errorf("expected %q in %v", want, got)
		}
	}
	if contains(got, "preferences") || contains(got, "ga_tracker") {
		t.Errorf("recorder picked up non-auth cookie: %v", got)
	}
}

// guard against accidentally serialising a recording with an empty
// SecretsScope (would defeat the cookie-restore path).
func TestRecording_ToPortalConfig_AlwaysSetsSecretsScope(t *testing.T) {
	rec := &Recording{
		Name: "r",
		URL:  "https://x.example/",
		Selectors: config.PortalSelectors{
			Input:    "textarea",
			Response: "[data-x]",
		},
		ResponseDonePredicate: config.PortalPredicate{
			Type:  PredicateSelectorVisible,
			Value: "textarea",
		},
	}
	cfg := rec.ToPortalConfig()
	if cfg.SecretsScope != SecretsScopePrefix+"r" {
		t.Errorf("SecretsScope = %q", cfg.SecretsScope)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
