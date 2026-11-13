// Package health serves the liveness and readiness probes used by Docker Compose (and,
// later, any orchestrator or load balancer).
package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

const (
	// LivenessPath answers "is the process up?". It checks no dependencies, so an outage
	// elsewhere never gets a healthy process restarted.
	LivenessPath = "/healthz"
	// ReadinessPath answers "can the process serve traffic?": every Check must pass.
	ReadinessPath = "/readyz"

	readinessTimeout = 2 * time.Second

	statusOK          = "ok"
	statusUnavailable = "unavailable"
)

// Check reports whether a dependency is usable. It must return promptly once ctx is done.
type Check func(ctx context.Context) error

// Probes serves the liveness and readiness endpoints.
type Probes struct {
	logger *slog.Logger
	checks map[string]Check
}

// NewProbes returns probes whose readiness runs checks, keyed by dependency name.
func NewProbes(logger *slog.Logger, checks map[string]Check) *Probes {
	return &Probes{logger: logger, checks: checks}
}

type response struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks,omitempty"`
}

// Live handles LivenessPath.
func (p *Probes) Live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, response{Status: statusOK})
}

// Ready handles ReadinessPath. It responds 503 when any check fails. Failure details are
// logged, never returned, so the endpoint does not leak internals.
func (p *Probes) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readinessTimeout)
	defer cancel()

	res := response{Status: statusOK, Checks: make(map[string]string, len(p.checks))}
	code := http.StatusOK
	for name, check := range p.checks {
		if err := check(ctx); err != nil {
			p.logger.WarnContext(ctx, "readiness check failed", slog.String("check", name), slog.Any("error", err))
			res.Checks[name] = statusUnavailable
			res.Status = statusUnavailable
			code = http.StatusServiceUnavailable
			continue
		}
		res.Checks[name] = statusOK
	}
	writeJSON(w, code, res)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	// An encoding error here means the client went away; there is nobody left to tell.
	_ = json.NewEncoder(w).Encode(v)
}
