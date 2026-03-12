package views

import tea "github.com/charmbracelet/bubbletea"

// View is the interface that all TUI views implement
type View interface {
	Update(msg tea.Msg) (View, tea.Cmd)
	View() string
	ShortHelp() string
}
