package views

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/weslien/maestro/internal/orchestrator"
	"github.com/weslien/maestro/internal/tracker"
)

// ActionKind identifies what type of action this is
type ActionKind int

const (
	ActionStartIssue   ActionKind = iota // Start processing an actionable issue
	ActionStopIssue                      // Stop a running issue
	ActionMoveTodo                       // Move a Backlog issue to Todo
	ActionRefreshBoard                   // Re-poll the tracker
)

// ActionItem is a concrete action the user can execute
type ActionItem struct {
	Kind        ActionKind
	Label       string
	Description string
	Issue       *tracker.Issue // nil for project-level actions
}

// ActionsView lists available actions derived from current project state
type ActionsView struct {
	orch        *orchestrator.Orchestrator
	items       []ActionItem
	selectedIdx int
	statusMsg   string
	width       int
	height      int
}

// NewActionsView creates the actions view
func NewActionsView(orch *orchestrator.Orchestrator) *ActionsView {
	return &ActionsView{
		orch:  orch,
		width: 120,
	}
}

func (v *ActionsView) SetSize(w, h int) {
	v.width = w
	v.height = h
}

func (v *ActionsView) Update(msg tea.Msg) (View, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if v.selectedIdx > 0 {
				v.selectedIdx--
			}
		case "down", "j":
			if v.selectedIdx < len(v.items)-1 {
				v.selectedIdx++
			}
		case "enter":
			if v.selectedIdx < len(v.items) {
				return v, v.executeAction(v.items[v.selectedIdx])
			}
		}
	case actionResultMsg:
		v.statusMsg = msg.message
		return v, nil
	}

	return v, nil
}

func (v *ActionsView) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39")).Render("Actions")

	var b strings.Builder
	b.WriteString(fmt.Sprintf("  %s\n\n", title))

	if len(v.items) == 0 {
		b.WriteString(dimStyle.Render("  No actions available. Press r to refresh.\n"))
	}

	for i, item := range v.items {
		prefix := "  "
		if i == v.selectedIdx {
			prefix = selectedMarker.Render("> ")
		}

		label := lipgloss.NewStyle().Bold(true).Render(item.Label)
		desc := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render(item.Description)

		line := fmt.Sprintf("%s%s  %s", prefix, label, desc)

		if i == v.selectedIdx {
			b.WriteString(selectedRowStyle.Render(line))
		} else {
			b.WriteString(normalRowStyle.Render(line))
		}
		b.WriteString("\n")
	}

	if v.statusMsg != "" {
		b.WriteString("\n")
		b.WriteString(statusStyle.Render(fmt.Sprintf("  %s", v.statusMsg)))
		b.WriteString("\n")
	}

	return b.String()
}

func (v *ActionsView) ShortHelp() string {
	return "j/k: navigate | enter: execute action"
}

// Refresh rebuilds the action list from current orchestrator and tracker state
func (v *ActionsView) Refresh(boardIssues []tracker.Issue) {
	v.items = v.buildActions(boardIssues)
	if v.selectedIdx >= len(v.items) {
		v.selectedIdx = max(0, len(v.items)-1)
	}
}

func (v *ActionsView) buildActions(boardIssues []tracker.Issue) []ActionItem {
	var items []ActionItem

	activeMap := make(map[int]bool)
	for _, is := range v.orch.ActiveIssues() {
		activeMap[is.Issue.Number] = true
	}

	// Issues in actionable phases that aren't currently running → offer to start
	for i := range boardIssues {
		issue := &boardIssues[i]
		if activeMap[issue.Number] {
			continue
		}
		switch issue.Status {
		case "Todo", "Research", "Planning", "In Progress", "Validation":
			items = append(items, ActionItem{
				Kind:        ActionStartIssue,
				Label:       fmt.Sprintf("Start #%d", issue.Number),
				Description: fmt.Sprintf("%s [%s]", truncate(issue.Title, 50), issue.Status),
				Issue:       issue,
			})
		}
	}

	// Issues in Backlog → offer to move to Todo
	for i := range boardIssues {
		issue := &boardIssues[i]
		if issue.Status == "Backlog" {
			items = append(items, ActionItem{
				Kind:        ActionMoveTodo,
				Label:       fmt.Sprintf("Queue #%d", issue.Number),
				Description: fmt.Sprintf("%s [Backlog → Todo]", truncate(issue.Title, 50)),
				Issue:       issue,
			})
		}
	}

	// Active issues → offer to stop
	for _, is := range v.orch.ActiveIssues() {
		items = append(items, ActionItem{
			Kind:        ActionStopIssue,
			Label:       fmt.Sprintf("Stop #%d", is.Issue.Number),
			Description: fmt.Sprintf("%s [%s]", truncate(is.Issue.Title, 50), is.Machine.CurrentPhase().String()),
			Issue: &tracker.Issue{
				Number: is.Issue.Number,
				Title:  is.Issue.Title,
			},
		})
	}

	// Always offer refresh
	items = append(items, ActionItem{
		Kind:        ActionRefreshBoard,
		Label:       "Refresh Board",
		Description: "Re-poll GitHub project for updated issue states",
	})

	return items
}

func (v *ActionsView) executeAction(item ActionItem) tea.Cmd {
	orch := v.orch
	switch item.Kind {
	case ActionStartIssue:
		issueNum := item.Issue.Number
		return func() tea.Msg {
			err := orch.StartIssueByNumber(context.Background(), issueNum)
			if err != nil {
				return actionResultMsg{message: fmt.Sprintf("Failed to start #%d: %v", issueNum, err)}
			}
			return actionResultMsg{message: fmt.Sprintf("Started processing #%d", issueNum)}
		}

	case ActionStopIssue:
		issueNum := item.Issue.Number
		return func() tea.Msg {
			err := orch.StopIssue(issueNum)
			if err != nil {
				return actionResultMsg{message: fmt.Sprintf("Failed to stop #%d: %v", issueNum, err)}
			}
			return actionResultMsg{message: fmt.Sprintf("Stopped #%d", issueNum)}
		}

	case ActionMoveTodo:
		issueNum := item.Issue.Number
		return func() tea.Msg {
			err := orch.AdvanceIssuePhase(context.Background(), issueNum, "Todo")
			if err != nil {
				return actionResultMsg{message: fmt.Sprintf("Failed to queue #%d: %v", issueNum, err)}
			}
			return actionResultMsg{message: fmt.Sprintf("Moved #%d to Todo", issueNum)}
		}

	case ActionRefreshBoard:
		return func() tea.Msg {
			return RefreshMsg{}
		}
	}

	return nil
}

type actionResultMsg struct {
	message string
}

