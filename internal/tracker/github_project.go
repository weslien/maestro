package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// fieldName is the custom single-select field maestro uses for lifecycle tracking.
// We use a custom field because the built-in "Status" field can't be modified via API.
const fieldName = "Phase"

type GitHubProjectTracker struct {
	owner         string
	repo          string
	projectNumber int
	projectID     string
	fieldID       string
	fieldOptions  map[string]string // option name -> option ID
}

func NewGitHubProjectTracker(owner, repo string, projectNumber int) *GitHubProjectTracker {
	return &GitHubProjectTracker{
		owner:         owner,
		repo:          repo,
		projectNumber: projectNumber,
		fieldOptions:  make(map[string]string),
	}
}

// Init discovers the project ID and Phase field options.
func (t *GitHubProjectTracker) Init(ctx context.Context) error {
	// Discover project ID first
	if err := t.discoverProject(ctx); err != nil {
		return err
	}

	// Find the Phase field
	return t.discoverField(ctx)
}

func (t *GitHubProjectTracker) discoverProject(ctx context.Context) error {
	// Try user first
	query := fmt.Sprintf(`query {
		user(login: %q) {
			projectV2(number: %d) { id }
		}
	}`, t.owner, t.projectNumber)

	out, err := t.ghGraphQL(ctx, query)
	if err == nil {
		var resp struct {
			Data struct {
				User *struct {
					ProjectV2 *struct {
						ID string `json:"id"`
					} `json:"projectV2"`
				} `json:"user"`
			} `json:"data"`
		}
		if json.Unmarshal(out, &resp) == nil && resp.Data.User != nil && resp.Data.User.ProjectV2 != nil {
			t.projectID = resp.Data.User.ProjectV2.ID
			return nil
		}
	}

	// Try org
	orgQuery := fmt.Sprintf(`query {
		organization(login: %q) {
			projectV2(number: %d) { id }
		}
	}`, t.owner, t.projectNumber)

	out, err = t.ghGraphQL(ctx, orgQuery)
	if err != nil {
		return fmt.Errorf("failed to query org project: %w", err)
	}

	var orgResp struct {
		Data struct {
			Organization *struct {
				ProjectV2 *struct {
					ID string `json:"id"`
				} `json:"projectV2"`
			} `json:"organization"`
		} `json:"data"`
	}
	if json.Unmarshal(out, &orgResp) == nil && orgResp.Data.Organization != nil && orgResp.Data.Organization.ProjectV2 != nil {
		t.projectID = orgResp.Data.Organization.ProjectV2.ID
		return nil
	}

	return fmt.Errorf("project #%d not found for %s", t.projectNumber, t.owner)
}

