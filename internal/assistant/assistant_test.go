package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
	"github.com/bengisupinar/campaign/internal/delivery"
	"github.com/bengisupinar/campaign/internal/segmentation"
)

var now = time.Date(2026, 11, 16, 7, 15, 0, 0, time.UTC)

// scripted is a model that answers from a script and records what it was sent. Past the end
// of its script it says "done".
type scripted struct {
	mu      sync.Mutex
	replies []func(history []message) (message, error)
	seen    [][]message
}

func (m *scripted) reply(_ context.Context, _ string, history []message) (message, error) {
	m.mu.Lock()
	m.seen = append(m.seen, slices.Clone(history))
	if len(m.replies) == 0 {
		m.mu.Unlock()
		return message{assistant: true, text: "done"}, nil
	}
	next := m.replies[0]
	m.replies = m.replies[1:]
	m.mu.Unlock()
	return next(history) // unlocked: a reply may block, holding its turn in flight
}

func say(text string) func([]message) (message, error) {
	return func([]message) (message, error) { return message{assistant: true, text: text}, nil }
}

func call(name, input string) func([]message) (message, error) {
	return func(history []message) (message, error) {
		id := "call_" + name + "_" + string(rune('a'+len(history)))
		return message{assistant: true, calls: []toolCall{{ID: id, Name: name, Input: json.RawMessage(input)}}}, nil
	}
}

func fail(err error) func([]message) (message, error) {
	return func([]message) (message, error) { return message{}, err }
}

// fakeCampaigns records the calls the assistant makes. Without a campaign it fails each of
// them: the unit tests check what is (not) called, the integration tests what the real
// services do. With one, every call answers a copy of it.
type fakeCampaigns struct {
	mu       sync.Mutex
	calls    []string
	campaign *campaign.Campaign
}

func (f *fakeCampaigns) record(name string) (*campaign.Campaign, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	if f.campaign == nil {
		return nil, campaign.ErrNotFound
	}
	c := *f.campaign
	return &c, nil
}

func (f *fakeCampaigns) Create(context.Context, campaign.Draft) (*campaign.Campaign, error) {
	return f.record("Create")
}

func (f *fakeCampaigns) Update(context.Context, int64, campaign.Draft) (*campaign.Campaign, error) {
	return f.record("Update")
}

func (f *fakeCampaigns) Get(context.Context, int64) (*campaign.Campaign, error) {
	return f.record("Get")
}

func (f *fakeCampaigns) List(context.Context) ([]*campaign.Campaign, error) {
	_, err := f.record("List")
	return nil, err
}

func (f *fakeCampaigns) DispatchApproved(context.Context, int64, time.Time, *time.Time) (*campaign.Campaign, error) {
	return f.record("DispatchApproved")
}

