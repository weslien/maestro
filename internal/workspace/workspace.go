package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Manager struct {
	root    string
	repoDir string // the main repo directory (for git worktree add)
	hooks   struct {
		afterCreate string
	}
}

func NewManager(root string, afterCreateHook string) *Manager {
	// Expand ~ to home dir
	if strings.HasPrefix(root, "~/") {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, root[2:])
	}

	// Detect repo root from worktree root path
	// .maestro/worktrees -> repo root is two dirs up
	repoDir := filepath.Dir(filepath.Dir(root))

	return &Manager{
		root:    root,
		repoDir: repoDir,
		hooks:   struct{ afterCreate string }{afterCreate: afterCreateHook},
	}
}

// Ensure creates a git worktree for an issue on a new branch
func (m *Manager) Ensure(ctx context.Context, issueNumber int) (string, error) {
	dir := m.Dir(issueNumber)

	if _, err := os.Stat(dir); err == nil {
		// Already exists
		return dir, nil
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", fmt.Errorf("failed to create worktrees dir: %w", err)
	}

	// Create a git worktree with a new branch
	branch := fmt.Sprintf("maestro/issue-%d", issueNumber)
	cmd := exec.CommandContext(ctx, "git", "worktree", "add", "-b", branch, dir)
	cmd.Dir = m.repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		// Branch might already exist, try without -b
		cmd2 := exec.CommandContext(ctx, "git", "worktree", "add", dir, branch)
		cmd2.Dir = m.repoDir
		if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
			return "", fmt.Errorf("failed to create worktree: %s\n%s", string(out), string(out2))
		}
	}

	// Create .maestro artifacts dir inside worktree
	artifactsDir := filepath.Join(dir, ".maestro")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create artifacts dir: %w", err)
	}

	// Run after_create hook
	if m.hooks.afterCreate != "" {
		hookCmd := exec.CommandContext(ctx, "sh", "-c", m.hooks.afterCreate)
		hookCmd.Dir = dir
		hookCmd.Stdout = os.Stdout
		hookCmd.Stderr = os.Stderr
		if err := hookCmd.Run(); err != nil {
			return "", fmt.Errorf("after_create hook failed: %w", err)
		}
	}

	return dir, nil
}

// Dir returns the workspace directory path for an issue
func (m *Manager) Dir(issueNumber int) string {
	return filepath.Join(m.root, fmt.Sprintf("issue-%d", issueNumber))
}

// ArtifactPath returns the path for a phase artifact file
func (m *Manager) ArtifactPath(issueNumber int, filename string) string {
	return filepath.Join(m.Dir(issueNumber), ".maestro", filename)
}

// WriteArtifact writes content to a phase artifact file
func (m *Manager) WriteArtifact(issueNumber int, filename, content string) error {
	path := m.ArtifactPath(issueNumber, filename)
	return os.WriteFile(path, []byte(content), 0o644)
}

// ReadArtifact reads a phase artifact file
func (m *Manager) ReadArtifact(issueNumber int, filename string) (string, error) {
	path := m.ArtifactPath(issueNumber, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Cleanup removes a worktree and its branch
func (m *Manager) Cleanup(issueNumber int) error {
	dir := m.Dir(issueNumber)

	// Remove the git worktree
	cmd := exec.Command("git", "worktree", "remove", "--force", dir)
	cmd.Dir = m.repoDir
	_ = cmd.Run()

	// Clean up if dir still exists
	return os.RemoveAll(dir)
}
