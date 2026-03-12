package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/weslien/maestro/internal/gsdstate"
	"github.com/weslien/maestro/internal/tracker"
)

// ReadGSDState reads project state via stclaude CLI
func ReadGSDState(ctx context.Context, repoDir string) (*gsdstate.State, error) {
	cmd := exec.CommandContext(ctx, "stclaude", "get-state", "--json")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("stclaude get-state failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("stclaude not found — install stgsd or ensure stclaude is in PATH: %w", err)
	}

	// stclaude may emit info lines (e.g. "ℹ️ INFO ...") to stdout before JSON.
	// Find the first '{' to locate the JSON object start.
	jsonStart := strings.IndexByte(string(out), '{')
	if jsonStart < 0 {
		return nil, fmt.Errorf("stclaude output contains no JSON: %s", string(out))
	}
	jsonBytes := out[jsonStart:]

	var state gsdstate.State
	if err := json.Unmarshal(jsonBytes, &state); err != nil {
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

// ProgressFunc is called with status updates during seeding.
// phase is the current 1-based index, total is the total count,
// name is the phase name, and status describes the current step.
type ProgressFunc func(phase, total int, name, status string)

// Seed creates GitHub issues for each GSD phase and adds them to the project.
// If onProgress is non-nil, it is called with status updates for each phase.
func Seed(ctx context.Context, trk *tracker.GitHubProjectTracker, state *gsdstate.State, repo string, onProgress ProgressFunc) (*SeedResult, error) {
	result := &SeedResult{}
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format: %s", repo)
	}
	owner, repoName := parts[0], parts[1]
	total := len(state.Data.Phases)

	progress := func(i int, name, status string) {
		if onProgress != nil {
			onProgress(i+1, total, name, status)
		}
	}

	for i, phase := range state.Data.Phases {
		title := fmt.Sprintf("[Phase %s] %s", phase.Number, phase.Name)

		// Check if issue already exists
		progress(i, phase.Name, "checking")
		exists, err := issueExists(ctx, owner, repoName, title)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Phase %s: check failed: %v", phase.Number, err))
			progress(i, phase.Name, "error")
			continue
		}
		if exists {
			result.Skipped = append(result.Skipped, fmt.Sprintf("Phase %s: %s (already exists)", phase.Number, phase.Name))
			progress(i, phase.Name, "skipped")
			continue
		}

		// Create the issue
		progress(i, phase.Name, "creating issue")
		body := fmt.Sprintf("## Phase %s: %s\n\n**Goal:** %s\n\n**GSD Status:** %s\n\n---\n*Seeded by maestro from GSD project data*",
			phase.Number, phase.Name, phase.Goal, phase.Status)

		issueNum, err := createIssue(ctx, owner, repoName, title, body)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Phase %s: create failed: %v", phase.Number, err))
			progress(i, phase.Name, "error")
			continue
		}

		// Add issue to project
		progress(i, phase.Name, "adding to project")
		itemID, err := addIssueToProject(ctx, trk, owner, repoName, issueNum)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Phase %s: add to project failed: %v", phase.Number, err))
			progress(i, phase.Name, "error")
			continue
		}

		// Set Phase field
		progress(i, phase.Name, "setting status")
		maestroPhase := gsdstate.PhaseToMaestro(phase.Status)
		issue := tracker.Issue{
			Number:        issueNum,
			ProjectItemID: itemID,
		}
		if err := trk.UpdateStatus(ctx, issue, maestroPhase); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Phase %s: set phase failed: %v", phase.Number, err))
			progress(i, phase.Name, "error")
			continue
		}

		result.Created = append(result.Created, fmt.Sprintf("Phase %s: %s → %s (#%d)", phase.Number, phase.Name, maestroPhase, issueNum))
		progress(i, phase.Name, "done")
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
		"--body", body)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return 0, fmt.Errorf("%s", string(exitErr.Stderr))
		}
		return 0, err
	}
	// gh issue create outputs a URL like: https://github.com/owner/repo/issues/42
	url := strings.TrimSpace(string(out))
	parts := strings.Split(url, "/")
	if len(parts) == 0 {
		return 0, fmt.Errorf("unexpected gh issue create output: %s", url)
	}
	num, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return 0, fmt.Errorf("failed to parse issue number from URL %q: %w", url, err)
	}
	return num, nil
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
