package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/weslien/maestro/internal/tracker"
)

// GSDState is the response from `stclaude get-state --json`
type GSDState struct {
	OK   bool `json:"ok"`
	Data struct {
		Project struct {
			Name      string `json:"name"`
			CoreValue string `json:"coreValue"`
		} `json:"project"`
		Phases []GSDPhase `json:"phases"`
	} `json:"data"`
}

type GSDPhase struct {
	ID     string `json:"id"`
	Number string `json:"number"`
	Name   string `json:"name"`
	Status string `json:"status"` // "complete", "pending", "in_progress"
	Goal   string `json:"goal"`
}

// PhaseToMaestro maps GSD phase status to maestro Phase field values
func PhaseToMaestro(gsdStatus string) string {
	switch gsdStatus {
	case "complete":
		return "Done"
	case "in_progress":
		return "In Progress"
	default:
		return "Backlog"
	}
}

// ReadGSDState reads project state via stclaude CLI
func ReadGSDState(ctx context.Context, repoDir string) (*GSDState, error) {
	cmd := exec.CommandContext(ctx, "stclaude", "get-state", "--json")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("stclaude get-state failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("stclaude not found — install stgsd or ensure stclaude is in PATH: %w", err)
	}

	var state GSDState
	if err := json.Unmarshal(out, &state); err != nil {
		return nil, fmt.Errorf("failed to parse stclaude output: %w", err)
	}
	if !state.OK {
		return nil, fmt.Errorf("stclaude returned error state")
	}
	return &state, nil
}

// SeedResult tracks what was created
type SeedResult struct {
	Created []string
	Skipped []string
	Errors  []string
}

// Seed creates GitHub issues for each GSD phase and adds them to the project
func Seed(ctx context.Context, trk *tracker.GitHubProjectTracker, state *GSDState, repo string) (*SeedResult, error) {
	result := &SeedResult{}
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format: %s", repo)
	}
	owner, repoName := parts[0], parts[1]

	for _, phase := range state.Data.Phases {
		title := fmt.Sprintf("[Phase %s] %s", phase.Number, phase.Name)

		// Check if issue already exists
		exists, err := issueExists(ctx, owner, repoName, title)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Phase %s: check failed: %v", phase.Number, err))
			continue
		}
		if exists {
			result.Skipped = append(result.Skipped, fmt.Sprintf("Phase %s: %s (already exists)", phase.Number, phase.Name))
			continue
		}

		// Create the issue
		body := fmt.Sprintf("## Phase %s: %s\n\n**Goal:** %s\n\n**GSD Status:** %s\n\n---\n*Seeded by maestro from GSD project data*",
			phase.Number, phase.Name, phase.Goal, phase.Status)

		issueNum, err := createIssue(ctx, owner, repoName, title, body)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Phase %s: create failed: %v", phase.Number, err))
			continue
		}

		// Add issue to project
		itemID, err := addIssueToProject(ctx, trk, owner, repoName, issueNum)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Phase %s: add to project failed: %v", phase.Number, err))
			continue
		}

		// Set Phase field
		maestroPhase := PhaseToMaestro(phase.Status)
		issue := tracker.Issue{
			Number:        issueNum,
			ProjectItemID: itemID,
		}
		if err := trk.UpdateStatus(ctx, issue, maestroPhase); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Phase %s: set phase failed: %v", phase.Number, err))
			continue
		}

		result.Created = append(result.Created, fmt.Sprintf("Phase %s: %s → %s (#%d)", phase.Number, phase.Name, maestroPhase, issueNum))
	}

	return result, nil
}

func issueExists(ctx context.Context, owner, repo, title string) (bool, error) {
	// Search for existing issue with matching title
	cmd := exec.CommandContext(ctx, "gh", "issue", "list",
		"--repo", owner+"/"+repo,
		"--search", title,
		"--json", "title",
		"--limit", "5")
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	var issues []struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &issues); err != nil {
		return false, err
	}
	for _, i := range issues {
		if i.Title == title {
			return true, nil
		}
	}
	return false, nil
}

func createIssue(ctx context.Context, owner, repo, title, body string) (int, error) {
	cmd := exec.CommandContext(ctx, "gh", "issue", "create",
		"--repo", owner+"/"+repo,
		"--title", title,
		"--body", body,
		"--json", "number")
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return 0, fmt.Errorf("%s", string(exitErr.Stderr))
		}
		return 0, err
	}
	var result struct {
		Number int `json:"number"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return 0, err
	}
	return result.Number, nil
}

func addIssueToProject(ctx context.Context, trk *tracker.GitHubProjectTracker, owner, repo string, issueNum int) (string, error) {
	// Get issue node ID
	cmd := exec.CommandContext(ctx, "gh", "issue", "view",
		fmt.Sprintf("%d", issueNum),
		"--repo", owner+"/"+repo,
		"--json", "id")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	var issueResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(out, &issueResp); err != nil {
		return "", err
	}

	// Add to project using gh project item-add
	addCmd := exec.CommandContext(ctx, "gh", "project", "item-add",
		fmt.Sprintf("%d", trk.ProjectNumber()),
		"--owner", owner,
		"--url", fmt.Sprintf("https://github.com/%s/%s/issues/%d", owner, repo, issueNum),
		"--format", "json")
	addOut, err := addCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%s", string(exitErr.Stderr))
		}
		return "", err
	}

	var addResp struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(addOut, &addResp); err != nil {
		return "", err
	}
	return addResp.ID, nil
}
