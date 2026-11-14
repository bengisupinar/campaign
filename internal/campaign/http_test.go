package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// validBody is validDraft as a request body.
const validBody = `{
	"name": "Abandoned baskets in İstanbul",
	"segment": {"version": 1, "match": "all", "conditions": [
		{"type": "city", "in": ["İstanbul"]},
		{"type": "basket", "within_days": 30},
		{"type": "purchased", "within_days": 30, "negate": true}
	]},
	"content": {"sms": {"body": "Merhaba {{first_name}}, sepetin seni bekliyor!"}}
}`

func TestCreateCampaign(t *testing.T) {
	api := newTestAPI(t)

	rec := api.do(t, http.MethodPost, "/api/campaigns", validBody)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusCreated, rec.Body)
	}
	if got := rec.Header().Get("Location"); got != "/api/campaigns/1" {
		t.Errorf("Location = %q, want /api/campaigns/1", got)
	}
	assertJSON(t, rec, `{
		"id": 1,
		"status": "draft",
		"channels": ["sms"],
		"name": "Abandoned baskets in İstanbul",
		"segment": {"version": 1, "match": "all", "conditions": [
			{"type": "city", "in": ["İstanbul"]},
			{"type": "basket", "within_days": 30},
			{"type": "purchased", "within_days": 30, "negate": true}
		]},
		"content": {"sms": {"body": "Merhaba {{first_name}}, sepetin seni bekliyor!"}},
		"created_at": "2026-11-14T10:00:00Z",
		"updated_at": "2026-11-14T10:00:00Z"
	}`)
}

func TestCreateReportsEveryProblem(t *testing.T) {
	api := newTestAPI(t)
	body := fmt.Sprintf(`{
		"name": " ",
		"segment": {"version": 1, "match": "all", "conditions": [{"type": "country", "in": ["tr"]}]},
		"content": {"sms": {"body": "%s{{coupon}}"}}
	}`, strings.Repeat("a", 161))

	rec := api.do(t, http.MethodPost, "/api/campaigns", body)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusUnprocessableEntity, rec.Body)
	}
	assertJSON(t, rec, `{"error": "invalid_campaign", "problems": [
		{"field": "name", "problem": "required"},
		{"field": "segment.conditions[0].in[0]", "problem": "invalid"},
		{"field": "content.sms.body", "problem": "too_long"},
		{"field": "content.sms.body", "problem": "unknown_placeholder", "detail": "coupon"}
	]}`)
	if len(api.repo.campaigns) != 0 {
		t.Errorf("an invalid campaign was stored")
	}
}

func TestUnknownReferencesAreProblems(t *testing.T) {
	longSMS := strings.Replace(validBody, "Merhaba {{first_name}}, sepetin seni bekliyor!", strings.Repeat("a", 161), 1)
	tests := []struct {
		name       string
		body       string
		checkErr   error
		wantStatus int
		want       string
	}{
		{"unknown city", validBody, nil, http.StatusUnprocessableEntity, `{"error": "invalid_campaign", "problems": [
			{"field": "segment.conditions[0].in[0]", "problem": "unknown", "detail": "Istanbul"}
		]}`},
		{"reported with the draft's other problems", longSMS, nil, http.StatusUnprocessableEntity, `{"error": "invalid_campaign", "problems": [
			{"field": "content.sms.body", "problem": "too_long"},
			{"field": "segment.conditions[0].in[0]", "problem": "unknown", "detail": "Istanbul"}
		]}`},
		{"the check fails", validBody, errors.New("connection refused"), http.StatusInternalServerError, `{"error": "internal"}`},
	}
	for _, tt := range tests {
		for _, req := range []struct{ method, path string }{
			{http.MethodPost, "/api/campaigns"},
			{http.MethodPut, "/api/campaigns/1"},
		} {
			t.Run(tt.name+"/"+req.method, func(t *testing.T) {
				api := newTestAPI(t)
				api.do(t, http.MethodPost, "/api/campaigns", validBody) // campaign 1, saved while references were fine
				api.references.problems = []Problem{{Field: "segment.conditions[0].in[0]", Code: ProblemUnknown, Detail: "Istanbul"}}
				api.references.err = tt.checkErr

				rec := api.do(t, req.method, req.path, tt.body)

				if rec.Code != tt.wantStatus {
					t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
				}
				assertJSON(t, rec, tt.want)
				if len(api.repo.campaigns) != 1 || api.repo.campaigns[1].UpdatedAt() != now {
					t.Errorf("the draft was saved")
				}
			})
		}
	}

	// A campaign past the draft stage can't change, whatever its segment names.
	api := newTestAPI(t)
	api.do(t, http.MethodPost, "/api/campaigns", validBody)
	api.repo.campaigns[1].status = StatusScheduled
	api.references.problems = []Problem{{Field: "segment.conditions[0].in[0]", Code: ProblemUnknown, Detail: "Istanbul"}}
	if rec := api.do(t, http.MethodPut, "/api/campaigns/1", validBody); rec.Code != http.StatusConflict {
		t.Errorf("PUT on a scheduled campaign: status = %d, want %d", rec.Code, http.StatusConflict)
	}
}

