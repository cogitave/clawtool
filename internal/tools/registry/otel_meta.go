// Package registry — OpenTelemetry W3C Trace Context propagation
// over MCP `_meta`, per SEP-414.
//
// MCP request `_meta` and response `_meta` carry W3C Trace
// Context (https://www.w3.org/TR/trace-context/) so a client and
// a server can stitch a single tool call into one distributed
// trace. The two fields are:
//
//   - `traceparent` — `00-<32-hex-trace>-<16-hex-span>-<2-hex-flags>`
//   - `tracestate`  — vendor-specific key-value list, optional
//
// Direction of flow:
//
//  1. Client serialises the active span as `_meta.traceparent`
//     (and optionally `_meta.tracestate`) on the CallToolRequest.
//  2. Server middleware extracts those keys and parents the
//     handler's ctx via the W3C TraceContext propagator. With the
//     observability subsystem off, the remote span context is
//     still attached but no live span is recorded — Inject on the
//     way out simply re-emits the same trace_id/span_id so the
//     client's reply correlates against its own active span.
//     With observability on, agents.NewSupervisor / handler-side
//     `obs.StartSpan` open a child span on top, and the response
//     meta carries that child span's id so the client sees the
//     whole subtree.
//  3. Server middleware Inject()s the (possibly child-of-remote)
//     span context back onto the response's `_meta` envelope.
//
// Fallback: when the request carries no `_meta.traceparent`, the
// middleware honours the `TRACEPARENT` (and `TRACESTATE`) process
// env vars. This keeps the v0.22 env-driven shim working for
// hosts that don't speak SEP-414 yet — they just set the env on
// the spawned MCP child and clawtool stitches them in.
//
// Backwards compatibility: requests with NO trace context (no
// `_meta.traceparent` and no env var) are passed through
// unchanged; the response carries no `traceparent` either, so
// older clients that weren't reading these keys see exactly the
// pre-SEP-414 wire shape.
package registry

import (
	"context"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// W3C Trace Context header names. Per the spec these are
// lowercase and used as both the HTTP header keys and the MCP
// `_meta` keys (SEP-414 reuses them verbatim so a host can pipe
// HTTP-extracted values straight through without renaming).
const (
	TraceparentMetaKey = "traceparent"
	TracestateMetaKey  = "tracestate"

	// TraceparentEnv / TracestateEnv name the process-env
	// fallback inputs. Uppercased per the v0.22 convention
	// (matches `TRACEPARENT` documented in the wiki).
	TraceparentEnv = "TRACEPARENT"
	TracestateEnv  = "TRACESTATE"
)

// TraceContextMiddleware returns a tool-handler middleware that
// (1) extracts W3C Trace Context from the incoming request's
// `_meta` (or, as a fallback, from the TRACEPARENT/TRACESTATE
// process env), parents the handler's ctx with the remote span
// context, and (2) injects the resulting span context back onto
// the response's `_meta` so the client can stitch the call into
// its own trace.
//
// Wire it on server bootstrap via
// `server.WithToolHandlerMiddleware(registry.TraceContextMiddleware())`.
//
// The middleware is a no-op for requests that carry no trace
// context AND no env fallback — the response's `_meta` is left
// untouched, preserving the pre-SEP-414 wire shape for older
// clients.
func TraceContextMiddleware() server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx = ExtractTraceContextFromRequest(ctx, req)
			res, err := next(ctx, req)
			if res != nil {
				EchoTraceContextOnResult(ctx, res)
			}
			return res, err
		}
	}
}

// ExtractTraceContextFromRequest reads `_meta.traceparent` /
// `_meta.tracestate` off `req` (with TRACEPARENT/TRACESTATE env
// as the fallback) and parents `ctx` with the resulting remote
// SpanContext via the W3C TraceContext propagator. Returns the
// (possibly unchanged) ctx; safe to call when the request has no
// trace context — in that case ctx is returned as-is.
//
// Exposed alongside the middleware so handlers that bypass the
// MCP-go middleware path (e.g. direct in-process dispatch in
// tests) can reuse the same extraction logic.
func ExtractTraceContextFromRequest(ctx context.Context, req mcp.CallToolRequest) context.Context {
	tp, ts := readMetaTraceContext(req.Params.Meta)
	if tp == "" {
		// Fallback to env (preserves the v0.22 shim).
		tp = os.Getenv(TraceparentEnv)
		if ts == "" {
			ts = os.Getenv(TracestateEnv)
		}
	}
	if tp == "" {
		return ctx
	}
	carrier := propagation.MapCarrier{TraceparentMetaKey: tp}
	if ts != "" {
		carrier[TracestateMetaKey] = ts
	}
	return propagation.TraceContext{}.Extract(ctx, carrier)
}

// EchoTraceContextOnResult writes the active span context from
// `ctx` onto `res.Meta.AdditionalFields` as `traceparent` (and
// `tracestate` when non-empty) so the client can stitch the
// call into its own trace.
//
// Safe to call when ctx has no recording span: a nil res is a
// no-op, and a ctx with no valid SpanContext leaves res.Meta
// untouched (no empty `_meta` envelope is created — preserves
// backwards compat for old-shape responses).
//
// Existing `_meta` content is preserved: this function only
// writes the two W3C keys, never clobbering peer namespaces
// (e.g. `clawtool/usage_hint` from EnrichListToolsResult).
func EchoTraceContextOnResult(ctx context.Context, res *mcp.CallToolResult) {
	if res == nil {
		return
	}
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	tp := carrier[TraceparentMetaKey]
	if tp == "" {
		// Inject only writes when sc.IsValid(); the IsValid
		// check above already guards this, but a stricter
		// guard here lets us survive a future propagator
		// swap that lazily refuses some span contexts.
		return
	}
	if res.Meta == nil {
		res.Meta = &mcp.Meta{}
	}
	if res.Meta.AdditionalFields == nil {
		res.Meta.AdditionalFields = map[string]any{}
	}
	res.Meta.AdditionalFields[TraceparentMetaKey] = tp
	if ts := carrier[TracestateMetaKey]; ts != "" {
		res.Meta.AdditionalFields[TracestateMetaKey] = ts
	}
}

// readMetaTraceContext extracts the two W3C keys off an
// mcp.Meta envelope. Tolerant of every shape the JSON decoder
// can produce for AdditionalFields: string values pass through,
// non-string values are dropped (the W3C spec defines both keys
// as ASCII strings, so a non-string is malformed input — log-
// noise rather than tooling burden).
func readMetaTraceContext(meta *mcp.Meta) (traceparent, tracestate string) {
	if meta == nil || meta.AdditionalFields == nil {
		return "", ""
	}
	if v, ok := meta.AdditionalFields[TraceparentMetaKey].(string); ok {
		traceparent = v
	}
	if v, ok := meta.AdditionalFields[TracestateMetaKey].(string); ok {
		tracestate = v
	}
	return traceparent, tracestate
}
