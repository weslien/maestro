package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// IssueTypeName constants used by maestro.
const (
	TypeMilestone = "Milestone"
	TypePhase     = "Phase"
	TypeTask      = "Task"
)

// typeFallbacks maps maestro types to acceptable fallbacks from GitHub defaults.
// If the preferred type doesn't exist and can't be created, use the fallback.
var typeFallbacks = map[string]string{
	TypeMilestone: "Feature",
	TypePhase:     "Feature",
	TypeTask:      "Task",
}

// MaestroIssueTypes returns the issue types maestro manages.
func MaestroIssueTypes() []string {
	return []string{TypeMilestone, TypePhase, TypeTask}
}

// issueTypeCache maps type name → node ID, populated by EnsureIssueTypes.
type issueTypeCache map[string]string

var issueTypes issueTypeCache

// IssueTypesAvailable returns true if issue types were successfully initialized.
func IssueTypesAvailable() bool {
	return len(issueTypes) > 0
}

// EnsureIssueTypes discovers existing issue types and creates missing ones.
// If creation fails (e.g. no org admin), falls back to GitHub's default types.
// Returns nil even on partial success — SetIssueType will skip silently if
// a type isn't available.
func (t *GitHubProjectTracker) EnsureIssueTypes(ctx context.Context) error {
	existing, err := t.listIssueTypes(ctx)
	if err != nil {
		return fmt.Errorf("failed to list issue types: %w", err)
	}

	issueTypes = make(issueTypeCache)
	for name, id := range existing {
		issueTypes[name] = id
	}

	// Try to create missing types; fall back to defaults on failure
	var createFailed bool
	for _, typeName := range MaestroIssueTypes() {
		if _, ok := issueTypes[typeName]; ok {
			continue
		}
		if createFailed {
			// Already failed once — just use fallback
			if fb, ok := typeFallbacks[typeName]; ok {
				if id, exists := issueTypes[fb]; exists {
					issueTypes[typeName] = id
				}
			}
			continue
		}

		ownerID, err := t.resolveOwnerID(ctx)
		if err != nil {
			createFailed = true
			continue
		}
		id, err := t.createIssueType(ctx, ownerID, typeName)
		if err != nil {
			createFailed = true
			// Fall back to default type
			if fb, ok := typeFallbacks[typeName]; ok {
				if fbID, exists := issueTypes[fb]; exists {
					issueTypes[typeName] = fbID
				}
			}
			continue
		}
		issueTypes[typeName] = id
	}

	return nil
}

func (t *GitHubProjectTracker) listIssueTypes(ctx context.Context) (map[string]string, error) {
	parts := strings.SplitN(t.repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format: %s", t.repo)
	}

	query := fmt.Sprintf(`query {
		repository(owner: %q, name: %q) {
			issueTypes(first: 50) {
				nodes { id name }
			}
		}
	}`, parts[0], parts[1])

	out, err := t.ghGraphQLWithHeaders(ctx, query, "GraphQL-Features: issue_types")
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data struct {
			Repository struct {
				IssueTypes struct {
					Nodes []struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"issueTypes"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse issue types: %w", err)
	}

	result := make(map[string]string)
	for _, t := range resp.Data.Repository.IssueTypes.Nodes {
		result[t.Name] = t.ID
	}
	return result, nil
}

func (t *GitHubProjectTracker) createIssueType(ctx context.Context, ownerID, name string) (string, error) {
	mutation := fmt.Sprintf(`mutation {
		createIssueType(input: {
			ownerId: %q
			name: %q
			isEnabled: true
		}) {
			issueType { id name }
		}
	}`, ownerID, name)

	out, err := t.ghGraphQLWithHeaders(ctx, mutation, "GraphQL-Features: issue_types")
	if err != nil {
		return "", err
	}

	var resp struct {
		Data struct {
			CreateIssueType struct {
				IssueType struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"issueType"`
			} `json:"createIssueType"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", fmt.Errorf("failed to parse create issue type response: %w", err)
	}
	return resp.Data.CreateIssueType.IssueType.ID, nil
}

// SetIssueType sets the issue type on an existing issue.
// issueNodeID is the GraphQL node ID of the issue.
// Returns nil silently if the type isn't available (no org admin permissions).
func (t *GitHubProjectTracker) SetIssueType(ctx context.Context, issueNodeID, typeName string) error {
	typeID, ok := issueTypes[typeName]
	if !ok {
		return nil // type not available, skip silently
	}

	mutation := fmt.Sprintf(`mutation {
		updateIssue(input: {
			id: %q
			issueTypeId: %q
		}) {
			issue { id }
		}
	}`, issueNodeID, typeID)

	_, err := t.ghGraphQLWithHeaders(ctx, mutation, "GraphQL-Features: issue_types")
	return err
}

// AddSubIssue creates a parent-child relationship between two issues.
// Both IDs are GraphQL node IDs.
func (t *GitHubProjectTracker) AddSubIssue(ctx context.Context, parentNodeID, childNodeID string) error {
	mutation := fmt.Sprintf(`mutation {
		addSubIssue(input: {
			issueId: %q
			subIssueId: %q
		}) {
			issue { id }
			subIssue { id }
		}
	}`, parentNodeID, childNodeID)

	_, err := t.ghGraphQLWithHeaders(ctx, mutation, "GraphQL-Features: sub_issues")
	return err
}

// AddDependency marks dependentNodeID as blocked by blockerNodeID.
func (t *GitHubProjectTracker) AddDependency(ctx context.Context, dependentNodeID, blockerNodeID string) error {
	mutation := fmt.Sprintf(`mutation {
		addBlockedByRelationship(input: {
			issueId: %q
			blockedByIssueId: %q
		}) {
			blockedByIssue { id }
		}
	}`, dependentNodeID, blockerNodeID)

	_, err := t.ghGraphQL(ctx, mutation)
	return err
}

// GetIssueNodeID returns the GraphQL node ID for an issue number.
func (t *GitHubProjectTracker) GetIssueNodeID(ctx context.Context, issueNumber int) (string, error) {
	parts := strings.SplitN(t.repo, "/", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid repo format: %s", t.repo)
	}

	query := fmt.Sprintf(`query {
		repository(owner: %q, name: %q) {
			issue(number: %d) { id }
		}
	}`, parts[0], parts[1], issueNumber)

	out, err := t.ghGraphQL(ctx, query)
	if err != nil {
		return "", err
	}

	var resp struct {
		Data struct {
			Repository struct {
				Issue struct {
					ID string `json:"id"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", err
	}
	if resp.Data.Repository.Issue.ID == "" {
		return "", fmt.Errorf("issue #%d not found", issueNumber)
	}
	return resp.Data.Repository.Issue.ID, nil
}

// ghGraphQLWithHeaders runs a GraphQL query with additional HTTP headers.
func (t *GitHubProjectTracker) ghGraphQLWithHeaders(ctx context.Context, query string, headers ...string) ([]byte, error) {
	args := []string{"api", "graphql", "-f", "query=" + query}
	for _, h := range headers {
		args = append(args, "-H", h)
	}
	cmd := exec.CommandContext(ctx, "gh", args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh api graphql failed: %s", string(exitErr.Stderr))
		}
		return nil, err
	}
	return out, nil
}
