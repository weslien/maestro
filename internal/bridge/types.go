package bridge

import (
	"encoding/json"
	"time"
)

// EventEnvelope wraps a GitHub webhook event with metadata for streaming.
type EventEnvelope struct {
	EventType   string          `json:"event_type"`             // "projects_v2_item", "projects_v2"
	Action      string          `json:"action"`                 // "created", "edited", etc.
	DeliveryID  string          `json:"delivery_id"`            // X-GitHub-Delivery header
	Timestamp   time.Time       `json:"timestamp"`              // when the event was received
	ProjectID   string          `json:"project_id"`             // project_node_id
	ContentID   string          `json:"content_id,omitempty"`   // content_node_id (for item events)
	ContentType string          `json:"content_type,omitempty"` // "Issue", "PullRequest", "DraftIssue"
	Enrichment  json.RawMessage `json:"enrichment,omitempty"`   // GraphQL-fetched field values
	RawPayload  json.RawMessage `json:"raw_payload"`            // original webhook payload
}

// FieldValue represents an enriched field value from GraphQL.
type FieldValue struct {
	FieldName string `json:"field_name"`
	Value     string `json:"value"`
}
