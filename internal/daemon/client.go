// Package daemon — HTTP client helper. One canonical dial path for
// everything that wants to call the local daemon's HTTP listener:
// CLI subcommands (`clawtool peer …`, `clawtool a2a peers`) and the
// orchestrator TUI's peers panel both pump through here.
//
// Centralizing this avoids three near-identical copies of "read
// state, read token, build request, set bearer + Content-Type, do
// it with a 5s timeout, decode JSON, surface daemon errors as Go
// errors" — and keeps timeout/auth invariants in one spot when we
// want to tune them.
package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// httpRequestTimeout is well under any hook's 60 s budget — a wedged
// daemon should not stall a Stop event while we wait on it.
const httpRequestTimeout = 5 * time.Second

// HTTPRequest dials the local daemon's HTTP listener with the shared
// bearer token. body may be nil for GET/DELETE; out may be nil when
// the caller doesn't care about the response payload. Daemon-side
// errors (HTTP >= 300) are surfaced as Go errors with the daemon's
// JSON {"error": "..."} string when present.
func HTTPRequest(method, path string, body *bytes.Reader, out any) error {
	state, err := ReadState()
	if err != nil {
		return fmt.Errorf("read daemon state: %w", err)
	}
	if state == nil {
		return errors.New("no daemon running — start it with `clawtool daemon start`")
	}
	tok, _ := ReadToken()
	url := fmt.Sprintf("http://127.0.0.1:%d%s", state.Port, path)

	ctx, cancel := context.WithTimeout(context.Background(), httpRequestTimeout)
	defer cancel()
	var req *http.Request
	if body != nil {
		req, err = http.NewRequestWithContext(ctx, method, url, body)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, url, nil)
	}
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: httpRequestTimeout}).Do(req)
	if err != nil {
		return fmt.Errorf("dial daemon: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return fmt.Errorf("daemon returned %d: %s", resp.StatusCode, e.Error)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