func (t *GitHubProjectTracker) discoverField(ctx context.Context) error {
	query := fmt.Sprintf(`query {
		node(id: %q) {
			... on ProjectV2 {
				field(name: %q) {
					... on ProjectV2SingleSelectField {
						id
						options { id name }
					}
				}
			}
		}
	}`, t.projectID, fieldName)

	out, err := t.ghGraphQL(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query %s field: %w", fieldName, err)
	}

	var resp struct {
		Data struct {
			Node struct {
				Field *struct {
					ID      string        `json:"id"`
					Options []fieldOption `json:"options"`
				} `json:"field"`
			} `json:"node"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return fmt.Errorf("failed to parse field response: %w", err)
	}

	if resp.Data.Node.Field == nil || resp.Data.Node.Field.ID == "" {
		return fmt.Errorf("%q field not found on project #%d — run 'maestro setup' to create it", fieldName, t.projectNumber)
	}

	t.fieldID = resp.Data.Node.Field.ID
	t.fieldOptions = make(map[string]string)
	for _, opt := range resp.Data.Node.Field.Options {
		t.fieldOptions[opt.Name] = opt.ID
	}

	return nil
}

type fieldOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (t *GitHubProjectTracker) Poll(ctx context.Context) ([]Issue, error) {
	query := fmt.Sprintf(`query {
		node(id: %q) {
			... on ProjectV2 {
				items(first: 100) {
					nodes {
						id
						fieldValueByName(name: %q) {
							... on ProjectV2ItemFieldSingleSelectValue {
								name
							}
						}
						content {
							... on Issue {
								number
								title
								body
								url
								createdAt
								updatedAt
								labels(first: 10) {
									nodes {
										name
									}
								}
							}
						}
					}
				}
			}
		}
	}`, t.projectID, fieldName)

	out, err := t.ghGraphQL(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to poll project: %w", err)
	}

	var resp struct {
		Data struct {
			Node struct {
				Items struct {
					Nodes []struct {
						ID         string `json:"id"`
						FieldValue *struct {
							Name string `json:"name"`
						} `json:"fieldValueByName"`
						Content *struct {
							Number    int       `json:"number"`
							Title     string    `json:"title"`
							Body      string    `json:"body"`
							URL       string    `json:"url"`
							CreatedAt time.Time `json:"createdAt"`
							UpdatedAt time.Time `json:"updatedAt"`
							Labels    struct {
								Nodes []struct {
									Name string `json:"name"`
								} `json:"nodes"`
							} `json:"labels"`
						} `json:"content"`
					} `json:"nodes"`
				} `json:"items"`
			} `json:"node"`
		} `json:"data"`
	}

	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse poll response: %w", err)
	}

	var issues []Issue
	for _, node := range resp.Data.Node.Items.Nodes {
		if node.Content == nil {
			continue
		}

		status := ""
		if node.FieldValue != nil {
			status = node.FieldValue.Name
		}

		var labels []string
		for _, l := range node.Content.Labels.Nodes {
			labels = append(labels, l.Name)
		}

		issues = append(issues, Issue{
			ID:            strconv.Itoa(node.Content.Number),
			Number:        node.Content.Number,
			Title:         node.Content.Title,
			Body:          node.Content.Body,
			Labels:        labels,
			Status:        status,
			ProjectItemID: node.ID,
			URL:           node.Content.URL,
			CreatedAt:     node.Content.CreatedAt,
			UpdatedAt:     node.Content.UpdatedAt,
		})
	}

	return issues, nil
}

func (t *GitHubProjectTracker) UpdateStatus(ctx context.Context, issue Issue, newStatus string) error {
	optionID, ok := t.fieldOptions[newStatus]
	if !ok {
		return fmt.Errorf("unknown phase %q, available: %v", newStatus, t.availableStatuses())
	}

	mutation := fmt.Sprintf(`mutation {
		updateProjectV2ItemFieldValue(input: {
			projectId: %q
			itemId: %q
			fieldId: %q
			value: { singleSelectOptionId: %q }
		}) {
			projectV2Item {
				id
			}
		}
	}`, t.projectID, issue.ProjectItemID, t.fieldID, optionID)

	_, err := t.ghGraphQL(ctx, mutation)
	if err != nil {
		return fmt.Errorf("failed to update phase to %q: %w", newStatus, err)
	}

	return nil
}

func (t *GitHubProjectTracker) GetIssue(ctx context.Context, number int) (*Issue, error) {
	parts := strings.SplitN(t.repo, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid repo format: %s", t.repo)
	}

	query := fmt.Sprintf(`query {
		repository(owner: %q, name: %q) {
			issue(number: %d) {
				number
				title
				body
				url
				createdAt
				updatedAt
				labels(first: 10) {
					nodes {
						name
					}
				}
			}
		}
	}`, parts[0], parts[1], number)

	out, err := t.ghGraphQL(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get issue #%d: %w", number, err)
	}

	var resp struct {
		Data struct {
			Repository struct {
				Issue struct {
					Number    int       `json:"number"`
					Title     string    `json:"title"`
					Body      string    `json:"body"`
					URL       string    `json:"url"`
					CreatedAt time.Time `json:"createdAt"`
					UpdatedAt time.Time `json:"updatedAt"`
					Labels    struct {
						Nodes []struct {
							Name string `json:"name"`
						} `json:"nodes"`
					} `json:"labels"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
	}

	if err := json.Unmarshal(out, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse issue response: %w", err)
	}

	i := resp.Data.Repository.Issue
	var labels []string
	for _, l := range i.Labels.Nodes {
		labels = append(labels, l.Name)
	}

	return &Issue{
		ID:        strconv.Itoa(i.Number),
		Number:    i.Number,
		Title:     i.Title,
		Body:      i.Body,
		Labels:    labels,
		URL:       i.URL,
		CreatedAt: i.CreatedAt,
		UpdatedAt: i.UpdatedAt,
	}, nil
}

// EnsurePhaseField creates the custom "Phase" single-select field with all
// required options. Idempotent — skips if field already has the right options.
func (t *GitHubProjectTracker) EnsurePhaseField(ctx context.Context, statuses []string) error {
	if t.projectID == "" {
		return fmt.Errorf("project not initialized")
	}

	// Check if Phase field already exists
	err := t.discoverField(ctx)
	if err == nil {
		// Field exists — check if options match
		have := make(map[string]bool, len(t.fieldOptions))
		for name := range t.fieldOptions {
			have[name] = true
		}
		allPresent := true
		for _, s := range statuses {
			if !have[s] {
				allPresent = false
				break
			}
		}
		if allPresent && len(t.fieldOptions) == len(statuses) {
			fmt.Printf("  %s field already configured correctly\n", fieldName)
			return nil
		}

		// Options don't match — delete and recreate
		fmt.Printf("  Updating %s field options...\n", fieldName)
		cmd := exec.CommandContext(ctx, "gh", "project", "field-delete", "--id", t.fieldID)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("failed to delete existing %s field: %s", fieldName, strings.TrimSpace(string(out)))
		}
	}

	// Create the Phase field with all options
	optsList := strings.Join(statuses, ",")
	cmd := exec.CommandContext(ctx, "gh", "project", "field-create",
		fmt.Sprintf("%d", t.projectNumber),
		"--owner", t.owner,
		"--name", fieldName,
		"--data-type", "SINGLE_SELECT",
		"--single-select-options", optsList,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to create %s field: %s", fieldName, strings.TrimSpace(string(out)))
	}

	for _, s := range statuses {
		fmt.Printf("  Phase %q configured\n", s)
	}

	// Re-discover the field ID
	return t.discoverField(ctx)
}

