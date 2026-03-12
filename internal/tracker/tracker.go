package tracker

import "context"

type Tracker interface {
	// Poll returns issues that are in actionable statuses (Todo, Research, Planning, In Progress, Validation)
	Poll(ctx context.Context) ([]Issue, error)

	// UpdateStatus changes an issue's status on the project board
	UpdateStatus(ctx context.Context, issue Issue, newStatus string) error

	// GetIssue fetches a single issue's current state
	GetIssue(ctx context.Context, number int) (*Issue, error)
}
