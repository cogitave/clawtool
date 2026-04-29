// internal/cli/onboard_widgets.go — minimal custom wizard widgets
// (Select / MultiSelect / Confirm) that replace charmbracelet/huh
// inside the onboard alt-screen TUI.
//
// Why custom: huh.Form embedding inside our parent tea.Program had
// two intractable bugs we kept rediscovering:
//
//  1. huh's Select widget renders only the cursor row when its
//     internal viewport height is unset. WindowSizeMsg.Height does
//     NOT propagate to per-field viewports — only Form.WithHeight()
//     and Select.Height() do, and we don't want clamping anyway.
//  2. Wrapping huh.View() in a height-clamped lipgloss style fights
//     huh's own internal styles.Base.Height() — the inner clamp
//     wins at minHeight=1, killing the option list.
//
// These widgets render every option every frame, no viewport, no
// height drama. They expose:
//
//   - Update(msg) — route a tea.Msg, returns updated widget + cmd
//   - View()      — render full natural-size output
//   - Done()      — true once the operator submitted
//   - Keybinds()  — short hint string for the wizard's footer
//     (e.g. "↑/↓ select  ·  enter confirm")
//
// The wizard's outer model owns navigation between widgets; the
// widgets only handle their own keys.
package cli

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// widgetStyles caches the styles each widget renders with. Built
// once at construction so we don't re-allocate lipgloss styles on
// every keystroke.
type widgetStyles struct {
	title    lipgloss.Style
	desc     lipgloss.Style
	cursor   lipgloss.Style // accent on selected row
	option   lipgloss.Style
	dim      lipgloss.Style
	check    lipgloss.Style // multi-select check glyph
	uncheck  lipgloss.Style
	yesNoOff lipgloss.Style
	yesNoOn  lipgloss.Style
}

func newWidgetStyles() widgetStyles {
	return widgetStyles{
		title:    lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")),
		desc:     lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		cursor:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")),
		option:   lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		dim:      lipgloss.NewStyle().Foreground(lipgloss.Color("241")),
		check:    lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true),
		uncheck:  lipgloss.NewStyle().Foreground(lipgloss.Color("241")),
		yesNoOff: lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Padding(0, 2),
		yesNoOn:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212")).Padding(0, 2),
	}
}

// widgetOption is one entry in a Select / MultiSelect.
type widgetOption struct {
	Label string
	Value string
}

// selectWidget is a single-choice picker. Renders every option
// every frame. ↑/↓ moves cursor; enter submits.
type selectWidget struct {
	title   string
	desc    string
	options []widgetOption
	cursor  int
	done    bool
	style   widgetStyles
}

func newSelectWidget(title, desc string, opts []widgetOption, initialValue string) *selectWidget {
	cursor := 0
	for i, o := range opts {
		if o.Value == initialValue {
			cursor = i
			break
		}
	}
	return &selectWidget{
		title:   title,
		desc:    desc,
		options: opts,
		cursor:  cursor,
		style:   newWidgetStyles(),
	}
}

func (s *selectWidget) Update(msg tea.Msg) (*selectWidget, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up", "k":
			if s.cursor > 0 {
				s.cursor--
			}
		case "down", "j":
			if s.cursor < len(s.options)-1 {
				s.cursor++
			}
		case "home", "g":
			s.cursor = 0
		case "end", "G":
			s.cursor = len(s.options) - 1
		case "enter":
			s.done = true
		}
	}
	return s, nil
}

func (s *selectWidget) View() string {
	var b strings.Builder
	b.WriteString(s.style.title.Render(s.title))
	b.WriteString("\n")
	if s.desc != "" {
		b.WriteString(s.style.desc.Render(s.desc))
		b.WriteString("\n\n")
	} else {
		b.WriteString("\n")
	}
	for i, o := range s.options {
		if i == s.cursor {
			b.WriteString(s.style.cursor.Render("▸ " + o.Label))
		} else {
			b.WriteString(s.style.option.Render("  " + o.Label))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (s *selectWidget) Done() bool    { return s.done }
func (s *selectWidget) Value() string { return s.options[s.cursor].Value }
func (s *selectWidget) Keybinds() string {
	return "↑/↓ select  ·  enter confirm"
}

// multiSelectWidget is a checklist picker. Space toggles the
// cursor row; enter submits.
type multiSelectWidget struct {
	title    string
	desc     string
	options  []widgetOption
	selected map[int]bool
	cursor   int
	done     bool
	style    widgetStyles
}

func newMultiSelectWidget(title, desc string, opts []widgetOption, initial []string) *multiSelectWidget {
	sel := map[int]bool{}
	for i, o := range opts {
		for _, v := range initial {
			if o.Value == v {
				sel[i] = true
				break
			}
		}
	}
	return &multiSelectWidget{
		title:    title,
		desc:     desc,
		options:  opts,
		selected: sel,
		style:    newWidgetStyles(),
	}
}

func (m *multiSelectWidget) Update(msg tea.Msg) (*multiSelectWidget, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.options)-1 {
				m.cursor++
			}
		case " ", "x":
			m.selected[m.cursor] = !m.selected[m.cursor]
		case "a":
			// Select all when none selected, else clear all —
			// keyboard parity with most multi-select TUIs.
			anySelected := false
			for _, v := range m.selected {
				if v {
					anySelected = true
					break
				}
			}
			for i := range m.options {
				m.selected[i] = !anySelected
			}
		case "enter":
			m.done = true
		}
	}
	return m, nil
}

