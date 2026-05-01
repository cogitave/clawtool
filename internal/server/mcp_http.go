// MCP-over-HTTP Accept-header content negotiation shim.
//
// The mark3labs/mcp-go StreamableHTTPServer (v0.49.0) always replies
// to a single-response /mcp POST with `Content-Type: application/json`
// and a bare JSON-RPC body, regardless of the client's Accept header
// (see streamable_http.go:546 in that release).
//
// rmcp (the Rust MCP SDK that codex's HTTP client is built on) opens
// initialize with `Accept: text/event-stream` only. When the upstream
// answers with raw JSON the rmcp parser tries to decode the body as
// SSE-framed (`data: <json>\n\n`), finds no `event:` lines, and
// surfaces the misleading
//
//	Deserialize error: data did not match any variant of untagged
//	enum JsonRpcMessage when send initialize request
//
// MCP Streamable-HTTP (2025-06-18) says the server SHOULD honor the
// client's Accept by responding with `text/event-stream` framed as
// `data: <json>\n\n`; a single SSE event is a valid response shape.
//
// mcpAcceptShim wraps the streamable handler and post-processes the
// outgoing response when the client asked for SSE — buffer the
// `application/json` body the inner handler emits, then write it
// back as a single `data: ...\n\n` SSE event with the right
// Content-Type. When the inner handler already chose
// `text/event-stream` (multi-event drain path, the upgradedHeader
// branch in mcp-go) we pass through unchanged.
package server

import (
	"bytes"
	"net/http"
	"strings"
)

// mcpAcceptShim returns an http.Handler wrapping `inner`. When the
// request's Accept header includes `text/event-stream`, the response
// body is reframed as a single SSE event whenever inner emits
// `Content-Type: application/json`. All other behavior (status,
// headers other than Content-Type, body bytes for non-JSON
// responses, already-SSE responses) is preserved.
func mcpAcceptShim(inner http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !acceptsEventStream(r.Header.Get("Accept")) {
			inner.ServeHTTP(w, r)
			return
		}
		rec := &sseCapture{header: http.Header{}, status: http.StatusOK, body: &bytes.Buffer{}}
		inner.ServeHTTP(rec, r)
		rec.flushAsSSE(w)
	})
}

// acceptsEventStream returns true when the Accept header explicitly
// lists `text/event-stream`. The MCP spec recommends preferring SSE
// when the client lists both `application/json` and
// `text/event-stream`, so a single positive match is enough — we
// don't need to weigh quality factors.
func acceptsEventStream(accept string) bool {
	if accept == "" {
		return false
	}
	for _, part := range strings.Split(accept, ",") {
		// strip parameters (`text/event-stream;q=1.0`)
		mt := strings.TrimSpace(part)
		if i := strings.Index(mt, ";"); i >= 0 {
			mt = strings.TrimSpace(mt[:i])
		}
		if strings.EqualFold(mt, "text/event-stream") {
			return true
		}
	}
	return false
}

// sseCapture buffers everything the inner handler writes so the shim
// can decide, after the body is complete, whether to reframe it as
// SSE. Implements http.ResponseWriter + http.Flusher (Flush is a
// no-op while buffering — the outer writer's Flush fires after
// flushAsSSE).
type sseCapture struct {
	header      http.Header
	status      int
	body        *bytes.Buffer
	wroteHeader bool
}

func (s *sseCapture) Header() http.Header { return s.header }

func (s *sseCapture) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.status = code
	s.wroteHeader = true
}

func (s *sseCapture) Write(p []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	return s.body.Write(p)
}

// Flush is required because the streamable handler casts to
// http.Flusher and calls it during multi-event drains. Buffering
// makes Flush a no-op — the real flush happens once flushAsSSE
// writes to the outer ResponseWriter.
func (s *sseCapture) Flush() {}

// flushAsSSE copies the captured response onto `w`, reframing the
// body when the inner handler chose `application/json`. Inner
// already-SSE responses pass through verbatim. Non-2xx errors
// (e.g. mcp-go's "Invalid content type" 400 from http.Error) keep
// their text/plain body — rmcp surfaces those as transport errors,
// the SSE conversion would only mask them.
func (s *sseCapture) flushAsSSE(w http.ResponseWriter) {
	ct := s.header.Get("Content-Type")
	mediaType := ct
	if i := strings.Index(mediaType, ";"); i >= 0 {
		mediaType = strings.TrimSpace(mediaType[:i])
	}
	body := s.body.Bytes()

	// Reframe only successful JSON responses. text/event-stream
	// already-framed → pass through. text/plain (http.Error) →
	// pass through. Empty body (202 Accepted) → pass through.
	if strings.EqualFold(mediaType, "application/json") && s.status >= 200 && s.status < 300 && len(body) > 0 {
		// Trim the trailing newline json.Encoder appends so the
		// SSE event payload is exactly the JSON object.
		payload := bytes.TrimRight(body, "\n")
		out := w.Header()
		// Copy through every non-Content-Type header so session-id
		// (Mcp-Session-Id), Cache-Control, etc. survive the
		// rewrite.
		for k, vs := range s.header {
			if strings.EqualFold(k, "Content-Type") || strings.EqualFold(k, "Content-Length") {
				continue
			}
			for _, v := range vs {
				out.Add(k, v)
			}
		}
		out.Set("Content-Type", "text/event-stream")
		out.Set("Cache-Control", "no-cache")
		out.Set("Connection", "keep-alive")
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte("data: "))
		_, _ = w.Write(payload)
		_, _ = w.Write([]byte("\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}

	// Pass-through path. Copy headers verbatim, then body.
	for k, vs := range s.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(s.status)
	_, _ = w.Write(body)
}
