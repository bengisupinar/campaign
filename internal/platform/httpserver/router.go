// Package httpserver holds the HTTP plumbing shared by every capability: a router with
// cross-cutting middleware, and a server lifecycle with graceful shutdown.
package httpserver

import (
	"log/slog"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter returns a router that assigns request IDs, writes one structured access-log
// line per request and turns handler panics into 500 responses.
//
// Successful requests to quietPaths are logged at debug level; use it for health probes
// that are polled every few seconds and would otherwise drown out real traffic.
func NewRouter(logger *slog.Logger, quietPaths ...string) chi.Router {
	r := chi.NewRouter()
	r.Use(
		middleware.RequestID,
		accessLog(logger, quietPaths),
		recoverPanics(logger), // inside accessLog, so a recovered panic is logged as a 500
	)
	return r
}
