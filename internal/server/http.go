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
	"github.com/cogitave/clawtool/internal/setup"
	"github.com/cogitave/clawtool/internal/telemetry"
	"github.com/cogitave/clawtool/internal/version"

	// Blank import: ensures every recipe package's init() runs before
	// runRecipeApply touches the registry. Mirrors the same trick
	// recipes_tool.go uses for the MCP path.
	_ "github.com/cogitave/clawtool/internal/setup/recipes"

	mcpserver "github.com/mark3labs/mcp-go/server"
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
// MCP-over-HTTP (`--mcp-http`) mounts the full toolset at /mcp via
// mark3labs/mcp-go's StreamableHTTPServer (the persistent shared
// daemon every host fans into; see internal/daemon).
func ServeHTTP(ctx context.Context, opts HTTPOptions) error {
	if strings.TrimSpace(opts.Listen) == "" {
		return errors.New("--listen is required (e.g. ':8080')")
	}
	token, err := loadToken(opts.TokenFile)
	if err != nil {
		return err
	}

	bootedAt := time.Now()
	mcpSrv, mgr, _, _, err := buildMCPServer(ctx, "http")
	if err != nil {
		return err
	}
	defer mgr.Stop()
	// Pair the server.start emit (fired in buildMCPServer) with a
	// matching server.stop on the way out. Pre-fix this only fired
	// for stdio, which made the stdio respawn-spam pattern look
	// like the only thing producing stop events — codex's diagnosis
	// of the v0.22.22 PostHog snapshot relied on that. Now both
	// transports are symmetric.
	defer func() {
		if tc := telemetry.Get(); tc != nil && tc.Enabled() {
			outcome := "success"
			if err != nil {
				outcome = "error"
			}
			tc.Track("server.stop", map[string]any{
				"version":      version.Resolved(),
				"duration_ms":  time.Since(bootedAt).Milliseconds(),
				"outcome":      outcome,
				"transport":    "http",
				"$session_end": true,
			})
			_ = tc.Close()
		}
	}()

	mux := http.NewServeMux()
	authed := authMiddleware(token)

	mux.Handle("/v1/health", authed(http.HandlerFunc(handleHealth)))
	mux.Handle("/v1/agents", authed(http.HandlerFunc(handleAgents)))
	mux.Handle("/v1/send_message", authed(http.HandlerFunc(handleSendMessage)))
	mux.Handle("/v1/recipes", authed(http.HandlerFunc(handleRecipes)))
	mux.Handle("/v1/recipe/apply", authed(http.HandlerFunc(handleRecipeApply)))
	// /v1/peers — A2A Phase 1 peer registry. The handler dispatches on
	// (method, path-suffix): GET /v1/peers (list), POST /v1/peers/register,
	// POST /v1/peers/{id}/heartbeat, DELETE /v1/peers/{id}, GET /v1/peers/{id}.
	// Single mux entry routes all subpaths via the trailing slash.
	mux.Handle("/v1/peers", authed(http.HandlerFunc(handlePeers)))
	mux.Handle("/v1/peers/", authed(http.HandlerFunc(handlePeers)))

	// Optional MCP-over-HTTP transport. Mounts the full clawtool MCP
	// toolset (Bash, Read, Edit, SendMessage, BridgeAdd, …) at /mcp via
	// mark3labs/mcp-go's StreamableHTTPServer. Bearer auth still
	// applies — the streamable handler is wrapped by authed.
	if opts.MCPHTTP {
		streamable := mcpserver.NewStreamableHTTPServer(mcpSrv)
		mux.Handle("/mcp", authed(streamable))
		mux.Handle("/mcp/", authed(http.StripPrefix("/mcp", streamable)))
	}

	// Catch-all for unknown paths — return 404 with a JSON body
	// mentioning the supported endpoints (mirrors ADR-014's
	// "default-deny on unrecognised paths" guidance).
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error": fmt.Sprintf("unknown path %q (see GET /v1/health for the live endpoint list)", r.URL.Path),
			"endpoints": []string{
				"GET    /v1/health",
				"GET    /v1/agents",
				"POST   /v1/send_message",
				"GET    /v1/recipes [?category=<c>]",
				"POST   /v1/recipe/apply",
				"GET    /v1/peers [?status=&backend=&circle=&path=]",
				"GET    /v1/peers/{peer_id}",
				"POST   /v1/peers/register",
				"POST   /v1/peers/{peer_id}/heartbeat",
				"DELETE /v1/peers/{peer_id}",
			},
		})
	})

	srv := &http.Server{
		Addr:              opts.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	// shutdownDone signals when the graceful Shutdown finished.
	// Without this, ListenAndServe returns ErrServerClosed the
	// instant Shutdown begins, and the caller proceeds to tear
	// down the manager / telemetry / store while in-flight
	// handlers are still draining. The bounded 30 s deadline on
	// Shutdown is the upper limit for any active SSE / streaming
	// MCP HTTP request to flush before we force-close.
	shutdownDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		close(shutdownDone)
	}()

	fmt.Fprintf(os.Stderr, "clawtool: listening on %s (token-file: %s)\n", opts.Listen, opts.TokenFile)
	listenErr := srv.ListenAndServe()
	if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
		return fmt.Errorf("listen %s: %w", opts.Listen, listenErr)
	}
	// Block until the shutdown goroutine finishes draining. ctx
	// already fired (that's why ListenAndServe returned), so this
	// just waits out the in-flight handlers. If ListenAndServe
	// errored for a non-shutdown reason (port in use, etc.) the
	// goroutine is still waiting on ctx.Done — let the caller's
	// ctx cancellation eventually fire it; a stuck goroutine
	// outlives a fatal listen error and that's fine.
	if errors.Is(listenErr, http.ErrServerClosed) {
		<-shutdownDone
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
	// Resolved() picks the goreleaser-baked ldflags string when
	// present, falls back to debug.ReadBuildInfo, then to the
	// const. Pre-fix this read version.Resolved() directly, so a
	// container running v0.22.x advertised "0.21.7" on /v1/health
	// (the const value at the time the var was introduced) — caught
	// during Docker e2e probe at v0.22.23.
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": version.Resolved(),
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
// Phase 4: top-level `tag` field is sugar for `opts.tag` so callers
// don't have to nest a single value under opts.
type sendMessageRequest struct {
	Instance string         `json:"instance"`
	Prompt   string         `json:"prompt"`
	Tag      string         `json:"tag,omitempty"`
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
	if req.Tag != "" {
		if req.Opts == nil {
			req.Opts = map[string]any{}
		}
		req.Opts["tag"] = req.Tag
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

// ── recipes ────────────────────────────────────────────────────────

// recipeInfo is the JSON shape /v1/recipes returns. Mirrors the MCP
// `RecipeList` tool's row shape so HTTP and MCP callers see the same
// fields. Body fields are populated read-only — Apply is the mutator.
type recipeInfoJSON struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Upstream    string `json:"upstream"`
	Stability   string `json:"stability"`
	Status      string `json:"status,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

func handleRecipes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "GET only"})
		return
	}
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))

	var recipes []setup.Recipe
	if category != "" {
		cat := setup.Category(category)
		if !cat.Valid() {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": fmt.Sprintf("unknown category %q", category),
			})
			return
		}
		recipes = setup.InCategory(cat)
	} else {
		for _, c := range setup.Categories() {
			recipes = append(recipes, setup.InCategory(c)...)
		}
	}
	out := make([]recipeInfoJSON, 0, len(recipes))
	for _, rc := range recipes {
		m := rc.Meta()
		row := recipeInfoJSON{
			Name:        m.Name,
			Category:    string(m.Category),
			Description: m.Description,
			Upstream:    m.Upstream,
			Stability:   string(m.Stability),
		}
		if repo != "" {
			st, detail, _ := rc.Detect(r.Context(), repo)
			row.Status = string(st)
			row.Detail = detail
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"recipes": out,
		"count":   len(out),
	})
}

// recipeApplyRequest is the inbound body shape. Repo and Options
// mirror the MCP tool's parameters.
type recipeApplyRequest struct {
	Name    string         `json:"name"`
	Repo    string         `json:"repo,omitempty"`
	Options map[string]any `json:"options,omitempty"`
}

func handleRecipeApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST only"})
		return
	}
	var req recipeApplyRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": fmt.Sprintf("decode body: %v", err)})
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name is required"})
		return
	}
	rc := setup.Lookup(req.Name)
	if rc == nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": fmt.Sprintf("unknown recipe %q", req.Name),
		})
		return
	}
	repo := strings.TrimSpace(req.Repo)
	if repo == "" {
		// HTTP callers (orchestrators / CI hooks) won't have a
		// terminal cwd; refuse rather than silently mutating $HOME.
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "repo is required when applying via HTTP (no implicit cwd)",
		})
		return
	}
	res, applyErr := setup.Apply(r.Context(), rc, setup.ApplyOptions{
		Repo:          repo,
		RecipeOptions: setup.Options(req.Options),
		Prompter:      setup.AlwaysSkip{},
	})
	body := map[string]any{
		"recipe":            res.Recipe,
		"category":          string(res.Category),
		"repo":              repo,
		"skipped":           res.Skipped,
		"skip_reason":       res.SkipReason,
		"installed_prereqs": res.Installed,
		"manual_prereqs":    res.ManualHints,
		"verify_ok":         res.VerifyErr == nil && !res.Skipped,
	}
	if res.VerifyErr != nil {
		body["verify_error"] = res.VerifyErr.Error()
	}
	if applyErr != nil {
		body["error"] = applyErr.Error()
		writeJSON(w, http.StatusBadRequest, body)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

// ── helpers ────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
