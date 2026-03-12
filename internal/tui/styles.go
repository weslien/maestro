package tui

import "github.com/charmbracelet/lipgloss"

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("170")).
			PaddingLeft(1)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39")).
			Background(lipgloss.Color("236")).
			PaddingLeft(1).
			PaddingRight(1)

	selectedRowStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("237")).
				Foreground(lipgloss.Color("255"))

	normalRowStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("252"))

	phaseStyles = map[string]lipgloss.Style{
		"Todo":         lipgloss.NewStyle().Foreground(lipgloss.Color("246")),
		"Research":     lipgloss.NewStyle().Foreground(lipgloss.Color("213")),
		"Planning":     lipgloss.NewStyle().Foreground(lipgloss.Color("111")),
		"In Progress":  lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		"Validation":   lipgloss.NewStyle().Foreground(lipgloss.Color("49")),
		"Human Review": lipgloss.NewStyle().Foreground(lipgloss.Color("196")),
		"Done":         lipgloss.NewStyle().Foreground(lipgloss.Color("40")),
		"Cancelled":    lipgloss.NewStyle().Foreground(lipgloss.Color("241")),
	}

	logStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245"))

	logTimestampStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("241"))

	logErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	statusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Foreground(lipgloss.Color("252")).
			PaddingLeft(1).
			PaddingRight(1)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))
)

func phaseStyle(phase string) lipgloss.Style {
	if s, ok := phaseStyles[phase]; ok {
		return s
	}
	return normalRowStyle
}
