// internal/cli/onboard_ux.go — visual rendering for `clawtool
// onboard`. Onboard is the first ten seconds the operator spends
// with clawtool; the wizard either hooks them or churns them. This
// file polishes that surface:
//
//   - Clear screen on entry so the operator sees a clean canvas,
//     not the pile of `npm install` / `git status` noise that was
//     in their terminal when they typed `clawtool onboard`.
//   - Boxed header with the live host-detection result rendered
//     as a single tight row of ✓ / ✗ pills.
//   - Phase-style side-effect output (Section / PhaseStart /
//     PhaseDone) instead of raw `stdoutLn` lines, so a multi-
//     bridge install reads as a labelled progress block.
//   - Tight final summary: a ✓-checklist of what was wired,
//     not the full `clawtool overview` dump.
//
// Mirrors upgrade_ux.go's design constraints: TTY-aware (plain
// ASCII when piped), no spinners (Ctrl-C-friendly), one-shot
// output.
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

// onboardUX is a thin renderer bound to one onboard invocation.
// Construct via newOnboardUX(stdout); the wizard drives it via
// Header / Section / Phase* / Summary in flow order.
type onboardUX struct {
	w     io.Writer
	color bool
	width int
	style onboardStyles
	now   time.Time
	phase string
}

type onboardStyles struct {
	headerBox    lipgloss.Style
	headerTitle  lipgloss.Style
	headerSub    lipgloss.Style
	pillOK       lipgloss.Style
	pillMissing  lipgloss.Style
	tickOK       lipgloss.Style
	tickWarn     lipgloss.Style
	tickFail     lipgloss.Style
	dim          lipgloss.Style
	sectionTitle lipgloss.Style
	bullet       lipgloss.Style
	arrow        lipgloss.Style
}

func newOnboardUX(w io.Writer) *onboardUX {
	color := false
	width := 80
	if f, ok := w.(*os.File); ok {
		color = isTTY(f)
		if color {
			if cols, _, err := term.GetSize(int(f.Fd())); err == nil && cols >= 60 {
				width = cols
				if width > 100 {
					width = 100
				}
			}
		}
	}
	return &onboardUX{
		w:     w,
		color: color,
		width: width,
		style: buildOnboardStyles(color),
	}
}

func buildOnboardStyles(color bool) onboardStyles {
	if !color {
		empty := lipgloss.NewStyle()
		return onboardStyles{
			headerBox: empty, headerTitle: empty, headerSub: empty,
			pillOK: empty, pillMissing: empty,
			tickOK: empty, tickWarn: empty, tickFail: empty,
			dim: empty, sectionTitle: empty, bullet: empty, arrow: empty,
		}
	}
	return onboardStyles{
		headerBox: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 2),
		headerTitle: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63")),
		headerSub:   lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		pillOK: lipgloss.NewStyle().
			Foreground(lipgloss.Color("83")).Bold(true).
			Padding(0, 1),
		pillMissing: lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Padding(0, 1),
		tickOK:       lipgloss.NewStyle().Foreground(lipgloss.Color("83")),
		tickWarn:     lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		tickFail:     lipgloss.NewStyle().Foreground(lipgloss.Color("203")),
		dim:          lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		sectionTitle: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63")),
		bullet:       lipgloss.NewStyle().Foreground(lipgloss.Color("63")),
		arrow:        lipgloss.NewStyle().Foreground(lipgloss.Color("63")),
	}
}

// ClearScreen wipes the terminal and parks the cursor at home.
// No-op when stdout isn't a tty so a piped invocation
// (`clawtool onboard | tee` / CI logs) keeps every line.
//
// Uses the standard `\033[2J\033[3J\033[H` sequence: clear visible
// area + scrollback + move-home. Without the 3J piece, scrolling
// up after onboard surfaces the pre-wizard noise the operator
// just escaped. With 3J the slate is genuinely clean.
func (u *onboardUX) ClearScreen() {
	if !u.color {
		return
	}
	fmt.Fprint(u.w, "\033[2J\033[3J\033[H")
}

// Header renders the rounded-box welcome panel: title + version
// + a single-line pill row showing which agent CLIs are present
// on the host. The box stretches the full terminal width
// (clamped to u.width, max 100) so the wizard occupies the
// viewport edge-to-edge instead of looking lost in a sea of
// whitespace on a wide terminal.
func (u *onboardUX) Header(version string, found map[string]bool) {
	families := []struct{ key, label string }{
		{"claude", "claude-code"},
		{"codex", "codex"},
		{"gemini", "gemini"},
		{"opencode", "opencode"},
		{"hermes", "hermes"},
	}
	var pills []string
	for _, f := range families {
		if found[f.key] {
			if u.color {
				pills = append(pills, u.style.pillOK.Render("✓ "+f.label))
			} else {
				pills = append(pills, "[OK] "+f.label)
			}
		} else {
			if u.color {
				pills = append(pills, u.style.pillMissing.Render("· "+f.label))
			} else {
				pills = append(pills, "[--] "+f.label)
			}
		}
	}
	pillRow := strings.Join(pills, "  ")
	title := u.style.headerTitle.Render("clawtool onboard")
	sub := u.style.headerSub.Render(fmt.Sprintf("v%s   ·   first-time setup wizard", version))
	body := title + "   " + sub + "\n" + pillRow
	if u.color {
		// Stretch the box to (terminal width - 2 for padding).
		// Lipgloss Width() sets the inner content width; the
		// rounded border + 2 padding cells live outside.
		boxed := u.style.headerBox.Width(u.width - 4).Render(body)
		fmt.Fprintln(u.w, boxed)
	} else {
		fmt.Fprintf(u.w, "clawtool onboard  v%s\n%s\n%s\n",
			version, strings.Repeat("-", u.width), pillRow)
	}
	fmt.Fprintln(u.w)
}

