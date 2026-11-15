package assistant

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/platform/httpserver"
)

// NewHandler serves the assistant, to be mounted at /api/assistant: chat messages, and the
// decisions on dispatch requests, which only a person makes, through this route.
func NewHandler(svc *Service, logger *slog.Logger) http.Handler {
	h := &handler{svc: svc, logger: logger}
	r := chi.NewRouter()
	r.Post("/messages", h.message)
	r.Post("/approvals/{id}", h.decide)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusNotFound, "not_found") })
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	})
	return r
}

type handler struct {
	svc    *Service
	logger *slog.Logger
}

type messageJSON struct {
	ConversationID string `json:"conversation_id"`
	Message        string `json:"message"`
	TimeZone       string `json:"time_zone"`
}

type decisionJSON struct {
	Decision string `json:"decision"` // "approve" or "cancel"
}

type errorJSON struct {
	Error    string             `json:"error"`
	Problems []campaign.Problem `json:"problems,omitempty"`
}

func (h *handler) message(w http.ResponseWriter, r *http.Request) {
	// A turn can take longer than the server's write timeout allows ordinary requests.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(turnTimeout + 15*time.Second))
	var body messageJSON
	if !h.decode(w, r, &body) {
		return
	}
	turn, err := h.svc.Chat(r.Context(), body.ConversationID, body.Message, body.TimeZone)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, turn)
}

func (h *handler) decide(w http.ResponseWriter, r *http.Request) {
	var body decisionJSON
	if !h.decode(w, r, &body) {
		return
	}
	if body.Decision != "approve" && body.Decision != "cancel" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_decision")
		return
	}
	decision, err := h.svc.Decide(r.Context(), chi.URLParam(r, "id"), body.Decision == "approve")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, decision)
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

// fail maps an error to a response. Unexpected errors are logged, never returned.
func (h *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *campaign.ValidationError
	var me *modelError
	switch {
	case errors.Is(err, ErrInvalidMessage):
		writeError(w, http.StatusUnprocessableEntity, "invalid_message")
	case errors.Is(err, ErrConversationNotFound):
		writeError(w, http.StatusNotFound, "conversation_not_found")
	case errors.Is(err, ErrConversationBusy):
		writeError(w, http.StatusConflict, "conversation_busy")
	case errors.Is(err, ErrConversationFull):
		writeError(w, http.StatusConflict, "conversation_full")
	case errors.Is(err, ErrTooManyConversations):
		writeError(w, http.StatusServiceUnavailable, "assistant_busy")
	case errors.Is(err, ErrApprovalNotFound):
		writeError(w, http.StatusNotFound, "approval_not_found")
	case errors.Is(err, ErrApprovalClosed):
		writeError(w, http.StatusConflict, "approval_closed")
	case errors.As(err, &me) && me.code == "assistant_timeout":
		writeError(w, http.StatusGatewayTimeout, me.code)
	case errors.As(err, &me):
		writeError(w, http.StatusServiceUnavailable, me.code)
	case errors.As(err, &invalid):
		httpserver.WriteJSON(w, http.StatusUnprocessableEntity, errorJSON{Error: "invalid_campaign", Problems: invalid.Problems})
	case errors.Is(err, campaign.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, campaign.ErrAlreadyDispatched), errors.Is(err, campaign.ErrChanged):
		writeError(w, http.StatusConflict, errorCode(err))
	default:
		h.logger.ErrorContext(r.Context(), "assistant request failed",
			slog.String("request_id", middleware.GetReqID(r.Context())),
			slog.Any("error", err),
		)
		writeError(w, http.StatusInternalServerError, "internal")
	}
}

func writeError(w http.ResponseWriter, status int, code string) {
	httpserver.WriteJSON(w, status, errorJSON{Error: code})
}
