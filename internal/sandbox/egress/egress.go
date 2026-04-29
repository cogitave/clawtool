// Package egress is the HTTP/HTTPS allowlist proxy that sandbox
// workers route their network traffic through (ADR-029 phase 4,
// task #209).
//
// claude.ai's mimic: container → egress proxy → whitelist
// decision (allow → forward; deny → 403 with `x-deny-reason`).
// clawtool's parity: this package implements that proxy. The
// worker container's HTTP_PROXY / HTTPS_PROXY env points at the
// egress listener; every outbound HTTP call passes through here
// before reaching the host network.
//
// Phase 4 scope:
//   - HTTP proxy: forwards GET/POST/etc to allowed hosts; 403 deny
//     for hosts not on the allowlist.
//   - HTTPS CONNECT: tunnels TLS bytes for allowed hosts; 403 deny
//     for the rest. We don't terminate TLS — that would require an
//     MITM cert the operator has to install everywhere; staying as
//     a CONNECT proxy keeps the trust model honest.
//   - Allowlist matching: exact host match OR suffix match (e.g.
//     ".openai.com" allows api.openai.com + status.openai.com).
//   - Optional shared bearer token: clients authenticate via
//     Proxy-Authorization: Bearer <token>. Off by default for
//     local-only deployments.
//
// Out of scope (future work):
//   - DNS pinning (allowlisted hostname → resolved IP at start;
//     prevents DNS rebind shenanigans).
//   - Per-target rate limits.
//   - Audit log persistence (allows / denies pipe to clawtool
//     dashboard's stream).
package egress

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options configures the egress proxy listener.
type Options struct {
	Listen string // ":3128" or "127.0.0.1:3128"
	// Allow is the host allowlist. Each entry matches either
	// the exact host (e.g. "api.openai.com") or as a suffix
	// when prefixed with "." (e.g. ".openai.com" matches every
	// subdomain). IPs are matched literally only.
	Allow []string
	// Token, when non-empty, requires every client to present
	// `Proxy-Authorization: Bearer <token>`. Constant-time
	// compare; mismatched tokens get 407.
	Token string
}

// Run blocks the calling goroutine, serving the proxy until ctx
// is cancelled. Returns nil on graceful shutdown, error on
// listener failure.
func Run(ctx context.Context, opts Options) error {
	if strings.TrimSpace(opts.Listen) == "" {
		return errors.New("egress: --listen is required")
	}
	allow, err := parseAllowList(opts.Allow)
	if err != nil {
		return fmt.Errorf("parse allow: %w", err)
	}
	// quit signals every active CONNECT tunnel to tear down. Tunnels
	// register on the proxy's WaitGroup so Run can join them before
	// returning — without this, srv.Shutdown only flushes plaintext
	// HTTP requests; hijacked CONNECT tunnels keep proxying TLS bytes
	// after Run exits, defeating the cancel.
	quit := make(chan struct{})
	p := &proxy{allow: allow, token: opts.Token, quit: quit}

	srv := &http.Server{
		Addr:              opts.Listen,
		Handler:           p,
		ReadHeaderTimeout: 10 * time.Second,
	}
	shutdownDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		close(quit)      // signal active tunnels
		p.tunnels.Wait() // join their goroutines
		close(shutdownDone)
	}()
	fmt.Fprintf(os.Stderr,
		"clawtool egress: listening on %s (allow %d host(s); auth=%s)\n",
		opts.Listen, allow.size(), authMode(opts.Token))
	listenErr := srv.ListenAndServe()
	if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
		return fmt.Errorf("egress listen %s: %w", opts.Listen, listenErr)
	}
	if errors.Is(listenErr, http.ErrServerClosed) {
		<-shutdownDone
	}
	return nil
}

func authMode(tok string) string {
	if strings.TrimSpace(tok) == "" {
		return "none (open)"
	}
	return "bearer"
}

