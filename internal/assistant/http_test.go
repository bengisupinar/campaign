package assistant

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTP(t *testing.T) {
	model := &scripted{replies: []func([]message) (message, error){
		say("Hello!"),
		fail(&modelError{code: "assistant_busy", status: 429}),
		fail(&modelError{code: "assistant_timeout"}),
	}}
	svc := newTestService(model, &fakeCampaigns{})
	open := svc.store.addApproval(approval{conversationID: "c", campaignID: 7, created: now})
	closed := svc.store.addApproval(approval{conversationID: "c", campaignID: 8, created: now})
	if _, err := svc.store.closeApproval(closed, now); err != nil {
		t.Fatal(err)
	}
	api := NewHandler(svc, slog.New(slog.DiscardHandler))

	tests := []struct {
		name, method, path, body string
		status                   int
		want                     string // a substring of the response body
	}{
		{"a message", http.MethodPost, "/messages", `{"message": "Hi", "time_zone": "Europe/Istanbul"}`, http.StatusOK, `"reply":"Hello!"`},
		{"the model is busy", http.MethodPost, "/messages", `{"message": "Hi"}`, http.StatusServiceUnavailable, `"error":"assistant_busy"`},
		{"the model is too slow", http.MethodPost, "/messages", `{"message": "Hi"}`, http.StatusGatewayTimeout, `"error":"assistant_timeout"`},
		{"an empty message", http.MethodPost, "/messages", `{"message": " "}`, http.StatusUnprocessableEntity, `"error":"invalid_message"`},
		{"a message too long", http.MethodPost, "/messages", `{"message": "` + strings.Repeat("a", maxMessageLength+1) + `"}`, http.StatusUnprocessableEntity, `"error":"invalid_message"`},
		{"an unknown conversation", http.MethodPost, "/messages", `{"conversation_id": "gone", "message": "Hi"}`, http.StatusNotFound, `"error":"conversation_not_found"`},
		{"malformed JSON", http.MethodPost, "/messages", `{"message":`, http.StatusBadRequest, `"error":"malformed_json"`},
		{"an unknown field", http.MethodPost, "/messages", `{"message": "Hi", "role": "system"}`, http.StatusBadRequest, `"error":"malformed_json"`},
		{"an invalid decision", http.MethodPost, "/approvals/" + open, `{"decision": "yes"}`, http.StatusUnprocessableEntity, `"error":"invalid_decision"`},
		{"an unknown request", http.MethodPost, "/approvals/nope", `{"decision": "approve"}`, http.StatusNotFound, `"error":"approval_not_found"`},
		{"a decided request", http.MethodPost, "/approvals/" + closed, `{"decision": "approve"}`, http.StatusConflict, `"error":"approval_closed"`},
		{"an approved campaign that is gone", http.MethodPost, "/approvals/" + open, `{"decision": "approve"}`, http.StatusNotFound, `"error":"not_found"`},
		{"an unknown path", http.MethodGet, "/conversations", "", http.StatusNotFound, `"error":"not_found"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			api.ServeHTTP(rec, req)
			if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.want) {
				t.Errorf("%s %s = %d %s, want %d with %s", tt.method, tt.path, rec.Code, rec.Body, tt.status, tt.want)
			}
		})
	}

	// A successful turn returns its conversation, to continue it.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/messages", strings.NewReader(`{"message": "Hi"}`))
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	var turn Turn
	if err := json.Unmarshal(rec.Body.Bytes(), &turn); err != nil || turn.ConversationID == "" || turn.Actions == nil {
		t.Errorf("turn = %s, want a conversation ID and an action list", rec.Body)
	}

	// When every conversation is answering, a new one waits.
	full := newTestService(&scripted{}, &fakeCampaigns{})
	for range maxConversations {
		if _, err := full.store.open("", now); err != nil {
			t.Fatal(err)
		}
	}
	req = httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/messages", strings.NewReader(`{"message": "Hi"}`))
	rec = httptest.NewRecorder()
	NewHandler(full, slog.New(slog.DiscardHandler)).ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), `"error":"assistant_busy"`) {
		t.Errorf("a new conversation in a full store = %d %s, want 503 assistant_busy", rec.Code, rec.Body)
	}
}
