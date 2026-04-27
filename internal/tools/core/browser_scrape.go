// Package core — BrowserScrape parallelises BrowserFetch across many
// URLs by wrapping `obscura scrape <url...> --concurrency N --eval ...
// --format json`. Use case: "give me the rendered headline from these
// 50 SPA blog posts", "bulk-snapshot a competitor's site map", etc.
//
// Per ADR-007 we wrap Obscura's scrape subcommand (Apache-2.0 Rust
// engine, V8 + CDP) — clawtool never re-implements parallel fetching.
// Stateless: each URL gets its own browser context, no cookies, no
// shared session. For interactive work use BrowserAction.
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	browserScrapeDefaultTimeoutMs = 120_000
	browserScrapeMaxTimeoutMs     = 600_000
	browserScrapeDefaultConc      = 10
	browserScrapeHardCapURLs      = 500
)

// BrowserScrapeResult lists per-URL outcomes plus aggregate counts.
type BrowserScrapeResult struct {
	BaseResult
	Results   []BrowserScrapeRow `json:"results"`
	Total     int                `json:"total"`
	Failed    int                `json:"failed"`
	Truncated bool               `json:"truncated"`
	FetchedAt string             `json:"fetched_at"`
}

// BrowserScrapeRow is one URL's outcome. `Result` carries the eval'd
// value (or rendered text); `Error` is set on per-URL failure so the
// rest of the batch keeps going.
type BrowserScrapeRow struct {
	URL    string `json:"url"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Render lists one row per URL.
func (r BrowserScrapeResult) Render() string {
	if r.IsError() {
		return r.ErrorLine("")
	}
	var b strings.Builder
	b.WriteString(r.HeaderLine(fmt.Sprintf("BROWSER SCRAPE · %d URL(s)", r.Total)))
	b.WriteByte('\n')
	for _, row := range r.Results {
		if row.Error != "" {
			fmt.Fprintf(&b, "✗ %s — %s\n", row.URL, row.Error)
			continue
		}
		fmt.Fprintf(&b, "✓ %s — %s\n", row.URL, truncateForRender(row.Result, 120))
	}
	extras := []string{fmt.Sprintf("%d ok / %d fail", r.Total-r.Failed, r.Failed)}
	if r.Truncated {
		extras = append(extras, "truncated")
	}
	b.WriteByte('\n')
	b.WriteString(r.FooterLine(extras...))
	return b.String()
}

// RegisterBrowserScrape wires the BrowserScrape MCP tool.
func RegisterBrowserScrape(s *server.MCPServer) {
	tool := mcp.NewTool(
		"BrowserScrape",
		mcp.WithDescription(
			"Render a list of URLs in parallel through a real browser "+
				"engine and capture a JS expression's value per page. "+
				"Wraps `obscura scrape ... --concurrency N --eval ... "+
				"--format json`. Stateless per URL (no shared cookies). "+
				"Use BrowserFetch for one-off renders, BrowserAction for "+
				"interactive multi-step flows.",
		),
		mcp.WithString("urls", mcp.Required(),
			mcp.Description("Newline- or comma-separated list of URLs (http:// or https://). Hard cap 500.")),
		mcp.WithString("eval", mcp.Required(),
			mcp.Description("JavaScript expression evaluated per page after load. Common pattern: `document.querySelector('h1').textContent`.")),
		mcp.WithNumber("concurrency",
			mcp.Description("Parallel browser contexts. Default 10, hard cap 50.")),
		mcp.WithString("wait_until",
			mcp.Description("When each page is considered ready: load | domcontentloaded | networkidle0. Default networkidle0.")),
		mcp.WithBoolean("stealth",
			mcp.Description("Pass Obscura's --stealth flag (anti-fingerprinting + tracker blocking).")),
		mcp.WithNumber("timeout_ms",
			mcp.Description("Total deadline in milliseconds across the whole batch. Default 120000, max 600000.")),
	)
	s.AddTool(tool, runBrowserScrape)
}

func runBrowserScrape(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	rawURLs, err := req.RequireString("urls")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: urls"), nil
	}
	eval, err := req.RequireString("eval")
	if err != nil {
		return mcp.NewToolResultError("missing required argument: eval"), nil
	}
	conc := int(req.GetFloat("concurrency", float64(browserScrapeDefaultConc)))
	if conc <= 0 {
		conc = browserScrapeDefaultConc
	}
	if conc > 50 {
		conc = 50
	}
	timeoutMs := int(req.GetFloat("timeout_ms", float64(browserScrapeDefaultTimeoutMs)))
	if timeoutMs <= 0 {
		timeoutMs = browserScrapeDefaultTimeoutMs
	}
	if timeoutMs > browserScrapeMaxTimeoutMs {
		timeoutMs = browserScrapeMaxTimeoutMs
	}
	urls := splitURLs(rawURLs)
	res := executeBrowserScrape(ctx, browserScrapeArgs{
		URLs:        urls,
		Eval:        eval,
		Concurrency: conc,
		WaitUntil:   req.GetString("wait_until", "networkidle0"),
		Stealth:     req.GetBool("stealth", false),
		TimeoutMs:   timeoutMs,
	})
	return resultOf(res), nil
}

