package agent

import (
	"encoding/json"
	"time"
)

// EventType represents the type of Claude stream event
type EventType string

const (
	EventInit       EventType = "init"
	EventMessage    EventType = "message"
	EventToolUse    EventType = "tool_use"
	EventToolResult EventType = "tool_result"
	EventResult     EventType = "result"
	EventError      EventType = "error"
	EventSystem     EventType = "system"
)

// StreamEvent represents a parsed NDJSON event from Claude CLI
type StreamEvent struct {
	Type      EventType `json:"type"`
	Timestamp time.Time `json:"timestamp"`
	SessionID string    `json:"session_id,omitempty"`

	// For message events
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`

	// For tool events
	ToolName string `json:"tool_name,omitempty"`
	ToolID   string `json:"tool_id,omitempty"`

	// For result events
	CostUSD    float64 `json:"cost_usd,omitempty"`
	DurationMS int64   `json:"duration_ms,omitempty"`
	NumTurns   int     `json:"num_turns,omitempty"`

	// For error events
	Error string `json:"error,omitempty"`

	// Raw JSON for debugging
	Raw json.RawMessage `json:"-"`
}
