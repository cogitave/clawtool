package portal

import (
	"context"
	"strings"
	"testing"

	"github.com/cogitave/clawtool/internal/config"
)

// chromedp's exec / remote allocators need a real browser to talk
// to, so the unit tests here cover the pieces we own:
//   - predicate-expression generation (pure function)
//   - jsString escaping (pure function)
//   - obscura ws:// banner scanner (pipe-based, no browser)
//   - typeAndSubmit's input-fill JS template (string assertions)
//
// Integration smoke against a real Chrome / Obscura is gated by
// the operator running `make integration` with the binaries
// installed.

func TestPredicateExpression_SelectorExists(t *testing.T) {
	got, err := predicateExpression(config.PortalPredicate{Type: PredicateSelectorExists, Value: "textarea"})
	if err != nil {
		t.Fatal(err)
	}
	want := `!!document.querySelector("textarea")`
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestPredicateExpression_SelectorVisible(t *testing.T) {
	got, err := predicateExpression(config.PortalPredicate{Type: PredicateSelectorVisible, Value: "textarea"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "offsetParent !== null") {
		t.Errorf("selector_visible should check offsetParent: %q", got)
	}
	if !strings.Contains(got, `"textarea"`) {
		t.Errorf("selector_visible should embed JS-escaped selector: %q", got)
	}
}

func TestPredicateExpression_EvalTruthy_PassesThrough(t *testing.T) {
	got, err := predicateExpression(config.PortalPredicate{Type: PredicateEvalTruthy, Value: "1+1"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "1+1" {
		t.Errorf("eval_truthy should return Value verbatim, got %q", got)
	}
}

func TestPredicateExpression_RejectsUnknown(t *testing.T) {
	if _, err := predicateExpression(config.PortalPredicate{Type: "what_even", Value: "x"}); err == nil {
		t.Fatal("expected error for unknown predicate type")
	}
}

func TestJSString_Escapes(t *testing.T) {
	got := jsString(`hello "world"\n`)
	want := `"hello \"world\"\\n"`
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestJSString_EmbedsCSSSelectors(t *testing.T) {
	// Selector-shaped strings must round-trip cleanly through
	// jsString since we splice them into JS source via fmt.
	for _, sel := range []string{
		`textarea`,
		`button[type='submit']`,
		`[data-message-author-role="assistant"]`,
		`div[class*='markdown'] > p:last-child`,
	} {
		got := jsString(sel)
		if !strings.HasPrefix(got, `"`) || !strings.HasSuffix(got, `"`) {
			t.Errorf("jsString(%q) should produce a JSON string literal: %q", sel, got)
		}
	}
}

func TestReadObscuraWS_FindsURLOnFirstLine(t *testing.T) {
	r, w := pipePair(t)
	go func() {
		_, _ = w.Write([]byte("DevTools listening on ws://127.0.0.1:9222/devtools/browser/abc\n"))
		_ = w.Close()
	}()
	got, err := readObscuraWS(r, 1_000_000_000) // 1s
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "ws://127.0.0.1:9222/") {
		t.Errorf("expected ws:// URL, got %q", got)
	}
}

func TestReadObscuraWS_TimesOutOnSilentStream(t *testing.T) {
	r, _ := pipePair(t)                    // never written to; reader blocks
	_, err := readObscuraWS(r, 50_000_000) // 50ms
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestAsk_RejectsInvalidPortal(t *testing.T) {
	bad := config.PortalConfig{Name: "x", BaseURL: ""} // missing required fields
	_, err := Ask(context.Background(), bad, "hi", AskOptions{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

// pipePair returns a pair (reader, writer) the test can use to
// simulate the obscura stderr stream. Wraps os.Pipe with cleanup.
func pipePair(t *testing.T) (rc readCloser, wc writeCloser) {
	t.Helper()
	r, w, err := osPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close(); _ = w.Close() })
	return r, w
}