// draft is an unsaved draft campaign, for a fakeCampaigns that succeeds.
func draft(t *testing.T) *campaign.Campaign {
	t.Helper()
	var args createArgs
	if err := json.Unmarshal([]byte(mockDraft), &args); err != nil {
		t.Fatal(err)
	}
	c, err := campaign.New(campaign.Draft{Name: args.Name, Segment: args.Segment, Content: args.Content}, now)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// fakeAudiences answers counts and reference names.
type fakeAudiences struct{ audience segmentation.Audience }

func (f fakeAudiences) CountAudience(_ context.Context, q segmentation.Query) (segmentation.Audience, error) {
	if problems := q.Segment.Problems(); len(problems) > 0 {
		return segmentation.Audience{}, &campaign.ValidationError{Problems: problems}
	}
	return f.audience, nil
}

func (fakeAudiences) Options(context.Context) (segmentation.Options, error) {
	return segmentation.Options{
		Countries:  []segmentation.CountryOption{{Code: "TR", Name: "Türkiye"}},
		Cities:     []segmentation.CityOption{{Name: "İstanbul", Country: "TR"}},
		Categories: []string{"Books"},
		Languages:  customer.Languages(),
	}, nil
}

func noProgress(context.Context, *campaign.Campaign) (delivery.Progress, error) {
	return delivery.Progress{}, nil
}

func newTestService(model Model, cs campaigns) *Service {
	audience := segmentation.Audience{Matched: 120, Reachable: 100, ByChannel: map[customer.Channel]int{customer.ChannelEmail: 80, customer.ChannelSMS: 60}}
	return NewService(cs, fakeAudiences{audience: audience}, noProgress, model, func() time.Time { return now }, slog.New(slog.DiscardHandler))
}

// resultsOf returns the tool results the model was sent last.
func resultsOf(t *testing.T, m *scripted) []toolResult {
	t.Helper()
	history := m.seen[len(m.seen)-1]
	return history[len(history)-1].results
}

func TestConversationKeepsItsHistory(t *testing.T) {
	model := &scripted{replies: []func([]message) (message, error){say("Which city?"), say("İstanbul it is.")}}
	svc := newTestService(model, &fakeCampaigns{})

	first, err := svc.Chat(t.Context(), "", "Bir kampanya hazırla", "Europe/Istanbul")
	if err != nil || first.ConversationID == "" || first.Reply != "Which city?" {
		t.Fatalf("first turn = %+v, %v", first, err)
	}
	second, err := svc.Chat(t.Context(), first.ConversationID, "İstanbul", "Europe/Istanbul")
	if err != nil || second.ConversationID != first.ConversationID || second.Reply != "İstanbul it is." {
		t.Fatalf("second turn = %+v, %v", second, err)
	}

	// The second call sees the whole conversation: both user messages and the first reply.
	history := model.seen[1]
	if len(history) != 3 || !history[1].assistant || history[1].text != "Which city?" {
		t.Fatalf("history sent with the second message = %+v", history)
	}
	if !strings.HasPrefix(history[2].text, "[Current time: Monday 2026-11-16 10:15 +03:00 (Europe/Istanbul)]") ||
		!strings.HasSuffix(history[2].text, "İstanbul") {
		t.Errorf("user message = %q, want the user's time, then their text", history[2].text)
	}
}

func TestInvalidToolCallsRunNothing(t *testing.T) {
	tests := []struct {
		name, tool, input, wantError string
	}{
		{"an unknown tool", "delete_campaign", `{"campaign_id": 1}`, "unknown_tool"},
		{"a tool to approve dispatches", "approve_dispatch", `{"campaign_id": 1}`, "unknown_tool"},
		{"an unknown argument", "get_campaign", `{"campaign_id": 1, "sql": "DROP TABLE campaigns"}`, "invalid_arguments"},
		{"a wrong type", "get_campaign", `{"campaign_id": "1"}`, "invalid_arguments"},
		{"a missing id", "dispatch_campaign", `{}`, "invalid_arguments"},
		{"nothing to change", "update_campaign", `{"campaign_id": 1}`, "invalid_arguments"},
		{"segment and campaign together", "estimate_audience", `{"campaign_id": 1, "segment": {"version": 1, "match": "all", "conditions": []}}`, "invalid_arguments"},
		{"neither segment nor campaign", "estimate_audience", `{}`, "invalid_arguments"},
		{"a condition field the DSL lacks", "create_campaign", `{"name": "x", "segment": {"version": 1, "match": "all", "conditions": [{"type": "city", "in": ["A"], "where": "1=1"}]}, "content": {"sms": {"body": "Hi"}}}`, "invalid_arguments"},
		{"a time that isn't RFC 3339", "dispatch_campaign", `{"campaign_id": 1, "scheduled_at": "tomorrow 9am"}`, "invalid_arguments"},
		{"not JSON", "list_campaigns", `{`, "invalid_arguments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := &scripted{replies: []func([]message) (message, error){call(tt.tool, tt.input)}}
			cs := &fakeCampaigns{}
			turn, err := newTestService(model, cs).Chat(t.Context(), "", "go", "")
			if err != nil {
				t.Fatalf("Chat(): %v", err)
			}
			results := resultsOf(t, model)
			if len(results) != 1 || !results[0].IsError || !strings.Contains(results[0].Content, `"error":"`+tt.wantError+`"`) {
				t.Fatalf("result = %+v, want error %q", results, tt.wantError)
			}
			if len(cs.calls) != 0 || len(turn.Actions) != 0 {
				t.Errorf("calls %v and actions %+v, want none", cs.calls, turn.Actions)
			}
		})
	}
}

