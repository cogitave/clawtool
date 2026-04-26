package core

import (
	"context"
	"strings"
	"testing"
	"time"
)

const cluttered = `<!DOCTYPE html>
<html>
<head><title>Article Title</title></head>
<body>
<header><nav>Home | About | Login</nav></header>
<aside>Subscribe to our newsletter!</aside>
<article>
<h1>Article Title</h1>
<p>This is the first paragraph of the article. It contains the actual reading content
that the agent cares about, distinct from navigation and chrome.</p>
<p>Second paragraph reinforces the topic with more substantive sentences so the
readability extractor has enough signal to choose this region as the article body.</p>
</article>
<footer>© 2026 Example Corp</footer>
</body>
</html>
`

func TestRead_HTML_StripsClutter(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "page.html", cluttered)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeRead(ctx, path, 1, 0, "")

	if res.Format != "html" {
		t.Errorf("format = %q, want html", res.Format)
	}
	if res.Engine != "go-readability" {
		t.Errorf("engine = %q, want go-readability", res.Engine)
	}
	if res.ErrorReason != "" {
		t.Fatalf("unexpected error: %s", res.ErrorReason)
	}

	// The article body must survive.
	if !strings.Contains(res.Content, "first paragraph of the article") {
		t.Errorf("article body missing:\n%s", res.Content)
	}
	if !strings.Contains(res.Content, "second paragraph") &&
		!strings.Contains(res.Content, "Second paragraph") {
		t.Errorf("second paragraph missing:\n%s", res.Content)
	}

	// Nav / footer chrome should be stripped.
	if strings.Contains(res.Content, "Home | About | Login") {
		t.Errorf("nav clutter leaked through:\n%s", res.Content)
	}
	if strings.Contains(res.Content, "© 2026 Example Corp") {
		t.Errorf("footer clutter leaked through:\n%s", res.Content)
	}
}

func TestRead_HTML_DetectedWithoutExtension(t *testing.T) {
	// File without .html extension but with HTML content sniff.
	dir := t.TempDir()
	body := "<!DOCTYPE html><html><body><article><h1>X</h1><p>Body Body Body.</p></article></body></html>"
	path := writeFile(t, dir, "noext", body)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := executeRead(ctx, path, 1, 0, "")

	if res.Format != "html" {
		t.Errorf("format = %q, want html (extension-less sniff)", res.Format)
	}
}
