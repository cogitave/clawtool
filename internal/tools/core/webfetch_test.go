package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const sampleArticle = `<!DOCTYPE html>
<html>
<head><title>WebFetch Article</title></head>
<body>
<header><nav>Home | About | Login | Subscribe Now</nav></header>
<aside>Newsletter sign-up</aside>
<article>
<h1>WebFetch Article</h1>
<p>The first paragraph of the article body — long enough to give the
Readability algorithm enough textual signal to mark this as the main
content region rather than the chrome around it.</p>
<p>A second paragraph reinforces the topic with concrete sentences and
keeps the textual density high.</p>
</article>
<footer>(c) 2026 Example Co</footer>
</body>
</html>
`

func TestWebFetch_HTML_Readability(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(sampleArticle))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeWebFetch(ctx, srv.URL, 3*time.Second)

	if res.Status != 200 {
		t.Errorf("status = %d, want 200", res.Status)
	}
	if res.Format != "html" {
		t.Errorf("format = %q, want html", res.Format)
	}
	if res.Engine != "go-readability" {
		t.Errorf("engine = %q, want go-readability", res.Engine)
	}
	if res.Title != "WebFetch Article" {
		t.Errorf("title = %q, want 'WebFetch Article'", res.Title)
	}
	if !strings.Contains(res.Content, "first paragraph of the article body") {
		t.Errorf("article body missing:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "Subscribe Now") {
		t.Errorf("nav clutter leaked through:\n%s", res.Content)
	}
	if res.SizeBytes <= 0 {
		t.Errorf("size_bytes = %d, want > 0", res.SizeBytes)
	}
	if res.FetchedAt == "" {
		t.Errorf("fetched_at must be populated")
	}
}

func TestWebFetch_PlainText(t *testing.T) {
	body := "line 1\nline 2\nline 3\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeWebFetch(ctx, srv.URL, 3*time.Second)

	if res.Format != "text" {
		t.Errorf("format = %q, want text", res.Format)
	}
	if res.Engine != "stdlib" {
		t.Errorf("engine = %q, want stdlib", res.Engine)
	}
	if res.Content != body {
		t.Errorf("content = %q, want %q", res.Content, body)
	}
}

func TestWebFetch_BinaryRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte{0x00, 0x01, 0x02, 0x03, 0x04})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeWebFetch(ctx, srv.URL, 3*time.Second)

	if res.Format != "binary-rejected" {
		t.Errorf("format = %q, want binary-rejected", res.Format)
	}
	if res.ErrorReason == "" {
		t.Errorf("error_reason should explain the rejection")
	}
}

func TestWebFetch_FollowsRedirect(t *testing.T) {
	var srvFinal *httptest.Server
	srvFinal = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = fmt.Fprintln(w, "after redirect")
	}))
	defer srvFinal.Close()

	srvStart := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srvFinal.URL, http.StatusFound)
	}))
	defer srvStart.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeWebFetch(ctx, srvStart.URL, 3*time.Second)

	if !strings.Contains(res.Content, "after redirect") {
		t.Errorf("redirect not followed: content = %q", res.Content)
	}
	if res.FinalURL == srvStart.URL {
		t.Errorf("final_url should differ from initial: %q", res.FinalURL)
	}
	if !strings.HasPrefix(res.FinalURL, srvFinal.URL) {
		t.Errorf("final_url = %q, want prefix %q", res.FinalURL, srvFinal.URL)
	}
}

func TestWebFetch_RejectsNonHTTPScheme(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeWebFetch(ctx, "ftp://example.com/file", 3*time.Second)
	if res.ErrorReason == "" || !strings.Contains(res.ErrorReason, "http") {
		t.Errorf("expected scheme rejection, got %q", res.ErrorReason)
	}
}

func TestWebFetch_RespectsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Sleep longer than client timeout.
		time.Sleep(2 * time.Second)
		_, _ = w.Write([]byte("never"))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	res := executeWebFetch(ctx, srv.URL, 250*time.Millisecond)
	elapsed := time.Since(start)

	if res.ErrorReason == "" {
		t.Error("expected timeout error")
	}
	if !strings.Contains(strings.ToLower(res.ErrorReason), "time") {
		t.Errorf("error should mention timeout: %q", res.ErrorReason)
	}
	// Returned within ~500ms — not waiting for the 2s server response.
	if elapsed > 1500*time.Millisecond {
		t.Errorf("waited too long for timeout: %s", elapsed)
	}
}
