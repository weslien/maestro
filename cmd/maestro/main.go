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
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/weslien/maestro/internal/agent"
	"github.com/weslien/maestro/internal/bridge"
	"github.com/weslien/maestro/internal/config"
	"github.com/weslien/maestro/internal/gsdstate"
	"github.com/weslien/maestro/internal/lifecycle"
	"github.com/weslien/maestro/internal/orchestrator"
	"github.com/weslien/maestro/internal/seed"
	"github.com/weslien/maestro/internal/tracker"
	"github.com/weslien/maestro/internal/tui"
)

var (
	cfgFile string

	// Set via ldflags at build time:
	//   go build -ldflags "-X main.version=v0.1.0 -X main.commit=abc123 -X main.date=2024-01-01"
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "maestro",
		Short:   "GSD-powered GitHub Projects orchestrator",
		Long:    "Maestro polls GitHub Projects V2 for issues and runs each through a GSD-style lifecycle using Claude Code agents.",
		Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date),
	}

	rootCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "WORKFLOW.md", "path to WORKFLOW.md config file")

	rootCmd.AddCommand(
		initCmd(),
		setupCmd(),
		seedCmd(),
		runCmd(),
		statusCmd(),
		stopCmd(),
		bridgeCmd(),
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
	var template string

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Ensure a GitHub Project exists with required fields and issue types",
		Long: `If project_number is set in WORKFLOW.md, ensures that project has
the correct status fields and issue types (Milestone, Phase, Task).
Otherwise creates a new project and updates WORKFLOW.md with the project number.

Use --template to copy from a template project that includes pre-configured views.
Pass --template=maestro to use the built-in GSD template, or pass a project node ID.`,
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
				if err := trk.EnsurePhaseField(cmd.Context(), statuses); err != nil {
					return err
				}
				fmt.Println("Project statuses configured.")
			} else if template != "" {
				// Copy from template project
				sourceID := template
				if sourceID == "maestro" {
					if tracker.TemplateProjectID == "" {
						return fmt.Errorf("built-in template project ID not configured yet — pass a project node ID instead")
					}
					sourceID = tracker.TemplateProjectID
				}

				projectTitle := fmt.Sprintf("Maestro: %s", cfg.Tracker.Repo)
				fmt.Printf("Copying template project to %q linked to %s...\n", projectTitle, cfg.Tracker.Repo)
				if err := trk.CopyProject(cmd.Context(), sourceID, projectTitle); err != nil {
					return err
				}
				fmt.Printf("Created project #%d (copied from template)\n", trk.ProjectNumber())

				if err := trk.EnsurePhaseField(cmd.Context(), statuses); err != nil {
					return err
				}

				// Auto-update WORKFLOW.md with the new project number
				if _, statErr := os.Stat(cfgFile); statErr == nil {
					patchProjectNumber(cfgFile, trk.ProjectNumber())
				}
			} else {
				// Create new blank project, named after the repo
				projectTitle := fmt.Sprintf("Maestro: %s", cfg.Tracker.Repo)
				fmt.Printf("Creating GitHub Project %q linked to %s...\n", projectTitle, cfg.Tracker.Repo)
				if err := trk.CreateProject(cmd.Context(), projectTitle); err != nil {
					return err
				}
				fmt.Printf("Created project #%d\n", trk.ProjectNumber())

				if err := trk.EnsurePhaseField(cmd.Context(), statuses); err != nil {
					return err
				}

				// Auto-update WORKFLOW.md with the new project number
				if _, statErr := os.Stat(cfgFile); statErr == nil {
					patchProjectNumber(cfgFile, trk.ProjectNumber())
				}
			}

			// Ensure issue types (Milestone, Phase, Task) exist at org level
			fmt.Println("Ensuring issue types...")
			if err := trk.EnsureIssueTypes(cmd.Context()); err != nil {
				fmt.Printf("  Warning: could not configure issue types: %v\n", err)
				fmt.Println("  (Issue types require org admin permissions)")
			} else {
				for _, t := range tracker.MaestroIssueTypes() {
					fmt.Printf("  ✓ %s\n", t)
				}
			}

			// Validate views and print warnings
			fmt.Println("\nValidating project views...")
			warnings, err := trk.ValidateViews(cmd.Context())
			if err != nil {
				fmt.Printf("  Warning: could not validate views: %v\n", err)
			} else if len(warnings) == 0 {
				fmt.Println("  All views configured correctly.")
			} else {
				for _, w := range warnings {
					fmt.Printf("  ⚠ %s\n", w)
				}
				fmt.Println("\n  Note: Views cannot be configured via API.")
				fmt.Println("  Open the project in GitHub and configure views manually,")
				fmt.Println("  or re-run with --template to copy from a pre-configured template.")
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&template, "template", "", "copy from a template project (use 'maestro' for built-in, or a project node ID)")
	return cmd
}

