package segmentation

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestHTTP(t *testing.T) {
	s := newShop(t)
	api := NewHandler(s.svc, func() time.Time { return asOf }, slog.New(slog.DiscardHandler))
	tests := []struct {
		name         string
		method, path string
		body         string
		status       int
		want         string
	}{
		{"options list what a segment can name", http.MethodGet, "/options", "", http.StatusOK, `{
			"countries": [{"code": "DE", "name": "Germany"}, {"code": "TR", "name": "Türkiye"}],
			"cities": [{"name": "Berlin", "country": "DE"}, {"name": "Ankara", "country": "TR"}, {"name": "İstanbul", "country": "TR"}],
			"categories": ["Books", "Electronics"],
			"languages": ["tr", "de", "en"]}`},
		{"an estimate counts both channels", http.MethodPost, "/audience",
			`{"segment": {"version": 1, "match": "all", "conditions": []}}`, http.StatusOK,
			`{"matched": 6, "reachable": 5, "by_channel": {"email": 4, "sms": 3}}`},
		{"an estimate applies the segment", http.MethodPost, "/audience",
			`{"segment": {"version": 1, "match": "all", "conditions": [{"type": "country", "in": ["DE"]}]}}`, http.StatusOK,
			`{"matched": 2, "reachable": 1, "by_channel": {"email": 1, "sms": 0}}`},
		{"a structural problem", http.MethodPost, "/audience",
			`{"segment": {"version": 1, "match": "all", "conditions": [{"type": "city"}]}}`, http.StatusUnprocessableEntity,
			`{"error": "invalid_segment", "problems": [{"field": "segment.conditions[0].in", "problem": "required"}]}`},
		{"an unknown name", http.MethodPost, "/audience",
			`{"segment": {"version": 1, "match": "all", "conditions": [{"type": "city", "in": ["Istanbul"]}]}}`, http.StatusUnprocessableEntity,
			`{"error": "invalid_segment", "problems": [{"field": "segment.conditions[0].in[0]", "problem": "unknown", "detail": "Istanbul"}]}`},
		{"no segment", http.MethodPost, "/audience", `{}`, http.StatusUnprocessableEntity,
			`{"error": "invalid_segment", "problems": [{"field": "segment", "problem": "required"}]}`},
		{"malformed JSON", http.MethodPost, "/audience", `{"segment":`, http.StatusBadRequest, `{"error": "malformed_json"}`},
		{"an unknown field", http.MethodPost, "/audience", `{"segment": {}, "channels": ["sms"]}`, http.StatusBadRequest,
			`{"error": "malformed_json"}`},
		{"estimates are posted", http.MethodGet, "/audience", "", http.StatusMethodNotAllowed, `{"error": "method_not_allowed"}`},
		{"an unknown path", http.MethodGet, "/recipients", "", http.StatusNotFound, `{"error": "not_found"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, strings.NewReader(tt.body))
			rec := httptest.NewRecorder()
			api.ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.status, rec.Body)
			}
			var got, want any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("response body %q: %v", rec.Body, err)
			}
			if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatalf("expected JSON: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("body = %s\nwant %s", rec.Body, tt.want)
			}
		})
	}
}
