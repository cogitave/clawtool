// Package core — BrowserFetch retrieves a URL through a real browser
// engine (Obscura, Chromium-via-CDP) so SPA / JS-rendered content lands
// in the agent's context. Sister tool to WebFetch (server-side via
// Mozilla Readability), which can't render React / Next.js / hydrated
// SPAs.
//
// Per ADR-007 we wrap mature engines: Obscura (V8 + Chrome DevTools
// Protocol, Apache 2.0). We never re-implement page loading. clawtool
// adds: agent-friendly polish (size cap, structured result, optional
// JS evaluator, optional CSS-selector wait, post-render readability
// pass for clean prose).
//
// Stateless: each call spins a fresh browser context. For interactive
// multi-step flows (login + cookie + click + capture) use BrowserAction.
package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"

	readability "github.com/go-shiori/go-readability"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	browserFetchDefaultTimeoutMs = 30_000
	browserFetchMaxTimeoutMs     = 180_000
	browserFetchBodyCapBytes     = 10 * 1024 * 1024
)

// BrowserFetchResult mirrors WebFetchResult so an agent can swap one
// for the other without rewriting downstream parsing. Adds EvalResult
// for callers that pass `eval` (raw stdout slice from obscura).
type BrowserFetchResult struct {
	BaseResult
	URL        string `json:"url"`
	FinalURL   string `json:"final_url,omitempty"`
	Format     string `json:"format"` // "html" | "text" | "eval"
	Title      string `json:"title,omitempty"`
	Byline     string `json:"byline,omitempty"`
	SiteName   string `json:"site_name,omitempty"`
	Content    string `json:"content"`
	EvalResult string `json:"eval_result,omitempty"`
	SizeBytes  int    `json:"size_bytes"`
	FetchedAt  string `json:"fetched_at"`
	Truncated  bool   `json:"truncated"`
}

// Render keeps parity with WebFetchResult: framed body + footer.
func (r BrowserFetchResult) Render() string {
	if r.IsError() {
		return r.ErrorLine(r.URL)
	}
	var b strings.Builder
	b.WriteString(r.HeaderLine(fmt.Sprintf("BROWSER %s · %s", r.URL, r.Format)))
	b.WriteByte('\n')
	b.WriteString("───\n")
	b.WriteString(r.Content)
	if !strings.HasSuffix(r.Content, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("───\n")
	extras := []string{humanBytes(int64(r.SizeBytes))}
	if r.Truncated {
		extras = append(extras, "truncated")
	}
	b.WriteString(r.FooterLine(extras...))
	return b.String()
}

// RegisterBrowserFetch wires the BrowserFetch MCP tool.
func RegisterBrowserFetch(s *server.MCPServer) {
	tool := mcp.NewTool(
		"BrowserFetch",
		mcp.WithDescription(
			"Render a URL inside a real headless browser (Obscura, "+
				"Chromium-via-CDP) and return clean prose for HTML or the "+
				"value of a custom JS `eval` expression. Use ONLY when "+
				"WebFetch returns empty shells / near-zero text (Next.js, "+
				"React, hydrated SPAs) or the operator explicitly needs "+
				"client-rendered content / JS evaluation / a CSS-selector "+
				"wait. NOT for static HTML — WebFetch is faster and cheaper "+
				"for that. Stateless: each call runs in a fresh browser "+
				"context. Requires the `obscura` binary on PATH "+
				"(https://github.com/h4ckf0r0day/obscura).",
		),
		mcp.WithString("url", mcp.Required(),
			mcp.Description("Target URL. http:// or https://.")),
		mcp.WithString("wait_until",
			mcp.Description("When to consider the page ready: load | domcontentloaded | networkidle0. Default networkidle0.")),
		mcp.WithString("selector",
			mcp.Description("Optional CSS selector to wait for before dumping (e.g. `.article-body`).")),
		mcp.WithString("eval",
			mcp.Description("Optional JavaScript expression evaluated after the page settles. When set, EvalResult holds its stdout and Content is the rendered HTML for fallback parsing.")),
		mcp.WithBoolean("stealth",
			mcp.Description("Enable Obscura's --stealth flag (anti-fingerprinting + tracker blocking). Off by default.")),
		mcp.WithNumber("timeout_ms",
			mcp.Description("Hard deadline in milliseconds. Default 30000, max 180000.")),
	)
	s.AddTool(tool, runBrowserFetch)
}

func runBrowserFetch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	target, err := req.RequireString("url")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: url"), nil
	}
	args := browserFetchArgs{
		URL:       target,
		WaitUntil: req.GetString("wait_until", "networkidle0"),
		Selector:  req.GetString("selector", ""),
		Eval:      req.GetString("eval", ""),
		Stealth:   req.GetBool("stealth", false),
		TimeoutMs: int(req.GetFloat("timeout_ms", float64(browserFetchDefaultTimeoutMs))),
	}
	if args.TimeoutMs <= 0 {
		args.TimeoutMs = browserFetchDefaultTimeoutMs
	}
	if args.TimeoutMs > browserFetchMaxTimeoutMs {
		args.TimeoutMs = browserFetchMaxTimeoutMs
	}
	res := executeBrowserFetch(ctx, args)
	return resultOf(res), nil
}

