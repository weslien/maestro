package tui

import (
	"fmt"
	"os/exec"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/weslien/maestro/internal/orchestrator"
)

// eventMsg wraps an orchestrator event for the TUI
type eventMsg orchestrator.Event

// tickMsg triggers a periodic refresh
type tickMsg time.Time

// Model is the top-level Bubbletea model
type Model struct {
	orch        *orchestrator.Orchestrator
	issues      []*orchestrator.IssueState
	selectedIdx int
	logPane     *logPane
	width       int
	height      int
	quitting    bool
}

// New creates a new TUI model
func New(orch *orchestrator.Orchestrator) *Model {
	return &Model{
		orch:    orch,
		logPane: newLogPane(),
		width:   120,
		height:  40,
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.listenEvents(),
		m.tick(),
	)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			m.orch.Stop()
			return m, tea.Quit
		case "up", "k":
			if m.selectedIdx > 0 {
				m.selectedIdx--
			}
		case "down", "j":
			if m.selectedIdx < len(m.issues)-1 {
				m.selectedIdx++
			}
		case "enter":
			if m.selectedIdx < len(m.issues) {
				state := m.issues[m.selectedIdx]
				cmd := exec.Command("tmux", "attach-session", "-t", state.TmuxName)
				return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
					return tickMsg(time.Now())
				})
			}
		case "r":
			m.refreshIssues()
		case "s":
			if m.selectedIdx < len(m.issues) {
				state := m.issues[m.selectedIdx]
				_ = m.orch.StopIssue(state.Issue.Number)
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case eventMsg:
		event := orchestrator.Event(msg)
		m.logPane.Add(event)
		m.refreshIssues()
		return m, m.listenEvents()

	case tickMsg:
		m.refreshIssues()
		return m, m.tick()
	}

	return m, nil
}

func (m *Model) View() string {
	if m.quitting {
		return "Shutting down maestro...\n"
	}

	title := titleStyle.Render("Maestro - GSD Agent Orchestrator")

	tableHeight := min(len(m.issues)+2, m.height/2)
	_ = tableHeight
	table := renderTable(m.issues, m.width, m.selectedIdx)

	sep := lipgloss.NewStyle().
		Foreground(lipgloss.Color("236")).
		Render(fmt.Sprintf("%*s", m.width, ""))

	logHeight := m.height - 15
	if logHeight < 5 {
		logHeight = 5
	}
	logs := m.logPane.Render(logHeight)

	activeCount := len(m.issues)
	var totalCost float64
	for _, s := range m.issues {
		totalCost += s.GetCost()
	}
	statusBar := statusBarStyle.Render(fmt.Sprintf(
		" Active: %d | Total Cost: $%.4f | %s",
		activeCount, totalCost, time.Now().Format("15:04:05"),
	))

	help := helpStyle.Render(" q: quit | enter: attach tmux | s: stop | r: refresh | j/k: navigate")

	return fmt.Sprintf("%s\n\n%s\n%s\n%s\n\n%s\n%s",
		title, table, sep, logs, statusBar, help)
}

func (m *Model) refreshIssues() {
	m.issues = m.orch.ActiveIssues()
	sort.Slice(m.issues, func(i, j int) bool {
		return m.issues[i].Issue.Number < m.issues[j].Issue.Number
	})
	if m.selectedIdx >= len(m.issues) {
		m.selectedIdx = max(0, len(m.issues)-1)
	}
}

func (m *Model) listenEvents() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.orch.Events()
		if !ok {
			return nil
		}
		return eventMsg(event)
	}
}

func (m *Model) tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
