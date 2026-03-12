package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/weslien/maestro/internal/agent"
	"github.com/weslien/maestro/internal/config"
	"github.com/weslien/maestro/internal/lifecycle"
	"github.com/weslien/maestro/internal/orchestrator"
	"github.com/weslien/maestro/internal/tracker"
	"github.com/weslien/maestro/internal/tui"
)

var cfgFile string

func main() {
	rootCmd := &cobra.Command{
		Use:   "maestro",
		Short: "GSD-powered GitHub Projects orchestrator",
		Long:  "Maestro polls GitHub Projects V2 for issues and runs each through a GSD-style lifecycle using Claude Code agents.",
	}

	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "WORKFLOW.md", "path to WORKFLOW.md config file")

	rootCmd.AddCommand(
		initCmd(),
		setupCmd(),
		runCmd(),
		statusCmd(),
		stopCmd(),
	)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Create a WORKFLOW.md auto-configured from the current git repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat("WORKFLOW.md"); err == nil {
				return fmt.Errorf("WORKFLOW.md already exists")
			}

			cfg, err := config.Detect()
			if err != nil {
				return fmt.Errorf("auto-detection failed: %w", err)
			}

			content := generateWorkflowMD(cfg)
			if err := os.WriteFile("WORKFLOW.md", []byte(content), 0o644); err != nil {
				return fmt.Errorf("failed to write WORKFLOW.md: %w", err)
			}

			// Ensure .maestro is in .gitignore
			ensureGitignore()

			fmt.Printf("Created WORKFLOW.md for %s\n", cfg.Tracker.Repo)
			fmt.Println("Workspaces will use git worktrees in .maestro/worktrees/")
			fmt.Println("\nNext: set tracker.project_number in WORKFLOW.md, or run 'maestro setup' to create a new project board.")
			return nil
		},
	}
}

func setupCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Ensure a GitHub Project exists with required status fields",
		Long: `If project_number is set in WORKFLOW.md, ensures that project has
the correct status fields. Otherwise creates a new project and
updates WORKFLOW.md with the project number.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			trk := tracker.NewGitHubProjectTracker(cfg.Tracker.Owner, cfg.Tracker.Repo, cfg.Tracker.ProjectNumber)
			statuses := lifecycle.AllStatuses()

			if cfg.Tracker.ProjectNumber > 0 {
				// Existing project — just ensure statuses
				fmt.Printf("Configuring existing project #%d for %s...\n", cfg.Tracker.ProjectNumber, cfg.Tracker.Owner)
				if err := trk.Init(cmd.Context()); err != nil {
					return fmt.Errorf("failed to find project #%d: %w", cfg.Tracker.ProjectNumber, err)
				}
				if err := trk.EnsureStatuses(cmd.Context(), statuses); err != nil {
					return err
				}
				fmt.Println("\nProject statuses configured.")
			} else {
				// Create new project
				fmt.Printf("Creating GitHub Project for %s...\n", cfg.Tracker.Owner)
				if err := trk.CreateProject(cmd.Context(), "Maestro Board"); err != nil {
					return err
				}
				fmt.Printf("Created project #%d\n", trk.ProjectNumber())

				if err := trk.EnsureStatuses(cmd.Context(), statuses); err != nil {
					return err
				}

				// Auto-update WORKFLOW.md with the new project number
				if _, statErr := os.Stat(cfgFile); statErr == nil {
					patchProjectNumber(cfgFile, trk.ProjectNumber())
				}
			}

			return nil
		},
	}
}

func runCmd() *cobra.Command {
	var headless bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Start the maestro daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			if err := cfg.Validate(); err != nil {
				return fmt.Errorf("invalid config: %w", err)
			}

			trk := tracker.NewGitHubProjectTracker(cfg.Tracker.Owner, cfg.Tracker.Repo, cfg.Tracker.ProjectNumber)
			if err := trk.Init(cmd.Context()); err != nil {
				return fmt.Errorf("failed to initialize tracker: %w", err)
			}

			orch := orchestrator.New(cfg, trk)

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			// Handle signals
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				fmt.Println("\nShutting down...")
				orch.Stop()
				cancel()
			}()

			// Start orchestrator in background
			go func() {
				if err := orch.Run(ctx); err != nil && ctx.Err() == nil {
					log.Printf("Orchestrator error: %v", err)
				}
			}()

			if headless {
				return runHeadless(ctx, orch)
			}

			// TUI mode
			model := tui.New(orch)
			p := tea.NewProgram(model, tea.WithAltScreen())
			_, err = p.Run()
			return err
		},
	}

	cmd.Flags().BoolVar(&headless, "headless", false, "run without TUI, output structured logs")
	return cmd
}

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current status of tracked issues",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			trk := tracker.NewGitHubProjectTracker(cfg.Tracker.Owner, cfg.Tracker.Repo, cfg.Tracker.ProjectNumber)
			if err := trk.Init(cmd.Context()); err != nil {
				return fmt.Errorf("failed to initialize tracker: %w", err)
			}

			issues, err := trk.Poll(cmd.Context())
			if err != nil {
				return fmt.Errorf("failed to poll: %w", err)
			}

			fmt.Printf("%-8s %-50s %-15s\n", "Issue", "Title", "Status")
			fmt.Printf("%-8s %-50s %-15s\n", "-----", "-----", "------")
			for _, issue := range issues {
				title := issue.Title
				if len(title) > 47 {
					title = title[:47] + "..."
				}
				fmt.Printf("#%-7d %-50s %-15s\n", issue.Number, title, issue.Status)
			}
			return nil
		},
	}
}

func stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <issue-number>",
		Short: "Stop processing a specific issue",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			issueNum, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid issue number: %w", err)
			}

			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			tm := agent.NewTmuxManager(cfg.Tmux.SessionPrefix)
			if err := tm.DestroySession(cmd.Context(), issueNum); err != nil {
				return fmt.Errorf("failed to stop issue #%d: %w", issueNum, err)
			}

			fmt.Printf("Stopped processing issue #%d\n", issueNum)
			return nil
		},
	}
}

// loadConfig loads from WORKFLOW.md if it exists, otherwise auto-detects from git.
func loadConfig() (*config.Config, error) {
	if _, err := os.Stat(cfgFile); err == nil {
		return config.Load(cfgFile)
	}

	// No WORKFLOW.md — auto-detect from git
	cfg, err := config.Detect()
	if err != nil {
		return nil, fmt.Errorf("no WORKFLOW.md found and auto-detection failed: %w\nRun 'maestro init' to create one.", err)
	}

	return cfg, nil
}

func generateWorkflowMD(cfg *config.Config) string {
	return fmt.Sprintf(`---
