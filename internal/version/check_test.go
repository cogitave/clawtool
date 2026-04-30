package version

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestCheckResult_ExitCode covers the documented contract that
// scripts will gate on: 0 = up-to-date, 1 = newer release
// available, 2 = check itself failed. Any change here breaks the
// CI pipelines wrapping `clawtool version --check`.
func TestCheckResult_ExitCode(t *testing.T) {
	cases := []struct {
		name string
		r    CheckResult
		want int
	}{
		{"up-to-date", CheckResult{Up: true}, 0},
		{"newer-available", CheckResult{Up: false}, 1},
		{"check-failed-overrides-up", CheckResult{Up: true, Error: "boom"}, 2},
		{"check-failed-overrides-not-up", CheckResult{Up: false, Error: "boom"}, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.r.ExitCode(); got != c.want {
				t.Errorf("ExitCode() = %d, want %d", got, c.want)
			}
		})
	}
}

// TestRunCheck_UpToDate stubs GitHub returning the same tag as the
// local Version — RunCheck must return exit 0 and a banner that
// names "up to date" + the running semver.
func TestRunCheck_UpToDate(t *testing.T) {
	withCacheDir(t)
	cleanup := stubGitHub(t, "v"+Version)
	defer cleanup()

	var buf bytes.Buffer
	code := RunCheck(context.Background(), false, &buf)
	if code != 0 {
		t.Errorf("exit = %d, want 0", code)
	}
	out := buf.String()
	if !strings.Contains(out, "up to date") {
		t.Errorf("banner missing 'up to date': %q", out)
	}
	if !strings.Contains(out, Version) {
		t.Errorf("banner missing current version %q: %q", Version, out)
	}
}

// TestRunCheck_HasUpdate stubs GitHub returning a tag higher than
// any plausible local Version — RunCheck must return exit 1 and a
// banner that names "update available".
func TestRunCheck_HasUpdate(t *testing.T) {
	withCacheDir(t)
	cleanup := stubGitHub(t, "v999.0.0")
	defer cleanup()

	var buf bytes.Buffer
	code := RunCheck(context.Background(), false, &buf)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	out := buf.String()
	if !strings.Contains(out, "update available") {
		t.Errorf("banner missing 'update available': %q", out)
	}
	if !strings.Contains(out, "999.0.0") {
		t.Errorf("banner missing latest tag: %q", out)
	}
	if !strings.Contains(out, "clawtool upgrade") {
		t.Errorf("banner missing upgrade hint: %q", out)
	}
}

// TestRunCheck_GitHubError points the HTTP client at a closed port
// so CheckForUpdate yields an error — RunCheck must return exit 2
// and a banner that says the check itself failed (NOT "up to
// date" — a script-fail is not an affirmative answer).
func TestRunCheck_GitHubError(t *testing.T) {
	withCacheDir(t)
	prevClient := updateHTTPClient
	prevURL := updateCheckURLOverride
	defer func() {
		updateHTTPClient = prevClient
		updateCheckURLOverride = prevURL
	}()
	updateHTTPClient = &http.Client{
		Transport: rewriteTransport{target: "http://127.0.0.1:1"},
		Timeout:   500 * time.Millisecond,
	}
	updateCheckURLOverride = "http://127.0.0.1:1/x"

	var buf bytes.Buffer
	code := RunCheck(context.Background(), false, &buf)
	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	out := buf.String()
	if !strings.Contains(out, "could not check") {
		t.Errorf("banner missing 'could not check': %q", out)
	}
	if strings.Contains(out, "up to date") {
		t.Errorf("banner must NOT claim up-to-date on check failure: %q", out)
	}
}

// TestRunCheck_JSONOutput asserts --json renders parseable JSON
// with snake_case keys + the documented exit code is preserved.
// Pipelines pipe this into jq; the contract has to stay stable.
func TestRunCheck_JSONOutput(t *testing.T) {
	withCacheDir(t)
	cleanup := stubGitHub(t, "v999.0.0")
	defer cleanup()

	var buf bytes.Buffer
	code := RunCheck(context.Background(), true, &buf)
	if code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	body := buf.String()
	for _, lit := range []string{`"up_to_date":`, `"current":`, `"latest":`, `"fetched_at":`} {
		if !strings.Contains(body, lit) {
			t.Errorf("JSON missing literal %s; body: %s", lit, body)
		}
	}
	var got CheckResult
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, body)
	}
	if got.Up {
		t.Error("up_to_date = true, want false (latest is newer)")
	}
	if got.Latest != "v999.0.0" {
		t.Errorf("latest = %q, want v999.0.0", got.Latest)
	}
}

// TestRunCheck_JSONOutput_HonoursStubbedTime confirms the
// fetched_at field is populated (non-zero) so monitoring scripts
// can reason about staleness even on the cached path.
func TestRunCheck_JSONOutput_HonoursStubbedTime(t *testing.T) {
	withCacheDir(t)
	cleanup := stubGitHub(t, "v"+Version)
	defer cleanup()

	var buf bytes.Buffer
	_ = RunCheck(context.Background(), true, &buf)

	var got CheckResult
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if got.FetchedAt.IsZero() {
		t.Error("fetched_at is zero; monitoring scripts can't reason about staleness")
	}
	if time.Since(got.FetchedAt) > time.Minute {
		t.Errorf("fetched_at = %v, want within last minute", got.FetchedAt)
	}
}

// TestRunCheck_ServerError covers the 5xx-from-GitHub case
// (rate-limit response would also land here). Must surface as
// exit 2, not exit 1 — a script-side error must not be
// indistinguishable from "newer release available".
func TestRunCheck_ServerError(t *testing.T) {
	withCacheDir(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()

	prevClient := updateHTTPClient
	prevURL := updateCheckURLOverride
	defer func() {
		updateHTTPClient = prevClient
		updateCheckURLOverride = prevURL
	}()
	updateHTTPClient = srv.Client()
	updateCheckURLOverride = srv.URL

	var buf bytes.Buffer
	code := RunCheck(context.Background(), false, &buf)
	if code != 2 {
		t.Errorf("exit = %d, want 2 on 403", code)
	}
}