func TestGetAndListCampaigns(t *testing.T) {
	api := newTestAPI(t)
	api.do(t, http.MethodPost, "/api/campaigns", validBody)
	api.clock = now.Add(time.Minute)
	api.do(t, http.MethodPost, "/api/campaigns", strings.Replace(validBody, "Abandoned baskets", "Second", 1))

	rec := api.do(t, http.MethodGet, "/api/campaigns/1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d, want %d", rec.Code, http.StatusOK)
	}
	var one campaignJSON
	decode(t, rec, &one)
	if one.ID != 1 || one.Name != "Abandoned baskets in İstanbul" {
		t.Errorf("GET /api/campaigns/1 = campaign %d %q, want campaign 1", one.ID, one.Name)
	}

	rec = api.do(t, http.MethodGet, "/api/campaigns", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", rec.Code, http.StatusOK)
	}
	var list struct {
		Campaigns []campaignJSON `json:"campaigns"`
	}
	decode(t, rec, &list)
	var ids []int64
	for _, c := range list.Campaigns {
		ids = append(ids, c.ID)
	}
	if want := []int64{2, 1}; !slices.Equal(ids, want) {
		t.Errorf("listed campaigns %v, want newest first %v", ids, want)
	}
}

func TestUpdateCampaign(t *testing.T) {
	api := newTestAPI(t)
	api.do(t, http.MethodPost, "/api/campaigns", validBody)
	api.clock = now.Add(time.Hour)

	rec := api.do(t, http.MethodPut, "/api/campaigns/1", `{
		"name": "Come back to your basket",
		"segment": {"version": 1, "match": "any", "conditions": [{"type": "language", "in": ["en"]}]},
		"content": {"email": {"subject": "{{first_name}}, your basket misses you", "body": "It's still waiting."}}
	}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body)
	}
	assertJSON(t, rec, `{
		"id": 1,
		"status": "draft",
		"channels": ["email"],
		"name": "Come back to your basket",
		"segment": {"version": 1, "match": "any", "conditions": [{"type": "language", "in": ["en"]}]},
		"content": {"email": {"subject": "{{first_name}}, your basket misses you", "body": "It's still waiting."}},
		"created_at": "2026-11-14T10:00:00Z",
		"updated_at": "2026-11-14T11:00:00Z"
	}`)
}

func TestDeleteCampaign(t *testing.T) {
	api := newTestAPI(t)
	api.do(t, http.MethodPost, "/api/campaigns", validBody)

	rec := api.do(t, http.MethodDelete, "/api/campaigns/1", "")

	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("status = %d with body %q, want %d and no body", rec.Code, rec.Body, http.StatusNoContent)
	}
	if rec := api.do(t, http.MethodGet, "/api/campaigns/1", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET after DELETE: status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestRejectedRequests(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		path      string
		body      string
		wantCode  int
		wantError string // the error code in the body
	}{
		{"malformed JSON", http.MethodPost, "/api/campaigns", `{"name":`, http.StatusBadRequest, "malformed_json"},
		{"status set by the client", http.MethodPost, "/api/campaigns",
			strings.Replace(validBody, `"name"`, `"status": "sending", "name"`, 1), http.StatusBadRequest, "malformed_json"},
		{"body over 64 KB", http.MethodPost, "/api/campaigns",
			`{"name": "` + strings.Repeat("a", 65<<10) + `"}`, http.StatusRequestEntityTooLarge, "body_too_large"},
		{"unknown campaign", http.MethodGet, "/api/campaigns/99", "", http.StatusNotFound, "not_found"},
		{"ID that isn't a number", http.MethodGet, "/api/campaigns/first", "", http.StatusNotFound, "not_found"},
		{"update of an unknown campaign", http.MethodPut, "/api/campaigns/99", validBody, http.StatusNotFound, "not_found"},
		{"update of a scheduled campaign", http.MethodPut, "/api/campaigns/2", validBody, http.StatusConflict, "not_draft"},
		{"invalid update", http.MethodPut, "/api/campaigns/1", `{"name": "No content"}`, http.StatusUnprocessableEntity, "invalid_campaign"},
		{"delete of an unknown campaign", http.MethodDelete, "/api/campaigns/99", "", http.StatusNotFound, "not_found"},
		{"delete of a scheduled campaign", http.MethodDelete, "/api/campaigns/2", "", http.StatusConflict, "not_draft"},
		{"no send endpoint", http.MethodPost, "/api/campaigns/1/send", "", http.StatusNotFound, "not_found"},
		{"no schedule endpoint", http.MethodPost, "/api/campaigns/1/schedule", "", http.StatusNotFound, "not_found"},
		{"no partial update", http.MethodPatch, "/api/campaigns/1", `{"status": "sending"}`, http.StatusMethodNotAllowed, "method_not_allowed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := newTestAPI(t)
			api.do(t, http.MethodPost, "/api/campaigns", validBody) // campaign 1, a draft
			api.do(t, http.MethodPost, "/api/campaigns", validBody) // campaign 2, scheduled below
			api.repo.campaigns[2].status = StatusScheduled

			rec := api.do(t, tt.method, tt.path, tt.body)

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantCode, rec.Body)
			}
			var body errorJSON
			decode(t, rec, &body)
			if body.Error != tt.wantError {
				t.Errorf("error = %q, want %q", body.Error, tt.wantError)
			}
			if got := api.repo.campaigns[2]; got.Status() != StatusScheduled {
				t.Errorf("the scheduled campaign became %s", got.Status())
			}
		})
	}
}

// testAPI serves the campaign API over a real Service, an in-memory repository, a reference
// checker and a clock the test sets.
type testAPI struct {
	repo       *fakeRepository
	references *fakeReferences
	handler    http.Handler
	clock      time.Time
}

func newTestAPI(t *testing.T) *testAPI {
	t.Helper()
	api := &testAPI{
		repo:       &fakeRepository{campaigns: make(map[int64]*Campaign)},
		references: &fakeReferences{},
		clock:      now,
	}
	svc := NewService(api.repo, api.references, func() time.Time { return api.clock })
	r := chi.NewRouter()
	r.Mount("/api/campaigns", NewHandler(svc, slog.New(slog.DiscardHandler)))
	api.handler = r
	return api
}

func (a *testAPI) do(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode response %s: %v", rec.Body, err)
	}
}

// assertJSON compares a response body with the expected JSON, ignoring layout and key order.
func assertJSON(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got, wantValue any
	decode(t, rec, &got)
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("expected JSON: %v", err)
	}
	if !reflect.DeepEqual(got, wantValue) {
		t.Errorf("body = %s\nwant %s", rec.Body, want)
	}
}

// fakeRepository keeps campaigns in memory with the store's rules: copies in and out, and
// changes only to drafts.
type fakeRepository struct {
	campaigns map[int64]*Campaign
	lastID    int64
}

func (f *fakeRepository) Create(_ context.Context, c *Campaign) error {
	f.lastID++
	c.id = f.lastID
	f.campaigns[c.id] = snapshot(c)
	return nil
}

func (f *fakeRepository) Update(_ context.Context, c *Campaign) error {
	if err := f.draft(c.id); err != nil {
		return err
	}
	f.campaigns[c.id] = snapshot(c)
	return nil
}

func (f *fakeRepository) Delete(_ context.Context, id int64) error {
	if err := f.draft(id); err != nil {
		return err
	}
	delete(f.campaigns, id)
	return nil
}

func (f *fakeRepository) Get(_ context.Context, id int64) (*Campaign, error) {
	c, ok := f.campaigns[id]
	if !ok {
		return nil, ErrNotFound
	}
	return snapshot(c), nil
}

func (f *fakeRepository) List(context.Context) ([]*Campaign, error) {
	var campaigns []*Campaign
	for _, id := range slices.Backward(slices.Sorted(maps.Keys(f.campaigns))) {
		campaigns = append(campaigns, snapshot(f.campaigns[id]))
	}
	return campaigns, nil
}

func (f *fakeRepository) draft(id int64) error {
	c, ok := f.campaigns[id]
	switch {
	case !ok:
		return ErrNotFound
	case c.Status() != StatusDraft:
		return ErrNotDraft
	}
	return nil
}

// fakeReferences answers reference checks with the problems, or the error, a test sets.
type fakeReferences struct {
	problems []Problem
	err      error
}

func (f *fakeReferences) CheckReferences(context.Context, Segment) ([]Problem, error) {
	return f.problems, f.err
}

func snapshot(c *Campaign) *Campaign {
	s := *c
	s.draft = c.draft.clone()
	return &s
}
