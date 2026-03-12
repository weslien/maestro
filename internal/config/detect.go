package config

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Detect auto-detects configuration from the current git repository.
// It reads the git remote URL to determine owner/repo, finds the repo
// root for workspace paths, and sets sensible defaults.
func Detect() (*Config, error) {
	repoRoot, err := gitRepoRoot()
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}

	owner, repo, err := detectOwnerRepo()
	if err != nil {
		return nil, fmt.Errorf("failed to detect repo from git remotes: %w", err)
	}

	return &Config{
		Tracker: TrackerConfig{
			Kind:  "github_project",
			Owner: owner,
			Repo:  owner + "/" + repo,
		},
		Workspace: WorkspaceConfig{
			Root: filepath.Join(repoRoot, ".maestro", "worktrees"),
		},
		Agent: AgentConfig{
			MaxConcurrent:     5,
			Model:             "sonnet",
			ResearchModel:     "opus",
			PlanningModel:     "opus",
			ExecutionModel:    "sonnet",
			ValidationModel:   "sonnet",
			MaxBudgetPerIssue: 10.00,
			PermissionMode:    "plan",
		},
		Polling: PollingConfig{
			Interval: 30 * time.Second,
		},
		Tmux: TmuxConfig{
			SessionPrefix: "maestro",
		},
		PromptTemplate: defaultPromptTemplate,
	}, nil
}

// detectOwnerRepo parses git remote URLs to extract owner and repo name.
// Tries "maestro" remote first, then "origin".
func detectOwnerRepo() (owner, repo string, err error) {
	// Try maestro remote first, then origin
	for _, remote := range []string{"maestro", "origin"} {
		url, err := gitRemoteURL(remote)
		if err != nil {
			continue
		}
		owner, repo, err = parseGitURL(url)
		if err == nil {
			return owner, repo, nil
		}
	}
	return "", "", fmt.Errorf("no usable git remote found (tried 'maestro', 'origin')")
}

func gitRemoteURL(remote string) (string, error) {
	out, err := exec.Command("git", "remote", "get-url", remote).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func gitRepoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

var (
	// git@github.com:owner/repo.git
	sshPattern = regexp.MustCompile(`git@github\.com:([^/]+)/([^/.]+?)(?:\.git)?$`)
	// https://github.com/owner/repo.git
	httpsPattern = regexp.MustCompile(`https?://github\.com/([^/]+)/([^/.]+?)(?:\.git)?$`)
)

func parseGitURL(url string) (owner, repo string, err error) {
	if m := sshPattern.FindStringSubmatch(url); m != nil {
		return m[1], m[2], nil
	}
	if m := httpsPattern.FindStringSubmatch(url); m != nil {
		return m[1], m[2], nil
	}
	return "", "", fmt.Errorf("cannot parse GitHub URL: %s", url)
}

const defaultPromptTemplate = `You are working on issue #{{ issue.number }}: {{ issue.title }}

## Phase: {{ phase }}

## Issue Description
{{ issue.body }}

## Instructions
Follow the GSD methodology for the {{ phase }} phase. Be thorough, write tests, and make atomic commits.`
