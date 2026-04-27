package portal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/config"
)

// fakePortalBrowser drives a minimal in-memory simulation of a chat
// portal page. It implements the Browser interface so portal.Ask
// runs against it end-to-end without spawning Chrome / Obscura.
//
// Behaviour:
//   - SetCookies / SetExtraHTTPHeaders / Navigate record the calls.
//   - login_check / ready_predicate become truthy immediately
//     (login is "already done" because cookies were just set).
//   - response_done_predicate becomes truthy after the
//     submit-mock has been invoked AND `responseReadyAfter` ticks
//     of EvaluateBool have polled it. This simulates the
//     async-streaming behaviour real chat UIs have.
//   - typeAndSubmit's JS template lands as a single Evaluate call
//     and is recognised so the fake "submits" the prompt and
//     queues the canned reply.
type fakePortalBrowser struct {
	mu sync.Mutex

	calls             []string
	cookiesSeeded     []Cookie
	headersSeeded     map[string]string
	navigatedTo       string
	prompt            string
	cannedResponse    string
	submitted         bool
	donePollsRequired int
	donePollsObserved int

	failOn map[string]error // optional: fail a named phase
}

func newFake(canned string) *fakePortalBrowser {
	return &fakePortalBrowser{
		cannedResponse:    canned,
		donePollsRequired: 2, // simulate 2 polls of streaming before "done"
	}
}

func (f *fakePortalBrowser) record(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *fakePortalBrowser) Navigate(_ context.Context, url string) error {
	f.record("Navigate:" + url)
	if err := f.failOn["Navigate"]; err != nil {
		return err
	}
	f.navigatedTo = url
	return nil
}

func (f *fakePortalBrowser) SetCookies(_ context.Context, cookies []Cookie) error {
	f.record(fmt.Sprintf("SetCookies:%d", len(cookies)))
	f.cookiesSeeded = cookies
	return nil
}

func (f *fakePortalBrowser) SetExtraHTTPHeaders(_ context.Context, headers map[string]string) error {
	f.record(fmt.Sprintf("SetExtraHTTPHeaders:%d", len(headers)))
	f.headersSeeded = headers
	return nil
}

// classifyExpr returns a short tag describing what JS the caller
// just evaluated. Used only by the fake to drive realistic
// responses; real Browser implementations don't need this.
//
// Real callers receive expressions BEFORE the chromedp-side
// Boolean() wrap (since the wrap happens inside BrowserSession's
// EvaluateBool, not at our Browser interface boundary). The fake
// gets raw predicate JS, so we detect the four well-known shapes
// and treat everything else as a predicate by default.
func classifyExpr(expr string) string {
	switch {
	case strings.Contains(expr, "setter.set.call(el"):
		return "fill_input"
	case strings.Contains(expr, "b.click()"):
		return "click_submit"
	case strings.Contains(expr, "dispatchEvent(new KeyboardEvent('keydown'"):
		return "dispatch_enter"
	case strings.Contains(expr, "querySelectorAll") && strings.Contains(expr, "innerText"):
		return "extract_response"
	default:
		return "predicate"
	}
}

// markPromptSubmitted is what the fake does when typeAndSubmit
// fires either click_submit or dispatch_enter — flips the bit
// that response_done_predicate checks.
func (f *fakePortalBrowser) markPromptSubmitted() {
	f.mu.Lock()
	f.submitted = true
	f.donePollsObserved = 0
	f.mu.Unlock()
}

func (f *fakePortalBrowser) Evaluate(_ context.Context, expr string, out any) error {
	tag := classifyExpr(expr)
	f.record("Evaluate:" + tag)
	switch tag {
	case "fill_input":
		// Capture the prompt text by parsing it out of the
		// JS template. The template contains `setter.set.call(el, "<json prompt>")`.
		// Cheap to recover with a couple of finds.
		if i := strings.Index(expr, "setter.set.call(el, "); i >= 0 {
			tail := expr[i+len("setter.set.call(el, "):]
			if j := strings.Index(tail, "); }"); j >= 0 {
				var p string
				_ = json.Unmarshal([]byte(strings.TrimSpace(tail[:j])), &p)
				f.prompt = p
			}
		}
		// Caller decodes into a struct {ok bool, reason string}.
		raw := json.RawMessage(`{"ok":true}`)
		return json.Unmarshal(raw, out)
	case "extract_response":
		raw, _ := json.Marshal(f.cannedResponse)
		return json.Unmarshal(raw, out)
	case "click_submit":
		// EvaluateBool path; we actually receive the wrapped
		// Boolean(...) call via Evaluate too, but that goes
		// through the predicate branch. This branch is dead in
		// practice — kept for completeness.
		return json.Unmarshal([]byte("true"), out)
	}
	// Default: unmarshal a true-ish payload.
	return json.Unmarshal([]byte("null"), out)
}

