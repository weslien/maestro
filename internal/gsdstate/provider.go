package gsdstate

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Provider fetches and caches GSD state from stclaude CLI
type Provider struct {
	repoDir string
	ttl     time.Duration

	mu       sync.Mutex
	cached   *State
	cachedAt time.Time
}

// NewProvider creates a Provider that caches state for the given TTL
func NewProvider(repoDir string, ttl time.Duration) *Provider {
	return &Provider{
		repoDir: repoDir,
		ttl:     ttl,
	}
}

// Get returns the current GSD state, using cache if fresh
func (p *Provider) Get(ctx context.Context) (*State, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cached != nil && time.Since(p.cachedAt) < p.ttl {
		return p.cached, nil
	}

	state, err := p.fetch(ctx)
	if err != nil {
		return nil, err
	}

	p.cached = state
	p.cachedAt = time.Now()
	return state, nil
}

// Refresh forces a fresh fetch regardless of cache
func (p *Provider) Refresh(ctx context.Context) (*State, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, err := p.fetch(ctx)
	if err != nil {
		return nil, err
	}

	p.cached = state
	p.cachedAt = time.Now()
	return state, nil
}

func (p *Provider) fetch(ctx context.Context) (*State, error) {
	cmd := exec.CommandContext(ctx, "stclaude", "get-state", "--json")
	cmd.Dir = p.repoDir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("stclaude get-state failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("stclaude not found — install stgsd or ensure stclaude is in PATH: %w", err)
	}

	// stclaude may emit info lines before JSON
	jsonStart := strings.IndexByte(string(out), '{')
	if jsonStart < 0 {
		return nil, fmt.Errorf("stclaude output contains no JSON: %s", string(out))
	}
	jsonBytes := out[jsonStart:]

	var state State
	if err := json.Unmarshal(jsonBytes, &state); err != nil {
		return nil, fmt.Errorf("failed to parse stclaude output: %w", err)
	}
	if !state.OK {
		return nil, fmt.Errorf("stclaude returned error state")
	}
	return &state, nil
}
