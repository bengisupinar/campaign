package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
	"github.com/bengisupinar/campaign/internal/delivery"
	"github.com/bengisupinar/campaign/internal/platform/database/databasetest"
	"github.com/bengisupinar/campaign/internal/segmentation"
)

// These tests need PostgreSQL and are skipped without it: run `make db`, then
// `make test-integration`. They use the real campaign, segmentation and delivery services.

// world is a database with three customers in İstanbul who allow both channels, and the
// services the assistant uses.
type world struct {
	db        *gorm.DB
	campaigns *campaign.Service
	audiences *segmentation.Service
}

func newWorld(t *testing.T) world {
	t.Helper()
	db := databasetest.New(t)
	ctx := t.Context()
	people := customer.NewStore(db)
	must(t, people.CreateCountries(ctx, []customer.Country{{Code: "TR", Name: "Türkiye"}}))
	cities := []customer.City{{CountryCode: "TR", Name: "İstanbul"}}
	must(t, people.CreateCities(ctx, cities))
	signedUp := time.Now().Add(-48 * time.Hour)
	var all []*customer.Customer
	for i := range 3 {
		email, err := customer.ParseEmail(fmt.Sprintf("c%d@example.com", i))
		must(t, err)
		phone, err := customer.ParsePhone(fmt.Sprintf("+9053200000%02d", i))
		must(t, err)
		c, err := customer.New(customer.Profile{FirstName: "Ayşe", LastName: "Test", Email: email, Phone: phone, CityID: cities[0].ID, Language: customer.LanguageTurkish}, signedUp)
		must(t, err)
		must(t, c.Grant(customer.ChannelEmail, signedUp))
		must(t, c.Grant(customer.ChannelSMS, signedUp))
		all = append(all, c)
	}
	must(t, people.CreateCustomers(ctx, all))

	sqlDB, err := db.DB()
	must(t, err)
	queue, err := delivery.NewQueue(sqlDB, slog.New(slog.DiscardHandler))
	must(t, err)
	audiences := segmentation.NewService(db)
	return world{db: db, campaigns: campaign.NewService(campaign.NewStore(db), audiences, queue, time.Now), audiences: audiences}
}

func (w world) assistant(model Model) *Service {
	progress := func(ctx context.Context, c *campaign.Campaign) (delivery.Progress, error) {
		return delivery.ReadProgress(ctx, w.db, c)
	}
	return NewService(w.campaigns, w.audiences, progress, model, time.Now, slog.New(slog.DiscardHandler))
}

// draft creates a valid draft directly, as the wizard would.
func (w world) draft(t *testing.T) *campaign.Campaign {
	t.Helper()
	c, err := w.campaigns.Create(t.Context(), campaign.Draft{
		Name:    "Spring sale",
		Segment: campaign.Segment{Version: 1, Match: campaign.MatchAll, Conditions: []campaign.Condition{{Type: campaign.ConditionCity, In: []string{"İstanbul"}}}},
		Content: campaign.Content{SMS: &campaign.SMS{Body: "Merhaba {{first_name}}!"}},
	})
	must(t, err)
	return c
}

func (w world) status(t *testing.T, id int64) campaign.Status {
	t.Helper()
	c, err := w.campaigns.Get(t.Context(), id)
	must(t, err)
	return c.Status()
}

