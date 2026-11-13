package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// maxBodyBytes bounds a JSON request body, so a client can't make the server read without end.
const maxBodyBytes = 64 << 10

// ErrBodyTooLarge reports a request body over the 64 KB DecodeJSON accepts.
var ErrBodyTooLarge = errors.New("request body too large")

// DecodeJSON decodes the request body, a single JSON value of at most 64 KB, into dst. It
// rejects unknown fields and trailing data, so a typo in a request never passes silently. A
// body that is too large fails with an error wrapping ErrBodyTooLarge.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	switch err := dec.Decode(&struct{}{}); {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return errors.New("decode json: more than one value in the body")
	default:
		return decodeError(err)
	}
}

func decodeError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return fmt.Errorf("decode json: %w", ErrBodyTooLarge)
	}
	return fmt.Errorf("decode json: %w", err)
}

// WriteJSON writes v as a JSON response with the given status. API responses describe the
// current state, so they are never cached.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	// An encoding error here means the client went away; there is nobody left to tell.
	_ = json.NewEncoder(w).Encode(v)
}
