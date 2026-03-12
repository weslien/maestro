package views

import "github.com/charmbracelet/lipgloss"

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// Shared styles used across views
var (
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

	selectedMarker = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	statusStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
)

func phaseStyle(phase string) lipgloss.Style {
	if s, ok := phaseStyles[phase]; ok {
		return s
	}
	return normalRowStyle
}
