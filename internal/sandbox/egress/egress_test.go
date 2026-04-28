package egress

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestAllowSet_ExactMatch(t *testing.T) {
	a, err := parseAllowList([]string{"api.openai.com"})
	if err != nil {
		t.Fatal(err)
	}
	if !a.matches("api.openai.com") {
		t.Error("exact match should pass")
	}
	if a.matches("status.openai.com") {
		t.Error("exact match must not match a sibling")
	}
}

func TestAllowSet_SuffixMatch(t *testing.T) {
	a, err := parseAllowList([]string{".openai.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"api.openai.com", "status.openai.com", "openai.com"} {
		if !a.matches(host) {
			t.Errorf("suffix should match %q", host)
		}
	}
	if a.matches("notopenai.com") {
		t.Error("suffix match must not bleed into unrelated domains")
	}
}

func TestAllowSet_Wildcard(t *testing.T) {
	a, _ := parseAllowList([]string{"*"})
	for _, host := range []string{"a.com", "anything.example", "8.8.8.8"} {
		if !a.matches(host) {
			t.Errorf("wildcard should match %q", host)
		}
	}
}

func TestAllowSet_EmptyDeniesAll(t *testing.T) {
	a, _ := parseAllowList(nil)
	if a.matches("api.openai.com") {
		t.Error("empty allowlist must deny everything")
	}
}

// startEgress spawns the proxy in the background, returns its
// http://127.0.0.1:PORT URL + cleanup. Used by the live tests
// below.
func startEgress(t *testing.T, opts Options) (string, func()) {
	t.Helper()
	if opts.Listen == "" {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		opts.Listen = fmt.Sprintf("127.0.0.1:%d", port)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = Run(ctx, opts) }()
	// Wait for the listener to come up.
	deadline := time.Now().Add(2 * time.Second)
	addr := opts.Listen
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			c.Close()
			return "http://" + addr, cancel
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	t.Fatalf("egress did not come up at %s", addr)
	return "", cancel
}

func TestEgress_HTTPDeniesNonAllowedHost(t *testing.T) {
	proxyURL, stop := startEgress(t, Options{Allow: []string{"only-allowed.example"}})
	defer stop()

	pu, _ := url.Parse(proxyURL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(pu)},
		Timeout:   2 * time.Second,
	}
	resp, err := client.Get("http://blocked.example/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
	if r := resp.Header.Get("x-deny-reason"); r == "" {
		t.Error("expected x-deny-reason header on denial")
	}
}

func TestEgress_HTTPAllowsAllowedHost(t *testing.T) {
	// Stand up an upstream we can dial.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "upstream-ok")
	}))
	defer upstream.Close()
	upstreamHost := strings.TrimPrefix(upstream.URL, "http://")
	upstreamHostOnly := upstreamHost
	if h, _, err := net.SplitHostPort(upstreamHost); err == nil {
		upstreamHostOnly = h
	}

	proxyURL, stop := startEgress(t, Options{Allow: []string{upstreamHostOnly}})
	defer stop()

	pu, _ := url.Parse(proxyURL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(pu)},
		Timeout:   2 * time.Second,
	}
	resp, err := client.Get(upstream.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%q", resp.StatusCode, body)
	}
	if string(body) != "upstream-ok" {
		t.Errorf("body = %q, want %q", body, "upstream-ok")
	}
}

func TestEgress_BearerAuthRequired(t *testing.T) {
	proxyURL, stop := startEgress(t, Options{
		Allow: []string{"*"},
		Token: "sekret",
	})
	defer stop()

	// No auth: 407.
	pu, _ := url.Parse(proxyURL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(pu)},
		Timeout:   2 * time.Second,
	}
	resp, err := client.Get("http://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("status = %d, want 407", resp.StatusCode)
	}
	if !strings.HasPrefix(resp.Header.Get("Proxy-Authenticate"), "Bearer") {
		t.Error("expected Proxy-Authenticate: Bearer challenge")
	}
}
