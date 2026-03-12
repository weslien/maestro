package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// RunResult contains the outcome of a Claude run
type RunResult struct {
	SessionID  string
	CostUSD    float64
	DurationMS int64
	NumTurns   int
	Success    bool
	Error      string
}

// ClaudeRunner manages Claude CLI invocations
type ClaudeRunner struct {
	model          string
	permissionMode string
	sessionID      string
	workDir        string
	logFile        string

	mu      sync.Mutex
	cost    float64
	running bool
	cancel  context.CancelFunc
}

// NewClaudeRunner creates a runner for a specific issue workspace
func NewClaudeRunner(model, permissionMode, workDir, sessionID string) *ClaudeRunner {
	return &ClaudeRunner{
		model:          model,
		permissionMode: permissionMode,
		workDir:        workDir,
		sessionID:      sessionID,
		logFile:        filepath.Join(workDir, ".maestro", "claude.log"),
	}
}

// Run executes Claude with the given prompt, streaming events to the channel
func (cr *ClaudeRunner) Run(ctx context.Context, prompt string, events chan<- StreamEvent) (*RunResult, error) {
	cr.mu.Lock()
	if cr.running {
		cr.mu.Unlock()
		return nil, fmt.Errorf("runner already active")
	}
	cr.running = true
	cr.mu.Unlock()

	defer func() {
		cr.mu.Lock()
		cr.running = false
		cr.mu.Unlock()
	}()

	ctx, cancel := context.WithCancel(ctx)
	cr.mu.Lock()
	cr.cancel = cancel
	cr.mu.Unlock()
	defer cancel()

	args := []string{
		"--output-format", "stream-json",
		"--verbose",
		"--model", cr.model,
		"--max-turns", "200",
	}

	if cr.sessionID != "" {
		args = append(args, "--session-id", cr.sessionID)
	}

	if cr.permissionMode != "" {
		switch cr.permissionMode {
		case "dangerously-skip-permissions":
			args = append(args, "--dangerously-skip-permissions")
		default:
			args = append(args, "--permission-mode", cr.permissionMode)
		}
	}

	args = append(args, "--print", prompt)

	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = cr.workDir

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Open log file for writing
	logDir := filepath.Dir(cr.logFile)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create log dir: %w", err)
	}
	logF, err := os.Create(cr.logFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}
	defer logF.Close()

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start claude: %w", err)
	}

	result := &RunResult{
		SessionID: cr.sessionID,
	}

	// Parse NDJSON stream
	cr.parseStream(stdout, logF, events, result)

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		result.Error = err.Error()
		return result, nil
	}

	result.Success = result.Error == ""
	return result, nil
}

func (cr *ClaudeRunner) parseStream(r io.Reader, logW io.Writer, events chan<- StreamEvent, result *RunResult) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB buffer

	for scanner.Scan() {
		line := scanner.Bytes()

		// Write to log file
		_, _ = logW.Write(line)
		_, _ = logW.Write([]byte("\n"))

		var raw map[string]interface{}
		if err := json.Unmarshal(line, &raw); err != nil {
			continue
		}

		event := StreamEvent{
			Timestamp: time.Now(),
			Raw:       json.RawMessage(line),
		}

		if t, ok := raw["type"].(string); ok {
			event.Type = EventType(t)
		}

		switch event.Type {
		case EventInit, "system":
			if sid, ok := raw["session_id"].(string); ok {
				event.SessionID = sid
				result.SessionID = sid
				cr.sessionID = sid
			}
		case EventMessage, "assistant":
			if content, ok := raw["content"].(string); ok {
				event.Content = content
			}
			// Handle content blocks array
			if blocks, ok := raw["content"].([]interface{}); ok {
				for _, b := range blocks {
					if block, ok := b.(map[string]interface{}); ok {
						if text, ok := block["text"].(string); ok {
							event.Content += text
						}
					}
				}
			}
		case EventToolUse:
			if name, ok := raw["name"].(string); ok {
				event.ToolName = name
			}
			if name, ok := raw["tool"].(string); ok {
				event.ToolName = name
			}
		case EventResult:
			if cost, ok := raw["cost_usd"].(float64); ok {
				event.CostUSD = cost
				result.CostUSD = cost
				cr.mu.Lock()
				cr.cost = cost
				cr.mu.Unlock()
			}
			if dur, ok := raw["duration_ms"].(float64); ok {
				event.DurationMS = int64(dur)
				result.DurationMS = int64(dur)
			}
			if turns, ok := raw["num_turns"].(float64); ok {
				event.NumTurns = int(turns)
				result.NumTurns = int(turns)
			}
		case EventError:
			if msg, ok := raw["error"].(string); ok {
				event.Error = msg
				result.Error = msg
			}
			if body, ok := raw["body"].(map[string]interface{}); ok {
				if msg, ok := body["msg"].(string); ok {
					event.Error = msg
					result.Error = msg
				}
			}
		}

		if events != nil {
			select {
			case events <- event:
			default:
				// Drop event if channel is full
			}
		}
	}
}

// Stop cancels the running Claude process
func (cr *ClaudeRunner) Stop() {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	if cr.cancel != nil {
		cr.cancel()
	}
}

// Cost returns the accumulated cost
func (cr *ClaudeRunner) Cost() float64 {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.cost
}

// IsRunning returns whether a Claude process is active
func (cr *ClaudeRunner) IsRunning() bool {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.running
}

// GetSessionID returns the current session ID
func (cr *ClaudeRunner) GetSessionID() string {
	cr.mu.Lock()
	defer cr.mu.Unlock()
	return cr.sessionID
}

// LogFile returns the path to the log file
func (cr *ClaudeRunner) LogFile() string {
	return cr.logFile
}
