// Package version — update-check primitive. Hits the GitHub
// Releases API for the latest tag and reports whether the local
// build is older. Cached for 24h in ~/.cache/clawtool/update.json
// so a `clawtool doctor` (or any other surface that calls
// CheckForUpdate) doesn't pay a network roundtrip on every
// invocation.
//
// No background polling, no telemetry — this is a stateless,
// user-initiated check. The cache exists purely to avoid being
// rude to the GitHub API.
package version

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/atomicfile"
	"github.com/cogitave/clawtool/internal/xdg"
)

// UpdateCheckURL is the GitHub Releases API endpoint we hit. The
// public API permits ~60 unauthenticated requests/hour per IP; the
// 24h cache keeps us well under that even on shared CI runners.
const UpdateCheckURL = "https://api.github.com/repos/cogitave/clawtool/releases/latest"

// updateCheckURLOverride is the test-only seam. Empty string =
// production path uses UpdateCheckURL. Tests assign this to an
// httptest.Server URL via stubGitHub before calling
// CheckForUpdate, then restore it on cleanup.
var updateCheckURLOverride string

func currentUpdateCheckURL() string {
	if updateCheckURLOverride != "" {
		return updateCheckURLOverride
	}
	return UpdateCheckURL
}

// UpdateInfo is the result a caller surfaces in the UI.
type UpdateInfo struct {
	// HasUpdate is true when the upstream tag is newer than the
	// local build. Defaults to false on any error so we never nag
	// the user about a non-confirmed update.
	HasUpdate bool

	// Latest is the tag string from GitHub (e.g. "v0.10.0"). Empty
	// on error.
	Latest string

	// Current is the local build version as Resolved() returns it
	// — i.e. the goreleaser-stamped tag when present, else the
	// debug.BuildInfo Main.Version, else the package fallback.
	// Reading the bare `Version` var here would mis-report on every
	// production binary (it carries the dev-fallback "0.21.7"); a
	// HasUpdate=true would fire for already-current installs.
	Current string

	// FetchedAt is when we last asked GitHub. Surfaced so the user
	// knows how stale the answer is. UTC.
	FetchedAt time.Time

	// Err is non-nil when the check itself failed (network, parse,
	// rate-limit). Callers display "could not check" instead of
	// pretending no update exists.
	Err error
}

// updateCachePath returns the file we read/write update results
// to. Honors XDG_CACHE_HOME, falls back to $HOME/.cache, falls
// back further to a tempfile path. Never returns empty.
func updateCachePath() string {
	return filepath.Join(xdg.CacheDirOrTemp(), "update.json")
}

// updateCacheTTL controls how long we trust a cached result.
// Short by design — `clawtool doctor` is user-invoked, not
// polled, so refreshing freely is fine. The cache exists purely
// to coalesce repeated invocations within a single shell loop.
const updateCacheTTL = 5 * time.Minute

// cachedUpdate is what we serialize to disk. UpdateInfo carries an
// error which doesn't JSON-marshal cleanly, so we stash the
// stringified error and rebuild on read.
type cachedUpdate struct {
	Latest    string    `json:"latest"`
	FetchedAt time.Time `json:"fetched_at"`
	ErrString string    `json:"error,omitempty"`
}

// readCache returns the cached update info, or (zero, false) when
// no fresh cache exists. Parse errors and stale entries both
// return (zero, false) — we re-fetch instead of trusting bad data.
func readCache() (cachedUpdate, bool) {
	b, err := os.ReadFile(updateCachePath())
	if err != nil {
		return cachedUpdate{}, false
	}
	var c cachedUpdate
	if err := json.Unmarshal(b, &c); err != nil {
		return cachedUpdate{}, false
	}
	if time.Since(c.FetchedAt) > updateCacheTTL {
		return cachedUpdate{}, false
	}
	return c, true
}

// writeCache persists info atomically. Best-effort: failures are
// logged via the returned error and the caller should ignore them
// (the next invocation will just hit GitHub again).
func writeCache(c cachedUpdate) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFileMkdir(updateCachePath(), b, 0o644, 0o755)
}

// updateHTTPClient is package-level so tests can swap it. Real
// callers get the default 5-second timeout.
var updateHTTPClient = &http.Client{Timeout: 5 * time.Second}

// CheckForUpdate is the entry point. Returns the cached result
// when fresh; otherwise hits the API, persists, returns. Never
// blocks longer than the HTTP client's timeout.
func CheckForUpdate(ctx context.Context) UpdateInfo {
	if c, ok := readCache(); ok {
		return buildInfo(c)
	}

	c := cachedUpdate{FetchedAt: time.Now().UTC()}
	latest, err := fetchLatestTag(ctx)
	if err != nil {
		c.ErrString = err.Error()
	} else {
		c.Latest = latest
	}
	_ = writeCache(c) // best-effort
	return buildInfo(c)
}

func buildInfo(c cachedUpdate) UpdateInfo {
	// Resolved() honours the goreleaser ldflags-baked tag, then
	// debug.BuildInfo, then the dev-fallback const — using the
	// bare `Version` here would always return "0.21.7" on a
	// production binary and falsely flag every install as out of
	// date. The HasUpdate compare must read the same source so
	// the two facts (Current shown, HasUpdate decided) stay in
	// agreement.
	current := Resolved()
	info := UpdateInfo{
		Latest:    c.Latest,
		Current:   current,
		FetchedAt: c.FetchedAt,
	}
	if c.ErrString != "" {
		info.Err = errors.New(c.ErrString)
		return info
	}
	if c.Latest == "" {
		return info
	}
	info.HasUpdate = isNewer(c.Latest, current)
	return info
}

// fetchLatestTag hits the Releases API and returns the tag_name
// of the latest release. Anonymous; rate-limit applies per IP.
func fetchLatestTag(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentUpdateCheckURL(), nil)
	if err != nil {
		return "", err
	}
	// User-Agent is required by the GitHub API for anonymous calls.
	// Resolved() so production binaries identify themselves with
	// the actual goreleaser tag — bare Version would freeze the UA
	// at the dev-fallback "0.21.7" forever and rate-limit forensics
	// across hosts would be useless.
	req.Header.Set("User-Agent", "clawtool-update-check/"+Resolved())
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := updateHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("github releases: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode releases response: %w", err)
	}
	return payload.TagName, nil
}

// isNewer reports whether `latest` (e.g. "v0.10.0") describes a
// newer release than `current` (e.g. "0.9.0"). Stripped of
// leading "v"; comparison done lexicographically over the dotted
// integer triplet. Pre-release suffixes (-rc.1, -dev) are ignored.
func isNewer(latest, current string) bool {
	l := normalizeVersion(latest)
	c := normalizeVersion(current)
	if l == nil || c == nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// normalizeVersion turns "v0.10.0", "0.9.0", "0.8.6-13-gabc-dirty"
// into [0,10,0] / [0,9,0] / [0,8,6]. Returns nil on malformed
// input — the caller treats that as "don't know, no update."
func normalizeVersion(v string) []int {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return nil
	}
	out := []int{0, 0, 0}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}
