package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
	"github.com/bengisupinar/campaign/internal/platform/httpserver"
)

// Event stream timing.
const (
	pollInterval      = time.Second      // how often a stream re-reads the progress
	heartbeatInterval = 15 * time.Second // keeps idle streams open through proxies
	writeWindow       = 10 * time.Second // each write must finish within this
)

// campaignGetter reads a campaign: *campaign.Service.
type campaignGetter interface {
	Get(ctx context.Context, id int64) (*campaign.Campaign, error)
}

// NewHandler serves a campaign's delivery progress, to be mounted at
// /api/campaigns/{id}/delivery: a snapshot, and a stream of them. Streams end when shutdown
// is done, since server shutdown doesn't cancel requests.
func NewHandler(shutdown context.Context, campaigns campaignGetter, db *gorm.DB, logger *slog.Logger) http.Handler {
	h := &handler{campaigns: campaigns, db: db, shutdown: shutdown, logger: logger}
	r := chi.NewRouter()
	r.Get("/", h.get)
	r.Get("/events", h.events)
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) { writeError(w, http.StatusNotFound, "not_found") })
	r.MethodNotAllowed(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
	})
	return r
}

type handler struct {
	campaigns campaignGetter
	db        *gorm.DB
	shutdown  context.Context
	logger    *slog.Logger
}

// snapshot is a campaign's delivery progress.
type snapshot struct {
	Status      campaign.Status            `json:"status"`
	ScheduledAt *time.Time                 `json:"scheduled_at"`
	StartedAt   *time.Time                 `json:"started_at"`
	FinishedAt  *time.Time                 `json:"finished_at"`
	Recipients  int                        `json:"recipients"`
	Messages    tally                      `json:"messages"`
	ByChannel   map[customer.Channel]tally `json:"by_channel"`
	// Final is true once nothing can change: the campaign is sent or failed and no message is
	// pending or waiting for its receipt.
	Final bool `json:"final"`
}

// tally counts messages by status.
type tally struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Accepted  int `json:"accepted"` // accepted by the provider, waiting for the receipt
	Delivered int `json:"delivered"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
}

func (t *tally) add(status string, n int) {
	t.Total += n
	switch status {
	case statusPending:
		t.Pending += n
	case statusAccepted:
		t.Accepted += n
	case statusDelivered:
		t.Delivered += n
	case statusFailed:
		t.Failed += n
	case statusSkipped:
		t.Skipped += n
	}
}

func (h *handler) snapshot(ctx context.Context, id int64) (snapshot, error) {
	c, err := h.campaigns.Get(ctx, id)
	if err != nil {
		return snapshot{}, err
	}
	counts, recipients, err := newStore(h.db).counts(ctx, id)
	if err != nil {
		return snapshot{}, err
	}
	s := snapshot{
		Status:      c.Status(),
		ScheduledAt: nullTime(c.ScheduledAt()),
		StartedAt:   nullTime(c.StartedAt()),
		FinishedAt:  nullTime(c.FinishedAt()),
		Recipients:  recipients,
		ByChannel:   make(map[customer.Channel]tally),
	}
	for _, ch := range c.Channels() {
		s.ByChannel[ch] = tally{}
	}
	for _, n := range counts {
		s.Messages.add(n.Status, n.N)
		t := s.ByChannel[n.Channel]
		t.add(n.Status, n.N)
		s.ByChannel[n.Channel] = t
	}
	done := s.Status == campaign.StatusSent || s.Status == campaign.StatusFailed
	s.Final = done && s.Messages.Pending == 0 && s.Messages.Accepted == 0
	return s, nil
}

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	s, err := h.snapshot(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpserver.WriteJSON(w, http.StatusOK, s)
}

// events streams snapshots as server-sent events: one at once, then one each time the progress
// changes, with a comment every 15 s to keep the connection open. Once the progress is final,
// it sends the last snapshot again as a "done" event and ends; clients close their
// EventSource on it, since it would otherwise reconnect. A draft has nothing to follow, so its
// stream ends at once the same way.
func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	id, ok := campaignID(w, r)
	if !ok {
		return
	}
	s, err := h.snapshot(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // nginx streams rather than buffers it
	w.WriteHeader(http.StatusOK)
	stream := eventStream{w: w, rc: http.NewResponseController(w)}

	last, err := stream.send("", s)
	poll := time.NewTicker(pollInterval)
	defer poll.Stop()
	heartbeat := time.NewTicker(heartbeatInterval)
	defer heartbeat.Stop()
	for err == nil && !s.Final && s.Status != campaign.StatusDraft {
		select {
		case <-r.Context().Done():
			return
		case <-h.shutdown.Done():
			return
		case <-heartbeat.C:
			err = stream.comment("ping")
		case <-poll.C:
			if s, err = h.snapshot(r.Context(), id); err != nil {
				break
			}
			var data []byte
			if data, err = json.Marshal(s); err == nil && !bytes.Equal(data, last) {
				last, err = stream.send("", s)
			}
		}
	}
	if err != nil {
		if r.Context().Err() == nil {
			h.logger.WarnContext(r.Context(), "delivery event stream ended", slog.Int64("campaign_id", id), slog.Any("error", err))
		}
		return
	}
	_, _ = stream.send("done", s) // a client that went away needs no last event
}

// eventStream writes server-sent events, extending the write deadline for each, since the
// server's own write timeout would otherwise end a long stream.
type eventStream struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

// send writes s as an event of the given type ("" for the default) and returns its JSON.
func (s eventStream) send(event string, v snapshot) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if event != "" {
		fmt.Fprintf(&b, "event: %s\n", event)
	}
	fmt.Fprintf(&b, "data: %s\n\n", data)
	return data, s.write(b.Bytes())
}

func (s eventStream) comment(text string) error {
	return s.write([]byte(": " + text + "\n\n"))
}

func (s eventStream) write(p []byte) error {
	if err := s.rc.SetWriteDeadline(time.Now().Add(writeWindow)); err != nil {
		return err
	}
	if _, err := s.w.Write(p); err != nil {
		return err
	}
	return s.rc.Flush()
}

// campaignID parses the {id} path parameter. Anything but a positive integer names no campaign.
func campaignID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, "not_found")
		return 0, false
	}
	return id, true
}

func (h *handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, campaign.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	h.logger.ErrorContext(r.Context(), "delivery request failed", slog.Any("error", err))
	writeError(w, http.StatusInternalServerError, "internal")
}

func writeError(w http.ResponseWriter, status int, code string) {
	httpserver.WriteJSON(w, status, map[string]string{"error": code})
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
