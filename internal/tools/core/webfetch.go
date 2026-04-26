// Package core — WebFetch retrieves a URL and renders the body in the
// best shape for an AI agent: clean article text for HTML, raw text for
// plain-text MIME types, structured rejection for binaries.
//
// Per ADR-007 we wrap two mature engines:
//   - net/http stdlib client for transport (proxy, TLS, redirect handling
//     are all stock — battle-tested across Go's user base);
//   - github.com/go-shiori/go-readability for HTML extraction (Mozilla
//     Readability port, the same algorithm Firefox Reader View ships).
//
// What clawtool adds: agent-friendly polish — a hard body cap so a runaway
// page can't blow context, structured result with content-type-aware
// `format`, citation metadata (final URL after redirects, fetched_at).
package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	webFetchDefaultTimeoutMs = 30_000
	webFetchMaxTimeoutMs     = 120_000
	webFetchBodyCapBytes     = 10 * 1024 * 1024 // 10 MB hard cap on response body read
	webFetchUA               = "clawtool/0.7 (+https://github.com/cogitave/clawtool)"
)

// WebFetchResult is the uniform shape returned to the agent.
type WebFetchResult struct {
	URL         string `json:"url"`
	FinalURL    string `json:"final_url"`
	Status      int    `json:"status"`
	ContentType string `json:"content_type"`
	Format      string `json:"format"` // "html" | "text" | "binary-rejected"
	Engine      string `json:"engine"` // "go-readability" | "stdlib"
	Title       string `json:"title,omitempty"`
	Byline      string `json:"byline,omitempty"`
	SiteName    string `json:"site_name,omitempty"`
	Content     string `json:"content"`
	SizeBytes   int    `json:"size_bytes"`
	FetchedAt   string `json:"fetched_at"`
	DurationMs  int64  `json:"duration_ms"`
	Truncated   bool   `json:"truncated"`
	ErrorReason string `json:"error_reason,omitempty"`
}

// RegisterWebFetch adds the WebFetch tool to the given MCP server.
func RegisterWebFetch(s *server.MCPServer) {
	tool := mcp.NewTool(
		"WebFetch",
		mcp.WithDescription(
			"Retrieve a URL and return its body as agent-friendly content. "+
				"For text/html, runs the Mozilla Readability algorithm via go-readability "+
				"to strip nav/ads/chrome and return title + byline + article body. "+
				"For text/* MIME types, returns the body verbatim. Binary content is "+
				"refused with a structured error. Hard 10 MB body cap protects context.",
		),
		mcp.WithString("url", mcp.Required(),
			mcp.Description("URL to fetch. http:// and https:// only.")),
		mcp.WithNumber("timeout_ms",
			mcp.Description("Request timeout in milliseconds. Default 30000, max 120000.")),
	)
	s.AddTool(tool, runWebFetch)
}

func runWebFetch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := req.RequireString("url")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: url"), nil
	}
	timeoutMs := int(req.GetFloat("timeout_ms", float64(webFetchDefaultTimeoutMs)))
	if timeoutMs <= 0 {
		timeoutMs = webFetchDefaultTimeoutMs
	}
	if timeoutMs > webFetchMaxTimeoutMs {
		timeoutMs = webFetchMaxTimeoutMs
	}
	res := executeWebFetch(ctx, target, time.Duration(timeoutMs)*time.Millisecond)
	body, _ := json.Marshal(res)
	return mcp.NewToolResultText(string(body)), nil
}

// httpClient is a package-level client so tests can inject a transport.
// Tests in webfetch_test.go set this to point at httptest.Server with
// custom redirect / timeout policies.
var httpClient = &http.Client{
	Timeout: webFetchMaxTimeoutMs * time.Millisecond,
}

