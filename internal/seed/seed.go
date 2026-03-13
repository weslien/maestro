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

// planKey uniquely identifies a plan within a phase for dependency resolution.
type planKey struct {
	phaseNumber string
	planNumber  string
}

// seededIssue tracks a created or existing issue with its node ID.
type seededIssue struct {
	number int
	nodeID string
	isNew  bool
}

// Seed creates GitHub issues for each GSD phase (and its plans) and adds them to the project.
// It sets issue types (Phase/Task), wires sub-issue relationships (phase→plan),
// and creates dependencies between plans based on depends_on in plan frontmatter.
// repoDir is the repository root used to locate .planning/phases/XX/PLAN-NN.md files.
func Seed(ctx context.Context, trk *tracker.GitHubProjectTracker, state *gsdstate.State, repo, repoDir string, update bool, onProgress ProgressFunc) (*SeedResult, error) {
	result := &SeedResult{}
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format: %s", repo)
	}
	owner, repoName := parts[0], parts[1]

	// Build a lookup of plans by phase ID from stclaude state,
	// falling back to disk discovery for phases with no plans in the DB.
	plansByPhase := make(map[string][]gsdstate.Plan)
	for _, plan := range state.Data.Plans {
		plansByPhase[plan.PhaseID] = append(plansByPhase[plan.PhaseID], plan)
	}
	for _, phase := range state.Data.Phases {
		if _, ok := plansByPhase[phase.ID]; !ok {
			if discovered := DiscoverPlans(repoDir, phase.Number, phase.ID); len(discovered) > 0 {
				plansByPhase[phase.ID] = discovered
			}
		}
	}

	// Count total items (phases + plans) for progress
	totalPlans := 0
	for _, plans := range plansByPhase {
		totalPlans += len(plans)
	}
	total := len(state.Data.Phases) + totalPlans
	idx := 0

	progress := func(name, status string) {
		if onProgress != nil {
			onProgress(idx+1, total, name, status)
		}
	}

	// Track node IDs for dependency wiring (deferred to end)
	planNodeIDs := make(map[planKey]string) // plan key → node ID
	type deferredDep struct {
		dependentKey planKey
		blockerKey   planKey
		label        string
	}
	var deferredDeps []deferredDep

	for _, phase := range state.Data.Phases {
		phaseTitle := fmt.Sprintf("[Phase %s] %s", phase.Number, phase.Name)

		// Check if phase issue already exists
		progress(phase.Name, "checking")
		phaseIssueNum, err := findIssue(ctx, owner, repoName, phaseTitle)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Phase %s: check failed: %v", phase.Number, err))
			progress(phase.Name, "error")
			idx++
			continue
		}

		var phaseNodeID string

		if phaseIssueNum > 0 {
			// Resolve node ID for sub-issue wiring
			phaseNodeID, _ = trk.GetIssueNodeID(ctx, phaseIssueNum)
			if update && phaseNodeID != "" {
				progress(phase.Name, "updating type")
				_ = trk.SetIssueType(ctx, phaseNodeID, tracker.TypePhase)
				result.Skipped = append(result.Skipped, fmt.Sprintf("Phase %s: %s (updated)", phase.Number, phase.Name))
				progress(phase.Name, "updated")
			} else {
				result.Skipped = append(result.Skipped, fmt.Sprintf("Phase %s: %s (already exists)", phase.Number, phase.Name))
				progress(phase.Name, "skipped")
			}
		} else {
			// Create the phase issue
			progress(phase.Name, "creating issue")
			body := fmt.Sprintf("## Phase %s: %s\n\n**Goal:** %s\n\n**GSD Status:** %s\n\n---\n*Seeded by maestro from GSD project data*",
				phase.Number, phase.Name, phase.Goal, phase.Status)

			phaseIssueNum, err = createIssue(ctx, owner, repoName, phaseTitle, body)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Phase %s: create failed: %v", phase.Number, err))
				progress(phase.Name, "error")
				idx++
				idx += len(plansByPhase[phase.ID])
				continue
			}

			// Get node ID, set type, add to project
			phaseNodeID, err = trk.GetIssueNodeID(ctx, phaseIssueNum)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Phase %s: get node ID failed: %v", phase.Number, err))
				progress(phase.Name, "error")
				idx++
				idx += len(plansByPhase[phase.ID])
				continue
			}

			// Set issue type
			progress(phase.Name, "setting type")
			if err := trk.SetIssueType(ctx, phaseNodeID, tracker.TypePhase); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Phase %s: set type failed: %v", phase.Number, err))
				// Non-fatal — continue with project board setup
			}

			// Add issue to project
			progress(phase.Name, "adding to project")
			itemID, err := addIssueToProject(ctx, trk, owner, repoName, phaseIssueNum)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Phase %s: add to project failed: %v", phase.Number, err))
				progress(phase.Name, "error")
				idx++
				idx += len(plansByPhase[phase.ID])
				continue
			}

			// Set Phase field
			progress(phase.Name, "setting status")
			maestroPhase := gsdstate.PhaseToMaestro(phase.Status)
			issue := tracker.Issue{
				Number:        phaseIssueNum,
				ProjectItemID: itemID,
			}
			if err := trk.UpdateStatus(ctx, issue, maestroPhase); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Phase %s: set phase failed: %v", phase.Number, err))
				progress(phase.Name, "error")
				idx++
				idx += len(plansByPhase[phase.ID])
				continue
			}

			result.Created = append(result.Created, fmt.Sprintf("Phase %s: %s → %s (#%d)", phase.Number, phase.Name, maestroPhase, phaseIssueNum))
			progress(phase.Name, "done")
		}
		idx++

		// Seed plans for this phase
		plans := plansByPhase[phase.ID]
		for _, plan := range plans {
			paddedPlan := zeroPad(plan.PlanNumber, 2)
			planName := fmt.Sprintf("Phase %s / Plan %s", phase.Number, paddedPlan)
			pk := planKey{phaseNumber: phase.Number, planNumber: paddedPlan}

			// Read plan metadata (objective + depends_on)
			meta, _ := ReadPlanMeta(repoDir, phase.Number, plan.PlanNumber)
			summary := ""
			if meta != nil {
				summary = meta.Summary
			}
			if summary == "" {
				summary = fmt.Sprintf("Plan %s", paddedPlan)
			}

			planTitle := fmt.Sprintf("[Phase %s / Plan %s] %s", phase.Number, paddedPlan, summary)

			progress(planName, "checking")
			planIssueNum, err := findIssue(ctx, owner, repoName, planTitle)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: check failed: %v", planName, err))
				progress(planName, "error")
				idx++
				continue
			}
			if planIssueNum > 0 {
				// Resolve node ID for dependency wiring
				if nodeID, err := trk.GetIssueNodeID(ctx, planIssueNum); err == nil {
					planNodeIDs[pk] = nodeID
					if update {
						progress(planName, "updating")
						_ = trk.SetIssueType(ctx, nodeID, tracker.TypeTask)
						if phaseNodeID != "" {
							_ = trk.AddSubIssue(ctx, phaseNodeID, nodeID)
						}
						result.Skipped = append(result.Skipped, fmt.Sprintf("%s: %s (updated)", planName, summary))
						progress(planName, "updated")
					} else {
						result.Skipped = append(result.Skipped, fmt.Sprintf("%s: %s (already exists)", planName, summary))
						progress(planName, "skipped")
					}
				} else {
					result.Skipped = append(result.Skipped, fmt.Sprintf("%s: %s (already exists)", planName, summary))
					progress(planName, "skipped")
				}
				// Collect deferred dependencies for existing plans too
				if update && meta != nil {
					for _, dep := range meta.DependsOn {
						blockerPlan := zeroPad(dep, 2)
						deferredDeps = append(deferredDeps, deferredDep{
							dependentKey: pk,
							blockerKey:   planKey{phaseNumber: phase.Number, planNumber: blockerPlan},
							label:        planName,
						})
					}
				}
				idx++
				continue
			}

			// Build plan issue body
			progress(planName, "creating issue")
			planBody := fmt.Sprintf("## Phase %s / Plan %s\n\n", phase.Number, paddedPlan)
			if meta != nil && meta.Objective != "" {
				planBody += fmt.Sprintf("**Objective:** %s\n\n", meta.Objective)
			}
			planBody += fmt.Sprintf("**GSD Status:** %s\n\n---\n*Seeded by maestro from GSD project data*",
				plan.Status)

			planIssueNum, err = createIssue(ctx, owner, repoName, planTitle, planBody)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: create failed: %v", planName, err))
				progress(planName, "error")
				idx++
				continue
			}

			// Get node ID
			planNodeID, err := trk.GetIssueNodeID(ctx, planIssueNum)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: get node ID failed: %v", planName, err))
				progress(planName, "error")
				idx++
				continue
			}
			planNodeIDs[pk] = planNodeID

			// Set issue type
			progress(planName, "setting type")
			if err := trk.SetIssueType(ctx, planNodeID, tracker.TypeTask); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: set type failed: %v", planName, err))
				// Non-fatal
			}

			// Wire as sub-issue of phase
			if phaseNodeID != "" {
				progress(planName, "wiring sub-issue")
				if err := trk.AddSubIssue(ctx, phaseNodeID, planNodeID); err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("%s: sub-issue failed: %v", planName, err))
					// Non-fatal
				}
			}

			// Add to project
			progress(planName, "adding to project")
			planItemID, err := addIssueToProject(ctx, trk, owner, repoName, planIssueNum)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: add to project failed: %v", planName, err))
				progress(planName, "error")
				idx++
				continue
			}

			// Set status
			progress(planName, "setting status")
			maestroStatus := gsdstate.PlanToMaestro(plan.Status)
			planIssue := tracker.Issue{
				Number:        planIssueNum,
				ProjectItemID: planItemID,
			}
			if err := trk.UpdateStatus(ctx, planIssue, maestroStatus); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: set status failed: %v", planName, err))
				progress(planName, "error")
				idx++
				continue
			}

			// Collect deferred dependencies
			if meta != nil {
				for _, dep := range meta.DependsOn {
					blockerPlan := zeroPad(dep, 2)
					deferredDeps = append(deferredDeps, deferredDep{
						dependentKey: pk,
						blockerKey:   planKey{phaseNumber: phase.Number, planNumber: blockerPlan},
						label:        planName,
					})
				}
			}

			result.Created = append(result.Created, fmt.Sprintf("%s: %s → %s (#%d)", planName, summary, maestroStatus, planIssueNum))
			progress(planName, "done")
			idx++
		}
	}

	// Wire deferred dependencies
	for _, dep := range deferredDeps {
		depNodeID, ok1 := planNodeIDs[dep.dependentKey]
		blockerNodeID, ok2 := planNodeIDs[dep.blockerKey]
		if !ok1 || !ok2 {
			continue
		}
		if err := trk.AddDependency(ctx, depNodeID, blockerNodeID); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: dependency on Plan %s failed: %v",
				dep.label, dep.blockerKey.planNumber, err))
		}
	}

	return result, nil
}

func issueExists(ctx context.Context, owner, repo, title string) (bool, error) {
	num, err := findIssue(ctx, owner, repo, title)
	return num > 0, err
}

// findIssue searches for an existing issue with the given title and returns its number.
// Returns 0 if no matching issue is found.
func findIssue(ctx context.Context, owner, repo, title string) (int, error) {
	cmd := exec.CommandContext(ctx, "gh", "issue", "list",
		"--repo", owner+"/"+repo,
		"--search", title,
		"--json", "title,number",
		"--limit", "5")
	out, err := cmd.Output()
	if err != nil {
		return 0, err
	}
	var issues []struct {
		Title  string `json:"title"`
		Number int    `json:"number"`
	}
	if err := json.Unmarshal(out, &issues); err != nil {
		return 0, err
	}
	for _, i := range issues {
		if i.Title == title {
			return i.Number, nil
		}
	}
	return 0, nil
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
