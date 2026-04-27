package agents

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// dispatchLimiter enforces config.DispatchLimits per agent instance.
// One token bucket + one concurrency semaphore per instance, shared
// across CLI / MCP / HTTP because they all hit Supervisor.dispatch.
//
// Per ADR-007 we wrap golang.org/x/time/rate (BSD-3-Clause); we
// don't roll our own token bucket.
type dispatchLimiter struct {
	mu          sync.Mutex
	rate        rate.Limit
	burst       int
	concurrency int
	buckets     map[string]*rate.Limiter
	semaphores  map[string]chan struct{}
}

// newDispatchLimiter parses the config block once. Rate "" disables
// the limiter completely (zero allocations on the hot path).
func newDispatchLimiter(rateStr string, burst, maxConcurrent int) (*dispatchLimiter, error) {
	r, err := parseRate(rateStr)
	if err != nil {
		return nil, err
	}
	if burst <= 0 && r > 0 {
		// Default burst = 1 second worth of tokens, with a floor of 1.
		burst = int(r) + 1
		if burst < 1 {
			burst = 1
		}
	}
	return &dispatchLimiter{
		rate:        r,
		burst:       burst,
		concurrency: maxConcurrent,
		buckets:     map[string]*rate.Limiter{},
		semaphores:  map[string]chan struct{}{},
	}, nil
}

// acquire blocks until the per-instance bucket has a token AND the
// semaphore has a slot. Returns a release func the caller must defer.
// When the limiter is disabled (rate==0, concurrency==0) acquire is
// a no-op + the release is a no-op.
func (l *dispatchLimiter) acquire(ctx context.Context, instance string) (release func(), err error) {
	if l == nil || (l.rate == 0 && l.concurrency == 0) {
		return func() {}, nil
	}

	// Token bucket — wait until a token is available or ctx cancels.
	if l.rate > 0 {
		bucket := l.bucket(instance)
		if err := bucket.Wait(ctx); err != nil {
			return nil, fmt.Errorf("dispatch rate-limited: %w", err)
		}
	}

	// Concurrency semaphore — channel-based so ctx cancellation works.
	if l.concurrency > 0 {
		sem := l.semaphore(instance)
		select {
		case sem <- struct{}{}:
			return func() { <-sem }, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return func() {}, nil
}

// bucket returns (or lazily creates) the rate.Limiter for instance.
func (l *dispatchLimiter) bucket(instance string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[instance]
	if !ok {
		b = rate.NewLimiter(l.rate, l.burst)
		l.buckets[instance] = b
	}
	return b
}

func (l *dispatchLimiter) semaphore(instance string) chan struct{} {
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.semaphores[instance]
	if !ok {
		s = make(chan struct{}, l.concurrency)
		l.semaphores[instance] = s
	}
	return s
}

// parseRate accepts "<n>/<dur>" forms (e.g. "30/m", "5/s", "1000/h").
// Returns 0 + nil error when the input is empty (limiter disabled).
func parseRate(s string) (rate.Limit, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	slash := strings.IndexByte(s, '/')
	if slash <= 0 || slash == len(s)-1 {
		return 0, errors.New(`dispatch.rate: expect "<n>/<dur>" e.g. "30/m"`)
	}
	n, err := strconv.ParseFloat(s[:slash], 64)
	if err != nil {
		return 0, fmt.Errorf(`dispatch.rate: numerator: %w`, err)
	}
	durStr := s[slash+1:]
	// Allow bare "s" / "m" / "h" without a leading 1; normalise to "1<unit>".
	if len(durStr) == 1 || (len(durStr) > 0 && (durStr[0] < '0' || durStr[0] > '9')) {
		durStr = "1" + durStr
	}
	d, err := time.ParseDuration(durStr)
	if err != nil {
		return 0, fmt.Errorf(`dispatch.rate: denominator: %w`, err)
	}
	if d <= 0 {
		return 0, errors.New(`dispatch.rate: duration must be positive`)
	}
	return rate.Limit(n / d.Seconds()), nil
}
