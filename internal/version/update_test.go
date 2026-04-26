package version

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeVersion_StripsPrefixesAndSuffixes(t *testing.T) {
	cases := []struct {
		in   string
		want []int
	}{
		{"v0.10.0", []int{0, 10, 0}},
		{"0.9.0", []int{0, 9, 0}},
		{"v1.2.3", []int{1, 2, 3}},
		{"0.8.6-13-gabc-dirty", []int{0, 8, 6}},
		{"  v0.10.0  ", []int{0, 10, 0}},
		{"0.10", []int{0, 10, 0}},
		{"junk", nil},
		{"v0.x.0", nil},
		{"", nil},
	}
	for _, c := range cases {
		got := normalizeVersion(c.in)
		if c.want == nil {
			if got != nil {
				t.Errorf("normalizeVersion(%q) = %v, want nil", c.in, got)
			}
			continue
		}
		if len(got) != 3 {
			t.Errorf("normalizeVersion(%q) length = %d, want 3", c.in, len(got))
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("normalizeVersion(%q)[%d] = %d, want %d", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestIsNewer_ComparesAcrossPrefixes(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"v0.10.0", "0.9.0", true},
		{"v0.9.0", "v0.9.0", false},
		{"v0.9.0", "v0.10.0", false},
		{"v1.0.0", "v0.99.0", true},
		{"v0.10.1", "0.10.0", true},
		{"junk", "0.9.0", false},
		{"v0.9.0", "junk", false},
	}
	for _, c := range cases {
		got := isNewer(c.latest, c.current)
		if got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.latest, c.current, got, c.want)
		}
	}
}

// withCacheDir redirects the update-cache path into a tempdir so
// tests don't pollute the real ~/.cache/clawtool.
func withCacheDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prev := os.Getenv("XDG_CACHE_HOME")
	os.Setenv("XDG_CACHE_HOME", dir)
	t.Cleanup(func() { os.Setenv("XDG_CACHE_HOME", prev) })
	return dir
}

func TestCheckForUpdate_HitsAPIAndCaches(t *testing.T) {
	withCacheDir(t)

	// Stub a GitHub-API-shaped server returning a tag newer than
	// whatever version.Version happens to be.
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tag_name": "v999.0.0",
		})
	}))
	defer server.Close()

	prevURL := UpdateCheckURL
	prevClient := updateHTTPClient
	defer func() {
		updateHTTPClient = prevClient
	}()
	// We can't shadow the const, but we can swap the client to a
	// transport that always rewrites URL → server.URL. Cleaner: use
	// a custom RoundTripper.
	updateHTTPClient = &http.Client{
		Transport: rewriteTransport{target: server.URL},
		Timeout:   3 * time.Second,
	}
	_ = prevURL

	info := CheckForUpdate(context.Background())
	if info.Err != nil {
		t.Fatalf("CheckForUpdate err: %v", info.Err)
	}
	if info.Latest != "v999.0.0" {
		t.Errorf("Latest = %q, want v999.0.0", info.Latest)
	}
	if !info.HasUpdate {
		t.Errorf("HasUpdate should be true (latest > current=%s)", info.Current)
	}
	if hits != 1 {
		t.Errorf("first call should hit API once; got %d hits", hits)
	}

	// Second call within TTL → must use cache, no extra hit.
	_ = CheckForUpdate(context.Background())
	if hits != 1 {
		t.Errorf("second call should hit cache; got %d total hits", hits)
	}
}

func TestCheckForUpdate_NetworkErrorYieldsErr(t *testing.T) {
	withCacheDir(t)
	prevClient := updateHTTPClient
	defer func() { updateHTTPClient = prevClient }()

	updateHTTPClient = &http.Client{
		Transport: rewriteTransport{target: "http://127.0.0.1:1"}, // closed port
		Timeout:   500 * time.Millisecond,
	}
	info := CheckForUpdate(context.Background())
	if info.Err == nil {
		t.Fatal("expected Err on network failure")
	}
	if info.HasUpdate {
		t.Error("HasUpdate must be false when the check itself failed")
	}
}

func TestCheckForUpdate_StaleCacheTriggersRefetch(t *testing.T) {
	dir := withCacheDir(t)

	// Pre-write a stale entry (older than TTL) for v0.0.1 so the
	// next call refetches. We do this by writing a cache file with
	// an old FetchedAt.
	stale := cachedUpdate{
		Latest:    "v0.0.1",
		FetchedAt: time.Now().Add(-48 * time.Hour),
	}
	b, _ := json.MarshalIndent(stale, "", "  ")
	cacheDir := filepath.Join(dir, "clawtool")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "update.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}

	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.10.0"})
	}))
	defer server.Close()

	prevClient := updateHTTPClient
	defer func() { updateHTTPClient = prevClient }()
	updateHTTPClient = &http.Client{
		Transport: rewriteTransport{target: server.URL},
		Timeout:   3 * time.Second,
	}

	_ = CheckForUpdate(context.Background())
	if hits != 1 {
		t.Errorf("stale cache should trigger one refetch; got %d", hits)
	}
}

// rewriteTransport bends every outgoing request to a local
// httptest server, regardless of the URL the caller passed.
type rewriteTransport struct{ target string }

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	url := req.URL
	url.Scheme = "http"
	parsedTarget := req.Clone(req.Context())
	_ = parsedTarget
	// Simplest: mutate the host to the target server. The
	// httptest.Server URL is "http://127.0.0.1:PORT".
	target := rt.target
	for _, prefix := range []string{"http://", "https://"} {
		if len(target) > len(prefix) && target[:len(prefix)] == prefix {
			target = target[len(prefix):]
			break
		}
	}
	url.Host = target
	url.Scheme = "http"
	req.URL = url
	return http.DefaultTransport.RoundTrip(req)
}