// Section starts a new visually distinct block. Renders as a
// full-width title bar with a thin separator rule beneath it so
// the eye lands on each block's start. Mirrors the upgrade flow's
// section semantics — operators who've run `clawtool upgrade`
// already know the cadence.
func (u *onboardUX) Section(title string) {
	if u.color {
		// Subtle separator rule across the viewport — the eye
		// uses it to chunk the wizard into reading units.
		rule := strings.Repeat("─", u.width-4)
		fmt.Fprintf(u.w, "\n  %s\n  %s\n",
			u.style.sectionTitle.Render(title),
			u.style.dim.Render(rule),
		)
	} else {
		fmt.Fprintf(u.w, "\n  %s\n  %s\n", title, strings.Repeat("-", len(title)))
	}
}

// PhaseStart announces a step about to begin. Pair with PhaseDone
// (success), PhaseSkip (no-op), or PhaseFail (error).
func (u *onboardUX) PhaseStart(label string) {
	u.now = time.Now()
	u.phase = label
	if u.color {
		fmt.Fprintf(u.w, "  %s %s\n", u.style.arrow.Render("→"), label)
	} else {
		fmt.Fprintf(u.w, "  -> %s\n", label)
	}
}

// PhaseDone marks the most-recent PhaseStart as successful.
// Optional detail rides as a dim suffix.
func (u *onboardUX) PhaseDone(detail string) {
	dt := time.Since(u.now).Round(time.Millisecond)
	tick := "✓"
	if !u.color {
		tick = "OK"
	}
	suffix := u.style.dim.Render(fmt.Sprintf("(%s)", dt))
	if detail != "" {
		suffix = u.style.dim.Render(fmt.Sprintf("(%s · %s)", dt, detail))
	}
	fmt.Fprintf(u.w, "  %s %s %s\n", u.style.tickOK.Render(tick), u.phase, suffix)
	u.phase = ""
}

// PhaseSkip marks a phase as intentionally skipped (e.g. operator
// declined identity creation). Distinct visual from a fail so the
// final summary reads correctly.
func (u *onboardUX) PhaseSkip(reason string) {
	tick := "·"
	if !u.color {
		tick = "--"
	}
	suffix := ""
	if reason != "" {
		suffix = "  " + u.style.dim.Render(reason)
	}
	fmt.Fprintf(u.w, "  %s %s%s\n", u.style.dim.Render(tick), u.phase, suffix)
	u.phase = ""
}

// PhaseFail marks the most-recent PhaseStart as failed. Reason
// goes inline; a multi-line stack/error stays on the next line.
func (u *onboardUX) PhaseFail(reason string) {
	tick := "✗"
	if !u.color {
		tick = "FAIL"
	}
	fmt.Fprintf(u.w, "  %s %s\n", u.style.tickFail.Render(tick), u.phase)
	if reason != "" {
		fmt.Fprintf(u.w, "    %s\n", u.style.tickFail.Render(reason))
	}
	u.phase = ""
}

// Note prints an informational line outside the phase protocol —
// for "this was already configured" style observations that
// aren't really phases.
func (u *onboardUX) Note(text string) {
	fmt.Fprintf(u.w, "  %s %s\n", u.style.dim.Render("·"), u.style.dim.Render(text))
}

// Summary prints the closing checklist. Each pair is (label,
// outcome) where outcome is "ok" | "skip" | "fail". Tight,
// scan-friendly view of "what just happened" — operator can
// see the wins and misses on one screen.
func (u *onboardUX) Summary(rows []SummaryRow) {
	u.Section("Summary")
	for _, r := range rows {
		var marker string
		switch r.Outcome {
		case "ok":
			marker = u.style.tickOK.Render("✓")
			if !u.color {
				marker = "[OK]"
			}
		case "skip":
			marker = u.style.dim.Render("·")
			if !u.color {
				marker = "[--]"
			}
		case "fail":
			marker = u.style.tickFail.Render("✗")
			if !u.color {
				marker = "[XX]"
			}
		default:
			marker = " "
		}
		detail := ""
		if r.Detail != "" {
			detail = "  " + u.style.dim.Render(r.Detail)
		}
		fmt.Fprintf(u.w, "    %s %s%s\n", marker, r.Label, detail)
	}
	fmt.Fprintln(u.w)
}

// SummaryRow is one line in the closing checklist.
type SummaryRow struct {
	Label   string
	Outcome string // "ok" | "skip" | "fail"
	Detail  string // optional dim suffix
}

// NextSteps prints follow-up commands the operator may want to
// run next. Same shape as the upgrade UX's NextSteps.
func (u *onboardUX) NextSteps(items []string) {
	if len(items) == 0 {
		return
	}
	u.Section("Next steps")
	for _, item := range items {
		fmt.Fprintf(u.w, "    %s %s\n", u.style.bullet.Render("•"), item)
	}
	fmt.Fprintln(u.w)
}
