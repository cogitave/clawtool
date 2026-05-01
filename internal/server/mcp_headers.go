// Mcp-Method / Mcp-Name HTTP header handling for the Streamable
// HTTP transport (SEP-2243, finalized 2026-04-17).
//
// SEP-2243 adds two request headers on Streamable HTTP POSTs so
// load balancers, rate-limiters, and metrics pipelines can route
// without parsing the JSON-RPC body:
//
//   - Mcp-Method: <method-name>     e.g. "tools/call", "tools/list"
//   - Mcp-Name:   <tool-or-prompt>  e.g. "mcp__clawtool__SendMessage"
//
// Mcp-Name is only meaningful for methods that carry a sub-target
// (`tools/call` → params.name, `prompts/get` → params.name); for
// methods like `notifications/initialized` we OMIT the header
// rather than send empty — the spec language ("for methods with a
// sub-target") reads as "don't include otherwise" and an absent
// header is unambiguous to proxies, where empty-string is a
// matchable value that complicates rules.
//
// Server side (mcpHeaderMiddleware): reads incoming Mcp-Method,
// falls back to body inspection when absent, prefers the body's
// JSON-RPC method when the two disagree (logging a stderr
// warning), exposes the resolved values via context, and echoes
// them on the response so clients can see what we processed.
//
// Client side (BuildMCPRequest): sets Mcp-Method from the
// JSON-RPC body's method field and sets Mcp-Name from params.name
// only when the method is one that carries a sub-target. Used by
// any code in clawtool that POSTs to an upstream Streamable HTTP
// MCP server (today: tests + future outbound transports).
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// HTTP header names. Exported so callers building requests can
// reference them without re-declaring the constants.
const (
	HeaderMcpMethod = "Mcp-Method"
	HeaderMcpName   = "Mcp-Name"
)

// mcpCtxKey is the unexported context-key type used by the
// middleware so handlers can pull the resolved method / name out
// without colliding with other context values.
type mcpCtxKey struct{ k string }

var (
	ctxKeyMcpMethod = mcpCtxKey{k: "mcp-method"}
	ctxKeyMcpName   = mcpCtxKey{k: "mcp-name"}
)

// MCPMethodFromContext returns the JSON-RPC method resolved by the
// middleware (body wins on mismatch). Empty string when the
// middleware did not run or the body was unparseable.
func MCPMethodFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyMcpMethod).(string)
	return v
}

// MCPNameFromContext returns the sub-target (tool or prompt name)
// for tools/call / prompts/get. Empty string for other methods or
// when params.name is missing.
func MCPNameFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyMcpName).(string)
	return v
}

// methodHasSubTarget reports whether a JSON-RPC method carries a
// `params.name` field that should populate Mcp-Name. Only the two
// methods SEP-2243 calls out qualify.
func methodHasSubTarget(method string) bool {
	switch method {
	case "tools/call", "prompts/get":
		return true
	default:
		return false
	}
}

// jsonrpcEnvelope is the minimum shape we need to extract method +
// params.name. Everything else is left to the inner handler.
type jsonrpcEnvelope struct {
	Method string `json:"method"`
	Params struct {
		Name string `json:"name"`
	} `json:"params"`
}

// peekJSONRPC reads + restores r.Body, returning the parsed
// envelope and whether the parse succeeded. A failed parse is not
// fatal — the inner handler will surface its own JSON-RPC error;
// the middleware just doesn't get to populate context.
func peekJSONRPC(r *http.Request) (jsonrpcEnvelope, bool) {
	if r.Body == nil {
		return jsonrpcEnvelope{}, false
	}
	// Cap the peek so a multi-megabyte body doesn't stall the
	// middleware. Real JSON-RPC envelopes are tiny; a 1 MiB cap
	// matches the limit handleSendMessage applies to its own
	// inputs.
	const peekCap = 1 << 20
	buf, err := io.ReadAll(io.LimitReader(r.Body, peekCap))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(buf))
	if err != nil {
		return jsonrpcEnvelope{}, false
	}
	var env jsonrpcEnvelope
	if err := json.Unmarshal(buf, &env); err != nil {
		return jsonrpcEnvelope{}, false
	}
	return env, true
}

