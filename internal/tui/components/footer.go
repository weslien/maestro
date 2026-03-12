package components

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// RenderFooter renders the context-sensitive footer
func RenderFooter(viewHelp string, width int) string {
	common := "q: quit | 1/2/3: switch view | r: refresh"
	help := fmt.Sprintf(" %s | %s", common, viewHelp)

	return footerStyle.Render(help)
}

var footerStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("241"))
