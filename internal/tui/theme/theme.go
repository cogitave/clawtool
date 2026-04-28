// Package theme — color palette + lipgloss style factory shared
// across every clawtool TUI surface (dashboard, orchestrator,
// future split-pane views). Catppuccin-ish dark default, adaptive
// to light terminals via lipgloss.AdaptiveColor.
//
// Operators who want a different palette set CLAWTOOL_THEME=light
// or wire a custom Theme via WithTheme(). The dispatch surfaces all
// pull styles through the package-level Default() — swapping the
// pointer at boot is enough to retheme every pane.
package theme

import "github.com/charmbracelet/lipgloss"

// Theme is a single rendered style set. Built once per TUI boot.
type Theme struct {
	// Surfaces
	Background    lipgloss.Style // root canvas
	PaneBorder    lipgloss.Style // inactive pane chrome
	PaneFocused   lipgloss.Style // focused pane chrome (accent border)
	PaneTitle     lipgloss.Style // header line inside a pane
	PaneSubtitle  lipgloss.Style // muted second-line under title
	StatusBar     lipgloss.Style // footer container
	HeaderBar     lipgloss.Style // top banner container
	HeaderTitle   lipgloss.Style // app name in the banner
	HeaderVersion lipgloss.Style // version pill

	// Status pills (rendered with bg fill so they stand out)
	StatusActive    lipgloss.Style
	StatusPending   lipgloss.Style
	StatusDone      lipgloss.Style
	StatusFailed    lipgloss.Style
	StatusCancelled lipgloss.Style

	// Content
	Body       lipgloss.Style // default text
	Dim        lipgloss.Style // de-emphasised metadata
	Accent     lipgloss.Style // primary highlight
	AccentSoft lipgloss.Style // secondary highlight
	Success    lipgloss.Style
	Warning    lipgloss.Style
	Error      lipgloss.Style

	// Selection / focus
	SelectedRow   lipgloss.Style
	UnselectedRow lipgloss.Style

	// Stream pane
	StreamLine    lipgloss.Style
	StreamCaret   lipgloss.Style // ">" prefix on each frame line
	StreamElapsed lipgloss.Style // (timestamp / duration tag)

	// Help bar (key-binding hints)
	HelpKey  lipgloss.Style
	HelpDesc lipgloss.Style
	HelpSep  lipgloss.Style
}

// Default returns the singleton theme. Idempotent.
func Default() *Theme { return defaultTheme }

var defaultTheme = build(catppuccinDark())

// palette is the raw color set a Theme is materialised from. Light
// and dark variants share the same struct so AdaptiveColor can map
// between them cleanly.
type palette struct {
	bg, surface, surfaceAlt, border, borderFocus lipgloss.AdaptiveColor
	fg, fgDim, fgMuted                           lipgloss.AdaptiveColor
	accent, accentAlt, accentSoft                lipgloss.AdaptiveColor
	success, warning, danger, info               lipgloss.AdaptiveColor
}

// catppuccinDark is the default palette — Catppuccin Mocha bg with
// Mocha accents on dark, Latte fg on light. Picked for muscle-memory
// familiarity (gh-dash, lazygit, k9s all converge here).
func catppuccinDark() palette {
	return palette{
		bg:          lipgloss.AdaptiveColor{Light: "#eff1f5", Dark: "#1e1e2e"},
		surface:     lipgloss.AdaptiveColor{Light: "#e6e9ef", Dark: "#181825"},
		surfaceAlt:  lipgloss.AdaptiveColor{Light: "#dce0e8", Dark: "#11111b"},
		border:      lipgloss.AdaptiveColor{Light: "#9ca0b0", Dark: "#45475a"},
		borderFocus: lipgloss.AdaptiveColor{Light: "#8839ef", Dark: "#cba6f7"}, // mauve
		fg:          lipgloss.AdaptiveColor{Light: "#4c4f69", Dark: "#cdd6f4"},
		fgDim:       lipgloss.AdaptiveColor{Light: "#6c6f85", Dark: "#a6adc8"},
		fgMuted:     lipgloss.AdaptiveColor{Light: "#9ca0b0", Dark: "#6c7086"},
		accent:      lipgloss.AdaptiveColor{Light: "#8839ef", Dark: "#cba6f7"}, // mauve
		accentAlt:   lipgloss.AdaptiveColor{Light: "#1e66f5", Dark: "#89b4fa"}, // blue
		accentSoft:  lipgloss.AdaptiveColor{Light: "#179299", Dark: "#94e2d5"}, // teal
		success:     lipgloss.AdaptiveColor{Light: "#40a02b", Dark: "#a6e3a1"}, // green
		warning:     lipgloss.AdaptiveColor{Light: "#df8e1d", Dark: "#f9e2af"}, // yellow
		danger:      lipgloss.AdaptiveColor{Light: "#d20f39", Dark: "#f38ba8"}, // red
		info:        lipgloss.AdaptiveColor{Light: "#04a5e5", Dark: "#89dceb"}, // sapphire
	}
}

