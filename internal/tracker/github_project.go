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

type GitHubProjectTracker struct {
	owner         string
	repo          string
	projectNumber int
	projectID     string
	statusFieldID string
	statusOptions map[string]string // status name -> option ID
}

func NewGitHubProjectTracker(owner, repo string, projectNumber int) *GitHubProjectTracker {
	return &GitHubProjectTracker{
		owner:         owner,
		repo:          repo,
		projectNumber: projectNumber,
		statusOptions: make(map[string]string),
	}
}

// Init discovers the project ID and status field options
func (t *GitHubProjectTracker) Init(ctx context.Context) error {
	// Query project ID and status field
	query := fmt.Sprintf(`query {
		user(login: %q) {
			projectV2(number: %d) {
				id
				field(name: "Status") {
					... on ProjectV2SingleSelectField {
						id
						options {
							id
							name
						}
					}
				}
			}
		}
	}`, t.owner, t.projectNumber)

	// Try user first, fall back to organization
	out, err := t.ghGraphQL(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to query project: %w", err)
	}

	var resp struct {
		Data struct {
			User *struct {
				ProjectV2 *projectV2Response `json:"projectV2"`
			} `json:"user"`
			Organization *struct {
				ProjectV2 *projectV2Response `json:"projectV2"`
			} `json:"organization"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}

	if err := json.Unmarshal(out, &resp); err != nil {
		return fmt.Errorf("failed to parse project response: %w", err)
	}

	var proj *projectV2Response
	if resp.Data.User != nil && resp.Data.User.ProjectV2 != nil {
		proj = resp.Data.User.ProjectV2
	}

	// Try org query if user query didn't find the project
	if proj == nil {
		orgQuery := fmt.Sprintf(`query {
			organization(login: %q) {
				projectV2(number: %d) {
					id
					field(name: "Status") {
						... on ProjectV2SingleSelectField {
							id
							options {
								id
								name
							}
						}
					}
				}
			}
		}`, t.owner, t.projectNumber)

		out, err = t.ghGraphQL(ctx, orgQuery)
		if err != nil {
			return fmt.Errorf("failed to query org project: %w", err)
		}

		if err := json.Unmarshal(out, &resp); err != nil {
			return fmt.Errorf("failed to parse org project response: %w", err)
		}

		if resp.Data.Organization != nil && resp.Data.Organization.ProjectV2 != nil {
			proj = resp.Data.Organization.ProjectV2
		}
	}

	if proj == nil {
		return fmt.Errorf("project #%d not found for %s", t.projectNumber, t.owner)
	}

	t.projectID = proj.ID
	t.statusFieldID = proj.Field.ID

	for _, opt := range proj.Field.Options {
		t.statusOptions[opt.Name] = opt.ID
	}

	return nil
}

type projectV2Response struct {
	ID    string `json:"id"`
	Field struct {
		ID      string `json:"id"`
		Options []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"options"`
	} `json:"field"`
}

func (t *GitHubProjectTracker) Poll(ctx context.Context) ([]Issue, error) {
	query := fmt.Sprintf(`query {
		node(id: %q) {
			... on ProjectV2 {
				items(first: 100) {
					nodes {
						id
						fieldValueByName(name: "Status") {
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
	}`, t.projectID)

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
	optionID, ok := t.statusOptions[newStatus]
	if !ok {
		return fmt.Errorf("unknown status %q, available: %v", newStatus, t.availableStatuses())
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
	}`, t.projectID, issue.ProjectItemID, t.statusFieldID, optionID)

	_, err := t.ghGraphQL(ctx, mutation)
	if err != nil {
		return fmt.Errorf("failed to update status to %q: %w", newStatus, err)
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

// CreateProjectWithStatuses creates a new GitHub Project V2 with the required status fields
func (t *GitHubProjectTracker) CreateProjectWithStatuses(ctx context.Context, title string, statuses []string) error {
	// Create project

	// First we need the owner's node ID
	ownerQuery := fmt.Sprintf(`query {
		user(login: %q) { id }
	}`, t.owner)

	out, err := t.ghGraphQL(ctx, ownerQuery)
	if err != nil {
		// Try org
		ownerQuery = fmt.Sprintf(`query {
			organization(login: %q) { id }
		}`, t.owner)
		out, err = t.ghGraphQL(ctx, ownerQuery)
		if err != nil {
			return fmt.Errorf("failed to get owner ID: %w", err)
		}
	}

	var ownerResp struct {
		Data struct {
			User         *struct{ ID string } `json:"user"`
			Organization *struct{ ID string } `json:"organization"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &ownerResp); err != nil {
		return fmt.Errorf("failed to parse owner response: %w", err)
	}

	ownerID := ""
	if ownerResp.Data.User != nil {
		ownerID = ownerResp.Data.User.ID
	} else if ownerResp.Data.Organization != nil {
		ownerID = ownerResp.Data.Organization.ID
	}
	if ownerID == "" {
		return fmt.Errorf("could not determine owner node ID for %s", t.owner)
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

	out, err = t.ghGraphQL(ctx, createMutation)
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

	fmt.Printf("Created project #%d (ID: %s)\n", t.projectNumber, t.projectID)
	fmt.Println("Note: Please configure the Status field options manually in GitHub Project settings.")
	fmt.Println("Required statuses:", strings.Join(statuses, ", "))

	return nil
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
	statuses := make([]string, 0, len(t.statusOptions))
	for name := range t.statusOptions {
		statuses = append(statuses, name)
	}
	return statuses
}
