// internal/cli/upgrade_ux.go — visual rendering for `clawtool
// upgrade`. The upgrade flow is one of the rare CLI moments where
// the user is actively waiting on us; that's where polish earns
// disproportionate trust. This file encapsulates the rendering so
// upgrade.go's orchestration stays linear and readable.
//
// Design constraints:
//   - TTY-aware: colours + box-drawing only when stdout is a real
//     terminal. Pipe-redirect (e.g. `clawtool upgrade | tee`) gets
//     plain ASCII so log files stay greppable.
//   - No spinner / animation: the upgrade is short (1–5s on a
//     local network), and an animated spinner stuck to the
//     terminal control codes turns into garbage when redirected.
//     Static phase markers ("→ doing X" → "✓ X (350ms)") read
//     fine in both modes.
//   - One-shot output: each phase prints its line as it
//     completes, so a Ctrl-C mid-flow leaves a partial but
//     legible transcript instead of a half-redrawn screen.
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// upgradeUX is a thin renderer bound to one upgrade invocation.
// Construct via newUpgradeUX(stdout); call HeaderDelta /
// PhaseStart / PhaseDone / Section / NextSteps in the order
// upgrade.go drives the flow.
type upgradeUX struct {
	w     io.Writer
	color bool        // lipgloss styles render iff true
	width int         // terminal width clamp; 80 when not a tty
	style ux          // pre-built styles bound to color=on/off
	now   time.Time   // last PhaseStart timestamp — paired with PhaseDone for elapsed
	phase string      // last phase label — to print in PhaseDone
}

type ux struct {
	headerBox    lipgloss.Style
	headerLabel  lipgloss.Style
	versionFrom  lipgloss.Style
	versionTo    lipgloss.Style
	versionArrow lipgloss.Style
	tickOK       lipgloss.Style
	tickWarn     lipgloss.Style
	tickFail     lipgloss.Style
	dim          lipgloss.Style
	sectionTitle lipgloss.Style
	bullet       lipgloss.Style
}

func newUpgradeUX(w io.Writer) *upgradeUX {
	color := false
	width := 80
	if f, ok := w.(*os.File); ok {
		// isTTY (defined in init_wizard.go) → file-mode-bit check;
		// matches what the wider CLI already uses, no second
		// definition needed here.
		color = isTTY(f)
		if color {
			if cols, _, err := term.GetSize(int(f.Fd())); err == nil && cols >= 60 {
				width = cols
				if width > 100 {
					width = 100 // cap so very wide terminals don't sprawl
				}
			}
		}
	}
	return &upgradeUX{
		w:     w,
		color: color,
		width: width,
		style: buildUXStyles(color),
	}
}

func buildUXStyles(color bool) ux {
	if !color {
		// Identity styles for the no-tty path. Render() returns
		// the input unchanged so call sites don't branch.
		empty := lipgloss.NewStyle()
		return ux{
			headerBox:    empty,
			headerLabel:  empty,
			versionFrom:  empty,
			versionTo:    empty,
			versionArrow: empty,
			tickOK:       empty,
			tickWarn:     empty,
			tickFail:     empty,
			dim:          empty,
			sectionTitle: empty,
			bullet:       empty,
		}
	}
	return ux{
		headerBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 2),
		headerLabel:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63")),
		versionFrom:  lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		versionTo:    lipgloss.NewStyle().Foreground(lipgloss.Color("83")).Bold(true),
		versionArrow: lipgloss.NewStyle().Foreground(lipgloss.Color("63")),
		tickOK:       lipgloss.NewStyle().Foreground(lipgloss.Color("83")),
		tickWarn:     lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		tickFail:     lipgloss.NewStyle().Foreground(lipgloss.Color("203")),
		dim:          lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		sectionTitle: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63")),
		bullet:       lipgloss.NewStyle().Foreground(lipgloss.Color("63")),
	}
}

// HeaderDelta prints the rounded box at the top with the version
// transition. `from` is the operator's current version; `to` is
// the release the upgrade is moving them to.
func (u *upgradeUX) HeaderDelta(from, to string) {
	label := u.style.headerLabel.Render("clawtool upgrade")
	delta := fmt.Sprintf("%s  %s  %s",
		u.style.versionFrom.Render(from),
		u.style.versionArrow.Render("→"),
		u.style.versionTo.Render(to),
	)
	body := label + "\n" + delta
	if u.color {
		fmt.Fprintln(u.w, u.style.headerBox.Render(body))
	} else {
		// Plain shape for log files. Two-line block, separator
		// underneath — survives copy-paste and grep cleanly.
		fmt.Fprintf(u.w, "clawtool upgrade\n%s -> %s\n%s\n", from, to, strings.Repeat("-", 30))
	}
	fmt.Fprintln(u.w)
}

