package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// EnsureOrgWebhook creates an organization webhook for Projects V2 events.
// This only works when the owner is a GitHub organization.
// Returns true if the webhook already existed.
func (t *GitHubProjectTracker) EnsureOrgWebhook(ctx context.Context, webhookURL, secret string) (exists bool, err error) {
	// List existing org webhooks
	listCmd := exec.CommandContext(ctx, "gh", "api",
		fmt.Sprintf("/orgs/%s/hooks", t.owner),
	)
	out, err := listCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if strings.Contains(stderr, "404") || strings.Contains(stderr, "Not Found") {
				return false, fmt.Errorf("%s is not an organization — use 'maestro setup --app' to create a GitHub App instead", t.owner)
			}
			return false, fmt.Errorf("failed to list org webhooks: %s", stderr)
		}
		return false, fmt.Errorf("failed to list org webhooks: %w", err)
	}

	var hooks []struct {
		ID     int `json:"id"`
		Config struct {
			URL string `json:"url"`
		} `json:"config"`
		Active bool `json:"active"`
	}
	if err := json.Unmarshal(out, &hooks); err != nil {
		return false, fmt.Errorf("failed to parse webhooks: %w", err)
	}

	for _, h := range hooks {
		if h.Config.URL == webhookURL {
			fmt.Printf("  Org webhook already exists (id=%d, active=%v)\n", h.ID, h.Active)
			return true, nil
		}
	}

	// Create the org webhook
	payload := map[string]interface{}{
		"name":   "web",
		"active": true,
		"events": []string{"projects_v2_item"},
		"config": map[string]string{
			"url":          webhookURL,
			"content_type": "json",
			"secret":       secret,
			"insecure_ssl": "0",
		},
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("failed to marshal webhook payload: %w", err)
	}

	createCmd := exec.CommandContext(ctx, "gh", "api",
		fmt.Sprintf("/orgs/%s/hooks", t.owner),
		"-X", "POST",
		"--input", "-",
	)
	createCmd.Stdin = strings.NewReader(string(payloadJSON))

	createOut, err := createCmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("failed to create org webhook: %s", strings.TrimSpace(string(createOut)))
	}

	var result struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(createOut, &result); err != nil {
		return false, fmt.Errorf("failed to parse create response: %w", err)
	}

	fmt.Printf("  Created org webhook (id=%d) → %s\n", result.ID, webhookURL)
	return false, nil
}
