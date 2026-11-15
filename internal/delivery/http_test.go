package delivery

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
)

// serve mounts the delivery routes as cmd/api does, beside the campaign API.
func (f *fixture) serve(shutdown context.Context, t *testing.T) *httptest.Server {
	t.Helper()
	r := chi.NewRouter()
	r.Mount("/api/campaigns/{id}/delivery", NewHandler(shutdown, f.campaigns, f.db, discard))
	r.Mount("/api/campaigns", campaign.NewHandler(f.campaigns, discard))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func get(ctx context.Context, t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	check(t, err)
	res, err := http.DefaultClient.Do(req)
	check(t, err)
	return res
}

func getSnapshot(t *testing.T, srv *httptest.Server, id int64) Progress {
	t.Helper()
	res := get(t.Context(), t, fmt.Sprintf("%s/api/campaigns/%d/delivery", srv.URL, id))
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET delivery: status %d", res.StatusCode)
	}
	var s Progress
	check(t, json.NewDecoder(res.Body).Decode(&s))
	return s
}

func TestDeliverySnapshot(t *testing.T) {
	f := newFixture(t, 1)
	srv := f.serve(t.Context(), t)

	draft := f.draft(t)
	if s := getSnapshot(t, srv, draft.ID()); s.Status != campaign.StatusDraft || s.Messages.Total != 0 || len(s.ByChannel) != 2 || s.Final {
		t.Errorf("draft snapshot = %+v, want zeros on both channels, not final", s)
	}

	id, deliveries := f.prepared(t) // one email (first) and one SMS
	check(t, completeMessage(t.Context(), f.db, pendingMessage{id: deliveries[0], campaignID: id}, outcome{status: statusAccepted, providerMessageID: "p-1"}, now()))
	check(t, completeMessage(t.Context(), f.db, pendingMessage{id: deliveries[1], campaignID: id}, outcome{status: statusSkipped, reason: "not_permitted"}, now()))
	s := getSnapshot(t, srv, id)
	want := tally{Total: 2, Accepted: 1, Skipped: 1}
	if s.Status != campaign.StatusSent || s.Recipients != 1 || s.Messages != want || s.Final {
		t.Errorf("snapshot = %+v, want sent with messages %+v, not final while a receipt is due", s, want)
	}
	if s.ByChannel[customer.ChannelEmail].Accepted != 1 || s.ByChannel[customer.ChannelSMS].Skipped != 1 {
		t.Errorf("by channel = %+v", s.ByChannel)
	}

	_, err := newStore(f.db).recordReceipt(t.Context(), "p-1", true, now())
	check(t, err)
	if s := getSnapshot(t, srv, id); s.Messages.Delivered != 1 || !s.Final {
		t.Errorf("after the receipt: %+v, want delivered and final", s)
	}

	unknown := get(t.Context(), t, srv.URL+"/api/campaigns/999/delivery")
	defer unknown.Body.Close()
	if unknown.StatusCode != http.StatusNotFound {
		t.Errorf("unknown campaign: status %d, want 404", unknown.StatusCode)
	}
	beside := get(t.Context(), t, fmt.Sprintf("%s/api/campaigns/%d", srv.URL, id)) // still the campaign API
	defer beside.Body.Close()
	if beside.StatusCode != http.StatusOK {
		t.Errorf("GET campaign beside the delivery routes: status %d, want 200", beside.StatusCode)
	}
}

// event is one server-sent event.
type event struct {
	name     string
	snapshot Progress
}

// readEvents reads events until the stream ends.
func readEvents(t *testing.T, body io.Reader) []event {
	t.Helper()
	var events []event
	var current event
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			current.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			check(t, json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &current.snapshot))
		case line == "" && current.snapshot.Status != "":
			events = append(events, current)
			current = event{}
		}
	}
	return events
}

func TestDeliveryEventStream(t *testing.T) {
	f := newFixture(t, 1)
	srv := f.serve(t.Context(), t)
	url := func(id int64) string { return fmt.Sprintf("%s/api/campaigns/%d/delivery/events", srv.URL, id) }

	t.Run("a draft gets one snapshot and done", func(t *testing.T) {
		res := get(t.Context(), t, url(f.draft(t).ID()))
		defer res.Body.Close()
		if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" || res.Header.Get("X-Accel-Buffering") != "no" {
			t.Errorf("headers = %v", res.Header)
		}
		events := readEvents(t, res.Body)
		if len(events) != 2 || events[0].name != "" || events[1].name != "done" {
			t.Errorf("events = %+v, want a snapshot and done", events)
		}
	})

	t.Run("a sending campaign streams until final", func(t *testing.T) {
		id, deliveries := f.prepared(t)
		res := get(t.Context(), t, url(id))
		defer res.Body.Close()
		go func() {
			time.Sleep(1200 * time.Millisecond)
			for i, d := range deliveries {
				out := outcome{status: statusAccepted, providerMessageID: fmt.Sprintf("stream-%d", i)}
				_ = completeMessage(context.Background(), f.db, pendingMessage{id: d, campaignID: id}, out, now())
			}
			time.Sleep(1200 * time.Millisecond)
			for i := range deliveries {
				_, _ = newStore(f.db).recordReceipt(context.Background(), fmt.Sprintf("stream-%d", i), true, now())
			}
		}()

		events := readEvents(t, res.Body)
		if len(events) < 3 {
			t.Fatalf("events = %+v, want at least the first snapshot, a change and done", events)
		}
		first, last := events[0], events[len(events)-1]
		if first.snapshot.Messages.Pending != 2 || last.name != "done" || !last.snapshot.Final || last.snapshot.Messages.Delivered != 2 {
			t.Errorf("first %+v, last %+v; want pending first and a final, delivered done", first, last)
		}
	})

	t.Run("the stream ends when the client leaves or the server shuts down", func(t *testing.T) {
		id, _ := f.prepared(t) // stays pending: nothing sends it
		shutdown, stop := context.WithCancel(t.Context())
		srv := f.serve(shutdown, t)
		ctx, cancel := context.WithCancel(t.Context())
		leaving := get(ctx, t, fmt.Sprintf("%s/api/campaigns/%d/delivery/events", srv.URL, id))
		defer leaving.Body.Close()
		cancel()
		_, _ = io.Copy(io.Discard, leaving.Body)

		deadline, cancelDeadline := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancelDeadline()
		res := get(deadline, t, fmt.Sprintf("%s/api/campaigns/%d/delivery/events", srv.URL, id))
		defer res.Body.Close()
		stop()
		events := readEvents(t, res.Body)
		if deadline.Err() != nil {
			t.Fatal("the stream outlived the shutdown")
		}
		if len(events) != 1 {
			t.Errorf("events = %+v, want only the first snapshot, without done", events)
		}
	})
}
