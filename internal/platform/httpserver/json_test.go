package httpserver

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}
	tests := []struct {
		name     string
		body     string
		want     payload
		wantErr  bool
		tooLarge bool
	}{
		{name: "one object", body: `{"name":"spring sale"}`, want: payload{Name: "spring sale"}},
		{name: "surrounding whitespace", body: " \n{\"name\":\"a\"}\n ", want: payload{Name: "a"}},
		{name: "unknown field", body: `{"name":"a","status":"sending"}`, wantErr: true},
		{name: "malformed", body: `{"name":`, wantErr: true},
		{name: "wrong type", body: `{"name":5}`, wantErr: true},
		{name: "second value", body: `{"name":"a"} {"name":"b"}`, wantErr: true},
		{name: "trailing garbage", body: `{"name":"a"} x`, wantErr: true},
		{name: "empty body", body: ``, wantErr: true},
		{name: "over 64 KB", body: `{"name":"` + strings.Repeat("a", maxBodyBytes) + `"}`, wantErr: true, tooLarge: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(tt.body))
			var got payload

			err := DecodeJSON(httptest.NewRecorder(), r, &got)

			if (err != nil) != tt.wantErr {
				t.Fatalf("DecodeJSON() error = %v, want error %v", err, tt.wantErr)
			}
			if errors.Is(err, ErrBodyTooLarge) != tt.tooLarge {
				t.Errorf("DecodeJSON() error = %v, want ErrBodyTooLarge %v", err, tt.tooLarge)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("decoded %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestWriteJSON(t *testing.T) {
	rec := httptest.NewRecorder()

	WriteJSON(rec, http.StatusCreated, map[string]int{"id": 7})

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusCreated)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"id":7}` {
		t.Errorf("body = %s, want {\"id\":7}", got)
	}
	for header, want := range map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}