func (f *fakePortalBrowser) EvaluateBool(_ context.Context, expr string) (bool, error) {
	tag := classifyExpr(expr)
	f.record("EvaluateBool:" + tag)

	// EvaluateBool wraps inner JS in Boolean(...). Strip the wrapper
	// so we see the actual selector / predicate body.
	inner := expr
	if strings.HasPrefix(inner, "Boolean(") && strings.HasSuffix(inner, ")") {
		inner = inner[len("Boolean(") : len(inner)-1]
	}

	// Submit / Enter dispatch JS: "click selector" templates
	// resolve here once Boolean()-wrapped.
	if strings.Contains(inner, "b.click()") || strings.Contains(inner, "KeyboardEvent('keydown'") {
		f.markPromptSubmitted()
		return true, nil
	}

	// Predicate: login_check / ready_predicate / response_done.
	// login_check + ready: truthy when navigation has happened
	// (we treat any post-navigate state as "logged in" because
	// the fake just got cookies).
	if !f.submitted {
		// pre-submit predicates always truthy in the fake.
		return f.navigatedTo != "", nil
	}
	// post-submit: response_done_predicate. Require N polls so the
	// test exercises the polling loop.
	f.mu.Lock()
	f.donePollsObserved++
	done := f.donePollsObserved >= f.donePollsRequired
	f.mu.Unlock()
	return done, nil
}

func (f *fakePortalBrowser) EvaluateString(_ context.Context, expr string) (string, error) {
	tag := classifyExpr(expr)
	f.record("EvaluateString:" + tag)
	if tag == "extract_response" {
		return f.cannedResponse, nil
	}
	return "", nil
}

// validPortalForFake — re-uses the wizard's predicate templates
// against an "input is textarea" stub.
func validPortalForFake() config.PortalConfig {
	return config.PortalConfig{
		Name:            "fake",
		BaseURL:         "https://chat.example.com/",
		StartURL:        "https://chat.example.com/",
		SecretsScope:    "portal.fake",
		AuthCookieNames: []string{"sid"},
		TimeoutMs:       30_000,
		LoginCheck: config.PortalPredicate{
			Type:  PredicateSelectorVisible,
			Value: "textarea",
		},
		ReadyPredicate: config.PortalPredicate{
			Type:  PredicateSelectorVisible,
			Value: "textarea",
		},
		Selectors: config.PortalSelectors{
			Input:    "textarea",
			Submit:   "button.send",
			Response: "div.assistant",
		},
		ResponseDonePredicate: config.PortalPredicate{
			Type:  PredicateEvalTruthy,
			Value: `(() => { return !document.querySelector('button[aria-label*="Stop"]'); })()`,
		},
		Headers: map[string]string{"Accept-Language": "en"},
		Browser: config.PortalBrowserSettings{
			Stealth:        true,
			ViewportWidth:  1024,
			ViewportHeight: 768,
			Locale:         "en-US",
		},
	}
}

