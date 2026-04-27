package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/cogitave/clawtool/internal/config"
)

// mockCDPServer accepts a single WS connection and replies to each
// {id, method, params} frame with a function the test supplies.
// Push events (no id) are never sent — keeps the unit tests
// deterministic.
type mockCDPServer struct {
	srv     *httptest.Server
	respond func(method string, params json.RawMessage) (json.RawMessage, *cdpError)
}

func newMockCDPServer(respond func(string, json.RawMessage) (json.RawMessage, *cdpError)) *mockCDPServer {
	m := &mockCDPServer{respond: respond}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "ok")
		ctx := context.Background()
		for {
			_, body, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var env cdpEnvelope
			if err := json.Unmarshal(body, &env); err != nil {
				continue
			}
			if env.ID == 0 {
				continue
			}
			result, cerr := m.respond(env.Method, env.Params)
			reply := cdpEnvelope{ID: env.ID, Result: result, Error: cerr}
			out, _ := json.Marshal(reply)
			_ = conn.Write(ctx, websocket.MessageText, out)
		}
	}))
	return m
}

func (m *mockCDPServer) URL() string {
	return strings.Replace(m.srv.URL, "http://", "ws://", 1)
}

func (m *mockCDPServer) Close() { m.srv.Close() }

func TestCDPClient_RoundTrip(t *testing.T) {
	srv := newMockCDPServer(func(method string, _ json.RawMessage) (json.RawMessage, *cdpError) {
		switch method {
		case "Target.createBrowserContext":
			return json.RawMessage(`{"browserContextId":"ctx-1"}`), nil
		case "Target.createTarget":
			return json.RawMessage(`{"targetId":"tgt-1"}`), nil
		case "Target.attachToTarget":
			return json.RawMessage(`{"sessionId":"sess-1"}`), nil
		}
		return json.RawMessage(`{}`), nil
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := DialCDP(ctx, srv.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()

	bc, err := cli.CreateBrowserContext(ctx)
	if err != nil || bc != "ctx-1" {
		t.Fatalf("CreateBrowserContext: bc=%q err=%v", bc, err)
	}
	tg, err := cli.CreateTarget(ctx, "about:blank", bc, 1024, 768)
	if err != nil || tg != "tgt-1" {
		t.Fatalf("CreateTarget: tg=%q err=%v", tg, err)
	}
	ses, err := cli.AttachToTarget(ctx, tg)
	if err != nil || ses != "sess-1" {
		t.Fatalf("AttachToTarget: ses=%q err=%v", ses, err)
	}
}

func TestCDPClient_SurfacesError(t *testing.T) {
	srv := newMockCDPServer(func(_ string, _ json.RawMessage) (json.RawMessage, *cdpError) {
		return nil, &cdpError{Code: -32601, Message: "Method not implemented"}
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, _ := DialCDP(ctx, srv.URL())
	defer cli.Close()

	_, err := cli.CreateBrowserContext(ctx)
	if err == nil || !strings.Contains(err.Error(), "Method not implemented") {
		t.Fatalf("expected error from CDP, got %v", err)
	}
}

func TestCDPClient_Evaluate_PassesValueThrough(t *testing.T) {
	srv := newMockCDPServer(func(method string, params json.RawMessage) (json.RawMessage, *cdpError) {
		if method != "Runtime.evaluate" {
			return json.RawMessage(`{}`), nil
		}
		// Decode the expression so we can echo something specific.
		var p struct {
			Expression string `json:"expression"`
		}
		_ = json.Unmarshal(params, &p)
		switch {
		case strings.Contains(p.Expression, "Boolean(true)"):
			return json.RawMessage(`{"result":{"value":true}}`), nil
		case strings.Contains(p.Expression, `"hello"`):
			return json.RawMessage(`{"result":{"value":"hello"}}`), nil
		}
		return json.RawMessage(`{"result":{"value":null}}`), nil
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, _ := DialCDP(ctx, srv.URL())
	defer cli.Close()

	got, err := cli.EvaluateBool(ctx, "true")
	if err != nil || !got {
		t.Fatalf("EvaluateBool(true) = %v, %v", got, err)
	}
	s, err := cli.EvaluateString(ctx, `"hello"`)
	if err != nil || s != "hello" {
		t.Fatalf("EvaluateString = %q, %v", s, err)
	}
}

func TestCDPClient_Evaluate_SurfacesJSException(t *testing.T) {
	srv := newMockCDPServer(func(_ string, _ json.RawMessage) (json.RawMessage, *cdpError) {
		return json.RawMessage(`{"exceptionDetails":{"text":"Uncaught","exception":{"description":"ReferenceError: nope is not defined"}}}`), nil
	})
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, _ := DialCDP(ctx, srv.URL())
	defer cli.Close()

	_, err := cli.Evaluate(ctx, "nope")
	if err == nil || !strings.Contains(err.Error(), "ReferenceError") {
		t.Fatalf("expected JS exception, got %v", err)
	}
}

func TestPredicateExpression(t *testing.T) {
	for _, tc := range []struct {
		typ, val string
		want     string
	}{
		{PredicateSelectorExists, "textarea", `!!document.querySelector("textarea")`},
		{PredicateSelectorVisible, "textarea", `(() => { const el = document.querySelector("textarea"); return !!el && el.offsetParent !== null; })()`},
		{PredicateEvalTruthy, "1+1", "1+1"},
	} {
		got, err := predicateExpression(config.PortalPredicate{Type: tc.typ, Value: tc.val})
		if err != nil {
			t.Errorf("type=%q: unexpected err %v", tc.typ, err)
			continue
		}
		if got != tc.want {
			t.Errorf("type=%q\n  got:  %q\n  want: %q", tc.typ, got, tc.want)
		}
	}
}

func TestPredicateExpression_RejectsUnknownType(t *testing.T) {
	if _, err := predicateExpression(config.PortalPredicate{Type: "what_even", Value: "x"}); err == nil {
		t.Fatal("expected error for unknown predicate type")
	}
}

func TestJSString_EscapesQuotes(t *testing.T) {
	got := jsString(`hello "world"\nthis`)
	// json.Marshal produces a fully-escaped JS-safe string literal.
	want := `"hello \"world\"\\nthis"`
	if got != want {
		t.Errorf("jsString:\n got %q\nwant %q", got, want)
	}
}
