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
		{StatusScheduled, StatusFailed}:  true,
		{StatusSending, StatusSent}:      true,
		{StatusSending, StatusFailed}:    true,
	}
	statuses := []Status{StatusDraft, StatusScheduled, StatusSending, StatusSent, StatusFailed}
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

func TestDispatch(t *testing.T) {
	later := new(now.Add(time.Hour))
	tests := []struct {
		name          string
		status        Status
		scheduledAt   *time.Time
		wantStatus    Status
		wantErr       error
		wantInvalid   bool
		wantStarted   time.Time
		wantScheduled time.Time
	}{
		{name: "now", status: StatusDraft, wantStatus: StatusSending, wantStarted: now},
		{name: "scheduled", status: StatusDraft, scheduledAt: later, wantStatus: StatusScheduled, wantScheduled: *later},
		{name: "a year ahead", status: StatusDraft, scheduledAt: new(now.Add(maxScheduleAhead)), wantStatus: StatusScheduled, wantScheduled: now.Add(maxScheduleAhead)},
		{name: "in the past", status: StatusDraft, scheduledAt: new(now.Add(-time.Minute)), wantStatus: StatusDraft, wantInvalid: true},
		{name: "this very moment", status: StatusDraft, scheduledAt: new(now), wantStatus: StatusDraft, wantInvalid: true},
		{name: "beyond a year", status: StatusDraft, scheduledAt: new(now.Add(maxScheduleAhead + time.Second)), wantStatus: StatusDraft, wantInvalid: true},
		{name: "already scheduled", status: StatusScheduled, wantStatus: StatusScheduled, wantErr: ErrNotDraft},
		{name: "already sent", status: StatusSent, wantStatus: StatusSent, wantErr: ErrNotDraft},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := mustNewCampaign(t, validDraft())
			c.status = tt.status

			err := c.Dispatch(tt.scheduledAt, now)

			if tt.wantInvalid {
				if got := problemsOfErr(t, err); len(got) != 1 || got[0].Field != "scheduled_at" {
					t.Errorf("Dispatch() problems = %+v, want scheduled_at", got)
				}
			} else if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Dispatch() error = %v, want %v", err, tt.wantErr)
			}
			if c.Status() != tt.wantStatus || !c.StartedAt().Equal(tt.wantStarted) || !c.ScheduledAt().Equal(tt.wantScheduled) {
				t.Errorf("after Dispatch(): %s, started %v, scheduled %v; want %s, %v, %v",
					c.Status(), c.StartedAt(), c.ScheduledAt(), tt.wantStatus, tt.wantStarted, tt.wantScheduled)
			}
		})
	}
}

func TestLifecycleMoves(t *testing.T) {
	later := now.Add(time.Hour)
	tests := []struct {
		name    string
		from    Status
		move    func(*Campaign, time.Time) error
		want    Status
		wantErr error
	}{
		{"a scheduled campaign starts", StatusScheduled, (*Campaign).Start, StatusSending, nil},
		{"a sending campaign is sent", StatusSending, (*Campaign).Finish, StatusSent, nil},
		{"a scheduled campaign fails", StatusScheduled, (*Campaign).Fail, StatusFailed, nil},
		{"a sending campaign fails", StatusSending, (*Campaign).Fail, StatusFailed, nil},
		{"a draft can't start", StatusDraft, (*Campaign).Start, StatusDraft, ErrStatusChange},
		{"a scheduled campaign can't be sent", StatusScheduled, (*Campaign).Finish, StatusScheduled, ErrStatusChange},
		{"a sent campaign can't fail", StatusSent, (*Campaign).Fail, StatusSent, ErrStatusChange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := mustNewCampaign(t, validDraft())
			c.status = tt.from
			if err := tt.move(c, later); !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want %v", err, tt.wantErr)
			}
			if c.Status() != tt.want {
				t.Errorf("status = %s, want %s", c.Status(), tt.want)
			}
			if tt.wantErr == nil && !c.StartedAt().Equal(later) && !c.FinishedAt().Equal(later) {
				t.Errorf("started %v, finished %v; want the move's time recorded", c.StartedAt(), c.FinishedAt())
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
	return problemsOfErr(t, err)
}

// problemsOfErr returns the problems in a *ValidationError, or nil for a nil error.
func problemsOfErr(t *testing.T, err error) []Problem {
	t.Helper()
	var invalid *ValidationError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &invalid):
		return invalid.Problems
	default:
		t.Fatalf("error = %v, want nil or a *ValidationError", err)
		return nil
	}
}
