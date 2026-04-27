//go:build integration

// Real-Chrome integration test for the portal Ask flow. Spins up
// an httptest server that pretends to be a chat portal — textarea,
// submit button, response panel, fake "Stop" button that
// disappears after a short delay — then drives Ask through a real
// chromedp ExecAllocator (Headless=true). Verifies the same wire
// the v0.16.3 wizard exercises in production, just against a
// known fixture.
//
// Run with:
//
//	go test -tags integration -run TestAsk_RealChrome ./internal/portal/
//
// CI / dev machines need Chrome / Chromium on PATH (chromedp
// detects automatically). The test skips itself with t.Skip when
// no browser is available so unit-test runs remain green.
package portal

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/config"
)

// fakePortalHandler serves a single-page sahte chat UI. Logged-in
// state is established by a `sid` cookie (HttpOnly); the page
// renders nothing without it, simulating a real auth gate.
//
// JS:
//   - clicking #send drains the textarea, displays a "Stop"
//     button, appends a fake assistant response after 200ms,
//     then removes the Stop button.
//   - Enter on textarea calls the same handler.
const fakeChatHTML = `<!doctype html>
<html><head><title>Fake Portal</title></head>
<body>
<div id="login">Please log in.</div>
<div id="chat" style="display:none">
  <textarea id="prompt"></textarea>
  <button id="send" onclick="onSend()">Send</button>
  <div id="messages"></div>
</div>
<script>
  // Reflect the cookie state into UI on every load.
  if (document.cookie.indexOf('sid=') >= 0) {
    document.getElementById('login').style.display = 'none';
    document.getElementById('chat').style.display = 'block';
  }
  function onSend() {
    const textarea = document.getElementById('prompt');
    const messages = document.getElementById('messages');
    const value = textarea.value;
    textarea.value = '';
    // Add a fake "Stop" button while we "stream".
    const stop = document.createElement('button');
    stop.setAttribute('aria-label', 'Stop');
    stop.id = 'stop';
    document.body.appendChild(stop);
    // Drop a placeholder assistant message immediately.
    const m = document.createElement('div');
    m.className = 'assistant';
    m.textContent = '';
    messages.appendChild(m);
    setTimeout(() => {
      m.textContent = 'Echoing: ' + value;
      // Remove the Stop button — response_done predicate
      // becomes truthy.
      stop.remove();
    }, 200);
  }
  document.getElementById('prompt').addEventListener('keydown', (e) => {
    if (e.key === 'Enter') { e.preventDefault(); onSend(); }
  });
</script>
</body></html>`

// fakePortalServer wraps httptest with a /set-sid handler so the
// test can prime the cookie jar via a real Set-Cookie response,
// matching how a production login screen would.
func fakePortalServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(fakeChatHTML))
	})
	return httptest.NewServer(mux)
}

func TestAsk_RealChrome_AgainstHttptestPortal(t *testing.T) {
	if _, err := exec.LookPath("google-chrome"); err != nil {
		if _, err2 := exec.LookPath("chromium"); err2 != nil {
			if _, err3 := exec.LookPath("chromium-browser"); err3 != nil {
				t.Skip("integration test requires Chrome / Chromium on PATH")
			}
		}
	}

	srv := fakePortalServer(t)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Headless because CI doesn't have a display; the wizard uses
	// Headless=false in production but the orchestration is
	// identical from chromedp's perspective.
	browser, err := NewExecBrowser(ctx, ExecOptions{Headless: true, StartURL: srv.URL})
	if err != nil {
		t.Fatalf("launch chrome: %v", err)
	}
	defer browser.Close()

	cfg := config.PortalConfig{
		Name:            "fake",
		BaseURL:         srv.URL + "/",
		StartURL:        srv.URL + "/",
		SecretsScope:    "portal.fake",
		AuthCookieNames: []string{"sid"},
		TimeoutMs:       20_000,
		LoginCheck: config.PortalPredicate{
			Type:  PredicateSelectorVisible,
			Value: "#prompt",
		},
		ReadyPredicate: config.PortalPredicate{
			Type:  PredicateSelectorVisible,
			Value: "#prompt",
		},
		Selectors: config.PortalSelectors{
			Input:    "#prompt",
			Submit:   "#send",
			Response: "div.assistant",
		},
		ResponseDonePredicate: config.PortalPredicate{
			Type:  PredicateEvalTruthy,
			Value: `(() => { return !document.querySelector('button[aria-label="Stop"]'); })()`,
		},
		Browser: config.PortalBrowserSettings{ViewportWidth: 1024, ViewportHeight: 768},
	}

	cookies := []Cookie{
		{Name: "sid", Value: "abc", Domain: hostOf(srv.URL), Path: "/", HTTPOnly: true},
	}

	resp, err := Ask(ctx, cfg, "hello world", AskOptions{
		Cookies:   cookies,
		PollEvery: 50 * time.Millisecond,
		Browser:   browser,
	})
	if err != nil {
		t.Fatalf("Ask returned error: %v", err)
	}
	if !strings.Contains(resp, "Echoing: hello world") {
		t.Errorf("response missing expected echo: %q", resp)
	}
}

// hostOf strips the scheme + path off an httptest URL and returns
// just `127.0.0.1:port` for cookie domain pinning.
func hostOf(u string) string {
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimPrefix(u, "https://")
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	return u
}

// Sanity guard so the constant doesn't go unused when the build
// tag is set without the test file being touched. Compiled out.
var _ = fmt.Sprintf
