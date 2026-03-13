package bridge

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockPublisher records published envelopes.
type mockPublisher struct {
	envelopes []EventEnvelope
	err       error
}

func (m *mockPublisher) Publish(_ context.Context, env EventEnvelope) error {
	m.envelopes = append(m.envelopes, env)
	return m.err
}

func (m *mockPublisher) Close() error { return nil }

// mockEnricher returns canned field values.
type mockEnricher struct {
	fields []FieldValue
	err    error
	calls  []string // content node IDs passed to Enrich
}

func (m *mockEnricher) Enrich(_ context.Context, contentNodeID string) ([]FieldValue, error) {
	m.calls = append(m.calls, contentNodeID)
	return m.fields, m.err
}

func sign(payload, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func makeItemPayload(action, projectNodeID, contentNodeID, contentType string) []byte {
	p := map[string]interface{}{
		"action": action,
		"projects_v2_item": map[string]interface{}{
			"project_node_id": projectNodeID,
			"content_node_id": contentNodeID,
			"content_type":    contentType,
		},
	}
	data, _ := json.Marshal(p)
	return data
}

func makeProjectPayload(action, nodeID string) []byte {
	p := map[string]interface{}{
		"action": action,
		"projects_v2": map[string]interface{}{
			"node_id": nodeID,
		},
	}
	data, _ := json.Marshal(p)
	return data
}

func TestWebhookHandler(t *testing.T) {
	secret := []byte("test-secret")

	tests := []struct {
		name           string
		method         string
		eventType      string
		deliveryID     string
		body           []byte
		signature      string // override; empty means compute from body
		enricherFields []FieldValue
		wantCode       int
		wantEnvelopes  int
		wantAction     string
		wantProjectID  string
		wantContentID  string
		wantEnriched   bool
	}{
		{
			name:          "valid projects_v2_item created",
			method:        http.MethodPost,
			eventType:     "projects_v2_item",
			deliveryID:    "delivery-1",
			body:          makeItemPayload("created", "PVT_abc", "PVTI_123", "Issue"),
			wantCode:      http.StatusAccepted,
			wantEnvelopes: 1,
			wantAction:    "created",
			wantProjectID: "PVT_abc",
			wantContentID: "PVTI_123",
		},
		{
			name:       "valid projects_v2_item edited with enrichment",
			method:     http.MethodPost,
			eventType:  "projects_v2_item",
			deliveryID: "delivery-2",
			body:       makeItemPayload("edited", "PVT_abc", "PVTI_456", "Issue"),
			enricherFields: []FieldValue{
				{FieldName: "Status", Value: "In Progress"},
			},
			wantCode:      http.StatusAccepted,
			wantEnvelopes: 1,
			wantAction:    "edited",
			wantProjectID: "PVT_abc",
			wantContentID: "PVTI_456",
			wantEnriched:  true,
		},
		{
			name:          "valid projects_v2_item deleted",
			method:        http.MethodPost,
			eventType:     "projects_v2_item",
			deliveryID:    "delivery-3",
			body:          makeItemPayload("deleted", "PVT_abc", "PVTI_789", "PullRequest"),
			wantCode:      http.StatusAccepted,
			wantEnvelopes: 1,
			wantAction:    "deleted",
			wantProjectID: "PVT_abc",
			wantContentID: "PVTI_789",
		},
		{
			name:          "valid projects_v2 event",
			method:        http.MethodPost,
			eventType:     "projects_v2",
			deliveryID:    "delivery-4",
			body:          makeProjectPayload("created", "PVT_xyz"),
			wantCode:      http.StatusAccepted,
			wantEnvelopes: 1,
			wantAction:    "created",
			wantProjectID: "PVT_xyz",
		},
		{
			name:      "missing signature",
			method:    http.MethodPost,
			eventType: "projects_v2_item",
			body:      makeItemPayload("created", "PVT_abc", "PVTI_123", "Issue"),
			signature: "", // will skip setting header
			wantCode:  http.StatusUnauthorized,
		},
		{
			name:      "invalid signature",
			method:    http.MethodPost,
			eventType: "projects_v2_item",
			body:      makeItemPayload("created", "PVT_abc", "PVTI_123", "Issue"),
			signature: "sha256=0000000000000000000000000000000000000000000000000000000000000000",
			wantCode:  http.StatusForbidden,
		},
		{
			name:          "non-project event ignored",
			method:        http.MethodPost,
			eventType:     "push",
			deliveryID:    "delivery-5",
			body:          []byte(`{"ref":"refs/heads/main"}`),
			wantCode:      http.StatusOK,
			wantEnvelopes: 0,
		},
		{
			name:     "wrong method",
			method:   http.MethodGet,
			wantCode: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pub := &mockPublisher{}
			enr := &mockEnricher{fields: tt.enricherFields}
			handler := NewWebhookHandler(secret, enr, pub)

			req := httptest.NewRequest(tt.method, "/webhook", bytes.NewReader(tt.body))
			if tt.eventType != "" {
				req.Header.Set("X-GitHub-Event", tt.eventType)
			}
			if tt.deliveryID != "" {
				req.Header.Set("X-GitHub-Delivery", tt.deliveryID)
			}

			// Signature handling
			switch {
			case tt.signature != "":
				req.Header.Set("X-Hub-Signature-256", tt.signature)
			case tt.signature == "" && tt.name == "missing signature":
				// Don't set header
			default:
				req.Header.Set("X-Hub-Signature-256", sign(tt.body, secret))
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantCode {
				t.Errorf("status code = %d, want %d", rec.Code, tt.wantCode)
			}

			if len(pub.envelopes) != tt.wantEnvelopes {
				t.Fatalf("published %d envelopes, want %d", len(pub.envelopes), tt.wantEnvelopes)
			}

			if tt.wantEnvelopes == 0 {
				return
			}

			env := pub.envelopes[0]
			if env.Action != tt.wantAction {
				t.Errorf("action = %q, want %q", env.Action, tt.wantAction)
			}
			if env.ProjectID != tt.wantProjectID {
				t.Errorf("project_id = %q, want %q", env.ProjectID, tt.wantProjectID)
			}
			if env.ContentID != tt.wantContentID {
				t.Errorf("content_id = %q, want %q", env.ContentID, tt.wantContentID)
			}
			if tt.wantEnriched && env.Enrichment == nil {
				t.Error("expected enrichment data, got nil")
			}
			if !tt.wantEnriched && env.Enrichment != nil {
				t.Errorf("expected no enrichment, got %s", string(env.Enrichment))
			}
		})
	}
}

func TestValidateSignature(t *testing.T) {
	secret := []byte("my-secret")
	payload := []byte(`{"action":"created"}`)

	tests := []struct {
		name      string
		payload   []byte
		signature string
		secret    []byte
		want      bool
	}{
		{
			name:      "valid",
			payload:   payload,
			signature: sign(payload, secret),
			secret:    secret,
			want:      true,
		},
		{
			name:      "wrong secret",
			payload:   payload,
			signature: sign(payload, []byte("wrong-secret")),
			secret:    secret,
			want:      false,
		},
		{
			name:      "tampered payload",
			payload:   []byte(`{"action":"edited"}`),
			signature: sign(payload, secret),
			secret:    secret,
			want:      false,
		},
		{
			name:      "missing prefix",
			payload:   payload,
			signature: "bad-sig",
			secret:    secret,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := validateSignature(tt.payload, tt.signature, tt.secret)
			if got != tt.want {
				t.Errorf("validateSignature() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnricherCalledOnlyForEdited(t *testing.T) {
	secret := []byte("test-secret")
	pub := &mockPublisher{}
	enr := &mockEnricher{
		fields: []FieldValue{{FieldName: "Phase", Value: "Done"}},
	}
	handler := NewWebhookHandler(secret, enr, pub)

	// "created" should NOT call enricher
	body := makeItemPayload("created", "PVT_1", "PVTI_1", "Issue")
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "projects_v2_item")
	req.Header.Set("X-GitHub-Delivery", "d1")
	req.Header.Set("X-Hub-Signature-256", sign(body, secret))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if len(enr.calls) != 0 {
		t.Errorf("enricher called %d times for 'created', want 0", len(enr.calls))
	}

	// "edited" SHOULD call enricher
	body = makeItemPayload("edited", "PVT_1", "PVTI_2", "Issue")
	req = httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "projects_v2_item")
	req.Header.Set("X-GitHub-Delivery", "d2")
	req.Header.Set("X-Hub-Signature-256", sign(body, secret))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if len(enr.calls) != 1 {
		t.Fatalf("enricher called %d times for 'edited', want 1", len(enr.calls))
	}
	if enr.calls[0] != "PVTI_2" {
		t.Errorf("enricher called with %q, want %q", enr.calls[0], "PVTI_2")
	}

	// Verify the envelope has enrichment
	if len(pub.envelopes) != 2 {
		t.Fatalf("got %d envelopes, want 2", len(pub.envelopes))
	}
	if pub.envelopes[1].Enrichment == nil {
		t.Error("expected enrichment on edited event")
	}
}