// dispatchJobs counts the dispatch jobs queued: what actually sends a campaign.
func (w world) dispatchJobs(t *testing.T) int {
	t.Helper()
	var n int
	must(t, w.db.Raw("SELECT count(*) FROM river_job WHERE kind = 'dispatch_campaign'").Scan(&n).Error)
	return n
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// callf calls a tool with arguments that depend on a value known only when the call is made.
func callf(name string, input func() string) func([]message) (message, error) {
	return func(history []message) (message, error) { return call(name, input())(history) }
}

func TestToolsThroughTheServices(t *testing.T) {
	w := newWorld(t)
	var id int64
	idArgs := func(rest string) func() string {
		return func() string { return fmt.Sprintf(`{"campaign_id": %d%s}`, id, rest) }
	}
	model := &scripted{}
	svc := w.assistant(model)
	turn := func(replies ...func([]message) (message, error)) Turn {
		t.Helper()
		model.replies = replies
		got, err := svc.Chat(t.Context(), "", "go", "Europe/Istanbul")
		must(t, err)
		return got
	}

	created := turn(call("create_campaign", `{"name": "Welcome back", "segment": {"version": 1, "match": "all",
		"conditions": [{"type": "city", "in": ["İstanbul"]}]}, "content": {"sms": {"body": "Hi {{first_name}}"}}}`))
	if len(created.Actions) != 1 || created.Actions[0].Type != "campaign_created" || created.Actions[0].Campaign.Status != campaign.StatusDraft {
		t.Fatalf("create: actions = %+v", created.Actions)
	}
	id = created.Actions[0].Campaign.ID

	updated := turn(callf("update_campaign", idArgs(`, "content": {"email": {"subject": "Hello", "body": "Hi {{first_name}}"}}`)))
	stored, err := w.campaigns.Get(t.Context(), id)
	must(t, err)
	if len(updated.Actions) != 1 || stored.Draft().Content.SMS != nil || stored.Draft().Content.Email.Subject != "Hello" || stored.Draft().Name != "Welcome back" {
		t.Fatalf("update: actions %+v, stored %+v; want the content replaced and the rest kept", updated.Actions, stored.Draft())
	}

	turn(callf("get_campaign", idArgs("")))
	var got map[string]json.RawMessage
	must(t, json.Unmarshal([]byte(resultsOf(t, model)[0].Content), &got))
	if _, ok := got["delivery"]; ok || !strings.Contains(string(got["campaign"]), `"name":"Welcome back"`) {
		t.Errorf("get_campaign = %s, want the draft without delivery counts", resultsOf(t, model)[0].Content)
	}

	turn(call("list_campaigns", `{}`))
	if result := resultsOf(t, model)[0].Content; !strings.Contains(result, `"name":"Welcome back"`) || strings.Contains(result, "Hi {{first_name}}") {
		t.Errorf("list_campaigns = %s, want names without texts", result)
	}

	estimated := turn(callf("estimate_audience", idArgs("")))
	if a := estimated.Actions[0].Audience; a == nil || a.Matched != 3 || a.ByChannel[customer.ChannelEmail] != 3 {
		t.Errorf("estimate = %+v, want 3 customers, all reachable by email", estimated.Actions)
	}

	invalid := turn(call("create_campaign", `{"name": "Nowhere", "segment": {"version": 1, "match": "all",
		"conditions": [{"type": "city", "in": ["Atlantis"]}]}, "content": {"sms": {"body": "Hi"}}}`))
	if result := resultsOf(t, model)[0]; !result.IsError || !strings.Contains(result.Content, `"problem":"unknown","detail":"Atlantis"`) || len(invalid.Actions) != 0 {
		t.Errorf("an unknown city: result %+v, want the problem for the model to fix", result)
	}
}

func TestDispatchWaitsForApproval(t *testing.T) {
	w := newWorld(t)
	c := w.draft(t)
	model := &scripted{replies: []func([]message) (message, error){
		call("dispatch_campaign", fmt.Sprintf(`{"campaign_id": %d}`, c.ID())), say("Please approve it on the card."),
		say("Done, it's sent!"), // what a model might claim when told "yes"
	}}
	svc := w.assistant(model)

	requested, err := svc.Chat(t.Context(), "", "Send it now", "")
	must(t, err)
	if len(requested.Actions) != 1 || requested.Actions[0].Type != "dispatch_requested" || requested.Actions[0].Approval.Reach != 3 {
		t.Fatalf("actions = %+v, want an approval card with a reach of 3", requested.Actions)
	}
	if !strings.Contains(resultsOf(t, model)[0].Content, `"status":"awaiting_user_approval"`) {
		t.Errorf("tool result = %s", resultsOf(t, model)[0].Content)
	}
	// A request dispatches nothing, and neither does agreeing in the chat.
	if _, err := svc.Chat(t.Context(), requested.ConversationID, "Yes, send it.", ""); err != nil {
		t.Fatal(err)
	}
	if w.status(t, c.ID()) != campaign.StatusDraft || w.dispatchJobs(t) != 0 {
		t.Fatalf("before approval: %s with %d jobs, want a draft and none", w.status(t, c.ID()), w.dispatchJobs(t))
	}

	approval := requested.Actions[0].Approval.ID
	decision, err := svc.Decide(t.Context(), approval, true)
	if err != nil || decision.Status != "dispatched" || decision.Campaign.Status != campaign.StatusSending {
		t.Fatalf("Decide(approve) = %+v, %v", decision, err)
	}
	if _, err := svc.Decide(t.Context(), approval, true); !errors.Is(err, ErrApprovalClosed) {
		t.Errorf("approving again: error = %v, want ErrApprovalClosed", err)
	}
	if w.dispatchJobs(t) != 1 {
		t.Errorf("%d dispatch jobs, want exactly one", w.dispatchJobs(t))
	}
}

func TestDispatchApprovalOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		args       string
		before     func(t *testing.T, w world, c *campaign.Campaign) // between the request and the decision
		approve    bool
		wantErr    error
		wantStatus campaign.Status
	}{
		{name: "cancelled", approve: false, wantStatus: campaign.StatusDraft},
		{
			name: "edited after the request",
			before: func(t *testing.T, w world, c *campaign.Campaign) {
				d := c.Draft()
				d.Name = "Edited in the wizard"
				_, err := w.campaigns.Update(t.Context(), c.ID(), d)
				must(t, err)
			},
			approve: true, wantErr: campaign.ErrChanged, wantStatus: campaign.StatusDraft,
		},
		{
			name:    "scheduled",
			args:    fmt.Sprintf(`, "scheduled_at": %q`, time.Now().Add(2*time.Hour).In(time.FixedZone("+03", 3*3600)).Format(time.RFC3339)),
			approve: true, wantStatus: campaign.StatusScheduled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newWorld(t)
			c := w.draft(t)
			model := &scripted{replies: []func([]message) (message, error){
				call("dispatch_campaign", fmt.Sprintf(`{"campaign_id": %d%s}`, c.ID(), tt.args)),
			}}
			svc := w.assistant(model)
			turn, err := svc.Chat(t.Context(), "", "send", "")
			must(t, err)
			if len(turn.Actions) != 1 {
				t.Fatalf("actions = %+v; result %s", turn.Actions, resultsOf(t, model)[0].Content)
			}
			if tt.before != nil {
				tt.before(t, w, c)
			}
			if _, err := svc.Decide(t.Context(), turn.Actions[0].Approval.ID, tt.approve); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Decide() error = %v, want %v", err, tt.wantErr)
			}
			if got := w.status(t, c.ID()); got != tt.wantStatus {
				t.Errorf("status = %s, want %s", got, tt.wantStatus)
			}
		})
	}
}

