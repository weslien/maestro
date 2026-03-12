package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type TmuxManager struct {
	prefix string
}

func NewTmuxManager(prefix string) *TmuxManager {
	return &TmuxManager{prefix: prefix}
}

// SessionName returns the tmux session name for an issue
func (tm *TmuxManager) SessionName(issueNumber int) string {
	return fmt.Sprintf("%s-%d", tm.prefix, issueNumber)
}

// CreateSession creates a tmux session that tails the log file
func (tm *TmuxManager) CreateSession(ctx context.Context, issueNumber int, logFile string) error {
	name := tm.SessionName(issueNumber)

	// Check if session already exists
	if tm.SessionExists(ctx, name) {
		return nil
	}

	cmd := exec.CommandContext(ctx, "tmux", "new-session", "-d", "-s", name,
		"tail", "-f", logFile)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create tmux session %s: %w", name, err)
	}

	return nil
}

// DestroySession kills a tmux session
func (tm *TmuxManager) DestroySession(ctx context.Context, issueNumber int) error {
	name := tm.SessionName(issueNumber)
	cmd := exec.CommandContext(ctx, "tmux", "kill-session", "-t", name)
	_ = cmd.Run() // Ignore error if session doesn't exist
	return nil
}

// SessionExists checks if a tmux session exists
func (tm *TmuxManager) SessionExists(ctx context.Context, name string) bool {
	cmd := exec.CommandContext(ctx, "tmux", "has-session", "-t", name)
	return cmd.Run() == nil
}

// ListSessions returns all maestro tmux sessions
func (tm *TmuxManager) ListSessions(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "tmux", "list-sessions", "-F", "#{session_name}")
	out, err := cmd.Output()
	if err != nil {
		// tmux returns error if no sessions exist
		return nil, nil
	}

	var sessions []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.HasPrefix(line, tm.prefix+"-") {
			sessions = append(sessions, line)
		}
	}

	return sessions, nil
}

// CleanupOrphaned destroys any maestro sessions that don't have running agents
func (tm *TmuxManager) CleanupOrphaned(ctx context.Context) error {
	sessions, err := tm.ListSessions(ctx)
	if err != nil {
		return err
	}

	for _, s := range sessions {
		cmd := exec.CommandContext(ctx, "tmux", "kill-session", "-t", s)
		_ = cmd.Run()
	}

	return nil
}

// CreateInteractiveSession creates a tmux session running an interactive command
func (tm *TmuxManager) CreateInteractiveSession(ctx context.Context, name string, command []string) error {
	if tm.SessionExists(ctx, name) {
		return nil
	}

	args := []string{"new-session", "-d", "-s", name}
	args = append(args, command...)
	cmd := exec.CommandContext(ctx, "tmux", args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create interactive tmux session %s: %w", name, err)
	}

	return nil
}