func TestEstimateReturnsCountsOnly(t *testing.T) {
	model := &scripted{replies: []func([]message) (message, error){
		call("estimate_audience", `{"segment": {"version": 1, "match": "all", "conditions": [{"type": "city", "in": ["İstanbul"]}]}}`),
	}}
	turn, err := newTestService(model, &fakeCampaigns{}).Chat(t.Context(), "", "how many?", "")
	if err != nil {
		t.Fatalf("Chat(): %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(resultsOf(t, model)[0].Content), &result); err != nil {
		t.Fatalf("result: %v", err)
	}
	keys := make([]string, 0, len(result))
	for k := range result {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"by_channel", "matched", "reachable"}) {
		t.Errorf("estimate result has %v, want only the counts", keys)
	}
	if len(turn.Actions) != 1 || turn.Actions[0].Type != "audience_estimated" || turn.Actions[0].Audience.Matched != 120 {
		t.Errorf("actions = %+v, want the estimate card", turn.Actions)
	}
}

func TestDomainProblemsGoBackToTheModel(t *testing.T) {
	model := &scripted{replies: []func([]message) (message, error){
		call("estimate_audience", `{"segment": {"version": 1, "match": "all", "conditions": [{"type": "city"}]}}`),
	}}
	if _, err := newTestService(model, &fakeCampaigns{}).Chat(t.Context(), "", "how many?", ""); err != nil {
		t.Fatalf("Chat(): %v", err)
	}
	result := resultsOf(t, model)[0]
	want := `{"error":"invalid_campaign","problems":[{"field":"segment.conditions[0].in","problem":"required"}]}`
	if !result.IsError || result.Content != want {
		t.Errorf("result = %+v, want %s", result, want)
	}
}

func TestTurnLimits(t *testing.T) {
	t.Run("the last model reply runs no tools", func(t *testing.T) {
		model := &scripted{}
		for range maxModelCalls - 1 {
			model.replies = append(model.replies, call("list_campaigns", `{}`))
		}
		model.replies = append(model.replies, call("create_campaign", mockDraft))
		cs := &fakeCampaigns{campaign: draft(t)}
		svc := newTestService(model, cs)
		turn, err := svc.Chat(t.Context(), "", "loop", "")
		if err != nil || turn.Notice != "step_limit" || len(model.seen) != maxModelCalls {
			t.Fatalf("turn = %+v, %v after %d model calls; want the step limit after %d", turn, err, len(model.seen), maxModelCalls)
		}
		if slices.Contains(cs.calls, "Create") || len(turn.Actions) != 0 {
			t.Errorf("calls %v, actions %+v: the last reply's tool ran", cs.calls, turn.Actions)
		}
		// The next message carries on from a valid history: the call that didn't run is answered.
		if _, err := svc.Chat(t.Context(), turn.ConversationID, "go on", ""); err != nil {
			t.Fatalf("next turn: %v", err)
		}
		history := model.seen[len(model.seen)-1]
		if got := history[len(history)-2].results; len(got) != 1 || !strings.Contains(got[0].Content, "too_many_tool_calls") {
			t.Errorf("the last reply's call was answered %+v, want too_many_tool_calls", got)
		}
	})

	many := message{assistant: true}
	for i := range maxToolCalls + 2 {
		many.calls = append(many.calls, toolCall{ID: string(rune('a' + i)), Name: "list_campaigns", Input: json.RawMessage(`{}`)})
	}
	manyCalls := func([]message) (message, error) { return many, nil }
	t.Run("tool calls past the budget don't run", func(t *testing.T) {
		model := &scripted{replies: []func([]message) (message, error){manyCalls, say("Ten of them ran.")}}
		cs := &fakeCampaigns{}
		turn, err := newTestService(model, cs).Chat(t.Context(), "", "many", "")
		if err != nil || turn.Reply != "Ten of them ran." || turn.Notice != "" {
			t.Fatalf("turn = %+v, %v; want the model's reply", turn, err)
		}
		results := resultsOf(t, model)
		if len(cs.calls) != maxToolCalls || !strings.Contains(results[maxToolCalls].Content, "too_many_tool_calls") {
			t.Errorf("ran %d calls, last result %q; want %d, then too_many_tool_calls", len(cs.calls), results[maxToolCalls].Content, maxToolCalls)
		}
	})
	t.Run("one model reply after the budget is spent", func(t *testing.T) {
		model := &scripted{replies: []func([]message) (message, error){manyCalls, call("list_campaigns", `{}`), call("list_campaigns", `{}`)}}
		cs := &fakeCampaigns{}
		turn, err := newTestService(model, cs).Chat(t.Context(), "", "many", "")
		if err != nil || turn.Notice != "step_limit" || len(model.seen) != 2 || len(cs.calls) != maxToolCalls {
			t.Errorf("turn = %+v, %v after %d model calls and %d tool calls; want the step limit after 2 and %d",
				turn, err, len(model.seen), len(cs.calls), maxToolCalls)
		}
	})
}

