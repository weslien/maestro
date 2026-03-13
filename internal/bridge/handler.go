package bridge

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// Enricher fetches additional field values for a content node.
type Enricher interface {
	Enrich(ctx context.Context, contentNodeID string) ([]FieldValue, error)
}

// Publisher sends event envelopes to a downstream system.
type Publisher interface {
	Publish(ctx context.Context, envelope EventEnvelope) error
	Close() error
}

// WebhookHandler receives GitHub webhook events, validates them,
// optionally enriches them, and publishes to a downstream system.
type WebhookHandler struct {
	secret    []byte
	enricher  Enricher
	publisher Publisher
}

// NewWebhookHandler creates a handler that validates webhook signatures,
// parses Projects V2 events, and publishes them.
func NewWebhookHandler(secret []byte, enricher Enricher, publisher Publisher) *WebhookHandler {
	return &WebhookHandler{
		secret:    secret,
		enricher:  enricher,
		publisher: publisher,
	}
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Validate signature
	sig := r.Header.Get("X-Hub-Signature-256")
	if sig == "" {
		http.Error(w, "missing signature", http.StatusUnauthorized)
		return
	}
	if !validateSignature(body, sig, h.secret) {
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	deliveryID := r.Header.Get("X-GitHub-Delivery")

	if eventType != "projects_v2_item" && eventType != "projects_v2" {
		// Not a Projects V2 event, acknowledge but ignore.
		w.WriteHeader(http.StatusOK)
		return
	}

	envelope, err := h.parseEvent(r.Context(), eventType, deliveryID, body)
	if err != nil {
		log.Printf("bridge: failed to parse event %s: %v", deliveryID, err)
		http.Error(w, "failed to parse event", http.StatusBadRequest)
		return
	}

	if err := h.publisher.Publish(r.Context(), envelope); err != nil {
		log.Printf("bridge: failed to publish event %s: %v", deliveryID, err)
		http.Error(w, "failed to publish event", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (h *WebhookHandler) parseEvent(ctx context.Context, eventType, deliveryID string, body []byte) (EventEnvelope, error) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return EventEnvelope{}, fmt.Errorf("failed to unmarshal payload: %w", err)
	}

	var action string
	if raw, ok := payload["action"]; ok {
		if err := json.Unmarshal(raw, &action); err != nil {
			return EventEnvelope{}, fmt.Errorf("failed to parse action: %w", err)
		}
	}

	env := EventEnvelope{
		EventType:  eventType,
		Action:     action,
		DeliveryID: deliveryID,
		Timestamp:  time.Now(),
		RawPayload: body,
	}

	switch eventType {
	case "projects_v2_item":
		item, err := parseItemPayload(payload)
		if err != nil {
			return EventEnvelope{}, err
		}
		env.ProjectID = item.ProjectNodeID
		env.ContentID = item.ContentNodeID
		env.ContentType = item.ContentType

		// Enrich edited events with field values
		if action == "edited" && h.enricher != nil && item.ContentNodeID != "" {
			fields, err := h.enricher.Enrich(ctx, item.ContentNodeID)
			if err != nil {
				log.Printf("bridge: enrichment failed for %s: %v", item.ContentNodeID, err)
			} else if len(fields) > 0 {
				enrichJSON, err := json.Marshal(fields)
				if err == nil {
					env.Enrichment = enrichJSON
				}
			}
		}

	case "projects_v2":
		projectID, err := parseProjectPayload(payload)
		if err != nil {
			return EventEnvelope{}, err
		}
		env.ProjectID = projectID
	}

	return env, nil
}

type itemFields struct {
	ProjectNodeID string
	ContentNodeID string
	ContentType   string
}

func parseItemPayload(payload map[string]json.RawMessage) (itemFields, error) {
	raw, ok := payload["projects_v2_item"]
	if !ok {
		return itemFields{}, fmt.Errorf("missing projects_v2_item field")
	}

	var item struct {
		ProjectNodeID string `json:"project_node_id"`
		ContentNodeID string `json:"content_node_id"`
		ContentType   string `json:"content_type"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return itemFields{}, fmt.Errorf("failed to parse projects_v2_item: %w", err)
	}

	return itemFields{
		ProjectNodeID: item.ProjectNodeID,
		ContentNodeID: item.ContentNodeID,
		ContentType:   item.ContentType,
	}, nil
}

func parseProjectPayload(payload map[string]json.RawMessage) (string, error) {
	raw, ok := payload["projects_v2"]
	if !ok {
		return "", fmt.Errorf("missing projects_v2 field")
	}

	var project struct {
		NodeID string `json:"node_id"`
	}
	if err := json.Unmarshal(raw, &project); err != nil {
		return "", fmt.Errorf("failed to parse projects_v2: %w", err)
	}

	return project.NodeID, nil
}

func validateSignature(payload []byte, signature string, secret []byte) bool {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}
