// Package server — HTTP gateway (ADR-014 Phase 2, v0.11).
//
// `clawtool serve --listen :8080 --token-file <path>` mounts a thin
// HTTP surface that proxies prompts to the supervisor and exposes the
// agent registry. Every dispatch goes through Supervisor.Send (same
// call site as the CLI / MCP). Auth is bearer-token at the edge —
// non-negotiable; the relay opens an exec-arbitrary-code-on-host
// surface.
//
// TLS is not terminated here. Operators front this with nginx /
// caddy / Cloudflare Tunnel — we do not invent a cert story (see
// ADR-014 Rationale).
package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cogitave/clawtool/internal/agents"
	"github.com/cogitave/clawtool/internal/version"
)

// HTTPOptions configures the listener.
type HTTPOptions struct {
	Listen    string // ":8080" or "0.0.0.0:8080" — passed to http.ListenAndServe.
	TokenFile string // path to a 0600 file containing the bearer token. Refused if missing/empty.
	MCPHTTP   bool   // when true, mount the MCP toolset at /mcp via mcp-go's Streamable HTTP transport.
}

// ServeHTTP runs clawtool as an HTTP gateway. Blocks until the
// listener returns. Mirrors ServeStdio's lifecycle: build the MCP
// server (so the same agents/recipes/tools are available), then
// route HTTP requests through it.
//
// The MCP-over-HTTP transport (when MCPHTTP=true) is wired in a
// follow-up patch — Phase 2's first iteration ships the v1 REST
// endpoints and bearer auth; mcp-go's StreamableHTTPServer plug-in
// lands as polish.
func ServeHTTP(ctx context.Context, opts HTTPOptions) error {
	if strings.TrimSpace(opts.Listen) == "" {
		return errors.New("--listen is required (e.g. ':8080')")
	}
	token, err := loadToken(opts.TokenFile)
	if err != nil {
		return err
	}

	_, mgr, _, _, err := buildMCPServer(ctx)
	if err != nil {
		return err
	}
	defer mgr.Stop()

	mux := http.NewServeMux()
	authed := authMiddleware(token)

	mux.Handle("/v1/health", authed(http.HandlerFunc(handleHealth)))
	mux.Handle("/v1/agents", authed(http.HandlerFunc(handleAgents)))
	mux.Handle("/v1/send_message", authed(http.HandlerFunc(handleSendMessage)))

	// Catch-all for unknown paths — return 404 with a JSON body
	// mentioning the supported endpoints (mirrors ADR-014's
	// "default-deny on unrecognised paths" guidance).
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": fmt.Sprintf("unknown path %q (see GET /v1/health for the live endpoint list)", r.URL.Path),
			"endpoints": []string{
				"GET  /v1/health",
				"GET  /v1/agents",
				"POST /v1/send_message",
			},
		})
	})

	srv := &http.Server{
		Addr:              opts.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		_ = srv.Shutdown(context.Background())
	}()

	fmt.Fprintf(os.Stderr, "clawtool: listening on %s (token-file: %s)\n", opts.Listen, opts.TokenFile)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen %s: %w", opts.Listen, err)
	}
	return nil
}

// loadToken reads + validates the bearer-token file. Empty / unreadable
// → hard error. Permissions check is best-effort and surfaced as a
// stderr warning rather than a refusal so dev setups (mode 644 in a
// container) still work; production hardens via the stricter file
// mode the operator chooses.
func loadToken(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("--token-file is required (run `clawtool serve init-token` to generate one)")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read token file %s: %w", path, err)
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("token file %s is empty", path)
	}
	if info, err := os.Stat(path); err == nil {
		if info.Mode().Perm()&0o077 != 0 {
			fmt.Fprintf(os.Stderr,
				"clawtool: token file %s is world/group-readable (mode %v) — chmod 0600 is recommended\n",
				path, info.Mode().Perm())
		}
	}
	return tok, nil
}

// InitTokenFile generates a fresh 32-byte (256-bit) hex token and writes
// it to path with 0600. Used by `clawtool serve init-token` and by tests
// that need a working credential.
func InitTokenFile(path string) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}

// ── auth ───────────────────────────────────────────────────────────

// authMiddleware enforces `Authorization: Bearer <token>`. Constant-time
// comparison so token-validity timing doesn't leak the prefix.
func authMiddleware(expected string) func(http.Handler) http.Handler {
	exp := []byte(expected)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(h, prefix) {
				writeJSON(w, http.StatusUnauthorized, map[string]any{
					"error": "missing or malformed Authorization header (expected `Bearer <token>`)",
				})
				return
			}
			got := []byte(strings.TrimSpace(h[len(prefix):]))
			if subtle.ConstantTimeCompare(got, exp) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]any{
					"error": "invalid token",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ── handlers ───────────────────────────────────────────────────────

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": version.Version,
	})
}

func handleAgents(w http.ResponseWriter, r *http.Request) {
	sup := agents.NewSupervisor()
	all, err := sup.Agents(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if r.URL.Query().Get("status") == "callable" {
		filtered := all[:0]
		for _, a := range all {
			if a.Callable {
				filtered = append(filtered, a)
			}
		}
		all = filtered
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"agents": all,
		"count":  len(all),
	})
}

// sendMessageRequest is the inbound shape. Mirrors the MCP tool's
// arguments exactly (ADR-014 promises the same shape across surfaces).
type sendMessageRequest struct {
	Instance string         `json:"instance"`
	Prompt   string         `json:"prompt"`
	Opts     map[string]any `json:"opts,omitempty"`
}

func handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return
	}
	var req sendMessageRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("decode body: %v", err)})
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "prompt is required"})
		return
	}
	sup := agents.NewSupervisor()
	rc, err := sup.Send(r.Context(), req.Instance, req.Prompt, req.Opts)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	defer rc.Close()

	// Stream the upstream's wire format verbatim. We set a
	// content-type that admits NDJSON / stream-json while staying
	// permissive — the actual wire format depends on the upstream
	// CLI's --format flag the caller passed.
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, rerr := rc.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // client disconnect; rc.Close cancels the upstream
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			return
		}
	}
}

// ── helpers ────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
