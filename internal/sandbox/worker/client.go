// Package worker — daemon-side client for the sandbox worker
// (ADR-029 phase 1).
//
// The daemon dials the worker once at first tool call and
// re-uses the connection for the lifetime of the dispatch.
// Phase 1 keeps a single connection per Client; multiple
// concurrent tool calls serialise through it. Phase 2 will
// pool connections.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// Client is the daemon's handle on a sandbox worker. Goroutine-
// safe: Send serialises through a mutex.
type Client struct {
	URL     string // ws://host:port/ws
	Token   string
	conn    *websocket.Conn
	connMu  sync.Mutex
	dialMu  sync.Mutex
	timeout time.Duration
}

// NewClient returns an unconnected client. Dial happens lazily
// on first Send.
func NewClient(url, token string) *Client {
	return &Client{URL: url, Token: token, timeout: 30 * time.Second}
}

// Close drops the underlying WebSocket. Safe to call repeatedly.
func (c *Client) Close() {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close(websocket.StatusNormalClosure, "client closing")
		c.conn = nil
	}
}

// Ping verifies the worker is reachable + auth is correct. Returns
// nil on success.
func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.send(ctx, &Request{Kind: KindPing})
	if err != nil {
		return err
	}
	if resp.Status != 0 {
		return fmt.Errorf("worker: ping status=%d %s", resp.Status, resp.Error)
	}
	return nil
}

// Exec routes a Bash tool call to the worker. Mirrors the host
// path's semantics so the daemon can route transparently.
func (c *Client) Exec(ctx context.Context, req ExecRequest) (*ExecResponse, error) {
	body, err := MarshalBody(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, &Request{Kind: KindExec, Body: body})
	if err != nil {
		return nil, err
	}
	if resp.Status != 0 {
		return nil, fmt.Errorf("worker exec: %s", resp.Error)
	}
	var out ExecResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, fmt.Errorf("decode exec response: %w", err)
	}
	return &out, nil
}

// Read routes a Read tool call.
func (c *Client) Read(ctx context.Context, req ReadRequest) (*ReadResponse, error) {
	body, err := MarshalBody(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, &Request{Kind: KindRead, Body: body})
	if err != nil {
		return nil, err
	}
	if resp.Status != 0 {
		return nil, fmt.Errorf("worker read: %s", resp.Error)
	}
	var out ReadResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, fmt.Errorf("decode read response: %w", err)
	}
	return &out, nil
}

// Write routes a Write tool call.
func (c *Client) Write(ctx context.Context, req WriteRequest) (*WriteResponse, error) {
	body, err := MarshalBody(req)
	if err != nil {
		return nil, err
	}
	resp, err := c.send(ctx, &Request{Kind: KindWrite, Body: body})
	if err != nil {
		return nil, err
	}
	if resp.Status != 0 {
		return nil, fmt.Errorf("worker write: %s", resp.Error)
	}
	var out WriteResponse
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return nil, fmt.Errorf("decode write response: %w", err)
	}
	return &out, nil
}

// ─── internals ──────────────────────────────────────────────────

// send enforces the request/response invariant: assigns an ID,
// writes the request, reads frames until one matches the ID.
// Other frames are dropped — Phase 1 has no concurrent in-flight
// requests.
func (c *Client) send(ctx context.Context, req *Request) (*Response, error) {
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	conn, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}

	c.connMu.Lock()
	defer c.connMu.Unlock()

	raw, err := EncodeRequest(req)
	if err != nil {
		return nil, err
	}
	wctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if err := conn.Write(wctx, websocket.MessageText, raw); err != nil {
		c.dropConn()
		return nil, fmt.Errorf("worker write: %w", err)
	}

	for {
		_, b, err := conn.Read(wctx)
		if err != nil {
			c.dropConn()
			return nil, fmt.Errorf("worker read: %w", err)
		}
		var resp Response
		if err := json.Unmarshal(b, &resp); err != nil {
			continue
		}
		if resp.ID != req.ID {
			continue
		}
		return &resp, nil
	}
}

func (c *Client) dial(ctx context.Context) (*websocket.Conn, error) {
	c.dialMu.Lock()
	defer c.dialMu.Unlock()

	c.connMu.Lock()
	have := c.conn
	c.connMu.Unlock()
	if have != nil {
		return have, nil
	}

	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer "+c.Token)
	wsURL := c.URL
	conn, _, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{HTTPHeader: hdr})
	if err != nil {
		return nil, fmt.Errorf("dial worker %s: %w", wsURL, err)
	}

	c.connMu.Lock()
	c.conn = conn
	c.connMu.Unlock()
	return conn, nil
}

func (c *Client) dropConn() {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn != nil {
		_ = c.conn.Close(websocket.StatusInternalError, "io error")
		c.conn = nil
	}
}

// ErrUnconfigured signals the daemon's tool path that no worker
// is wired (mode=off). Caller falls back to host execution.
var ErrUnconfigured = errors.New("worker: not configured (sandbox.worker.mode=off)")