func TestModelFailures(t *testing.T) {
	busy := &modelError{code: "assistant_busy", status: 429}
	model := &scripted{replies: []func([]message) (message, error){
		call("list_campaigns", `{}`), fail(busy), // the first turn fails after a tool ran
		say("Hello again."),
	}}
	svc := newTestService(model, &fakeCampaigns{})
	if _, err := svc.Chat(t.Context(), "", "first", ""); !errors.Is(err, busy) {
		t.Fatalf("Chat() error = %v, want the model's", err)
	}
	turn, err := svc.Chat(t.Context(), "", "second", "")
	if err != nil || turn.Reply != "Hello again." {
		t.Fatalf("next turn = %+v, %v", turn, err)
	}

	refusing := &scripted{replies: []func([]message) (message, error){
		func([]message) (message, error) { return message{assistant: true, refused: true}, nil },
	}}
	turn, err = newTestService(refusing, &fakeCampaigns{}).Chat(t.Context(), "", "something harmful", "")
	if err != nil || turn.Notice != "refused" || turn.Reply != "" {
		t.Errorf("refused turn = %+v, %v; want the refusal notice", turn, err)
	}
}

func TestFailedTurns(t *testing.T) {
	tests := []struct {
		name, tool, input string
		cancelled         bool   // the request is cancelled (the client left) as the model fails
		wantCard          string // the turn is kept, with this card; "": it is dropped
	}{
		{"after a draft was created", "create_campaign", mockDraft, false, "campaign_created"},
		{"after a draft was changed", "update_campaign", `{"campaign_id": 1, "name": "Renamed"}`, false, "campaign_updated"},
		{"after a dispatch was requested", "dispatch_campaign", `{"campaign_id": 1}`, false, "dispatch_requested"},
		{"cancelled after a draft was created", "create_campaign", mockDraft, true, "campaign_created"},
		{"after an estimate", "estimate_audience", `{"campaign_id": 1}`, false, ""},
		{"after a read", "get_campaign", `{"campaign_id": 1}`, false, ""},
		{"after a refused dispatch request", "dispatch_campaign", `{"campaign_id": 1, "scheduled_at": "2020-01-01T09:00:00Z"}`, false, ""},
		{"cancelled after a read", "list_campaigns", `{}`, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failing := fail(&modelError{code: "assistant_unavailable", status: 529})
			if tt.cancelled {
				failing = func([]message) (message, error) {
					cancel()
					return message{}, &modelError{code: "assistant_timeout", err: context.Canceled}
				}
			}
			model := &scripted{replies: []func([]message) (message, error){
				say("Hi."), call(tt.tool, tt.input), failing, say("Next."),
			}}
			svc := newTestService(model, &fakeCampaigns{campaign: draft(t)})
			first, _ := svc.Chat(t.Context(), "", "one", "")

			turn, err := svc.Chat(ctx, first.ConversationID, "two", "")
			if tt.wantCard != "" {
				if err != nil || turn.Notice != "interrupted" || len(turn.Actions) != 1 || turn.Actions[0].Type != tt.wantCard {
					t.Fatalf("turn = %+v, %v; want the %s card and the interrupted notice", turn, err, tt.wantCard)
				}
			} else if err == nil {
				t.Fatalf("turn = %+v, want the model's error", turn)
			}

			// The next message sees the failed turn only if it was kept: its message, the tool
			// call and its result.
			if _, err := svc.Chat(t.Context(), first.ConversationID, "three", ""); err != nil {
				t.Fatalf("next turn: %v", err)
			}
			history := model.seen[len(model.seen)-1]
			want := 3 // one, Hi., three
			if tt.wantCard != "" {
				want = 6 // one, Hi., two, the call, its result, three
			}
			if len(history) != want || !strings.HasSuffix(history[len(history)-1].text, "three") {
				t.Errorf("the next message was sent with %d messages, want %d", len(history), want)
			}
		})
	}
}

