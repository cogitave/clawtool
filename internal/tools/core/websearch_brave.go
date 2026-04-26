package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/cogitave/clawtool/internal/secrets"
)

// braveBaseURL is variable so tests can swap in an httptest.Server.
var braveBaseURL = "https://api.search.brave.com/res/v1/web/search"

// braveBackend talks to the Brave Search API.
//
// Why Brave first: free tier without credit-card requirement, generous
// rate limits, no political content filtering, well-documented JSON
// response. Tavily / SearXNG land in v0.8.
type braveBackend struct {
	apiKey string
}

func newBraveBackend(store *secrets.Store) (*braveBackend, error) {
	key, _ := store.Get("websearch", "BRAVE_API_KEY")
	if key == "" {
		key = strings.TrimSpace(os.Getenv("BRAVE_API_KEY"))
	}
	if key == "" {
		return nil, fmt.Errorf("%w: BRAVE_API_KEY (set via 'clawtool source set-secret websearch BRAVE_API_KEY' or env)", ErrMissingAPIKey)
	}
	return &braveBackend{apiKey: key}, nil
}

func (b *braveBackend) Name() string { return "brave" }

func (b *braveBackend) Search(ctx context.Context, query string, limit int) ([]WebSearchHit, error) {
	if query == "" {
		return nil, fmt.Errorf("empty query")
	}
	q := url.Values{}
	q.Set("q", query)
	q.Set("count", fmt.Sprintf("%d", limit))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, braveBaseURL+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.apiKey)
	req.Header.Set("User-Agent", webFetchUA)

	resp, err := websearchHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("brave http %d: %s", resp.StatusCode, truncate(string(body), 200))
	}

	var raw struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("brave parse: %w", err)
	}

	out := make([]WebSearchHit, 0, len(raw.Web.Results))
	for _, r := range raw.Web.Results {
		out = append(out, WebSearchHit{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: stripHTML(r.Description),
		})
	}
	return out, nil
}

// stripHTML removes the lightweight <strong>/<b> markers Brave embeds in
// `description`. We don't want them flowing into agent context as raw
// HTML — tags like <strong> are visual noise for an LLM.
func stripHTML(s string) string {
	var sb strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
