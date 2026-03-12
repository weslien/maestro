package tui

import (
	"fmt"
	"strings"

	"github.com/weslien/maestro/internal/orchestrator"
)

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

	// Auto-scroll to bottom
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

func (lp *logPane) ScrollUp() {
	if lp.offset > 0 {
		lp.offset--
	}
}

func (lp *logPane) ScrollDown() {
	if lp.offset < len(lp.lines)-1 {
		lp.offset++
	}
}
