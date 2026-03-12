package views

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/weslien/maestro/internal/orchestrator"
)

// IssuesView shows the active issue table and log pane
type IssuesView struct {
	orch        *orchestrator.Orchestrator
	issues      []*orchestrator.IssueState
	selectedIdx int
	logPane     *logPane
	width       int
	height      int
}

// NewIssuesView creates the issues view
func NewIssuesView(orch *orchestrator.Orchestrator) *IssuesView {
	return &IssuesView{
		orch:    orch,
		logPane: newLogPane(),
		width:   120,
		height:  40,
	}
}

func (v *IssuesView) SetSize(w, h int) {
	v.width = w
	v.height = h
}

func (v *IssuesView) Update(msg tea.Msg) (View, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if v.selectedIdx > 0 {
				v.selectedIdx--
			}
		case "down", "j":
			if v.selectedIdx < len(v.issues)-1 {
				v.selectedIdx++
			}
		case "enter":
			if v.selectedIdx < len(v.issues) {
				state := v.issues[v.selectedIdx]
				cmd := exec.Command("tmux", "attach-session", "-t", state.TmuxName)
				return v, tea.ExecProcess(cmd, func(err error) tea.Msg {
					return RefreshMsg{}
				})
			}
		case "s":
			if v.selectedIdx < len(v.issues) {
				state := v.issues[v.selectedIdx]
				_ = v.orch.StopIssue(state.Issue.Number)
			}
		}
	}

	return v, nil
}

func (v *IssuesView) View() string {
	table := renderTable(v.issues, v.width, v.selectedIdx)

	sep := lipgloss.NewStyle().
		Foreground(lipgloss.Color("236")).
		Render(fmt.Sprintf("%*s", v.width, ""))

	logHeight := v.height - 15
	if logHeight < 5 {
		logHeight = 5
	}
	logs := v.logPane.Render(logHeight)

	return fmt.Sprintf("%s\n%s\n%s", table, sep, logs)
}

func (v *IssuesView) ShortHelp() string {
	return "enter: attach tmux | s: stop | j/k: navigate"
}

// HandleEvent processes an orchestrator event
func (v *IssuesView) HandleEvent(event orchestrator.Event) {
	v.logPane.Add(event)
}

// RefreshIssues pulls current state from orchestrator
func (v *IssuesView) RefreshIssues() {
	v.issues = v.orch.ActiveIssues()
	sort.Slice(v.issues, func(i, j int) bool {
		return v.issues[i].Issue.Number < v.issues[j].Issue.Number
	})
	if v.selectedIdx >= len(v.issues) {
		v.selectedIdx = max(0, len(v.issues)-1)
	}
}

// ActiveCount returns the number of active issues
func (v *IssuesView) ActiveCount() int {
	return len(v.issues)
}

// TotalCost returns the total cost across all active issues
func (v *IssuesView) TotalCost() float64 {
	var total float64
	for _, s := range v.issues {
		total += s.GetCost()
	}
	return total
}

// RefreshMsg signals that state should be refreshed
type RefreshMsg struct{}

// --- Log Pane (moved from tui/logpane.go) ---

const maxLogLines = 200

type logPane struct {
	lines  []string
	offset int
}

func newLogPane() *logPane {
	return &logPane{
		lines: make([]string, 0, maxLogLines),
	}
}

func (lp *logPane) Add(event orchestrator.Event) {
	timestamp := logTimestampStyle.Render(event.Time.Format("15:04:05"))

	var line string
	if event.IssueNumber > 0 {
		prefix := fmt.Sprintf("#%d", event.IssueNumber)
		if event.Phase != "" {
			prefix += fmt.Sprintf("[%s]", event.Phase)
		}

		style := logStyle
		if event.Type == "error" {
			style = logErrorStyle
		}

		line = fmt.Sprintf("%s %s %s", timestamp, prefix, style.Render(event.Message))
	} else {
		line = fmt.Sprintf("%s %s", timestamp, logStyle.Render(event.Message))
	}

	lp.lines = append(lp.lines, line)
	if len(lp.lines) > maxLogLines {
		lp.lines = lp.lines[1:]
	}

	lp.offset = max(0, len(lp.lines)-1)
}

func (lp *logPane) Render(height int) string {
	if len(lp.lines) == 0 {
		return logStyle.Render("  Waiting for events...")
	}

	start := max(0, len(lp.lines)-height)
	end := len(lp.lines)

	visible := lp.lines[start:end]
	return strings.Join(visible, "\n")
}

// --- Table rendering (moved from tui/table.go) ---

type issueRow struct {
	Number   int
	Title    string
	Phase    string
	Duration time.Duration
	Cost     float64
	Tmux     string
}

func renderTable(issues []*orchestrator.IssueState, width int, selectedIdx int) string {
	if len(issues) == 0 {
		return "\n  No active issues\n"
	}

	rows := make([]issueRow, len(issues))
	for i, s := range issues {
		rows[i] = issueRow{
			Number:   s.Issue.Number,
			Title:    truncate(s.Issue.Title, 40),
			Phase:    s.Machine.CurrentPhase().String(),
			Duration: time.Since(s.StartTime).Round(time.Second),
			Cost:     s.GetCost(),
			Tmux:     s.TmuxName,
		}
	}

	colNum := 8
	colTitle := 42
	colPhase := 14
	colDuration := 12
	colCost := 10
	colTmux := max(20, width-colNum-colTitle-colPhase-colDuration-colCost-10)

	var b strings.Builder

	header := fmt.Sprintf(" %-*s %-*s %-*s %-*s %-*s %-*s",
		colNum, "Issue",
		colTitle, "Title",
		colPhase, "Phase",
		colDuration, "Duration",
		colCost, "Cost",
		colTmux, "Tmux Session",
	)
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")

	for i, row := range rows {
		phase := phaseStyle(row.Phase).Render(fmt.Sprintf("%-*s", colPhase, row.Phase))

		line := fmt.Sprintf(" #%-*d %-*s %s %-*s $%-*.4f %-*s",
			colNum-1, row.Number,
			colTitle, row.Title,
			phase,
			colDuration, row.Duration.String(),
			colCost-1, row.Cost,
			colTmux, row.Tmux,
		)

		if i == selectedIdx {
			b.WriteString(selectedRowStyle.Render(line))
		} else {
			b.WriteString(normalRowStyle.Render(line))
		}
		b.WriteString("\n")
	}

	return b.String()
}

