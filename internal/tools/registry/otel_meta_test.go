package registry

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"go.opentelemetry.io/otel/trace"
)

// validParent is a well-formed W3C traceparent (sampled flag).
// Trace ID and span ID are the canonical example from the W3C
// Trace Context examples — picking the public test fixture so a
// human eyeballing CI output recognises it.
const (
	validTraceID    = "4bf92f3577b34da6a3ce929d0e0e4736"
	validParentSpan = "00f067aa0ba902b7"
	validParent     = "00-" + validTraceID + "-" + validParentSpan + "-01"
	validTracestate = "vendor=clawtool,key=value"
)

// TestOTelPropagation_RequestParentInheritance — request with
// `_meta.traceparent` reaches the handler as a remote span
// context AND the response echoes `_meta.traceparent` carrying
// the same trace-id (the parent's id when no live span is
// recorded; a child span's id when observability is on).
func TestOTelPropagation_RequestParentInheritance(t *testing.T) {
	t.Setenv(TraceparentEnv, "")
	t.Setenv(TracestateEnv, "")

	mw := TraceContextMiddleware()

	var sawCtx context.Context
	handler := mw(func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sawCtx = ctx
		return &mcp.CallToolResult{}, nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Name = "Glob"
	req.Params.Meta = &mcp.Meta{AdditionalFields: map[string]any{
		TraceparentMetaKey: validParent,
		TracestateMetaKey:  validTracestate,
	}}

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	// Handler ctx must carry the remote span context with the
	// same trace ID the client sent.
	sc := trace.SpanContextFromContext(sawCtx)
	if !sc.IsValid() {
		t.Fatalf("handler ctx has no valid span context")
	}
	if got := sc.TraceID().String(); got != validTraceID {
		t.Errorf("trace_id mismatch: got %q, want %q", got, validTraceID)
	}
	if got := sc.SpanID().String(); got != validParentSpan {
		t.Errorf("span_id mismatch: got %q, want %q", got, validParentSpan)
	}
	if !sc.IsRemote() {
		t.Errorf("expected remote span context")
	}

	// Response `_meta.traceparent` must share the trace-id.
	gotTP := metaString(t, res, TraceparentMetaKey)
	if !strings.Contains(gotTP, validTraceID) {
		t.Errorf("response traceparent %q does not contain trace_id %q", gotTP, validTraceID)
	}
	gotTS := metaString(t, res, TracestateMetaKey)
	if gotTS != validTracestate {
		t.Errorf("response tracestate = %q, want %q", gotTS, validTracestate)
	}
}

// TestOTelPropagation_RequestWithoutMetaUsesEnv — TRACEPARENT env
// is honoured as the fallback when the request carries no
// `_meta.traceparent` (preserves the v0.22 env-driven shim).
func TestOTelPropagation_RequestWithoutMetaUsesEnv(t *testing.T) {
	t.Setenv(TraceparentEnv, validParent)
	t.Setenv(TracestateEnv, validTracestate)

	mw := TraceContextMiddleware()

	var sawCtx context.Context
	handler := mw(func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sawCtx = ctx
		return &mcp.CallToolResult{}, nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Name = "Bash"
	// No req.Params.Meta — env must take over.

	if _, err := handler(context.Background(), req); err != nil {
		t.Fatalf("handler: %v", err)
	}

	sc := trace.SpanContextFromContext(sawCtx)
	if !sc.IsValid() {
		t.Fatalf("env-driven trace context did not reach handler")
	}
	if got := sc.TraceID().String(); got != validTraceID {
		t.Errorf("env-driven trace_id mismatch: got %q, want %q", got, validTraceID)
	}
}

// TestOTelPropagation_ResponseEchoesTraceparent — every response
// from the test server carries a `_meta.traceparent` whenever
// the request supplied one, and the values are interoperable W3C
// strings (lowercase hex, dash-separated, version 00).
func TestOTelPropagation_ResponseEchoesTraceparent(t *testing.T) {
	t.Setenv(TraceparentEnv, "")
	t.Setenv(TracestateEnv, "")

	mw := TraceContextMiddleware()
	handler := mw(func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Tool returns a typical text payload; middleware
		// must layer `_meta.traceparent` ON TOP without
		// disturbing the existing fields.
		return mcp.NewToolResultText("ok"), nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Name = "Read"
	req.Params.Meta = &mcp.Meta{AdditionalFields: map[string]any{
		TraceparentMetaKey: validParent,
	}}

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	tp := metaString(t, res, TraceparentMetaKey)
	if tp == "" {
		t.Fatalf("response missing _meta.traceparent")
	}
	// W3C shape: 00-<32hex>-<16hex>-<2hex>
	parts := strings.Split(tp, "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		t.Errorf("response traceparent %q is not a valid W3C string", tp)
	}
	if got := parts[1]; got != validTraceID {
		t.Errorf("response trace_id %q != request trace_id %q", got, validTraceID)
	}

	// JSON wire shape: text content survives, `_meta` carries
	// the new keys without clobbering anything else.
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"traceparent":`) {
		t.Errorf("traceparent missing from wire JSON: %s", raw)
	}
	if !strings.Contains(string(raw), `"text":"ok"`) {
		t.Errorf("text content dropped from wire JSON: %s", raw)
	}
}

// TestOTelPropagation_NoTraceContextNoMeta — backwards-compat
// guarantee: a request with no `_meta` and no env fallback gets
// a response with no `_meta` envelope at all. Old clients see the
// pre-SEP-414 wire shape unchanged.
func TestOTelPropagation_NoTraceContextNoMeta(t *testing.T) {
	t.Setenv(TraceparentEnv, "")
	t.Setenv(TracestateEnv, "")

	mw := TraceContextMiddleware()
	handler := mw(func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Name = "Glob"

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if res.Meta != nil && res.Meta.AdditionalFields != nil {
		if _, ok := res.Meta.AdditionalFields[TraceparentMetaKey]; ok {
			t.Errorf("response should not carry traceparent without trace context input; got %+v", res.Meta.AdditionalFields)
		}
	}
}

// TestEchoTraceContextOnResult_PreservesExistingMeta — when a
// handler already attached its own `_meta` keys (e.g. a tool that
// surfaces structured progress under a vendor namespace), the
// echo step adds the W3C keys alongside without clobbering.
func TestEchoTraceContextOnResult_PreservesExistingMeta(t *testing.T) {
	t.Setenv(TraceparentEnv, "")
	t.Setenv(TracestateEnv, "")

	mw := TraceContextMiddleware()
	handler := mw(func(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res := mcp.NewToolResultText("ok")
		res.Meta = &mcp.Meta{AdditionalFields: map[string]any{
			"clawtool/usage_hint": "use this when foo",
		}}
		return res, nil
	})

	req := mcp.CallToolRequest{}
	req.Params.Name = "Glob"
	req.Params.Meta = &mcp.Meta{AdditionalFields: map[string]any{
		TraceparentMetaKey: validParent,
	}}

	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if v, _ := res.Meta.AdditionalFields["clawtool/usage_hint"].(string); v != "use this when foo" {
		t.Errorf("pre-existing usage_hint clobbered: %v", res.Meta.AdditionalFields)
	}
	if _, ok := res.Meta.AdditionalFields[TraceparentMetaKey].(string); !ok {
		t.Errorf("traceparent missing: %v", res.Meta.AdditionalFields)
	}
}

// metaString fishes a string-valued key out of res.Meta or
// fails the test loudly. Centralised so each test reads as
// intent ("response traceparent") rather than three-deep
// nested map access.
func metaString(t *testing.T, res *mcp.CallToolResult, key string) string {
	t.Helper()
	if res == nil || res.Meta == nil || res.Meta.AdditionalFields == nil {
		return ""
	}
	v, _ := res.Meta.AdditionalFields[key].(string)
	return v
}
