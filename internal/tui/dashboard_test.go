package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/cogitave/clawtool/internal/agents/biam"
)

// applyDash mirrors applyOrch — runs Update against the dashboard
// Model and asserts the returned tea.Model is still a Model. Cmds
// are ignored: the dashboard's watchReadCmd reads from a real
// socket so test paths only ever exercise the model mutation half.
func applyDash(m Model, msg interface{}) Model {
	out, _ := m.Update(msg)
	return out.(Model)
}

// TestDashModel_SystemBannerLatchAndFade is the dashboard mirror of
// TestOrchModel_SystemBannerLatchAndFade. Confirms a watchSystemMsg
// latches the banner, renderSystemBanner produces non-empty output
// while active, and a tickMsg past TTL fades it.
func TestDashModel_SystemBannerLatchAndFade(t *testing.T) {
	m := New(nil, nil)
	m.width = 80
	m.height = 30
	m.loaded = true

	// Latch a release-available notification (the v0.22-era poller's
	// canonical use case).
	m = applyDash(m, watchSystemMsg{notification: biam.SystemNotification{
		Kind:       "update_available",
		Severity:   "info",
		Title:      "clawtool update available: v0.22.5 → v0.22.10",
		ActionHint: "clawtool upgrade",
		TS:         time.Now(),
	}})
	if m.systemBanner == nil {
		t.Fatal("expected systemBanner set after watchSystemMsg")
	}
	if got := m.renderSystemBanner(); got == "" {
		t.Error("expected banner render non-empty when banner active")
	}
	// View() must include the banner row when active. We can't
	// match the exact escape-styled string but checking for the
	// title fragment is enough — proves the row joined into the
	// rendered output.
	if !strings.Contains(m.View(), "clawtool update available") {
		t.Error("View() should include banner title when banner active")
	}

	// Tick within TTL — banner stays.
	m = applyDash(m, tickMsg(time.Now()))
	if m.systemBanner == nil {
		t.Error("banner cleared too early")
	}

	// Backdate the arrival past TTL, tick again — banner clears.
	m.systemBannerAt = time.Now().Add(-2 * dashSystemBannerTTL)
	m = applyDash(m, tickMsg(time.Now()))
	if m.systemBanner != nil {
		t.Error("banner should have faded past TTL")
	}
	if got := m.renderSystemBanner(); got != "" {
		t.Errorf("rendered banner should be empty post-fade, got %q", got)
	}
}

// TestDashModel_RenderSystemBanner_SeverityVariants confirms the
// render switches icon + tint per Kind/Severity. Doesn't pin
// specific colors (those drift) but asserts the banner is
// non-empty in each case and contains the title — proves the
// switch arms wire through correctly.
func TestDashModel_RenderSystemBanner_SeverityVariants(t *testing.T) {
	m := New(nil, nil)
	m.width = 60

	for _, kind := range []string{"update_available", "warning", "error"} {
		m.systemBanner = &biam.SystemNotification{
			Kind:     kind,
			Severity: "warning",
			Title:    "title-" + kind,
		}
		m.systemBannerAt = time.Now()
		got := m.renderSystemBanner()
		if got == "" {
			t.Errorf("kind=%s: expected non-empty render", kind)
		}
		if !strings.Contains(got, "title-"+kind) {
			t.Errorf("kind=%s: rendered output missing title; got %q", kind, got)
		}
	}
}
