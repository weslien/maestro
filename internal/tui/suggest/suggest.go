package suggest

import (
	"fmt"
	"time"

	"strings"

	"github.com/weslien/maestro/internal/gsdstate"
	"github.com/weslien/maestro/internal/orchestrator"
	"github.com/weslien/maestro/internal/tracker"
)

// Suggestion is a recommended action for the user
type Suggestion struct {
	Icon        string // ">", "!", "?"
	Text        string
	IssueNumber int    // 0 for project-level suggestions
	ActionLabel string // empty if informational only
}

// Analyze inspects GSD state, orchestrator state, and board issues to produce suggestions
func Analyze(gsd *gsdstate.State, activeIssues []*orchestrator.IssueState, boardIssues []tracker.Issue) []Suggestion {
	var suggestions []Suggestion

	activeMap := make(map[int]bool)
	for _, is := range activeIssues {
		activeMap[is.Issue.Number] = true
	}

	// Issues in Human Review
	for _, issue := range boardIssues {
		if issue.Status == "Human Review" {
			suggestions = append(suggestions, Suggestion{
				Icon:        "!",
				Text:        fmt.Sprintf("#%d needs human review: %s", issue.Number, truncate(issue.Title, 50)),
				IssueNumber: issue.Number,
			})
		}
	}

	// Stuck issues (>30min in same phase)
	for _, is := range activeIssues {
		if time.Since(is.StartTime) > 30*time.Minute {
			suggestions = append(suggestions, Suggestion{
				Icon:        "?",
				Text:        fmt.Sprintf("#%d stuck in %s for %s", is.Issue.Number, is.Machine.CurrentPhase(), time.Since(is.StartTime).Round(time.Minute)),
				IssueNumber: is.Issue.Number,
			})
		}
	}

	// Actionable issues not currently running
	var startable []tracker.Issue
	for _, issue := range boardIssues {
		if activeMap[issue.Number] {
			continue
		}
		switch issue.Status {
		case "Todo", "Research", "Planning", "In Progress", "Validation":
			startable = append(startable, issue)
		}
	}

	if len(startable) > 0 {
		next := startable[0]
		suggestions = append(suggestions, Suggestion{
			Icon:        ">",
			Text:        fmt.Sprintf("%d issue(s) ready to process — next: #%d %s [%s]", len(startable), next.Number, truncate(next.Title, 40), next.Status),
			IssueNumber: next.Number,
			ActionLabel: fmt.Sprintf("Start #%d", next.Number),
		})
	}

	// Budget warnings
	for _, is := range activeIssues {
		cost := is.GetCost()
		if cost > 5.0 {
			suggestions = append(suggestions, Suggestion{
				Icon:        "!",
				Text:        fmt.Sprintf("#%d has used $%.2f — review costs", is.Issue.Number, cost),
				IssueNumber: is.Issue.Number,
			})
		}
	}

	// GSD-level progress
	if gsd != nil && len(gsd.Data.Phases) > 0 {
		var complete, total int
		for _, p := range gsd.Data.Phases {
			total++
			if strings.EqualFold(p.Status, "complete") {
				complete++
			}
		}
		pct := (complete * 100) / total
		suggestions = append(suggestions, Suggestion{
			Icon: ">",
			Text: fmt.Sprintf("Project progress: %d/%d phases complete (%d%%)", complete, total, pct),
		})

		if complete == total {
			suggestions = append(suggestions, Suggestion{
				Icon: ">",
				Text: "All GSD phases complete!",
			})
		}
	}

	if len(suggestions) == 0 {
		suggestions = append(suggestions, Suggestion{
			Icon: ">",
			Text: "No actionable items — waiting for issues to reach actionable phases",
		})
	}

	return suggestions
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