tracker:
  kind: github_project
  owner: %q
  project_number: 0
  repo: %q
workspace:
  root: .maestro/worktrees
agent:
  max_concurrent: 5
  model: sonnet
  research_model: opus
  planning_model: opus
  execution_model: sonnet
  validation_model: sonnet
  max_budget_per_issue: 10.00
  permission_mode: plan
polling:
  interval: 30s
tmux:
  session_prefix: maestro
---
You are working on issue #{{ issue.number }}: {{ issue.title }}

## Phase: {{ phase }}

## Issue Description
{{ issue.body }}

## Instructions
Follow the GSD methodology for the {{ phase }} phase. Be thorough, write tests, and make atomic commits.
`, cfg.Tracker.Owner, cfg.Tracker.Repo)
}

func ensureGitignore() {
	const entry = ".maestro/"

	data, err := os.ReadFile(".gitignore")
	if err == nil {
		// Check if already present
		for _, line := range splitLines(string(data)) {
			if line == entry || line == ".maestro" {
				return
			}
		}
	}

	f, err := os.OpenFile(".gitignore", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	// Add newline before entry if file doesn't end with one
	if len(data) > 0 && data[len(data)-1] != '\n' {
		_, _ = f.WriteString("\n")
	}
	_, _ = f.WriteString(entry + "\n")
}

func splitLines(s string) []string {
	var lines []string
	for _, line := range stringsSplit(s) {
		lines = append(lines, line)
	}
	return lines
}

func stringsSplit(s string) []string {
	if s == "" {
		return nil
	}
	lines := make([]string, 0)
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

var projectNumberRe = regexp.MustCompile(`(?m)^(\s*project_number:\s*)(\d+)`)

func patchProjectNumber(path string, number int) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	updated := projectNumberRe.ReplaceAll(data, []byte(fmt.Sprintf("${1}%d", number)))
	if err := os.WriteFile(path, updated, 0o644); err != nil {
		fmt.Printf("Warning: could not update %s with project number: %v\n", path, err)
		return
	}
	fmt.Printf("Updated %s with project_number: %d\n", path, number)
}

func runHeadless(ctx context.Context, orch *orchestrator.Orchestrator) error {
	enc := json.NewEncoder(os.Stdout)
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-orch.Events():
			if !ok {
				return nil
			}
			logEntry := map[string]interface{}{
				"time":    event.Time.Format(time.RFC3339),
				"type":    event.Type,
				"message": event.Message,
			}
			if event.IssueNumber > 0 {
				logEntry["issue"] = event.IssueNumber
				logEntry["title"] = event.IssueTitle
			}
			if event.Phase != "" {
				logEntry["phase"] = event.Phase
			}
			if event.CostUSD > 0 {
				logEntry["cost_usd"] = event.CostUSD
			}
			_ = enc.Encode(logEntry)
		}
	}
}