func TestDispatchRequestsAreChecked(t *testing.T) {
	w := newWorld(t)
	c := w.draft(t)
	tests := []struct {
		name, args, wantError string
	}{
		{"an unknown campaign", `{"campaign_id": 999999}`, "not_found"},
		{"a time in the past", fmt.Sprintf(`{"campaign_id": %d, "scheduled_at": "2020-01-01T09:00:00+03:00"}`, c.ID()), "invalid_campaign"},
		{"more than a year ahead", fmt.Sprintf(`{"campaign_id": %d, "scheduled_at": %q}`, c.ID(), time.Now().AddDate(2, 0, 0).Format(time.RFC3339)), "invalid_campaign"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &scripted{replies: []func([]message) (message, error){call("dispatch_campaign", tt.args)}}
			turn, err := w.assistant(model).Chat(t.Context(), "", "send", "")
			must(t, err)
			if result := resultsOf(t, model)[0]; !result.IsError || !strings.Contains(result.Content, `"error":"`+tt.wantError+`"`) || len(turn.Actions) != 0 {
				t.Errorf("result = %+v, actions %+v; want %s and no card", result, turn.Actions, tt.wantError)
			}
		})
	}
	sent, err := w.campaigns.Dispatch(t.Context(), c.ID(), nil)
	must(t, err)
	model := &scripted{replies: []func([]message) (message, error){call("dispatch_campaign", fmt.Sprintf(`{"campaign_id": %d}`, sent.ID()))}}
	if _, err := w.assistant(model).Chat(t.Context(), "", "send again", ""); err != nil {
		t.Fatal(err)
	}
	if result := resultsOf(t, model)[0]; !strings.Contains(result.Content, `"error":"not_draft"`) {
		t.Errorf("a dispatched campaign: result %s, want not_draft", result.Content)
	}
}

func TestMockPlaysItsScript(t *testing.T) {
	w := newWorld(t)
	svc := w.assistant(NewMock())
	want := []string{"campaign_created", "audience_estimated", "dispatch_requested", ""}
	conversation := ""
	for i, action := range want {
		turn, err := svc.Chat(t.Context(), conversation, "anything at all", "")
		must(t, err)
		conversation = turn.ConversationID
		got := ""
		if len(turn.Actions) > 0 {
			got = turn.Actions[0].Type
		}
		if got != action || turn.Reply == "" {
			t.Errorf("turn %d: action %q, reply %q; want %q and a reply", i+1, got, turn.Reply, action)
		}
	}
}
