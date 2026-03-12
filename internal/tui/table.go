package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/weslien/maestro/internal/orchestrator"
)

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

	// Column widths
	colNum := 8
	colTitle := 42
	colPhase := 14
	colDuration := 12
	colCost := 10
	colTmux := max(20, width-colNum-colTitle-colPhase-colDuration-colCost-10)

	var b strings.Builder

	// Header
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

	// Rows
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

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
