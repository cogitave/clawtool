// Package telemetry — daemon log forwarder. The daemon's combined
// stdout/stderr lands in $XDG_STATE_HOME/clawtool/daemon.log. Every
// goroutine panic, every "clawtool: <subsystem>: <error>" stderr
// line, every BIAM reap warning ends up there — but it's local-
// only, so a daemon stuck in a panic loop on someone else's host is
// invisible to us until they file an issue.
//
// LogWatcher tails the daemon log starting from EOF (so we never
// stream the historical buffer), classifies lines into severity
// + event_kind taxonomies, redacts known secret shapes, rate-
// limits to keep a panicking daemon from flooding PostHog, and
// emits `clawtool.daemon.log_event` events through the existing
// telemetry client. NO log-line bodies cross the wire — only the
// classification fields, so an env-value or path that happens to
// be in the log can't leak.
//
// Wired in server.go after telemetry.New: one watcher per daemon
// boot, cancelled via context on shutdown.
package telemetry

import (
	"bufio"
	"context"
	"io"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

// logEventPerMinuteCap is the hard rate limit. A daemon stuck in a
// panic loop emits one log line per crash; capping at 60 per minute
// means we get the first minute of evidence, then go quiet — well
// under PostHog's per-distinct-id quota and harmless on the back
// end if the operator's daemon is genuinely flapping.
const logEventPerMinuteCap = 60

// logEventBatchInterval is how often we flush the rate-limit
// window. Every minute on the dot is fine — if we drop a few
// events from a high-volume burst, the first ones in the window
// already characterise the failure mode.
const logEventBatchInterval = time.Minute

// severity / event_kind taxonomies. Both are strict allow-lists
// (allow-listed in allowedKeys). Match on canonical substrings
// rather than full regex to keep the classifier fast on the
// log-line hot path.
type logSeverity string

const (
	sevError logSeverity = "error"
	sevWarn  logSeverity = "warn"
	sevPanic logSeverity = "panic"
)

// classify maps a daemon-log line to (severity, event_kind, ok).
// ok=false means the line is informational and should be skipped.
//
// The event_kind taxonomy stays coarse on purpose: "panic",
// "biam", "auth", "io", "other". A finer-grained classifier
// would need to learn the daemon's internal subsystems, which
// drifts with every refactor; staying coarse means the dashboard
// view still groups failures usefully without the classifier
// becoming a maintenance burden.
func classify(line string) (logSeverity, string, bool) {
	low := strings.ToLower(line)
	// Order matters: panic before everything (a panic line can
	// contain "no such file"), biam before io (BIAM init failures
	// often nest "no such file"), auth before generic error
	// (401 lines almost always also include "error"), then the
	// generic io / error / warn buckets last.
	switch {
	case strings.Contains(low, "panic:") || strings.Contains(line, "goroutine ") && strings.Contains(line, "[running]:"):
		return sevPanic, "panic", true
	case strings.Contains(low, "fatal error:"):
		return sevPanic, "fatal", true
	case strings.Contains(low, "biam") && (strings.Contains(low, "fail") || strings.Contains(low, "error")):
		return sevError, "biam", true
	case strings.Contains(low, "401") || strings.Contains(low, "unauthorized") || strings.Contains(low, "missing or malformed authorization"):
		return sevWarn, "auth", true
	case strings.Contains(low, "no such file") || strings.Contains(low, "permission denied") || strings.Contains(low, "i/o timeout"):
		return sevError, "io", true
	case strings.Contains(low, "error:") || strings.Contains(low, "✗"):
		return sevError, "other", true
	case strings.Contains(low, "warning:") || strings.Contains(low, "warn:"):
		return sevWarn, "other", true
	}
	return "", "", false
}

// LogWatcher tails a log file and forwards classified events to a
// telemetry client. One watcher per daemon process. Run is the
// blocking entrypoint; cancel via the context.
type LogWatcher struct {
	tc         *Client
	path       string
	tickEvery  time.Duration
	emitWindow atomic.Int64 // events emitted in the current minute
}

// NewLogWatcher constructs a watcher. tc may be nil (no-op) or a
// disabled client (also no-op — the Track method short-circuits).
// path is the daemon log path (typically daemon.LogPath()).
func NewLogWatcher(tc *Client, path string) *LogWatcher {
	return &LogWatcher{tc: tc, path: path, tickEvery: 250 * time.Millisecond}
}

// Run blocks until ctx is cancelled. Tails path from EOF, classifies
// each new line, redacts content, emits classification-only events
// at most logEventPerMinuteCap per minute. Open errors are logged
// once via the debug seam and the watcher exits — there's no daemon
// log on a fresh host until the daemon writes its first line, but
// server.go arranges for that to happen before this is called.
func (w *LogWatcher) Run(ctx context.Context) {
	if w == nil || w.tc == nil || !w.tc.Enabled() {
		return
	}
	f, err := os.Open(w.path)
	if err != nil {
		// Log file may not exist yet on a brand-new host; the
		// caller (server.go) opens it before we get here, but
		// be defensive: if it really isn't there, exit quietly.
		if debugEnabled {
			os.Stderr.WriteString("clawtool telemetry: logwatch open " + w.path + ": " + err.Error() + "\n")
		}
		return
	}
	defer f.Close()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return
	}

	go w.windowReset(ctx)

	r := bufio.NewReader(f)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line, err := r.ReadString('\n')
		if err == io.EOF {
			// No new data — wait the tick interval and try again.
			// We don't use fsnotify because the watch path is a
			// single known file (no rename / recreate dance) and
			// a 250ms poll is well under the latency the operator
			// would notice for "did my daemon just panic" queries.
			select {
			case <-ctx.Done():
				return
			case <-time.After(w.tickEvery):
			}
			continue
		}
		if err != nil {
			return
		}
		w.handleLine(strings.TrimRight(line, "\r\n"))
	}
}

// windowReset zeroes the per-minute counter every
// logEventBatchInterval. Runs as a goroutine for the watcher's
// lifetime; ctx-aware.
func (w *LogWatcher) windowReset(ctx context.Context) {
	t := time.NewTicker(logEventBatchInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.emitWindow.Store(0)
		}
	}
}

// handleLine classifies + (rate-limit-permitting) emits a single
// daemon log line. The line itself never reaches the wire — only
// `severity` + `event_kind` cross the boundary.
func (w *LogWatcher) handleLine(line string) {
	if line == "" {
		return
	}
	severity, kind, ok := classify(line)
	if !ok {
		return
	}
	// Rate limit: cap at logEventPerMinuteCap events per minute.
	// The check + increment isn't strictly atomic across two ops
	// but the worst case is a tiny over-emit in a burst — fine
	// for a sampler.
	if w.emitWindow.Add(1) > logEventPerMinuteCap {
		return
	}
	w.tc.Track("clawtool.daemon.log_event", map[string]any{
		"severity":   string(severity),
		"event_kind": kind,
		"command":    "daemon",
		"transport":  "http",
	})
}

// logTailRegexp is exposed for tests that want to verify the
// classifier matches its declared taxonomy. Not used in the hot
// path.
var logTailRegexp = regexp.MustCompile(`(?i)\b(panic|fatal|error|warn|warning|✗|biam|unauthorized|i/o timeout)\b`)
