package components

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Tab represents a navigable tab
type Tab struct {
	Key  string
	Name string
}

// Tabs defines the available tabs
var Tabs = []Tab{
	{Key: "1", Name: "Dashboard"},
	{Key: "2", Name: "Issues"},
	{Key: "3", Name: "Actions"},
}

// RenderHeader renders the application header with tab bar
func RenderHeader(activeTab int, totalCost float64, width int) string {
	title := titleStyle.Render("Maestro")

	var tabs []string
	for i, tab := range Tabs {
		label := fmt.Sprintf(" %s %s ", tab.Key, tab.Name)
		if i == activeTab {
			tabs = append(tabs, activeTabStyle.Render(label))
		} else {
			tabs = append(tabs, inactiveTabStyle.Render(label))
		}
	}

	tabBar := lipgloss.JoinHorizontal(lipgloss.Top, tabs...)

	clock := dimHeaderStyle.Render(time.Now().Format("15:04:05"))
	cost := dimHeaderStyle.Render(fmt.Sprintf("$%.4f", totalCost))

	right := fmt.Sprintf("%s  %s", cost, clock)

	// Calculate spacing
	leftLen := lipgloss.Width(title) + 2 + lipgloss.Width(tabBar)
	rightLen := lipgloss.Width(right)
	gap := width - leftLen - rightLen - 2
	if gap < 1 {
		gap = 1
	}

	return fmt.Sprintf(" %s  %s%*s%s", title, tabBar, gap, "", right)
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("170"))

	activeTabStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("255")).
			Background(lipgloss.Color("63"))

	inactiveTabStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("245")).
				Background(lipgloss.Color("236"))

	dimHeaderStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))
)
