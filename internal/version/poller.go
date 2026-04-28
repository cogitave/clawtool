// Package version — daemon-side periodic update poller. Every
// `Interval` ticks (default 1h) the poller calls `CheckForUpdate`;
// when a transition from no-update → update-available is detected
// it broadcasts a SystemNotification onto the supplied publisher
// (typically biam.WatchHub.BroadcastSystem). Connected watchers —
// orchestrator, dashboard, `task watch`, MCP clients dialling the
// watch socket — render the inline banner immediately, no polling.
//
// Why daemon-side rather than per-CLI: the CLI is short-lived;
// the daemon (`clawtool serve`) is the long-running process the
// operator already keeps up. One canonical poller, single GitHub
// round-trip per host per hour, push to every active surface.
//
// Telemetry: each transition emits a `clawtool.update_check` event
// with the same allow-listed payload SessionStart uses, so the
// operator gets a unified PostHog view of update detection across
// surfaces.
package version

import (
	"context"
	"sync"
	"time"
)

// PublishFn is the slim function shape the poller needs from the
// caller. server.go wraps biam.WatchHub.BroadcastSystem; tests
// pass a recorder closure. Keeping this as a function instead of
// an interface avoids dragging biam into the version package's
// import graph (version stays a leaf).
type PublishFn func(kind, severity, title, body, actionHint string)

// PollerConfig overrides the defaults — useful for tests that need
// a tighter tick. Empty struct = production defaults.
type PollerConfig struct {
	// Interval between checks. Default 1h. Tests pass 50ms.
	Interval time.Duration
	// Timeout per HTTP round-trip. Default 5s.
	Timeout time.Duration
	// Now overrides time.Now for deterministic testing of
	// transitions. Production passes nil.
	Now func() time.Time
}

// Poller wraps the periodic update probe + publisher. Lifetime =
// daemon process. Stop via ctx cancellation.
type Poller struct {
	cfg   PollerConfig
	pub   PublishFn
	mu    sync.Mutex
	last  string // last seen latest tag — drives transition detection
	track func(outcome string)
}

// NewPoller constructs the poller with the given publisher and
// optional telemetry tracker. `track` is called on every check
// with the outcome enum ("up_to_date" | "update_available" |
// "check_failed"); pass nil to skip telemetry.
func NewPoller(pub PublishFn, cfg PollerConfig, track func(outcome string)) *Poller {
	if cfg.Interval <= 0 {
		cfg.Interval = time.Hour
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Poller{cfg: cfg, pub: pub, track: track}
}

// Run blocks until ctx cancels, ticking once per Interval. The
// first check fires immediately so a fresh daemon catches an
// already-pending update without waiting an hour.
func (p *Poller) Run(ctx context.Context) {
	p.tick(ctx) // first call before the timer starts
	t := time.NewTicker(p.cfg.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.tick(ctx)
		}
	}
}

// tick runs one check cycle: fetch latest, compare to current,
// publish on transition, emit telemetry. Failures fail-open:
// the poller never crashes the daemon.
func (p *Poller) tick(ctx context.Context) {
	c, cancel := context.WithTimeout(ctx, p.cfg.Timeout)
	defer cancel()
	info := CheckForUpdate(c)
	outcome := "up_to_date"
	switch {
	case info.Err != nil:
		outcome = "check_failed"
	case info.HasUpdate:
		outcome = "update_available"
	}
	if p.track != nil {
		p.track(outcome)
	}
	if !info.HasUpdate || p.pub == nil {
		return
	}
	// Transition gate: only publish when the latest tag CHANGES,
	// not on every tick. Without this every connected watcher
	// would see the banner re-fire hourly even though the state
	// is stable.
	p.mu.Lock()
	already := p.last == info.Latest
	p.last = info.Latest
	p.mu.Unlock()
	if already {
		return
	}
	p.pub(
		"update_available",
		"info",
		"clawtool update available: v"+Resolved()+" → "+info.Latest,
		"A new clawtool release shipped on cogitave/clawtool. Run `clawtool upgrade` to install — atomic temp+rename, the running daemon stays up until the next dispatch.",
		"clawtool upgrade",
	)
}