// PhaseStart announces a step about to begin. Pair with PhaseDone
// (success) or PhaseFail (error). The arrow + label show
// immediately so a user watching the terminal sees what we're
// working on, not just a result line that lands all at once.
func (u *upgradeUX) PhaseStart(label string) {
	u.now = time.Now()
	u.phase = label
	if u.color {
		fmt.Fprintf(u.w, "  %s %s\n",
			u.style.versionArrow.Render("→"),
			label,
		)
	} else {
		fmt.Fprintf(u.w, "  -> %s\n", label)
	}
}

// PhaseDone marks the most-recent PhaseStart as successful and
// prints the elapsed time so the user sees where the wait went.
// Optional detail string lands as a dim suffix (e.g. asset name,
// URL, file size).
func (u *upgradeUX) PhaseDone(detail string) {
	dt := time.Since(u.now).Round(time.Millisecond)
	tick := "✓"
	if !u.color {
		tick = "OK"
	}
	tickRendered := u.style.tickOK.Render(tick)
	suffix := u.style.dim.Render(fmt.Sprintf("(%s)", dt))
	if detail != "" {
		suffix = u.style.dim.Render(fmt.Sprintf("(%s · %s)", dt, detail))
	}
	fmt.Fprintf(u.w, "  %s %s %s\n", tickRendered, u.phase, suffix)
	u.phase = ""
}

// PhaseFail marks the most-recent PhaseStart as failed. The
// reason is surfaced as the failure-line body; an actionable
// hint string (optional) lands underneath in dim.
func (u *upgradeUX) PhaseFail(reason, hint string) {
	dt := time.Since(u.now).Round(time.Millisecond)
	tick := "✗"
	if !u.color {
		tick = "FAIL"
	}
	fmt.Fprintf(u.w, "  %s %s %s\n",
		u.style.tickFail.Render(tick),
		u.phase,
		u.style.dim.Render(fmt.Sprintf("(%s)", dt)),
	)
	if reason != "" {
		fmt.Fprintf(u.w, "    %s\n", u.style.tickFail.Render(reason))
	}
	if hint != "" {
		fmt.Fprintf(u.w, "    %s %s\n", u.style.bullet.Render("hint"), u.style.dim.Render(hint))
	}
	u.phase = ""
}

// Section starts a new visually distinct block (e.g. "Daemon
// restart", "What's new", "Next steps"). Use to group related
// phases under a heading the eye can land on.
func (u *upgradeUX) Section(title string) {
	if u.color {
		fmt.Fprintf(u.w, "\n  %s\n", u.style.sectionTitle.Render(title))
	} else {
		fmt.Fprintf(u.w, "\n  %s\n  %s\n", title, strings.Repeat("-", len(title)))
	}
}

// ReleaseNotes prints up to N non-empty lines of the release
// body — typically the GoReleaser-rendered "Features" / "Fixes"
// blocks. Falls back silently when the body is empty (some
// releases don't have notes; we don't want a "no notes" stub
// in the user's transcript).
func (u *upgradeUX) ReleaseNotes(body string, maxLines int) {
	if body = strings.TrimSpace(body); body == "" {
		return
	}
	u.Section("What's new")
	count := 0
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimRight(raw, " \t")
		if line == "" {
			continue
		}
		fmt.Fprintf(u.w, "    %s\n", line)
		count++
		if count >= maxLines {
			fmt.Fprintf(u.w, "    %s\n", u.style.dim.Render("…"))
			break
		}
	}
}

// NextSteps prints a small bulleted list of follow-up commands
// the user might want to run next. Positions the upgrade output
// as one waypoint in a longer flow rather than a dead-end
// success line.
func (u *upgradeUX) NextSteps(items []string) {
	if len(items) == 0 {
		return
	}
	u.Section("Next steps")
	for _, item := range items {
		fmt.Fprintf(u.w, "    %s %s\n", u.style.bullet.Render("•"), item)
	}
	fmt.Fprintln(u.w)
}

// Note prints an inline informational line outside the
// PhaseStart / PhaseDone protocol. Used for "no daemon was
// running" type observations that aren't really phases.
func (u *upgradeUX) Note(text string) {
	fmt.Fprintf(u.w, "  %s %s\n", u.style.dim.Render("·"), u.style.dim.Render(text))
}


