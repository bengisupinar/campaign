package campaign

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/bengisupinar/campaign/internal/customer"
)

// now is when the test campaigns are created.
var now = time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)

// validDraft returns the Phase 2A example as a campaign: customers in İstanbul who added to
// their basket in the last 30 days without buying, reached by SMS.
func validDraft() Draft {
	return Draft{
		Name: "Abandoned baskets in İstanbul",
		Segment: Segment{Version: SegmentVersion, Match: MatchAll, Conditions: []Condition{
			{Type: ConditionCity, In: []string{"İstanbul"}},
			{Type: ConditionBasket, WithinDays: new(30)},
			{Type: ConditionPurchased, WithinDays: new(30), Negate: true},
		}},
		Content: Content{SMS: &SMS{Body: "Merhaba {{first_name}}, sepetin seni bekliyor!"}},
	}
}

func TestStatusCanBecome(t *testing.T) {
	allowed := map[[2]Status]bool{
		{StatusDraft, StatusScheduled}:   true,
		{StatusDraft, StatusSending}:     true,
		{StatusScheduled, StatusDraft}:   true,
		{StatusScheduled, StatusSending}: true,
	}
	statuses := []Status{StatusDraft, StatusScheduled, StatusSending}
	for _, from := range statuses {
		for _, to := range statuses {
			if got, want := from.CanBecome(to), allowed[[2]Status{from, to}]; got != want {
				t.Errorf("%s.CanBecome(%s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestNew(t *testing.T) {
	d := validDraft()
	d.Name = "  Abandoned baskets in İstanbul\n"

	c, err := New(d, now)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if c.ID() != 0 || c.Status() != StatusDraft || !c.CreatedAt().Equal(now) || !c.UpdatedAt().Equal(now) {
		t.Errorf("New() = ID %d, %s, created %v, updated %v; want an unsaved draft created and updated at %v",
			c.ID(), c.Status(), c.CreatedAt(), c.UpdatedAt(), now)
	}
	if got, want := c.Draft(), validDraft(); !reflect.DeepEqual(got, want) {
		t.Errorf("Draft() = %+v, want %+v (name trimmed)", got, want)
	}
	if got, want := c.Channels(), []customer.Channel{customer.ChannelSMS}; !slices.Equal(got, want) {
		t.Errorf("Channels() = %v, want %v", got, want)
	}

	if _, err := New(Draft{}, now); !errors.As(err, new(*ValidationError)) {
		t.Errorf("New(empty draft) error = %v, want a *ValidationError", err)
	}
}

func TestUpdate(t *testing.T) {
	later := now.Add(time.Hour)
	changed := validDraft()
	changed.Name = "Come back to your basket"
	changed.Content = Content{Email: &Email{Subject: "{{first_name}}, your basket misses you", Body: "It's still waiting."}}

	tests := []struct {
		name        string
		status      Status
		draft       Draft
		wantErr     error
		wantInvalid bool
	}{
		{name: "draft", status: StatusDraft, draft: changed},
		{name: "scheduled", status: StatusScheduled, draft: changed, wantErr: ErrNotDraft},
		{name: "sending", status: StatusSending, draft: changed, wantErr: ErrNotDraft},
		{name: "invalid changes", status: StatusDraft, draft: Draft{Name: "No content"}, wantInvalid: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(validDraft(), now)
			if err != nil {
				t.Fatalf("New(): %v", err)
			}
			c.status = tt.status

			err = c.Update(tt.draft, later)

			if tt.wantInvalid {
				if !errors.As(err, new(*ValidationError)) {
					t.Fatalf("Update() error = %v, want a *ValidationError", err)
				}
			} else if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Update() error = %v, want %v", err, tt.wantErr)
			}
			wantDraft, wantUpdated := validDraft(), now // unchanged on error
			if err == nil {
				wantDraft, wantUpdated = changed, later
			}
			if got := c.Draft(); !reflect.DeepEqual(got, wantDraft) {
				t.Errorf("Draft() = %+v, want %+v", got, wantDraft)
			}
			if !c.UpdatedAt().Equal(wantUpdated) || !c.CreatedAt().Equal(now) {
				t.Errorf("created %v, updated %v; want created %v, updated %v", c.CreatedAt(), c.UpdatedAt(), now, wantUpdated)
			}
		})
	}
}

func TestCampaignKeepsItsOwnCopy(t *testing.T) {
	d := validDraft()
	c, err := New(d, now)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	d.Content.SMS.Body = "changed by the caller"
	d.Segment.Conditions[0].In[0] = "Ankara"
	*d.Segment.Conditions[1].WithinDays = 1
	got := c.Draft()
	got.Content.SMS.Body = "changed through Draft()"
	got.Segment.Conditions[0].In[0] = "Bursa"

	if got, want := c.Draft(), validDraft(); !reflect.DeepEqual(got, want) {
		t.Errorf("Draft() = %+v after outside changes, want %+v", got, want)
	}
}

// problemsOf returns the problems a draft fails validation with, or nil when it is valid.
func problemsOf(t *testing.T, d Draft) []Problem {
	t.Helper()
	_, err := New(d, now)
	var invalid *ValidationError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &invalid):
		return invalid.Problems
	default:
		t.Fatalf("New() error = %v, want nil or a *ValidationError", err)
		return nil
	}
}