// proxy implements http.Handler. Two paths: CONNECT (HTTPS
// tunneling) and forward (plaintext HTTP).
type proxy struct {
	allow allowSet
	token string

	allowed atomic.Uint64
	denied  atomic.Uint64

	// tunnels tracks every in-flight CONNECT tunnel goroutine so
	// Run can join them on shutdown. quit fires when Run is
	// tearing down; tunnel goroutines select on it alongside
	// io.Copy completion to drop their conns force-closed.
	tunnels sync.WaitGroup
	quit    chan struct{}
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Auth before any other logic — we don't reveal allowlist
	// composition via timing or 403 vs 407 distinction.
	if !p.checkAuth(r) {
		w.Header().Set("Proxy-Authenticate", `Bearer realm="clawtool-egress"`)
		http.Error(w, "proxy auth required", http.StatusProxyAuthRequired)
		return
	}
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

func (p *proxy) checkAuth(r *http.Request) bool {
	if strings.TrimSpace(p.token) == "" {
		return true
	}
	h := r.Header.Get("Proxy-Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return false
	}
	got := []byte(strings.TrimSpace(h[len(prefix):]))
	return subtle.ConstantTimeCompare(got, []byte(p.token)) == 1
}

// handleHTTP forwards plaintext HTTP traffic. Clients send
// absolute-form URIs (RFC 7230 §5.3.2) so we strip hop-by-hop
// headers and forward the request to its declared origin.
func (p *proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	host := stripPort(r.URL.Host)
	if host == "" {
		// non-CONNECT request without absolute URL — typical
		// when a client misconfigures Proxy vs direct URL
		http.Error(w, "egress: absolute URI required for non-CONNECT proxy requests", http.StatusBadRequest)
		return
	}
	if !p.allow.matches(host) {
		p.deny(w, host, "host not on allowlist")
		return
	}
	p.allowed.Add(1)
	rp := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = r.URL.Scheme
			req.URL.Host = r.URL.Host
			req.Host = r.URL.Host
			req.Header.Del("Proxy-Authorization")
			req.Header.Del("Proxy-Connection")
		},
		ErrorHandler: func(rw http.ResponseWriter, _ *http.Request, err error) {
			http.Error(rw, "egress: upstream error: "+err.Error(), http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

// handleConnect tunnels HTTPS bytes after allowlist + auth.
// We do not inspect the TLS payload — clawtool stays an honest
// proxy, not a MITM.
func (p *proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host := stripPort(r.Host)
	if !p.allow.matches(host) {
		p.deny(w, host, "host not on allowlist")
		return
	}
	p.allowed.Add(1)

	dest, err := net.DialTimeout("tcp", r.Host, 10*time.Second)
	if err != nil {
		http.Error(w, "egress: upstream dial: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer dest.Close()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "egress: hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, "egress: hijack: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Tell the client the tunnel is up; from here on out the
	// connection is opaque bytes.
	if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	// Copy in both directions until either end closes OR the
	// proxy's quit channel fires (shutdown). On quit we force-
	// close both ends so the io.Copy goroutines wake up and the
	// proxy can join them via p.tunnels.Wait. Without this the
	// tunnels survived srv.Shutdown indefinitely.
	p.tunnels.Add(1)
	defer p.tunnels.Done()

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(dest, clientConn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(clientConn, dest); done <- struct{}{} }()
	select {
	case <-done:
		// One direction closed; the other will see EOF
		// shortly. We don't wait for the second to keep
		// teardown snappy on half-closed sockets.
	case <-p.quit:
		// Shutdown — force both ends shut so the io.Copy
		// goroutines wake. The deferred clientConn.Close +
		// dest.Close above run after this select returns;
		// closing here is what unblocks the goroutines.
		_ = clientConn.Close()
		_ = dest.Close()
		<-done // wait for at least one io.Copy to observe EOF
	}
}

// deny emits a 403 with x-deny-reason mirroring claude.ai's
// mimic (operator-readable rejection rationale).
func (p *proxy) deny(w http.ResponseWriter, host, reason string) {
	p.denied.Add(1)
	w.Header().Set("x-deny-reason", reason)
	http.Error(w, fmt.Sprintf("egress denied: %s (%s)", host, reason), http.StatusForbidden)
}

// Stats returns allowed + denied counters since boot. Hooked
// from `clawtool egress stats` (CLI verb) to surface live
// throughput without scraping logs.
func (p *proxy) Stats() (allowed, denied uint64) {
	return p.allowed.Load(), p.denied.Load()
}

// ─── allowlist ──────────────────────────────────────────────────

type allowSet struct {
	exact   map[string]bool
	suffix  []string // entries starting with "." (e.g. ".openai.com")
	wildAll bool     // "*" → allow everything (debug only)
}

// size returns the total entry count for the boot log line.
func (a allowSet) size() int {
	n := len(a.exact) + len(a.suffix)
	if a.wildAll {
		n++
	}
	return n
}

func parseAllowList(in []string) (allowSet, error) {
	out := allowSet{exact: map[string]bool{}}
	for _, raw := range in {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == "" {
			continue
		}
		if s == "*" {
			out.wildAll = true
			continue
		}
		if strings.HasPrefix(s, ".") {
			out.suffix = append(out.suffix, s)
			continue
		}
		out.exact[s] = true
	}
	return out, nil
}

func (a allowSet) matches(host string) bool {
	if a.wildAll {
		return true
	}
	host = strings.ToLower(host)
	if a.exact[host] {
		return true
	}
	for _, suf := range a.suffix {
		// ".openai.com" matches "api.openai.com" + "openai.com"
		if strings.HasSuffix(host, suf) || host == strings.TrimPrefix(suf, ".") {
			return true
		}
	}
	return false
}

func stripPort(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}