func build(p palette) *Theme {
	pill := func(fg lipgloss.AdaptiveColor) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(fg).Bold(true).Padding(0, 1)
	}
	return &Theme{
		Background: lipgloss.NewStyle().Foreground(p.fg),
		PaneBorder: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.border).
			Padding(0, 1),
		PaneFocused: lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(p.borderFocus).
			Padding(0, 1),
		PaneTitle: lipgloss.NewStyle().
			Foreground(p.accent).
			Bold(true),
		PaneSubtitle: lipgloss.NewStyle().Foreground(p.fgMuted),
		StatusBar: lipgloss.NewStyle().
			Foreground(p.fgDim).
			Padding(0, 1),
		HeaderBar: lipgloss.NewStyle().
			Padding(0, 1),
		HeaderTitle: lipgloss.NewStyle().
			Foreground(p.accent).
			Bold(true),
		HeaderVersion: lipgloss.NewStyle().
			Foreground(p.fgMuted).
			Italic(true),

		StatusActive:    pill(p.accentAlt),
		StatusPending:   pill(p.warning),
		StatusDone:      pill(p.success),
		StatusFailed:    pill(p.danger),
		StatusCancelled: pill(p.fgMuted),

		Body:       lipgloss.NewStyle().Foreground(p.fg),
		Dim:        lipgloss.NewStyle().Foreground(p.fgMuted),
		Accent:     lipgloss.NewStyle().Foreground(p.accent),
		AccentSoft: lipgloss.NewStyle().Foreground(p.accentSoft),
		Success:    lipgloss.NewStyle().Foreground(p.success),
		Warning:    lipgloss.NewStyle().Foreground(p.warning),
		Error:      lipgloss.NewStyle().Foreground(p.danger),

		SelectedRow: lipgloss.NewStyle().
			Foreground(p.accent).
			Bold(true),
		UnselectedRow: lipgloss.NewStyle().Foreground(p.fg),

		StreamLine:    lipgloss.NewStyle().Foreground(p.fg),
		StreamCaret:   lipgloss.NewStyle().Foreground(p.accentSoft).Bold(true),
		StreamElapsed: lipgloss.NewStyle().Foreground(p.fgMuted),

		HelpKey:  lipgloss.NewStyle().Foreground(p.accent).Bold(true),
		HelpDesc: lipgloss.NewStyle().Foreground(p.fgDim),
		HelpSep:  lipgloss.NewStyle().Foreground(p.fgMuted),
	}
}

// StatusPill returns the pre-styled pill for a BIAM-style status
// label (pending / active / done / failed / cancelled / expired).
// Unknown statuses fall through to Dim.
func (t *Theme) StatusPill(status string) lipgloss.Style {
	switch status {
	case "active", "running":
		return t.StatusActive
	case "pending", "queued":
		return t.StatusPending
	case "done", "success":
		return t.StatusDone
	case "failed", "error":
		return t.StatusFailed
	case "cancelled", "expired":
		return t.StatusCancelled
	}
	return t.Dim
}
