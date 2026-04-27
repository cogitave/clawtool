// Package portal — minimal Chrome DevTools Protocol client.
//
// We only need six CDP methods to drive a portal flow end-to-end:
// Target.createBrowserContext, Target.createTarget, Page.navigate,
// Network.setCookies, Network.setExtraHTTPHeaders, Runtime.evaluate.
// chromedp/cdproto would pull ~5 MB of generated code and a chunky
// dependency tree for that surface; per ADR-007 we'd rather wrap the
// 6 frames we actually use over coder/websocket and skip the rest.
//
// The client is intentionally synchronous — every request gets an id,
// the reader goroutine fans replies into per-id channels, the caller
// waits on the channel. No event subscription / target attach
// complexity until we need it.
package portal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/coder/websocket"
)

// CDPClient is a minimal request/response Chrome DevTools Protocol
// client over a single WebSocket connection.
type CDPClient struct {
	conn   *websocket.Conn
	closed atomic.Bool

	mu       sync.Mutex
	nextID   int64
	pending  map[int64]chan cdpEnvelope
	readErr  chan error
	stopRead chan struct{}

	// SessionID tags every command after AttachToTarget so multi-page
	// servers route to the right tab; zero value = browser-level.
	SessionID string
}

// cdpEnvelope is the wire shape for both directions. CDP frames are
// either {"id":N,"method":"...","params":{}} (request),
// {"id":N,"result":{...}} (response), {"id":N,"error":{...}} or
// {"method":"event.x","params":{...}} (push event we ignore).
type cdpEnvelope struct {
	ID        int64           `json:"id,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

// DialCDP opens a WebSocket to the given CDP URL (browser-level
// debug WS, e.g. ws://127.0.0.1:9222/devtools/browser/<uuid>) and
// starts the reader goroutine.
func DialCDP(ctx context.Context, wsURL string) (*CDPClient, error) {
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{})
	if err != nil {
		return nil, fmt.Errorf("portal: dial CDP %s: %w", wsURL, err)
	}
	conn.SetReadLimit(64 * 1024 * 1024) // 64 MiB; CDP messages can be huge for full-page evals
	c := &CDPClient{
		conn:     conn,
		pending:  map[int64]chan cdpEnvelope{},
		readErr:  make(chan error, 1),
		stopRead: make(chan struct{}),
	}
	go c.readLoop()
	return c, nil
}

// Close terminates the underlying WebSocket. Idempotent.
func (c *CDPClient) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	close(c.stopRead)
	return c.conn.Close(websocket.StatusNormalClosure, "client close")
}

// Send invokes a CDP method synchronously and returns the raw result
// JSON. Errors propagate transport failures, CDP error frames, and
// caller-context cancellation — same precedence as net/http's Do.
func (c *CDPClient) Send(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.closed.Load() {
		return nil, errors.New("portal: CDP client is closed")
	}
	id := atomic.AddInt64(&c.nextID, 1)
	frame := cdpEnvelope{
		ID:        id,
		Method:    method,
		SessionID: c.SessionID,
	}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("portal: marshal %s params: %w", method, err)
		}
		frame.Params = raw
	}
	body, err := json.Marshal(frame)
	if err != nil {
		return nil, err
	}

	ch := make(chan cdpEnvelope, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.conn.Write(ctx, websocket.MessageText, body); err != nil {
		return nil, fmt.Errorf("portal: CDP send %s: %w", method, err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case env := <-ch:
		if env.Error != nil {
			return nil, fmt.Errorf("portal: CDP %s: %s (code %d)", method, env.Error.Message, env.Error.Code)
		}
		return env.Result, nil
	case err := <-c.readErr:
		return nil, fmt.Errorf("portal: CDP read loop died: %w", err)
	}
}

// readLoop fans incoming frames into the per-id pending map. Push
// events without an `id` field are dropped — we don't subscribe.
func (c *CDPClient) readLoop() {
	defer close(c.readErr)
	for {
		select {
		case <-c.stopRead:
			return
		default:
		}
		_, body, err := c.conn.Read(context.Background())
		if err != nil {
			if !c.closed.Load() {
				select {
				case c.readErr <- err:
				default:
				}
			}
			return
		}
		var env cdpEnvelope
		if err := json.Unmarshal(body, &env); err != nil {
			continue
		}
		if env.ID == 0 {
			continue // push event
		}
		c.mu.Lock()
		ch, ok := c.pending[env.ID]
		c.mu.Unlock()
		if ok {
			ch <- env
		}
	}
}

// ── typed CDP wrappers (only the surface portal flows need) ────────

// CreateBrowserContext returns a fresh BrowserContextID — like an
// incognito profile, isolated cookie jar.
func (c *CDPClient) CreateBrowserContext(ctx context.Context) (string, error) {
	raw, err := c.Send(ctx, "Target.createBrowserContext", map[string]any{
		"disposeOnDetach": true,
	})
	if err != nil {
		return "", err
	}
	var resp struct {
		BrowserContextID string `json:"browserContextId"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("portal: decode createBrowserContext: %w", err)
	}
	return resp.BrowserContextID, nil
}

