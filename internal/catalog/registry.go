package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultRegistryURL is the official MCP Registry base URL
// (https://registry.modelcontextprotocol.io). This endpoint
// replaced the third-party server list previously curated in
// modelcontextprotocol/servers (PR #3950, 2026-04-14): the
// registry is now the canonical discovery surface for MCP
// servers across the ecosystem.
//
// The clawtool catalog still ships an embedded `builtin.toml`
// for offline / hot-path source-add — the registry is a
// federated overlay, NOT a replacement. ProbeRegistry below is
// the foundation for future ticks that surface registry-only
// entries via `clawtool source registry` or fall back to the
// registry when a bare name misses the builtin catalog.
const DefaultRegistryURL = "https://registry.modelcontextprotocol.io"

// RegistryServer is the projection of one MCP Registry server
// entry that clawtool actually uses. The upstream payload is
// richer (remotes, packages, repository, _meta) — we capture
// only the fields useful for catalog probing + listing. Future
// migrations can extend this struct without breaking existing
// callers since JSON unmarshalling tolerates unknown fields by
// default.
type RegistryServer struct {
	// Name is the canonical server identifier in the registry,
	// e.g. "ac.inference.sh/mcp" or "io.github.octocat/server".
	// Always present.
	Name string `json:"name"`

	// Description is the one-line registry description. May be
	// empty when the server author didn't supply one.
	Description string `json:"description,omitempty"`

	// Version is the upstream's declared semver. Different
	// versions of the same server appear as separate entries
	// in the registry (server vendors publish each release).
	Version string `json:"version,omitempty"`
}

// RegistryResult is the parsed `ProbeRegistry` response: how
// many servers came back + their summary projections + the
// effective base URL used (echoes the resolved value back so
// callers writing diagnostics can show what was actually hit).
type RegistryResult struct {
	BaseURL string           `json:"base_url"`
	Count   int              `json:"count"`
	Servers []RegistryServer `json:"servers"`
}

// ProbeRegistry hits the MCP Registry's `/v0/servers` endpoint
// at `baseURL` and returns the first `limit` server entries.
//
// `baseURL` defaults to DefaultRegistryURL when empty. Trailing
// slashes are tolerated. `limit` is clamped to the inclusive
// range [1, 50] — the upstream caps page size around 50, and
// callers asking for 0 or a negative number get the friendly
// default of 10.
//
// The function is intentionally read-only and short-circuit-
// friendly: an unreachable endpoint, a non-200 response, or
// malformed JSON all surface as wrapped errors so the caller
// (a CLI verb or a future catalog-fallback path) can decide
// whether to fall back to the embedded builtin catalog or
// propagate the failure.
//
// HTTP timeout is 8 seconds — long enough for sane network
// conditions, short enough that an offline operator gets a
// clear failure rather than a 30+ second hang.
func ProbeRegistry(ctx context.Context, baseURL string, limit int) (*RegistryResult, error) {
	if baseURL == "" {
		baseURL = DefaultRegistryURL
	}
	baseURL = strings.TrimRight(baseURL, "/")

	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	endpoint, err := url.Parse(baseURL + "/v0/servers")
	if err != nil {
		return nil, fmt.Errorf("parse registry url: %w", err)
	}
	q := endpoint.Query()
	q.Set("limit", strconv.Itoa(limit))
	endpoint.RawQuery = q.Encode()

	rctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(rctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "clawtool-catalog-probe")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", endpoint.String(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read a small slice for diagnostics; the upstream
		// emits structured 4xx / 5xx JSON which is helpful
		// when the endpoint shape moves.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("registry returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// The registry response is `{servers: [{server: {...}, _meta: {...}}, ...], metadata: {...}}`.
	// We unwrap the inner `server` object since that's the only
	// part with stable, ecosystem-wide field names.
	var raw struct {
		Servers []struct {
			Server RegistryServer `json:"server"`
		} `json:"servers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode registry response: %w", err)
	}

	out := &RegistryResult{
		BaseURL: baseURL,
		Count:   len(raw.Servers),
		Servers: make([]RegistryServer, 0, len(raw.Servers)),
	}
	for _, s := range raw.Servers {
		out.Servers = append(out.Servers, s.Server)
	}
	return out, nil
}
