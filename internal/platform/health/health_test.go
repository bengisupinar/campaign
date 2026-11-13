package health_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bengisupinar/campaign/internal/platform/health"
)

func passing(context.Context) error { return nil }

func failing(context.Context) error {
	return errors.New("dial tcp 10.0.0.7:5432: connection refused")
}

func TestReady(t *testing.T) {
	tests := []struct {
		name       string
		checks     map[string]health.Check
		wantStatus int
		wantBody   string
	}{
		{
			name:       "no checks",
			checks:     nil,
			wantStatus: http.StatusOK,
			wantBody:   `{"status":"ok"}`,
		},
		{
			name:       "all checks pass",
			checks:     map[string]health.Check{"postgres": passing},
			wantStatus: http.StatusOK,
			wantBody:   `{"status":"ok","checks":{"postgres":"ok"}}`,
		},
		{
			name:       "one check fails",
			checks:     map[string]health.Check{"postgres": failing, "cache": passing},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   `{"status":"unavailable","checks":{"cache":"ok","postgres":"unavailable"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probes := health.NewProbes(slog.New(slog.DiscardHandler), tt.checks)
			rec := httptest.NewRecorder()

			probes.Ready(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, health.ReadinessPath, nil))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tt.wantBody {
				t.Errorf("body = %s, want %s", got, tt.wantBody)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
		})
	}
}

func TestReadyDoesNotLeakErrorDetails(t *testing.T) {
	probes := health.NewProbes(slog.New(slog.DiscardHandler), map[string]health.Check{"postgres": failing})
	rec := httptest.NewRecorder()

	probes.Ready(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, health.ReadinessPath, nil))

	if body := rec.Body.String(); strings.Contains(body, "10.0.0.7") || strings.Contains(body, "refused") {
		t.Errorf("readiness body leaks the check error: %s", body)
	}
}

func TestLiveIgnoresDependencies(t *testing.T) {
	probes := health.NewProbes(slog.New(slog.DiscardHandler), map[string]health.Check{"postgres": failing})
	rec := httptest.NewRecorder()

	probes.Live(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, health.LivenessPath, nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := strings.TrimSpace(rec.Body.String()), `{"status":"ok"}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}