type browserScrapeArgs struct {
	URLs        []string
	Eval        string
	Concurrency int
	WaitUntil   string
	Stealth     bool
	TimeoutMs   int
}

func executeBrowserScrape(ctx context.Context, a browserScrapeArgs) BrowserScrapeResult {
	start := time.Now()
	res := BrowserScrapeResult{
		BaseResult: BaseResult{Operation: "BrowserScrape", Engine: "obscura"},
		FetchedAt:  start.UTC().Format(time.RFC3339),
	}
	if len(a.URLs) == 0 {
		res.ErrorReason = "urls list is empty"
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	if len(a.URLs) > browserScrapeHardCapURLs {
		a.URLs = a.URLs[:browserScrapeHardCapURLs]
		res.Truncated = true
	}
	bin := obscuraBin()
	if bin == "" {
		res.ErrorReason = obscuraInstallHint()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}

	argv := []string{
		"scrape",
		"--concurrency", fmt.Sprintf("%d", a.Concurrency),
		"--eval", a.Eval,
		"--format", "json",
		"--wait-until", a.WaitUntil,
	}
	if a.Stealth {
		argv = append(argv, "--stealth")
	}
	argv = append(argv, a.URLs...)

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(a.TimeoutMs)*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin, argv...)
	applyProcessGroup(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	if runErr != nil && stdout.Len() == 0 {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			res.ErrorReason = fmt.Sprintf("obscura scrape timed out after %dms", a.TimeoutMs)
		} else {
			res.ErrorReason = fmt.Sprintf("obscura scrape: %v (%s)", runErr, strings.TrimSpace(stderr.String()))
		}
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}

	rows := parseScrapeJSON(stdout.Bytes())
	res.Results = rows
	res.Total = len(rows)
	for _, r := range rows {
		if r.Error != "" {
			res.Failed++
		}
	}
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}

// parseScrapeJSON tolerates both NDJSON (one object per line) and a
// JSON array — Obscura's --format json may emit either depending on
// version. Unparseable lines fold into a synthetic error row so the
// agent sees what failed.
func parseScrapeJSON(b []byte) []BrowserScrapeRow {
	trim := bytes.TrimSpace(b)
	if len(trim) == 0 {
		return nil
	}
	var asArray []scrapeWire
	if json.Unmarshal(trim, &asArray) == nil {
		return convertScrapeRows(asArray)
	}
	out := []BrowserScrapeRow{}
	for _, line := range bytes.Split(trim, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var row scrapeWire
		if err := json.Unmarshal(line, &row); err != nil {
			out = append(out, BrowserScrapeRow{Error: "parse: " + string(line)})
			continue
		}
		out = append(out, scrapeRowFromWire(row))
	}
	return out
}

type scrapeWire struct {
	URL    string          `json:"url"`
	Result json.RawMessage `json:"result,omitempty"`
	Value  json.RawMessage `json:"value,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func convertScrapeRows(in []scrapeWire) []BrowserScrapeRow {
	out := make([]BrowserScrapeRow, 0, len(in))
	for _, w := range in {
		out = append(out, scrapeRowFromWire(w))
	}
	return out
}

func scrapeRowFromWire(w scrapeWire) BrowserScrapeRow {
	row := BrowserScrapeRow{URL: w.URL, Error: w.Error}
	raw := w.Result
	if len(raw) == 0 {
		raw = w.Value
	}
	if len(raw) > 0 {
		// Strings come back JSON-quoted; numbers/objects stringify verbatim.
		var s string
		if json.Unmarshal(raw, &s) == nil {
			row.Result = s
		} else {
			row.Result = string(raw)
		}
	}
	return row
}

// splitURLs accepts either commas or newlines. Empty entries dropped;
// leading/trailing whitespace stripped. Caller already capped the count.
func splitURLs(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == ',' || r == '\r'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.HasPrefix(p, "http://") && !strings.HasPrefix(p, "https://") {
			continue
		}
		out = append(out, p)
	}
	return out
}
