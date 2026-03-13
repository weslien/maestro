package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Tracker        TrackerConfig   `yaml:"tracker"`
	Workspace      WorkspaceConfig `yaml:"workspace"`
	Agent          AgentConfig     `yaml:"agent"`
	Polling        PollingConfig   `yaml:"polling"`
	Tmux           TmuxConfig      `yaml:"tmux"`
	Bridge         BridgeConfig    `yaml:"bridge"`
	PromptTemplate string          `yaml:"-"`
}

type BridgeConfig struct {
	WebhookURL string `yaml:"webhook_url"`
}

type TrackerConfig struct {
	Kind          string `yaml:"kind"`
	Owner         string `yaml:"owner"`
	ProjectNumber int    `yaml:"project_number"`
	Repo          string `yaml:"repo"`
}

type WorkspaceConfig struct {
	Root  string      `yaml:"root"`
	Hooks HooksConfig `yaml:"hooks,omitempty"`
}

type HooksConfig struct {
	AfterCreate string `yaml:"after_create,omitempty"`
}

type AgentConfig struct {
	MaxConcurrent     int     `yaml:"max_concurrent"`
	Model             string  `yaml:"model"`
	ResearchModel     string  `yaml:"research_model"`
	PlanningModel     string  `yaml:"planning_model"`
	ExecutionModel    string  `yaml:"execution_model"`
	ValidationModel   string  `yaml:"validation_model"`
	MaxBudgetPerIssue float64 `yaml:"max_budget_per_issue"`
	PermissionMode    string  `yaml:"permission_mode"`
}

type PollingConfig struct {
	Interval time.Duration `yaml:"interval"`
}

type TmuxConfig struct {
	SessionPrefix string `yaml:"session_prefix"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}
	return Parse(string(data))
}

func Parse(content string) (*Config, error) {
	// Split on --- delimiters
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return nil, fmt.Errorf("invalid WORKFLOW.md: missing YAML frontmatter delimiters")
	}

	yamlContent := strings.TrimSpace(parts[1])
	promptTemplate := strings.TrimSpace(parts[2])

	var cfg Config
	if err := yaml.Unmarshal([]byte(yamlContent), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML frontmatter: %w", err)
	}

	cfg.PromptTemplate = promptTemplate

	// Apply defaults
	if cfg.Agent.MaxConcurrent == 0 {
		cfg.Agent.MaxConcurrent = 5
	}
	if cfg.Agent.Model == "" {
		cfg.Agent.Model = "sonnet"
	}
	if cfg.Agent.ResearchModel == "" {
		cfg.Agent.ResearchModel = cfg.Agent.Model
	}
	if cfg.Agent.PlanningModel == "" {
		cfg.Agent.PlanningModel = cfg.Agent.Model
	}
	if cfg.Agent.ExecutionModel == "" {
		cfg.Agent.ExecutionModel = cfg.Agent.Model
	}
	if cfg.Agent.ValidationModel == "" {
		cfg.Agent.ValidationModel = cfg.Agent.Model
	}
	if cfg.Polling.Interval == 0 {
		cfg.Polling.Interval = 30 * time.Second
	}
	if cfg.Tmux.SessionPrefix == "" {
		cfg.Tmux.SessionPrefix = "maestro"
	}
	if cfg.Agent.PermissionMode == "" {
		cfg.Agent.PermissionMode = "plan"
	}

	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.Tracker.Kind == "" {
		return fmt.Errorf("tracker.kind is required")
	}
	if c.Tracker.Kind != "github_project" {
		return fmt.Errorf("unsupported tracker kind: %s (only github_project supported)", c.Tracker.Kind)
	}
	if c.Tracker.Owner == "" {
		return fmt.Errorf("tracker.owner is required")
	}
	if c.Tracker.ProjectNumber == 0 {
		return fmt.Errorf("tracker.project_number is required — set it in WORKFLOW.md or run 'maestro setup'")
	}
	if c.Tracker.Repo == "" {
		return fmt.Errorf("tracker.repo is required")
	}
	if c.Workspace.Root == "" {
		return fmt.Errorf("workspace.root is required")
	}

	// Resolve relative workspace root to absolute path
	if !strings.HasPrefix(c.Workspace.Root, "/") && !strings.HasPrefix(c.Workspace.Root, "~") {
		if cwd, err := os.Getwd(); err == nil {
			c.Workspace.Root = filepath.Join(cwd, c.Workspace.Root)
		}
	}

	return nil
}
