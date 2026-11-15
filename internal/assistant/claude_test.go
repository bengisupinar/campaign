package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// fakeAnthropic stands in for the Messages API: it records each request and answers with the
// next scripted response.
type fakeAnthropic struct {
	requests  []map[string]any
	apiKeys   []string // the Authorization header: the SDK sends the key as a bearer token
	responses []func(w http.ResponseWriter)
}

func (f *fakeAnthropic) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req map[string]any
	_ = json.Unmarshal(body, &req)
	f.requests = append(f.requests, req)
	f.apiKeys = append(f.apiKeys, r.Header.Get("Authorization"))
	next := f.responses[0]
	f.responses = f.responses[1:]
	next(w)
}

func respond(status int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Request-Id", "req_test")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

const toolUseResponse = `{"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-haiku-5-5",
	"content": [{"type": "text", "text": "Let me look."}, {"type": "tool_use", "id": "toolu_1", "name": "list_campaigns", "input": {}}],
	"stop_reason": "tool_use", "usage": {"input_tokens": 10, "output_tokens": 5}}`

const textResponse = `{"id": "msg_2", "type": "message", "role": "assistant", "model": "claude-haiku-5-5",
	"content": [{"type": "text", "text": "You have no campaigns yet."}],
	"stop_reason": "end_turn", "usage": {"input_tokens": 20, "output_tokens": 7}}`

func newFakeClaude(t *testing.T, fake *fakeAnthropic) Model {
	t.Helper()
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	model, err := NewClaude("test-key", "claude-haiku-5-5", option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	if err != nil {
		t.Fatalf("NewClaude(): %v", err)
	}
	return model
}

func TestClaudeToolLoop(t *testing.T) {
	fake := &fakeAnthropic{responses: []func(http.ResponseWriter){respond(200, toolUseResponse), respond(200, textResponse)}}
	model := newFakeClaude(t, fake)

	history := []message{{text: "What campaigns do I have?"}}
	first, err := model.reply(t.Context(), "You are a test.", history)
	if err != nil {
		t.Fatalf("reply(): %v", err)
	}
	if first.text != "Let me look." || len(first.calls) != 1 || first.calls[0].ID != "toolu_1" || first.calls[0].Name != "list_campaigns" {
		t.Fatalf("reply = %+v, want the text and the tool call", first)
	}
	history = append(history, first, message{results: []toolResult{{CallID: "toolu_1", Content: `{"campaigns":[]}`}}})
	second, err := model.reply(t.Context(), "You are a test.", history)
	if err != nil || second.text != "You have no campaigns yet." || len(second.calls) != 0 {
		t.Fatalf("second reply = %+v, %v", second, err)
	}

	req := fake.requests[0]
	if fake.apiKeys[0] != "Bearer test-key" || req["model"] != "claude-haiku-5-5" || req["max_tokens"] != float64(claudeMaxTokens) {
		t.Errorf("request key %q, model %v, max_tokens %v", fake.apiKeys[0], req["model"], req["max_tokens"])
	}
	if system := req["system"].([]any)[0].(map[string]any); system["text"] != "You are a test." {
		t.Errorf("system = %v", system)
	}
	tools := req["tools"].([]any)
	var names []string
	for _, tool := range tools {
		tool := tool.(map[string]any)
		names = append(names, tool["name"].(string))
		schema := tool["input_schema"].(map[string]any)
		if schema["additionalProperties"] != false || schema["$defs"] == nil {
			t.Errorf("%s's schema = %v, want closed objects and the shared definitions", tool["name"], schema)
		}
	}
	if len(names) != 6 {
		t.Errorf("tools sent = %v, want the six", names)
	}

	// The second request sends Claude's message back as it was, and the tool's result.
	messages := fake.requests[1]["messages"].([]any)
	assistant := messages[1].(map[string]any)
	results := messages[2].(map[string]any)["content"].([]any)
	if assistant["role"] != "assistant" || len(assistant["content"].([]any)) != 2 {
		t.Errorf("assistant message sent back = %v", assistant)
	}
	if result := results[0].(map[string]any); result["type"] != "tool_result" || result["tool_use_id"] != "toolu_1" {
		t.Errorf("tool result sent = %v", result)
	}
}

func TestClaudeFailures(t *testing.T) {
	tests := []struct {
		name     string
		response func(http.ResponseWriter)
		timeout  time.Duration
		wantCode string
	}{
		{"rate limited", respond(429, `{"type": "error", "error": {"type": "rate_limit_error", "message": "slow down"}}`), 0, "assistant_busy"},
		{"overloaded", respond(529, `{"type": "error", "error": {"type": "overloaded_error", "message": "busy"}}`), 0, "assistant_busy"},
		{"server error", respond(500, `{"type": "error", "error": {"type": "api_error", "message": "oops"}}`), 0, "assistant_unavailable"},
		{"bad key", respond(401, `{"type": "error", "error": {"type": "authentication_error", "message": "invalid x-api-key"}}`), 0, "assistant_unavailable"},
		{"too slow", func(http.ResponseWriter) { time.Sleep(300 * time.Millisecond) }, 50 * time.Millisecond, "assistant_timeout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := newFakeClaude(t, &fakeAnthropic{responses: []func(http.ResponseWriter){tt.response}})
			ctx := t.Context()
			if tt.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tt.timeout)
				defer cancel()
			}
			_, err := model.reply(ctx, "system", []message{{text: "hi"}})
			var me *modelError
			if !errors.As(err, &me) || me.code != tt.wantCode {
				t.Fatalf("reply() error = %v, want %s", err, tt.wantCode)
			}
			if tt.timeout == 0 && me.requestID != "req_test" {
				t.Errorf("request ID = %q, want the API's", me.requestID)
			}
		})
	}
}
