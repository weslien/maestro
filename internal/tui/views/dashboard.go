package views

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/weslien/maestro/internal/gsdstate"
	"github.com/weslien/maestro/internal/orchestrator"
	"github.com/weslien/maestro/internal/tracker"
	"github.com/weslien/maestro/internal/tui/suggest"
)

// DashboardView shows the pipeline, summary, and suggestions
type DashboardView struct {
	orch        *orchestrator.Orchestrator
	gsdState    *gsdstate.State
	suggestions []suggest.Suggestion
	selectedIdx int
	statusMsg   string
	width       int
	height      int
	err         error
}

// NewDashboardView creates the dashboard view
func NewDashboardView(orch *orchestrator.Orchestrator) *DashboardView {
	return &DashboardView{
		orch:  orch,
		width: 120,
	}
}

func (v *DashboardView) SetSize(w, h int) {
	v.width = w
	v.height = h
}

func (v *DashboardView) Update(msg tea.Msg) (View, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if v.selectedIdx > 0 {
				v.selectedIdx--
			}
		case "down", "j":
			if v.selectedIdx < len(v.suggestions)-1 {
				v.selectedIdx++
			}
		case "enter":
			if v.selectedIdx < len(v.suggestions) {
				s := v.suggestions[v.selectedIdx]
				if s.IssueNumber > 0 && s.ActionLabel != "" {
					return v, v.startIssue(s.IssueNumber)
				}
			}
		}
	case actionResultMsg:
		v.statusMsg = msg.message
		return v, nil
	}

	return v, nil
}

func (v *DashboardView) startIssue(issueNumber int) tea.Cmd {
	orch := v.orch
	return func() tea.Msg {
		err := orch.StartIssueByNumber(context.Background(), issueNumber)
		if err != nil {
			return actionResultMsg{message: fmt.Sprintf("Failed to start #%d: %v", issueNumber, err)}
		}
		return actionResultMsg{message: fmt.Sprintf("Started processing #%d", issueNumber)}
	}
}

func (v *DashboardView) View() string {
	var b strings.Builder

	// Pipeline
	b.WriteString(v.renderPipeline())
	b.WriteString("\n\n")

	// Agent summary
	b.WriteString(v.renderAgentSummary())
	b.WriteString("\n\n")

	// Suggestions
	b.WriteString(v.renderSuggestions())

	if v.statusMsg != "" {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(fmt.Sprintf("  %s", v.statusMsg)))
		b.WriteString("\n")
	}

	return b.String()
}

func (v *DashboardView) ShortHelp() string {
	return "j/k: navigate suggestions | enter: execute"
}

// Refresh updates the dashboard state
func (v *DashboardView) Refresh(boardIssues []tracker.Issue) {
	issues := v.orch.ActiveIssues()
	v.suggestions = suggest.Analyze(v.gsdState, issues, boardIssues)
	if v.selectedIdx >= len(v.suggestions) {
		v.selectedIdx = max(0, len(v.suggestions)-1)
	}
}

// RefreshGSD updates the GSD state
func (v *DashboardView) RefreshGSD(state *gsdstate.State, err error) {
	v.gsdState = state
	v.err = err
}

func (v *DashboardView) renderPipeline() string {
	title := sectionTitle.Render("Pipeline")

	if v.gsdState == nil {
		if v.err != nil {
			return fmt.Sprintf("  %s\n  %s", title, dimStyle.Render("GSD state unavailable: "+v.err.Error()))
		}
		return fmt.Sprintf("  %s\n  %s", title, dimStyle.Render("Loading..."))
	}

	counts := map[string]int{}
	for _, p := range v.gsdState.Data.Phases {
		counts[strings.ToLower(p.Status)]++
	}

	type seg struct {
		label string
		count int
		color string
	}
	order := []seg{
		{"Done", counts["complete"], "40"},
		{"Active", counts["in_progress"], "214"},
		{"Pending", counts["pending"], "246"},
	}

	var segments []string
	for _, s := range order {
		if s.count == 0 {
			continue
		}
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(s.color)).Bold(true)
		segments = append(segments, style.Render(fmt.Sprintf("%s(%d)", s.label, s.count)))
	}

	pipeline := strings.Join(segments, dimStyle.Render(" → "))

	total := len(v.gsdState.Data.Phases)
	complete := counts["complete"]
	var pctStr string
	if total > 0 {
		pct := (complete * 100) / total
		pctStr = dimStyle.Render(fmt.Sprintf("  %d%%", pct))
	}

	return fmt.Sprintf("  %s\n  %s%s", title, pipeline, pctStr)
}

func (v *DashboardView) renderAgentSummary() string {
	title := sectionTitle.Render("Agents")

	issues := v.orch.ActiveIssues()
	activeCount := len(issues)

	var totalCost float64
	for _, s := range issues {
		totalCost += s.GetCost()
	}

	info := fmt.Sprintf("Active: %d | Total Cost: $%.4f", activeCount, totalCost)
	return fmt.Sprintf("  %s\n  %s", title, info)
}

func (v *DashboardView) renderSuggestions() string {
	title := sectionTitle.Render("Suggestions")

	if len(v.suggestions) == 0 {
		return fmt.Sprintf("  %s\n  %s", title, dimStyle.Render("No suggestions"))
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("  %s\n", title))

	for i, s := range v.suggestions {
		prefix := "  "
		if i == v.selectedIdx {
			prefix = selectedMarker.Render("> ")
		}

		text := s.Text
		if s.ActionLabel != "" {
			text += actionHint.Render(fmt.Sprintf(" [%s]", s.ActionLabel))
		}

		line := fmt.Sprintf("%s%s %s", prefix, s.Icon, text)
		if i == v.selectedIdx {
			b.WriteString(selectedRowStyle.Render(line))
		} else {
			b.WriteString(normalRowStyle.Render(line))
		}
		b.WriteString("\n")
	}

	return b.String()
}

var (
	sectionTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	actionHint   = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
)