// mcpHeaderMiddleware implements the SEP-2243 server-side contract:
//
//	resolved := body.method (if parseable) else r.Header[Mcp-Method]
//	if both present and differ → prefer body, log warning
//	store resolved (method, name) in ctx
//	echo on response: Mcp-Method always, Mcp-Name only when sub-target
//
// Wraps an inner http.Handler — typically the streamable handler
// chain — and is mounted on the same /mcp route. Idempotent: a
// request that already passed through the middleware (re-entrant
// mount, test harness chaining) is handled correctly because we
// always re-resolve from headers + body.
func mcpHeaderMiddleware(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdrMethod := strings.TrimSpace(r.Header.Get(HeaderMcpMethod))
		hdrName := strings.TrimSpace(r.Header.Get(HeaderMcpName))

		env, parsed := peekJSONRPC(r)

		// Resolve method: body wins. The header is a hint for
		// the proxy fast-path, not a source of truth — a
		// malicious or buggy client could mislabel and we
		// must not let that drift the handler's view.
		resolvedMethod := hdrMethod
		if parsed && env.Method != "" {
			if hdrMethod != "" && hdrMethod != env.Method {
				fmt.Fprintf(os.Stderr,
					"clawtool: SEP-2243 Mcp-Method header %q disagrees with body method %q; preferring body\n",
					hdrMethod, env.Method)
			}
			resolvedMethod = env.Method
		}

		// Resolve name: only for sub-target methods. Body's
		// params.name wins over the header for the same
		// "header is a hint" reason.
		resolvedName := ""
		if methodHasSubTarget(resolvedMethod) {
			if parsed && env.Params.Name != "" {
				resolvedName = env.Params.Name
			} else if hdrName != "" {
				resolvedName = hdrName
			}
		}

		ctx := r.Context()
		if resolvedMethod != "" {
			ctx = context.WithValue(ctx, ctxKeyMcpMethod, resolvedMethod)
		}
		if resolvedName != "" {
			ctx = context.WithValue(ctx, ctxKeyMcpName, resolvedName)
		}

		// Echo on the response so clients can see what we
		// processed. Set BEFORE the inner handler runs so the
		// values are flushed even when inner streams its body.
		// Mcp-Name is intentionally OMITTED (not set empty) for
		// methods without a sub-target — see package doc.
		if resolvedMethod != "" {
			w.Header().Set(HeaderMcpMethod, resolvedMethod)
		}
		if resolvedName != "" {
			w.Header().Set(HeaderMcpName, resolvedName)
		}

		inner.ServeHTTP(w, r.WithContext(ctx))
	})
}

// BuildMCPRequest constructs an *http.Request for a Streamable
// HTTP MCP POST with SEP-2243 headers populated from `body`.
//
//   - body must be a JSON-encoded JSON-RPC request (object).
//     Method + (when applicable) params.name are extracted by
//     re-parsing — caller doesn't have to pass them separately.
//   - Mcp-Method is always set when the body has a non-empty
//     method field.
//   - Mcp-Name is set only when the method is `tools/call` or
//     `prompts/get` AND params.name is non-empty. For any other
//     method, the header is OMITTED entirely (not sent as
//     empty-string) — this matches the spec wording and keeps
//     proxy rule-matching unambiguous.
//   - Content-Type is always set to application/json so the
//     caller doesn't have to remember.
//
// Returns a request ready to hand to http.Client.Do.
func BuildMCPRequest(ctx context.Context, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	var env jsonrpcEnvelope
	if err := json.Unmarshal(body, &env); err == nil {
		if env.Method != "" {
			req.Header.Set(HeaderMcpMethod, env.Method)
		}
		if methodHasSubTarget(env.Method) && env.Params.Name != "" {
			req.Header.Set(HeaderMcpName, env.Params.Name)
		}
	}
	return req, nil
}