// CreateTarget opens a new tab inside the given context. Returns the
// targetId and the dedicated WebSocket URL we need to attach for
// page-level commands.
func (c *CDPClient) CreateTarget(ctx context.Context, url, browserContextID string, width, height int) (string, error) {
	params := map[string]any{
		"url":              url,
		"browserContextId": browserContextID,
	}
	if width > 0 {
		params["width"] = width
	}
	if height > 0 {
		params["height"] = height
	}
	raw, err := c.Send(ctx, "Target.createTarget", params)
	if err != nil {
		return "", err
	}
	var resp struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("portal: decode createTarget: %w", err)
	}
	return resp.TargetID, nil
}

// AttachToTarget wires the client to a specific page target so
// Page/Runtime/Network commands route there. Returns the sessionId
// the caller must stash on c.SessionID before issuing follow-ups.
func (c *CDPClient) AttachToTarget(ctx context.Context, targetID string) (string, error) {
	raw, err := c.Send(ctx, "Target.attachToTarget", map[string]any{
		"targetId": targetID,
		"flatten":  true,
	})
	if err != nil {
		return "", err
	}
	var resp struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("portal: decode attachToTarget: %w", err)
	}
	return resp.SessionID, nil
}

// SetCookies seeds the browser context cookie jar before navigation.
// Cookies must be the CDP-native shape; portal.Cookie maps cleanly.
func (c *CDPClient) SetCookies(ctx context.Context, cookies []Cookie) error {
	if len(cookies) == 0 {
		return nil
	}
	wire := make([]map[string]any, 0, len(cookies))
	for _, ck := range cookies {
		entry := map[string]any{
			"name":     ck.Name,
			"value":    ck.Value,
			"domain":   ck.Domain,
			"path":     ck.Path,
			"secure":   ck.Secure,
			"httpOnly": ck.HTTPOnly,
		}
		if ck.SameSite != "" {
			entry["sameSite"] = ck.SameSite
		}
		if ck.Expires > 0 {
			entry["expires"] = ck.Expires
		}
		wire = append(wire, entry)
	}
	_, err := c.Send(ctx, "Network.setCookies", map[string]any{"cookies": wire})
	return err
}

// SetExtraHTTPHeaders applies on every subsequent request from the
// attached target.
func (c *CDPClient) SetExtraHTTPHeaders(ctx context.Context, headers map[string]string) error {
	if len(headers) == 0 {
		return nil
	}
	_, err := c.Send(ctx, "Network.setExtraHTTPHeaders", map[string]any{"headers": headers})
	return err
}

// EnableNetwork must run before SetCookies / SetExtraHTTPHeaders work.
func (c *CDPClient) EnableNetwork(ctx context.Context) error {
	_, err := c.Send(ctx, "Network.enable", map[string]any{})
	return err
}

// Navigate sends Page.navigate. Caller is responsible for waiting
// (predicates do that work explicitly).
func (c *CDPClient) Navigate(ctx context.Context, url string) error {
	_, err := c.Send(ctx, "Page.navigate", map[string]any{"url": url})
	return err
}

// EnablePage must run before Page.navigate. Some CDP servers tolerate
// navigate before enable; we always issue both for safety.
func (c *CDPClient) EnablePage(ctx context.Context) error {
	_, err := c.Send(ctx, "Page.enable", map[string]any{})
	return err
}

// Evaluate runs a JS expression and returns the result as raw JSON.
// `awaitPromise` is true so chained `await` blocks resolve before we
// see the result; `returnByValue` is true so we get a JSON-serialised
// payload instead of a remote object handle.
func (c *CDPClient) Evaluate(ctx context.Context, expr string) (json.RawMessage, error) {
	raw, err := c.Send(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
		"awaitPromise":  true,
	})
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("portal: decode evaluate: %w", err)
	}
	if resp.ExceptionDetails != nil {
		msg := resp.ExceptionDetails.Text
		if resp.ExceptionDetails.Exception != nil {
			msg = resp.ExceptionDetails.Exception.Description
		}
		return nil, fmt.Errorf("portal: JS exception: %s", msg)
	}
	return resp.Result.Value, nil
}

// EvaluateBool is a convenience for predicate evaluation. Truthy
// non-bool returns coerce per JS semantics on our side via
// `Boolean(...)` wrapping in the call site.
func (c *CDPClient) EvaluateBool(ctx context.Context, expr string) (bool, error) {
	raw, err := c.Evaluate(ctx, "Boolean("+expr+")")
	if err != nil {
		return false, err
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, fmt.Errorf("portal: decode bool: %w", err)
	}
	return b, nil
}

// EvaluateString is a convenience for "extract the rendered response
// text" calls. Strings come back JSON-quoted so unmarshal handles
// the unwrap.
func (c *CDPClient) EvaluateString(ctx context.Context, expr string) (string, error) {
	raw, err := c.Evaluate(ctx, expr)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("portal: decode string: %w", err)
	}
	return s, nil
}
