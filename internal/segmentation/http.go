package segmentation

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
	"github.com/bengisupinar/campaign/internal/platform/httpserver"
)

// NewHandler returns the composer's read-only segment API, to be mounted at /api/segments:
// what a segment can name, and how many customers a segment reaches. It answers with counts,
// never with customers. now is the time an estimate's windows end.
func NewHandler(svc *Service, now func() time.Time, logger *slog.Logger) http.Handler {
	h := &handler{svc: svc, now: now, logger: logger}
	r := chi.NewRouter()
	r.Get("/options", h.options)
	r.Post("/audience", h.audience)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusNotFound, "not_found") })
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	})
	return r
}

type handler struct {
	svc    *Service
	now    func() time.Time
	logger *slog.Logger
}

// audienceJSON is the body of an estimate request: a segment, under the same key as in a
// campaign draft, so its problems have the same field paths.
type audienceJSON struct {
	Segment campaign.Segment `json:"segment"`
}

// errorJSON is the body of every error response, as in the campaign API.
type errorJSON struct {
	Error    string             `json:"error"`
	Problems []campaign.Problem `json:"problems,omitempty"`
}

func (h *handler) options(w http.ResponseWriter, r *http.Request) {
	opts, err := h.svc.Options(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, opts)
}

// audience estimates who a segment reaches now. It counts both channels, so the composer can
// show the reach of whichever channels the campaign uses.
func (h *handler) audience(w http.ResponseWriter, r *http.Request) {
	var body audienceJSON
	if err := httpserver.DecodeJSON(w, r, &body); err != nil {
		if errors.Is(err, httpserver.ErrBodyTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large")
			return
		}
		writeError(w, http.StatusBadRequest, "malformed_json")
		return
	}
	q := Query{Segment: body.Segment, Channels: []customer.Channel{customer.ChannelEmail, customer.ChannelSMS}, AsOf: h.now()}
	audience, err := h.svc.CountAudience(r.Context(), q)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, audience)
}

// fail maps a service error to a response. Unexpected errors are logged, never returned.
func (h *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *campaign.ValidationError
	if errors.As(err, &invalid) {
		httpserver.WriteJSON(w, http.StatusUnprocessableEntity, errorJSON{Error: "invalid_segment", Problems: invalid.Problems})
		return
	}
	h.logger.ErrorContext(r.Context(), "segment request failed",
		slog.String("request_id", middleware.GetReqID(r.Context())),
		slog.Any("error", err),
	)
	writeError(w, http.StatusInternalServerError, "internal")
}

func writeError(w http.ResponseWriter, status int, code string) {
	httpserver.WriteJSON(w, status, errorJSON{Error: code})
}
