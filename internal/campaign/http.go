package campaign

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/bengisupinar/campaign/internal/customer"
	"github.com/bengisupinar/campaign/internal/platform/httpserver"
)

// NewHandler returns the campaign API, to be mounted at /api/campaigns. It only manages
// drafts: nothing schedules or sends a campaign until delivery exists.
func NewHandler(svc *Service, logger *slog.Logger) http.Handler {
	h := &handler{svc: svc, logger: logger}
	r := chi.NewRouter()
	r.Post("/", h.create)
	r.Get("/", h.list)
	r.Get("/{id}", h.get)
	r.Put("/{id}", h.update)
	r.Delete("/{id}", h.delete)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not_found")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	})
	return r
}

type handler struct {
	svc    *Service
	logger *slog.Logger
}

// campaignJSON is a campaign in API responses: the draft's fields plus the read-only ones.
type campaignJSON struct {
	ID       int64              `json:"id"`
	Status   Status             `json:"status"`
	Channels []customer.Channel `json:"channels"`
	Draft
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// errorJSON is the body of every error response. Error is a stable code; Problems lists what
// makes a draft invalid.
type errorJSON struct {
	Error    string    `json:"error"`
	Problems []Problem `json:"problems,omitempty"`
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	var d Draft
	if !h.decode(w, r, &d) {
		return
	}
	c, err := h.svc.Create(r.Context(), d)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Location", fmt.Sprintf("/api/campaigns/%d", c.ID()))
	httpserver.WriteJSON(w, http.StatusCreated, toJSON(c))
}

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	campaigns, err := h.svc.List(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body := struct {
		Campaigns []campaignJSON `json:"campaigns"`
	}{Campaigns: make([]campaignJSON, len(campaigns))}
	for i, c := range campaigns {
		body.Campaigns[i] = toJSON(c)
	}
	httpserver.WriteJSON(w, http.StatusOK, body)
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	c, err := h.svc.Get(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, toJSON(c))
}

func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	var d Draft
	if !h.decode(w, r, &d) {
		return
	}
	c, err := h.svc.Update(r.Context(), id, d)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, toJSON(c))
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := h.id(w, r)
	if !ok {
		return
	}
	if err := h.svc.Delete(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// id parses the {id} path parameter. Anything but a positive integer names no campaign.
func (h *handler) id(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return 0, false
	}
	return id, true
}

func (h *handler) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	err := httpserver.DecodeJSON(w, r, dst)
	switch {
	case err == nil:
		return true
	case errors.Is(err, httpserver.ErrBodyTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large")
	default:
		writeError(w, http.StatusBadRequest, "malformed_json")
	}
	return false
}

// fail maps a service error to a response. Unexpected errors are logged, never returned.
func (h *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *ValidationError
	switch {
	case errors.As(err, &invalid):
		httpserver.WriteJSON(w, http.StatusUnprocessableEntity, errorJSON{Error: "invalid_campaign", Problems: invalid.Problems})
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, ErrNotDraft):
		writeError(w, http.StatusConflict, "not_draft")
	default:
		h.logger.ErrorContext(r.Context(), "campaign request failed",
			slog.String("request_id", middleware.GetReqID(r.Context())),
			slog.Any("error", err),
		)
		writeError(w, http.StatusInternalServerError, "internal")
	}
}

func writeError(w http.ResponseWriter, status int, code string) {
	httpserver.WriteJSON(w, status, errorJSON{Error: code})
}

func toJSON(c *Campaign) campaignJSON {
	return campaignJSON{
		ID:        c.ID(),
		Status:    c.Status(),
		Channels:  c.Channels(),
		Draft:     c.Draft(),
		CreatedAt: c.CreatedAt(),
		UpdatedAt: c.UpdatedAt(),
	}
}