func TestChatRefuses(t *testing.T) {
	svc := newTestService(&scripted{}, &fakeCampaigns{})
	if _, err := svc.Chat(t.Context(), "", "   ", ""); !errors.Is(err, ErrInvalidMessage) {
		t.Errorf("an empty message: error = %v", err)
	}
	if _, err := svc.Chat(t.Context(), "", strings.Repeat("ş", maxMessageLength+1), ""); !errors.Is(err, ErrInvalidMessage) {
		t.Errorf("a long message: error = %v", err)
	}
	if _, err := svc.Chat(t.Context(), "nope", "hi", ""); !errors.Is(err, ErrConversationNotFound) {
		t.Errorf("an unknown conversation: error = %v", err)
	}

	turn, _ := svc.Chat(t.Context(), "", "hi", "")
	conv, _ := svc.store.open(turn.ConversationID, now) // a turn in progress
	if _, err := svc.Chat(t.Context(), turn.ConversationID, "hi again", ""); !errors.Is(err, ErrConversationBusy) {
		t.Errorf("a turn during another: error = %v", err)
	}
	svc.store.release(conv, now)
	conv.turns = maxUserTurns
	if _, err := svc.Chat(t.Context(), turn.ConversationID, "and again", ""); !errors.Is(err, ErrConversationFull) {
		t.Errorf("a long conversation: error = %v", err)
	}
}

// TestTurnInFlight holds a turn in progress while other goroutines send it messages, fill the
// store and move past its time to live: the turn alone runs, and its conversation stays.
func TestTurnInFlight(t *testing.T) {
	started, proceed := make(chan struct{}), make(chan struct{})
	model := &scripted{replies: []func([]message) (message, error){
		say("Hi."),
		func([]message) (message, error) {
			close(started)
			<-proceed // held in flight until the test lets it go
			return message{assistant: true, text: "Still here."}, nil
		},
		say("And again."),
	}}
	svc := newTestService(model, &fakeCampaigns{})
	first, err := svc.Chat(t.Context(), "", "one", "")
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	done := make(chan error)
	go func() {
		_, err := svc.Chat(t.Context(), first.ConversationID, "two", "")
		done <- err
	}()
	<-started

	var wg sync.WaitGroup
	var busy atomic.Int32
	for range 10 {
		wg.Go(func() {
			if _, err := svc.Chat(t.Context(), first.ConversationID, "again", ""); errors.Is(err, ErrConversationBusy) {
				busy.Add(1)
			}
		})
	}
	later := now.Add(conversationTTL + time.Minute)
	for range maxConversations {
		wg.Go(func() {
			if c, err := svc.store.open("", later); err == nil {
				svc.store.release(c, later)
			}
		})
	}
	wg.Wait()
	close(proceed)
	if err := <-done; err != nil {
		t.Fatalf("the turn in flight: %v", err)
	}
	if busy.Load() != 10 {
		t.Errorf("%d of 10 messages sent during the turn were refused as busy, want all", busy.Load())
	}
	if len(svc.store.conversations) != maxConversations || svc.store.conversations[first.ConversationID] == nil {
		t.Fatalf("%d conversations, ours kept: %t; want %d, ours among them",
			len(svc.store.conversations), svc.store.conversations[first.ConversationID] != nil, maxConversations)
	}
	turn, err := svc.Chat(t.Context(), first.ConversationID, "three", "")
	if err != nil || turn.Reply != "And again." || len(model.seen[len(model.seen)-1]) != 5 {
		t.Errorf("the turn after = %+v, %v; want the conversation to carry on", turn, err)
	}
}

