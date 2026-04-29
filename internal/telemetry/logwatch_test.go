package telemetry

import (
	"context"
	"testing"
)

// TestClassify_Taxonomy guards the classifier's coarse-grained
// rules. Each case should match the documented taxonomy in
// logwatch.go: severity ∈ {error, warn, panic} and event_kind
// from the small allow-list. Lines outside the allow-list return
// ok=false so the watcher skips them.
func TestClassify_Taxonomy(t *testing.T) {
	cases := []struct {
		line     string
		wantSev  logSeverity
		wantKind string
		wantOK   bool
	}{
		// Panics (Go runtime + clawtool fatal-error wrappers)
		{"panic: runtime error: invalid memory address", sevPanic, "panic", true},
		{"goroutine 1 [running]:", sevPanic, "panic", true},
		{"fatal error: concurrent map writes", sevPanic, "fatal", true},

		// BIAM subsystem errors (operator-actionable surface)
		{"clawtool: biam store init failed: open: no such file", sevError, "biam", true},
		{"clawtool: biam reap stale tasks error: …", sevError, "biam", true},

		// Auth surface (warn, not error — every operator hits this once)
		{"daemon returned 401: missing or malformed Authorization header", sevWarn, "auth", true},
		{"unauthorized: token mismatch", sevWarn, "auth", true},

		// I/O class errors
		{"clawtool: read /tmp/foo: no such file or directory", sevError, "io", true},
		{"clawtool: write /var/log: permission denied", sevError, "io", true},
		{"http: i/o timeout fetching", sevError, "io", true},

		// Generic error / warn classes
		{"clawtool: source X: error: spawn failed", sevError, "other", true},
		{"✗ Verify — module mismatch", sevError, "other", true},
		{"clawtool: warning: telemetry token missing", sevWarn, "other", true},
		{"clawtool warn: rate limited", sevWarn, "other", true},

		// Lines we should NOT forward
		{"", "", "", false},
		{"clawtool: server.start: pid 38723 listening on 127.0.0.1:8080", "", "", false},
		{"clawtool: registered tool Bash", "", "", false},
		{"clawtool telemetry: enqueued event=server.start", "", "", false},
	}
	for _, tc := range cases {
		gotSev, gotKind, gotOK := classify(tc.line)
		if gotOK != tc.wantOK {
			t.Errorf("classify(%q) ok=%v, want %v", tc.line, gotOK, tc.wantOK)
			continue
		}
		if !tc.wantOK {
			continue
		}
		if gotSev != tc.wantSev {
			t.Errorf("classify(%q) severity=%q, want %q", tc.line, gotSev, tc.wantSev)
		}
		if gotKind != tc.wantKind {
			t.Errorf("classify(%q) event_kind=%q, want %q", tc.line, gotKind, tc.wantKind)
		}
	}
}

// TestLogWatcher_NilClientNoOps guards the nil-safety contract
// the rest of the daemon's telemetry boundary follows: a disabled
// or unconfigured telemetry client must make Run a clean no-op
// rather than panic — boot order needs to keep working when the
// operator has telemetry off.
func TestLogWatcher_NilClientNoOps(t *testing.T) {
	w := NewLogWatcher(nil, "/tmp/does-not-matter")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Run(ctx) // returns immediately on nil client
}
