package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// Styles. lipgloss honors NO_COLOR / non-TTY automatically via termenv, so these
// degrade to plain text where appropriate.
var (
	borderColor = lipgloss.Color("63") // muted indigo
	focusColor  = lipgloss.Color("205")

	paneStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Padding(0, 1)

	focusedPaneStyle = paneStyle.BorderForeground(focusColor)

	dimStyle    = lipgloss.NewStyle().Faint(true)
	statusStyle = lipgloss.NewStyle().Faint(true).Padding(0, 1)
)

// layout sizes the list and viewport panes to the current terminal, reserving
// rows for the status line and help footer. Called on resize and help toggle.
func (m *model) layout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	helpHeight := 1
	if m.help.ShowAll {
		helpHeight = 3
	}
	// rows: 1 status + helpHeight, plus pane borders (2 each).
	chrome := 1 + helpHeight
	paneHeight := m.height - chrome - 2 // -2 for the pane's top/bottom border
	if paneHeight < 3 {
		paneHeight = 3
	}

	// Split width ~42% list / rest detail; account for borders+padding (4 each).
	listOuter := m.width * 42 / 100
	if listOuter < 24 {
		listOuter = 24
	}
	detailOuter := m.width - listOuter
	innerW := func(outer int) int {
		w := outer - 4 // border(2)+padding(2)
		if w < 10 {
			w = 10
		}
		return w
	}

	m.list.SetSize(innerW(listOuter), paneHeight)
	m.vp.Width = innerW(detailOuter)
	m.vp.Height = paneHeight
}

func (m model) View() string {
	if !m.ready {
		return "loading…"
	}

	listPane, detailPane := paneStyle, paneStyle
	if m.focusDetail {
		detailPane = focusedPaneStyle
	} else {
		listPane = focusedPaneStyle
	}

	left := listPane.Render(m.list.View())
	right := detailPane.Render(m.vp.View())
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	status := m.status
	if status == "" {
		status = dimStyle.Render("↑/↓ navigate · enter filter · d done · ] move · e edit · ? help · q quit")
	}
	footer := m.help.View(m.keys)

	return lipgloss.JoinVertical(lipgloss.Left,
		body,
		statusStyle.Render(status),
		footer,
	)
}
