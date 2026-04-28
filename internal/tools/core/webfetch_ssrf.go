// Package core — SSRF guard for WebFetch (ADR-021 phase B).
//
// Without this, an agent could ask WebFetch for `http://169.254.169.254/`
// (AWS metadata), `http://localhost:5432/` (the operator's local
// Postgres), or any RFC1918 address (the operator's internal network).
// The guard blocks resolution to those address ranges BEFORE the GET
// is issued, and re-checks every redirect target so a public
// 302→private redirect chain is rejected too.
//
// Per ADR-007 we don't ship our own DNS resolver — net.LookupIP is
// canonical. We only own the address-range allow/deny logic.
package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// ErrBlockedAddress is the sentinel returned when the resolved IP
// falls into a deny range. Caller surfaces it verbatim.
var ErrBlockedAddress = errors.New("WebFetch refused: target resolves to a private / loopback / link-local / cloud-metadata address (SSRF guard)")

// privateNets is the set of CIDRs WebFetch refuses by default. The
// list is conservative: every RFC1918 + loopback + link-local +
// cloud metadata + IPv6 unique-local + carrier-grade NAT range.
// Operators who need to fetch from these ranges (rare; usually a
// dev-against-localhost case) can opt out via the future
// `allow_private` flag.
var privateNets = mustParseCIDRs([]string{
	"127.0.0.0/8",    // loopback
	"::1/128",        // IPv6 loopback
	"10.0.0.0/8",     // RFC1918
	"172.16.0.0/12",  // RFC1918
	"192.168.0.0/16", // RFC1918
	"169.254.0.0/16", // link-local + cloud metadata (AWS / Azure / GCP)
	"100.64.0.0/10",  // carrier-grade NAT
	"fe80::/10",      // IPv6 link-local
	"fc00::/7",       // IPv6 unique-local
	"fd00::/8",       // IPv6 unique-local
	"::/128",         // IPv6 unspecified
	"0.0.0.0/8",      // IPv4 unspecified
	"224.0.0.0/4",    // multicast
	"ff00::/8",       // IPv6 multicast
})

func mustParseCIDRs(cidrs []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			// Programmer error — refuse to start.
			panic("webfetch: bad CIDR " + c + ": " + err.Error())
		}
		out = append(out, n)
	}
	return out
}

// resolveAndGuard looks up u.Host and returns the IPs it resolves to,
// failing with ErrBlockedAddress when ANY returned IP falls inside
// a private range. We deliberately fail-closed on partial matches
// so DNS rebinding attacks (public IP returned now, private later)
// don't slip through.
func resolveAndGuard(ctx context.Context, u *url.URL) error {
	host := u.Hostname()
	if host == "" {
		return errors.New("WebFetch: missing host")
	}
	// Literal IPs skip DNS but still go through the range check.
	if ip := net.ParseIP(host); ip != nil {
		return checkIPNotPrivate(ip)
	}
	resolver := net.DefaultResolver
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("WebFetch: resolve %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("WebFetch: %q has no IPs", host)
	}
	for _, a := range addrs {
		if err := checkIPNotPrivate(a.IP); err != nil {
			return err
		}
	}
	return nil
}

// checkIPNotPrivate returns ErrBlockedAddress wrapped with the IP
// when ip falls into the deny set. Plain net.IP shortcuts make this
// readable.
func checkIPNotPrivate(ip net.IP) error {
	for _, n := range privateNets {
		if n.Contains(ip) {
			return fmt.Errorf("%w (host resolved to %s, in %s)", ErrBlockedAddress, ip, n)
		}
	}
	return nil
}

// ssrfCheckRedirect is an http.Client.CheckRedirect that re-runs the
// guard for every hop. When the originating request opted into
// allow_private the guard's range check is skipped on the redirect
// chain too — surfaced through the request context.
func ssrfCheckRedirect(req *http.Request, via []*http.Request) error {
	// Cap the redirect chain at the same value the stdlib uses so
	// our guard doesn't accidentally tighten the existing default.
	if len(via) >= 10 {
		return errors.New("WebFetch: stopped after 10 redirects")
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return errors.New("WebFetch: redirect to non-http(s) scheme refused")
	}
	if strings.Contains(req.URL.Host, "@") {
		// Userinfo in URLs is a known phishing vector + breaks
		// the guard's host extraction.
		return errors.New("WebFetch: redirect URL contains userinfo, refused")
	}
	if allowPrivateFromContext(req.Context()) {
		return nil
	}
	return resolveAndGuard(req.Context(), req.URL)
}

// allowPrivateCtxKey carries the per-request opt-out flag through
// the redirect chain. Private type per Go's context conventions.
type allowPrivateCtxKey struct{}

func withAllowPrivate(ctx context.Context, allow bool) context.Context {
	return context.WithValue(ctx, allowPrivateCtxKey{}, allow)
}

func allowPrivateFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(allowPrivateCtxKey{}).(bool)
	return v
}