// executeWebFetch performs the HTTP GET and dispatches the body through
// the right engine based on Content-Type. The function never panics on
// network or parse failures — all errors fold into ReadResult.ErrorReason.
func executeWebFetch(ctx context.Context, rawURL string, timeout time.Duration) WebFetchResult {
	start := time.Now()
	res := WebFetchResult{
		URL:       rawURL,
		FetchedAt: start.UTC().Format(time.RFC3339),
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		res.ErrorReason = "url must be http:// or https://"
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		res.ErrorReason = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	httpReq.Header.Set("User-Agent", webFetchUA)
	httpReq.Header.Set("Accept", "text/html, text/plain, text/markdown, */*;q=0.1")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		// Distinguish timeout from generic transport failure for friendlier
		// agent messages.
		if errors.Is(reqCtx.Err(), context.DeadlineExceeded) {
			res.ErrorReason = fmt.Sprintf("request timed out after %s", timeout)
		} else {
			res.ErrorReason = err.Error()
		}
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	defer resp.Body.Close()

	res.Status = resp.StatusCode
	res.FinalURL = resp.Request.URL.String()
	res.ContentType = resp.Header.Get("Content-Type")

	body, truncated, readErr := readBodyCapped(resp.Body, webFetchBodyCapBytes)
	if readErr != nil {
		res.ErrorReason = "body read: " + readErr.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	res.SizeBytes = len(body)
	res.Truncated = truncated

	mime := normalizeMIME(res.ContentType)
	switch {
	case strings.HasPrefix(mime, "text/html"), strings.HasPrefix(mime, "application/xhtml"):
		res.Format = "html"
		res.Engine = "go-readability"
		extractHTMLArticle(body, parsed, &res)
	case strings.HasPrefix(mime, "text/"),
		mime == "application/json", mime == "application/yaml",
		mime == "application/xml", mime == "application/toml":
		res.Format = "text"
		res.Engine = "stdlib"
		res.Content = string(body)
	default:
		res.Format = "binary-rejected"
		res.Engine = "stdlib"
		res.ErrorReason = fmt.Sprintf("content-type %q is binary; clawtool refuses to dump raw bytes (use a specialised tool for binary downloads)", res.ContentType)
	}
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}

// extractHTMLArticle runs the Mozilla Readability algorithm and renders a
// concise text body with title / byline / sitename header. We deliberately
// do NOT return the raw HTML — every WebFetch result is meant to land in
// an agent's context window where prose beats markup.
func extractHTMLArticle(body []byte, base *url.URL, res *WebFetchResult) {
	article, err := readability.FromReader(strings.NewReader(string(body)), base)
	if err != nil {
		// Fall back to the raw body so the agent still has *something*.
		res.Engine = "stdlib"
		res.Format = "text"
		res.Content = string(body)
		return
	}
	res.Title = article.Title
	res.Byline = article.Byline
	res.SiteName = article.SiteName

	var sb strings.Builder
	if article.Title != "" {
		sb.WriteString("# ")
		sb.WriteString(article.Title)
		sb.WriteByte('\n')
	}
	if article.Byline != "" {
		sb.WriteString("by ")
		sb.WriteString(article.Byline)
		sb.WriteByte('\n')
	}
	if article.SiteName != "" {
		sb.WriteString("site: ")
		sb.WriteString(article.SiteName)
		sb.WriteByte('\n')
	}
	if article.Excerpt != "" {
		sb.WriteString("\n> ")
		sb.WriteString(article.Excerpt)
		sb.WriteByte('\n')
	}
	sb.WriteString("\n")
	sb.WriteString(article.TextContent)
	res.Content = sb.String()
}

// readBodyCapped reads at most max bytes; second return is true when the
// body was longer than the cap (and therefore truncated).
func readBodyCapped(r io.Reader, max int) ([]byte, bool, error) {
	limited := io.LimitReader(r, int64(max+1))
	b, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	if len(b) > max {
		return b[:max], true, nil
	}
	return b, false, nil
}

// normalizeMIME returns the bare MIME type, lowercased, with parameters
// stripped (`text/html; charset=utf-8` → `text/html`).
func normalizeMIME(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.ToLower(strings.TrimSpace(ct))
}