// CreateProject creates a new GitHub Project V2 and links it to the repository.
func (t *GitHubProjectTracker) CreateProject(ctx context.Context, title string) error {
	ownerID, err := t.resolveOwnerID(ctx)
	if err != nil {
		return err
	}

	createMutation := fmt.Sprintf(`mutation {
		createProjectV2(input: {
			ownerId: %q
			title: %q
		}) {
			projectV2 {
				id
				number
			}
		}
	}`, ownerID, title)

	out, err := t.ghGraphQL(ctx, createMutation)
	if err != nil {
		return fmt.Errorf("failed to create project: %w", err)
	}

	var createResp struct {
		Data struct {
			CreateProjectV2 struct {
				ProjectV2 struct {
					ID     string `json:"id"`
					Number int    `json:"number"`
				} `json:"projectV2"`
			} `json:"createProjectV2"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &createResp); err != nil {
		return fmt.Errorf("failed to parse create response: %w", err)
	}

	t.projectID = createResp.Data.CreateProjectV2.ProjectV2.ID
	t.projectNumber = createResp.Data.CreateProjectV2.ProjectV2.Number

	// Link project to repository
	if err := t.linkToRepo(ctx); err != nil {
		fmt.Printf("  Warning: could not link project to repo: %v\n", err)
	}

	return nil
}

// linkToRepo links the project to the repository so it appears under the repo's Projects tab.
func (t *GitHubProjectTracker) linkToRepo(ctx context.Context) error {
	parts := strings.SplitN(t.repo, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo format: %s", t.repo)
	}

	repoQuery := fmt.Sprintf(`query {
		repository(owner: %q, name: %q) { id }
	}`, parts[0], parts[1])

	out, err := t.ghGraphQL(ctx, repoQuery)
	if err != nil {
		return fmt.Errorf("failed to query repo: %w", err)
	}

	var repoResp struct {
		Data struct {
			Repository struct {
				ID string `json:"id"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &repoResp); err != nil {
		return fmt.Errorf("failed to parse repo response: %w", err)
	}

	repoID := repoResp.Data.Repository.ID
	if repoID == "" {
		return fmt.Errorf("repo %s not found", t.repo)
	}

	linkMutation := fmt.Sprintf(`mutation {
		linkProjectV2ToRepository(input: {
			projectId: %q
			repositoryId: %q
		}) {
			repository { id }
		}
	}`, t.projectID, repoID)

	if _, err := t.ghGraphQL(ctx, linkMutation); err != nil {
		return fmt.Errorf("failed to link project to repo: %w", err)
	}

	fmt.Printf("  Linked project to %s\n", t.repo)
	return nil
}

func (t *GitHubProjectTracker) resolveOwnerID(ctx context.Context) (string, error) {
	ownerQuery := fmt.Sprintf(`query { user(login: %q) { id } }`, t.owner)
	out, err := t.ghGraphQL(ctx, ownerQuery)
	if err == nil {
		var resp struct {
			Data struct {
				User *struct{ ID string } `json:"user"`
			} `json:"data"`
		}
		if json.Unmarshal(out, &resp) == nil && resp.Data.User != nil {
			return resp.Data.User.ID, nil
		}
	}

	orgQuery := fmt.Sprintf(`query { organization(login: %q) { id } }`, t.owner)
	out, err = t.ghGraphQL(ctx, orgQuery)
	if err != nil {
		return "", fmt.Errorf("failed to resolve owner %q: %w", t.owner, err)
	}

	var resp struct {
		Data struct {
			Organization *struct{ ID string } `json:"organization"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil || resp.Data.Organization == nil {
		return "", fmt.Errorf("could not determine owner node ID for %s", t.owner)
	}
	return resp.Data.Organization.ID, nil
}

// ProjectNumber returns the project number (useful after CreateProject).
func (t *GitHubProjectTracker) ProjectNumber() int {
	return t.projectNumber
}

func (t *GitHubProjectTracker) ghGraphQL(ctx context.Context, query string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "gh", "api", "graphql", "-f", "query="+query)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("gh api graphql failed: %s", string(exitErr.Stderr))
		}
		return nil, err
	}
	return out, nil
}

func (t *GitHubProjectTracker) availableStatuses() []string {
	statuses := make([]string, 0, len(t.fieldOptions))
	for name := range t.fieldOptions {
		statuses = append(statuses, name)
	}
	return statuses
}