func seedCmd() *cobra.Command {
	var update bool

	cmd := &cobra.Command{
		Use:   "seed",
		Short: "Seed GitHub Project with GSD phases and plans from stclaude",
		Long: `Reads GSD project state via stclaude CLI and creates GitHub issues
for each phase and its plans on the project board.

Use --update to re-apply issue types, sub-issue relationships, and
dependencies to existing issues (idempotent).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}

			cwd, _ := os.Getwd()
			fmt.Printf("Reading GSD state from %s...\n", cwd)

			state, err := seed.ReadGSDState(cmd.Context(), cwd)
			if err != nil {
				return err
			}

			fmt.Printf("Project: %s\n", state.Data.Project.Name)
			fmt.Printf("Phases: %d, Plans: %d\n\n", len(state.Data.Phases), len(state.Data.Plans))

			trk := tracker.NewGitHubProjectTracker(cfg.Tracker.Owner, cfg.Tracker.Repo, cfg.Tracker.ProjectNumber)
			if err := trk.Init(cmd.Context()); err != nil {
				return fmt.Errorf("failed to initialize tracker: %w", err)
			}

			// Ensure issue types exist before seeding
			if err := trk.EnsureIssueTypes(cmd.Context()); err != nil {
				fmt.Printf("Warning: issue types not available: %v\n", err)
				fmt.Println("(Continuing without issue types — run 'maestro setup' with org admin permissions)")
			}

			// Spinner frames for animated progress
			frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
			frameIdx := 0
			spinnerDone := make(chan struct{})
			var currentStatus string
			var statusMu sync.Mutex

			// Spinner goroutine — updates the current line at 80ms intervals
			go func() {
				ticker := time.NewTicker(80 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-spinnerDone:
						return
					case <-ticker.C:
						statusMu.Lock()
						s := currentStatus
						statusMu.Unlock()
						if s != "" {
							fmt.Fprintf(os.Stderr, "\r\033[K  %s %s", frames[frameIdx%len(frames)], s)
							frameIdx++
						}
					}
				}
			}()

			onProgress := func(phase, total int, name, status string) {
				statusMu.Lock()
				switch status {
				case "done":
					currentStatus = ""
					statusMu.Unlock()
					fmt.Fprintf(os.Stderr, "\r\033[K")
					fmt.Printf("  ✓ [%d/%d] %s\n", phase, total, name)
				case "updated":
					currentStatus = ""
					statusMu.Unlock()
					fmt.Fprintf(os.Stderr, "\r\033[K")
					fmt.Printf("  ✓ [%d/%d] %s (updated)\n", phase, total, name)
				case "skipped":
					currentStatus = ""
					statusMu.Unlock()
					fmt.Fprintf(os.Stderr, "\r\033[K")
					fmt.Printf("  ~ [%d/%d] %s (exists)\n", phase, total, name)
				case "error":
					currentStatus = ""
					statusMu.Unlock()
					fmt.Fprintf(os.Stderr, "\r\033[K")
					fmt.Printf("  ✗ [%d/%d] %s\n", phase, total, name)
				default:
					currentStatus = fmt.Sprintf("[%d/%d] %s — %s", phase, total, name, status)
					statusMu.Unlock()
				}
			}

			result, err := seed.Seed(cmd.Context(), trk, state, cfg.Tracker.Repo, cwd, update, onProgress)
			close(spinnerDone)
			fmt.Fprintf(os.Stderr, "\r\033[K") // clear spinner line

			if err != nil {
				return err
			}

			for _, msg := range result.Errors {
				fmt.Printf("  ! %s\n", msg)
			}

			fmt.Printf("\nCreated: %d, Skipped: %d, Errors: %d\n",
				len(result.Created), len(result.Skipped), len(result.Errors))
			return nil
		},
	}

	cmd.Flags().BoolVar(&update, "update", false, "re-apply types, sub-issues, and dependencies to existing issues")
	return cmd
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
			cwd, _ := os.Getwd()
			gsd := gsdstate.NewProvider(cwd, 30*time.Second)
			app := tui.NewApp(orch, gsd)
			p := tea.NewProgram(app, tea.WithAltScreen())
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

func bridgeCmd() *cobra.Command {
	var (
		port          int
		webhookSecret string
		publisherType string
		outputFile    string
		iggyAddr      string
		streamName    string
	)

	cmd := &cobra.Command{
		Use:   "bridge",
		Short: "Start webhook-to-stream bridge",
		Long: `Starts an HTTP server that receives GitHub webhook events for Projects V2
and publishes them to a message stream (Iggy, file, or stdout).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if webhookSecret == "" {
				webhookSecret = os.Getenv("MAESTRO_WEBHOOK_SECRET")
			}
			if webhookSecret == "" {
				return fmt.Errorf("webhook secret is required (use --webhook-secret or MAESTRO_WEBHOOK_SECRET env)")
			}

			var pub bridge.Publisher
			switch publisherType {
			case "log":
				pub = bridge.NewLogPublisher()
			case "file":
				if outputFile == "" {
					return fmt.Errorf("--output is required when using file publisher")
				}
				fp, err := bridge.NewFilePublisher(outputFile)
				if err != nil {
					return err
				}
				pub = fp
			case "iggy":
				return fmt.Errorf("iggy publisher requires a running Iggy server at %s — not yet fully wired", iggyAddr)
			default:
				return fmt.Errorf("unknown publisher type %q (options: log, file, iggy)", publisherType)
			}

			b, err := bridge.New(
				bridge.WithAddr(fmt.Sprintf(":%d", port)),
				bridge.WithWebhookSecret(webhookSecret),
				bridge.WithPublisher(pub),
				bridge.WithEnricher(bridge.NewGHEnricher()),
			)
			if err != nil {
				return err
			}

			ctx, cancel := context.WithCancel(cmd.Context())
			defer cancel()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			go func() {
				<-sigCh
				fmt.Println("\nShutting down bridge...")
				cancel()
			}()

			return b.Run(ctx)
		},
	}

	cmd.Flags().IntVar(&port, "port", 8080, "HTTP listen port")
	cmd.Flags().StringVar(&webhookSecret, "webhook-secret", "", "GitHub webhook secret (or MAESTRO_WEBHOOK_SECRET env)")
	cmd.Flags().StringVar(&publisherType, "publisher", "log", "publisher type: log, file, iggy")
	cmd.Flags().StringVar(&outputFile, "output", "", "output file path (for file publisher)")
	cmd.Flags().StringVar(&iggyAddr, "iggy-addr", "localhost:8090", "Iggy server address")
	cmd.Flags().StringVar(&streamName, "stream", "github-events", "Iggy stream name")

	return cmd
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