type browserFetchArgs struct {
	URL       string
	WaitUntil string
	Selector  string
	Eval      string
	Stealth   bool
	TimeoutMs int
}

// obscuraBin is overridable in tests so unit tests don't shell out to a
// real binary; production callers go through LookupEngine.
var obscuraBin = func() string { return LookupEngine("obscura").Bin }

func executeBrowserFetch(ctx context.Context, a browserFetchArgs) BrowserFetchResult {
	start := time.Now()
	res := BrowserFetchResult{
		BaseResult: BaseResult{Operation: "BrowserFetch", Engine: "obscura"},
		URL:        a.URL,
		FetchedAt:  start.UTC().Format(time.RFC3339),
	}

	parsed, err := url.Parse(a.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		res.ErrorReason = "url must be http:// or https://"
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	bin := obscuraBin()
	if bin == "" {
		res.ErrorReason = obscuraInstallHint()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}

	argv := []string{"fetch", a.URL, "--quiet", "--wait-until", a.WaitUntil}
	if a.Selector != "" {
		argv = append(argv, "--selector", a.Selector)
	}
	if a.Stealth {
		argv = append(argv, "--stealth")
	}
	if a.Eval != "" {
		argv = append(argv, "--eval", a.Eval)
		res.Format = "eval"
	} else {
		argv = append(argv, "--dump", "html")
		res.Format = "html"
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(a.TimeoutMs)*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin, argv...)
	applyProcessGroup(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if runErr != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			res.ErrorReason = fmt.Sprintf("obscura timed out after %dms", a.TimeoutMs)
		} else {
			res.ErrorReason = fmt.Sprintf("obscura: %v (%s)", runErr, strings.TrimSpace(stderr.String()))
		}
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}

	body := stdout.Bytes()
	if len(body) > browserFetchBodyCapBytes {
		body = body[:browserFetchBodyCapBytes]
		res.Truncated = true
	}
	res.SizeBytes = len(body)

	if a.Eval != "" {
		res.EvalResult = string(body)
		res.Content = res.EvalResult
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	extractRenderedHTML(body, parsed, &res)
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}

// extractRenderedHTML hydrates the BrowserFetchResult from rendered HTML.
// Mirrors WebFetch's Readability pass so callers see the same prose
// shape; falls through to the raw HTML when extraction fails so the
// agent never gets nothing.
func extractRenderedHTML(body []byte, base *url.URL, res *BrowserFetchResult) {
	article, err := readability.FromReader(bytes.NewReader(body), base)
	if err != nil {
		res.Format = "html"
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

// obscuraInstallHint returns a multi-line install instruction string
// the agent / operator sees when the binary is missing. Centralised so
// the three browser tools surface the same text.
func obscuraInstallHint() string {
	return strings.Join([]string{
		"obscura binary not on PATH — clawtool's browser tools wrap " +
			"github.com/h4ckf0r0day/obscura. Install:",
		"  Linux x86_64: curl -LO https://github.com/h4ckf0r0day/obscura/releases/latest/download/obscura-x86_64-linux.tar.gz && tar xzf obscura-x86_64-linux.tar.gz && sudo mv obscura /usr/local/bin/",
		"  macOS Apple Silicon: curl -LO https://github.com/h4ckf0r0day/obscura/releases/latest/download/obscura-aarch64-macos.tar.gz && tar xzf obscura-aarch64-macos.tar.gz && sudo mv obscura /usr/local/bin/",
		"  macOS Intel: curl -LO https://github.com/h4ckf0r0day/obscura/releases/latest/download/obscura-x86_64-macos.tar.gz && tar xzf obscura-x86_64-macos.tar.gz && sudo mv obscura /usr/local/bin/",
		"  Then re-run clawtool. See docs/browser-tools.md for the full surface.",
	}, "\n")
}