func (m *multiSelectWidget) View() string {
	var b strings.Builder
	b.WriteString(m.style.title.Render(m.title))
	b.WriteString("\n")
	if m.desc != "" {
		b.WriteString(m.style.desc.Render(m.desc))
		b.WriteString("\n\n")
	} else {
		b.WriteString("\n")
	}
	for i, o := range m.options {
		var box string
		if m.selected[i] {
			box = m.style.check.Render("[✓] ")
		} else {
			box = m.style.uncheck.Render("[ ] ")
		}
		var label string
		if i == m.cursor {
			label = m.style.cursor.Render("▸ " + o.Label)
		} else {
			label = m.style.option.Render("  " + o.Label)
		}
		b.WriteString(box + label + "\n")
	}
	return b.String()
}

func (m *multiSelectWidget) Done() bool { return m.done }

// Values returns the selected option values in the order the
// options were declared (stable across runs).
func (m *multiSelectWidget) Values() []string {
	var out []string
	for i, o := range m.options {
		if m.selected[i] {
			out = append(out, o.Value)
		}
	}
	return out
}

func (m *multiSelectWidget) Keybinds() string {
	return "↑/↓ navigate  ·  space toggle  ·  a all/none  ·  enter confirm"
}

// confirmWidget is a yes/no picker. ← / → or h / l toggles cursor,
// y / n picks immediately, enter submits the cursor's value.
type confirmWidget struct {
	title  string
	desc   string
	yesLbl string
	noLbl  string
	yes    bool
	done   bool
	answer bool
	style  widgetStyles
}

func newConfirmWidget(title, desc, yesLbl, noLbl string, initial bool) *confirmWidget {
	if yesLbl == "" {
		yesLbl = "Yes"
	}
	if noLbl == "" {
		noLbl = "No"
	}
	return &confirmWidget{
		title:  title,
		desc:   desc,
		yesLbl: yesLbl,
		noLbl:  noLbl,
		yes:    initial,
		style:  newWidgetStyles(),
	}
}

func (c *confirmWidget) Update(msg tea.Msg) (*confirmWidget, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "left", "h", "right", "l", "tab":
			c.yes = !c.yes
		case "y", "Y":
			c.yes = true
			c.done = true
			c.answer = true
		case "n", "N":
			c.yes = false
			c.done = true
			c.answer = false
		case "enter":
			c.done = true
			c.answer = c.yes
		}
	}
	return c, nil
}

func (c *confirmWidget) View() string {
	var b strings.Builder
	b.WriteString(c.style.title.Render(c.title))
	b.WriteString("\n")
	if c.desc != "" {
		b.WriteString(c.style.desc.Render(c.desc))
		b.WriteString("\n\n")
	} else {
		b.WriteString("\n")
	}
	yes := c.style.yesNoOff.Render(c.yesLbl)
	no := c.style.yesNoOff.Render(c.noLbl)
	if c.yes {
		yes = c.style.yesNoOn.Render("▸ " + c.yesLbl)
	} else {
		no = c.style.yesNoOn.Render("▸ " + c.noLbl)
	}
	b.WriteString(fmt.Sprintf("    %s    %s", yes, no))
	return b.String()
}

func (c *confirmWidget) Done() bool  { return c.done }
func (c *confirmWidget) Value() bool { return c.answer }
func (c *confirmWidget) Keybinds() string {
	return "←/→ toggle  ·  y / n quick  ·  enter confirm"
}

// stepWidget unifies the three widget types behind a single
// interface so the wizard's outer tea.Model can route messages and
// render a single active step without branching on widget kind.
type stepWidget interface {
	Update(tea.Msg) (stepWidget, tea.Cmd)
	View() string
	Done() bool
	Keybinds() string
}

// adapter wraps the concrete widget pointer to satisfy stepWidget.
// We can't put Update returning the concrete pointer on the
// interface because Go doesn't have covariant return types, so the
// adapters do the cast.
type selectAdapter struct{ w *selectWidget }
type multiAdapter struct{ w *multiSelectWidget }
type confirmAdapter struct{ w *confirmWidget }

func (a *selectAdapter) Update(msg tea.Msg) (stepWidget, tea.Cmd) {
	w, cmd := a.w.Update(msg)
	a.w = w
	return a, cmd
}
func (a *selectAdapter) View() string     { return a.w.View() }
func (a *selectAdapter) Done() bool       { return a.w.Done() }
func (a *selectAdapter) Keybinds() string { return a.w.Keybinds() }

func (a *multiAdapter) Update(msg tea.Msg) (stepWidget, tea.Cmd) {
	w, cmd := a.w.Update(msg)
	a.w = w
	return a, cmd
}
func (a *multiAdapter) View() string     { return a.w.View() }
func (a *multiAdapter) Done() bool       { return a.w.Done() }
func (a *multiAdapter) Keybinds() string { return a.w.Keybinds() }

func (a *confirmAdapter) Update(msg tea.Msg) (stepWidget, tea.Cmd) {
	w, cmd := a.w.Update(msg)
	a.w = w
	return a, cmd
}
func (a *confirmAdapter) View() string     { return a.w.View() }
func (a *confirmAdapter) Done() bool       { return a.w.Done() }
func (a *confirmAdapter) Keybinds() string { return a.w.Keybinds() }