func TestAsk_FullFlow_AgainstFakeBrowser(t *testing.T) {
	t.Parallel()

	fake := newFake("Hello from the fake portal!")
	cfg := validPortalForFake()
	cookies := []Cookie{
		{Name: "sid", Value: "abc", Domain: ".example.com", HTTPOnly: true},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := Ask(ctx, cfg, "ping", AskOptions{
		Cookies:   cookies,
		PollEvery: 5 * time.Millisecond,
		Browser:   fake,
	})
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}
	if resp != "Hello from the fake portal!" {
		t.Errorf("response wrong: %q", resp)
	}

	// Phase ordering — cookies + headers must come before navigate.
	wantPrefix := []string{
		"SetCookies:1",
		"SetExtraHTTPHeaders:1",
		"Navigate:https://chat.example.com/",
	}
	if len(fake.calls) < len(wantPrefix) {
		t.Fatalf("not enough calls recorded: %v", fake.calls)
	}
	for i, want := range wantPrefix {
		if fake.calls[i] != want {
			t.Errorf("call[%d]=%q, want %q (full sequence: %v)", i, fake.calls[i], want, fake.calls)
		}
	}

	// fill_input must precede the submit click.
	fillIdx := indexOf(fake.calls, "Evaluate:fill_input")
	clickIdx := indexOf(fake.calls, "EvaluateBool:click_submit")
	if fillIdx < 0 || clickIdx < 0 || fillIdx > clickIdx {
		t.Errorf("fill_input must come before click_submit; calls: %v", fake.calls)
	}

	// response_done_predicate must have polled at least the
	// fake's required count.
	doneCount := 0
	for _, c := range fake.calls {
		if c == "EvaluateBool:predicate" {
			doneCount++
		}
	}
	if doneCount < fake.donePollsRequired {
		t.Errorf("predicate polled %d times, want >= %d", doneCount, fake.donePollsRequired)
	}

	// Prompt round-tripped through the fill-input JS template.
	if fake.prompt != "ping" {
		t.Errorf("prompt round-trip failed: got %q want %q", fake.prompt, "ping")
	}

	// Cookies must be the ones we passed in.
	if len(fake.cookiesSeeded) != 1 || fake.cookiesSeeded[0].Name != "sid" {
		t.Errorf("cookies mis-seeded: %+v", fake.cookiesSeeded)
	}
}

func TestAsk_RejectsBeforeBrowser_OnMissingAuthCookie(t *testing.T) {
	t.Parallel()

	fake := newFake("never reached")
	cfg := validPortalForFake() // requires "sid"
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Ask(ctx, cfg, "ping", AskOptions{
		Cookies: nil, // nope, missing required auth name
		Browser: fake,
	})
	if err == nil {
		t.Fatal("expected missing-auth error")
	}
	if !strings.Contains(err.Error(), "sid") {
		t.Errorf("error should name the missing cookie: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Errorf("browser should not have been touched on auth failure: %v", fake.calls)
	}
}

func TestAsk_TimesOutWhenResponseDoneNeverFires(t *testing.T) {
	t.Parallel()

	fake := newFake("never finishes")
	fake.donePollsRequired = 999_999 // predicate never returns true
	cfg := validPortalForFake()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := Ask(ctx, cfg, "ping", AskOptions{
		Cookies:   []Cookie{{Name: "sid", Value: "abc"}},
		PollEvery: 5 * time.Millisecond,
		Browser:   fake,
	})
	if err == nil {
		t.Fatal("expected timeout")
	}
	if !strings.Contains(err.Error(), "response_done_predicate") {
		t.Errorf("error should name the failing phase: %v", err)
	}
}

func TestAsk_EnterFallback_WhenNoSubmitSelector(t *testing.T) {
	t.Parallel()

	fake := newFake("ok")
	cfg := validPortalForFake()
	cfg.Selectors.Submit = "" // → typeAndSubmit dispatches Enter

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := Ask(ctx, cfg, "ping", AskOptions{
		Cookies:   []Cookie{{Name: "sid", Value: "abc"}},
		PollEvery: 5 * time.Millisecond,
		Browser:   fake,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp != "ok" {
		t.Errorf("response: %q", resp)
	}
	enterIdx := indexOf(fake.calls, "EvaluateBool:dispatch_enter")
	if enterIdx < 0 {
		t.Errorf("Enter fallback should have fired; calls: %v", fake.calls)
	}
	if indexOf(fake.calls, "EvaluateBool:click_submit") >= 0 {
		t.Error("click_submit should NOT have fired when Submit selector is empty")
	}
}

func indexOf(haystack []string, needle string) int {
	for i, s := range haystack {
		if s == needle {
			return i
		}
	}
	return -1
}