func TestStore(t *testing.T) {
	at := func(d time.Duration) time.Time { return now.Add(d) }

	t.Run("one turn at a time", func(t *testing.T) {
		s := newStore()
		c, _ := s.open("", now)
		if _, err := s.open(c.id, now); !errors.Is(err, ErrConversationBusy) {
			t.Errorf("a second turn: error = %v, want ErrConversationBusy", err)
		}
		s.release(c, now)
		if _, err := s.open(c.id, now); err != nil {
			t.Errorf("a turn after the first: %v", err)
		}
	})

	t.Run("time to live counts from the end of the last turn", func(t *testing.T) {
		s := newStore()
		c, _ := s.open("", now)
		s.release(c, at(time.Hour)) // a turn that ended an hour after it started
		if _, err := s.open(c.id, at(time.Hour+conversationTTL)); err != nil {
			t.Fatalf("within its time to live: %v", err)
		}
		s.release(c, at(time.Hour+conversationTTL))
		if _, err := s.open(c.id, at(time.Hour+2*conversationTTL+time.Second)); !errors.Is(err, ErrConversationNotFound) {
			t.Errorf("past its time to live: error = %v, want ErrConversationNotFound", err)
		}
	})

	t.Run("a busy conversation is not evicted", func(t *testing.T) {
		s := newStore()
		busy, _ := s.open("", now)
		later := at(conversationTTL + time.Minute) // past its time to live, with newer ones filling the store
		for range maxConversations + 1 {
			c, err := s.open("", later)
			if err != nil {
				t.Fatalf("open(): %v", err)
			}
			s.release(c, later)
		}
		if len(s.conversations) != maxConversations || s.conversations[busy.id] != busy {
			t.Errorf("%d conversations, the busy one kept: %t; want %d, the busy one among them",
				len(s.conversations), s.conversations[busy.id] == busy, maxConversations)
		}
	})

	t.Run("a store full of busy conversations refuses another", func(t *testing.T) {
		s := newStore()
		for range maxConversations {
			if _, err := s.open("", now); err != nil {
				t.Fatalf("open(): %v", err)
			}
		}
		if _, err := s.open("", now); !errors.Is(err, ErrTooManyConversations) {
			t.Errorf("one more: error = %v, want ErrTooManyConversations", err)
		}
	})

	t.Run("dispatch requests", func(t *testing.T) {
		s := newStore()
		first := s.addApproval(approval{conversationID: "c", campaignID: 1, created: now})
		second := s.addApproval(approval{conversationID: "c", campaignID: 1, created: now})
		if _, err := s.closeApproval(first, now); !errors.Is(err, ErrApprovalClosed) {
			t.Errorf("a superseded request: error = %v, want ErrApprovalClosed", err)
		}
		if _, err := s.closeApproval(second, now); err != nil {
			t.Errorf("the latest request: %v", err)
		}
		if _, err := s.closeApproval(second, now); !errors.Is(err, ErrApprovalClosed) {
			t.Errorf("a decided request: error = %v, want ErrApprovalClosed", err)
		}
		if _, err := s.closeApproval("unknown", now); !errors.Is(err, ErrApprovalNotFound) {
			t.Errorf("an unknown request: error = %v, want ErrApprovalNotFound", err)
		}
	})
}

func TestToolsJSONMatchesTheAllowlist(t *testing.T) {
	definitions, _, err := toolDefinitions()
	if err != nil {
		t.Fatalf("toolDefinitions(): %v", err)
	}
	names := make([]string, len(definitions))
	for i, d := range definitions {
		names[i] = d.Name
	}
	want := []string{"create_campaign", "update_campaign", "get_campaign", "list_campaigns", "estimate_audience", "dispatch_campaign"}
	if !slices.Equal(names, want) {
		t.Errorf("tools.json describes %v, want %v", names, want)
	}
	for _, name := range want {
		model := &scripted{replies: []func([]message) (message, error){call(name, `{"no_such_field": 1}`)}}
		if _, err := newTestService(model, &fakeCampaigns{}).Chat(t.Context(), "", "go", ""); err != nil {
			t.Fatalf("Chat(): %v", err)
		}
		if got := resultsOf(t, model)[0].Content; strings.Contains(got, "unknown_tool") {
			t.Errorf("%s is described but not run: %s", name, got)
		}
	}
}
