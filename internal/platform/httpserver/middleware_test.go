package httpserver_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bengisupinar/campaign/internal/platform/httpserver"
)

// newTestRouter returns a router that logs JSON at debug level into the returned buffer.
func newTestRouter(t *testing.T, quietPaths ...string) (http.Handler, *bytes.Buffer, func(pattern string, h http.HandlerFunc)) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r := httpserver.NewRouter(logger, quietPaths...)
	return r, &buf, func(pattern string, h http.HandlerFunc) { r.Get(pattern, h) }
}

// logEntries decodes every JSON log line written to buf.
func logEntries(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	dec := json.NewDecoder(buf)
	for {
		var e map[string]any
		if err := dec.Decode(&e); errors.Is(err, io.EOF) {
			return entries
		} else if err != nil {
			t.Fatalf("decode log line: %v", err)
		}
		entries = append(entries, e)
	}
}

// accessLogEntry returns the single "http request" log entry.
func accessLogEntry(t *testing.T, entries []map[string]any) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, e := range entries {
		if e["msg"] == "http request" {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		t.Fatalf("got %d access log entries, want 1: %v", len(found), entries)
	}
	return found[0]
}

func TestAccessLogFields(t *testing.T) {
	router, logs, get := newTestRouter(t)
	get("/hello", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = io.WriteString(w, "hi")
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/hello", nil))

	entry := accessLogEntry(t, logEntries(t, logs))
	want := map[string]any{
		"level":  "INFO",
		"method": http.MethodGet,
		"path":   "/hello",
		"status": float64(http.StatusTeapot),
		"bytes":  float64(2),
	}
	for key, value := range want {
		if entry[key] != value {
			t.Errorf("log %s = %v, want %v", key, entry[key], value)
		}
	}
	if id, _ := entry["request_id"].(string); id == "" {
		t.Error("log request_id is empty")
	}
	if _, ok := entry["duration_ms"].(float64); !ok {
		t.Errorf("log duration_ms = %v, want a number", entry["duration_ms"])
	}
}

func TestAccessLogLevels(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		status    int
		wantLevel string
	}{
		{name: "regular request", path: "/api/things", status: http.StatusOK, wantLevel: "INFO"},
		{name: "client error", path: "/api/things", status: http.StatusNotFound, wantLevel: "INFO"},
		{name: "server error", path: "/api/things", status: http.StatusBadGateway, wantLevel: "ERROR"},
		{name: "quiet path succeeds", path: "/healthz", status: http.StatusOK, wantLevel: "DEBUG"},
		{name: "quiet path fails", path: "/healthz", status: http.StatusServiceUnavailable, wantLevel: "ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, logs, get := newTestRouter(t, "/healthz")
			get(tt.path, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tt.status) })

			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, tt.path, nil))

			if got := accessLogEntry(t, logEntries(t, logs))["level"]; got != tt.wantLevel {
				t.Errorf("level = %v, want %s", got, tt.wantLevel)
			}
		})
	}
}

func TestPanicBecomes500(t *testing.T) {
	router, logs, get := newTestRouter(t)
	get("/boom", func(http.ResponseWriter, *http.Request) { panic("boom") })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	entries := logEntries(t, logs)
	if got := accessLogEntry(t, entries)["status"]; got != float64(http.StatusInternalServerError) {
		t.Errorf("access log status = %v, want 500", got)
	}
	var panicLogged bool
	for _, e := range entries {
		if e["msg"] == "panic recovered" && e["panic"] == "boom" && e["stack"] != "" {
			panicLogged = true
		}
	}
	if !panicLogged {
		t.Errorf("no structured panic log entry in %v", entries)
	}
}

func TestAbortHandlerPanicPropagates(t *testing.T) {
	router, _, get := newTestRouter(t)
	get("/abort", func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })

	defer func() {
		rec := recover()
		if err, ok := rec.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Fatalf("recovered %v, want http.ErrAbortHandler to propagate", rec)
		}
	}()
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/abort", nil))
	t.Fatal("ServeHTTP returned normally, want a panic")
}
