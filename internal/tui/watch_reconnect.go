package tui

import "time"

// Auto-reconnect for the daemon's task-watch Unix socket.
//
// Both the dashboard and the orchestrator subscribe to the same
// socket; when the daemon restarts (manual `pkill`, `clawtool
// upgrade`, crash, OOM kill) the connection drops. The TUIs used
// to show "watch socket disconnected — fall back to polling" and
// stay disconnected until the user pressed `r`. That's a
// regression on the user's mental model: "the daemon's back, why
// is my dashboard still stale?" — and `clawtool upgrade` made
// this worse by restarting the daemon as part of every release.
//
// Reconnect strategy: exponential backoff, base 500ms, doubling,
// capped at 5s. The cap is deliberately short (vs the more usual
// 30s) because the recovery path is local-host fast: the daemon
// usually comes up within 1–3s, and a long backoff would leave
// the operator staring at a stale screen.
//
// Reset on every successful read (watchEventMsg / watchSystemMsg)
// so a one-off blip doesn't permanently widen the window.

const (
	watchReconnectBaseDelay = 500 * time.Millisecond
	watchReconnectMaxDelay  = 5 * time.Second
)

// nextWatchBackoff returns the delay for the next reconnect
// attempt. Pass the previous backoff (zero on first failure) and
// the result is the delay to wait before re-dialing. Pure
// function — easy to unit-test, easy for the caller to inspect.
func nextWatchBackoff(prev time.Duration) time.Duration {
	if prev <= 0 {
		return watchReconnectBaseDelay
	}
	next := prev * 2
	if next > watchReconnectMaxDelay {
		return watchReconnectMaxDelay
	}
	return next
}

// watchReconnectMsg is the model-internal signal that the backoff
// timer has elapsed and the model should re-fire its subscribe
// command. The dashboard and orchestrator each handle this in
// their own Update — re-using the message type keeps both surfaces
// reactive to the same lifecycle.
type watchReconnectMsg struct{}
