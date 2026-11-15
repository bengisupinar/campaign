// Package campaign is the Campaign bounded context: a marketer's message to a segment of
// customers, on SMS, email or both, from draft to delivery.
//
// Manual editing and, later, AI proposals both go through Service, so one set of rules applies
// to every campaign. Dispatch hands a campaign to the delivery capability, which sends it.
package campaign

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bengisupinar/campaign/internal/customer"
)

const (
	maxNameLength    = 100
	maxScheduleAhead = 365 * 24 * time.Hour // how far ahead a campaign may be scheduled
)

var (
	// ErrNotFound reports that no campaign has the given ID.
	ErrNotFound = errors.New("campaign not found")
	// ErrNotDraft reports an attempt to change or delete a campaign that is no longer a draft.
	ErrNotDraft = errors.New("campaign is not a draft")
	// ErrAlreadyDispatched reports a dispatch of a campaign that was dispatched before.
	ErrAlreadyDispatched = errors.New("campaign already dispatched")
	// ErrStatusChange reports a lifecycle move that CanBecome doesn't allow.
	ErrStatusChange = errors.New("campaign status change not allowed")
)

// Status is where a campaign is in its lifecycle.
type Status string

// Statuses.
const (
	StatusDraft     Status = "draft"     // being written; the only status that can change or be deleted
	StatusScheduled Status = "scheduled" // dispatched, to be sent at its scheduled time
	StatusSending   Status = "sending"   // its messages are being sent
	StatusSent      Status = "sent"      // every message reached an outcome; receipts may still arrive
	StatusFailed    Status = "failed"    // its dispatch couldn't start: the segment no longer validated
)

// CanBecome reports whether a campaign may move from s to next:
//
//	draft     → scheduled  dispatched with a send time
//	draft     → sending    dispatched to send now
//	scheduled → draft      unscheduled, to be edited again (no endpoint yet)
//	scheduled → sending    its send time arrived
//	scheduled → failed     its segment no longer validated when the time came
//	sending   → sent       every message reached an outcome
//	sending   → failed     its segment no longer validated as its dispatch started
func (s Status) CanBecome(next Status) bool {
	switch s {
	case StatusDraft:
		return next == StatusScheduled || next == StatusSending
	case StatusScheduled:
		return next == StatusDraft || next == StatusSending || next == StatusFailed
	case StatusSending:
		return next == StatusSent || next == StatusFailed
	default:
		return false
	}
}

// Draft is what a marketer, or later an AI proposal, writes: everything about a campaign that
// can change while it is a draft. Its JSON form is the body of the API's create and update
// requests.
type Draft struct {
	Name    string  `json:"name"`
	Segment Segment `json:"segment"`
	Content Content `json:"content"`
}

// validate returns d with its name trimmed and its parts copied, or a *ValidationError listing
// every problem with it.
func (d Draft) validate() (Draft, error) {
	var ps problems
	d.Name = strings.TrimSpace(d.Name)
	switch n := utf8.RuneCountInString(d.Name); {
	case n == 0:
		ps.add("name", ProblemRequired)
	case n > maxNameLength:
		ps.add("name", ProblemTooLong)
	}
	d.Segment.validate(&ps)
	d.Content.validate(&ps)
	if err := ps.err(); err != nil {
		return Draft{}, err
	}
	return d.clone(), nil
}

func (d Draft) clone() Draft {
	d.Segment = d.Segment.clone()
	d.Content = d.Content.clone()
	return d
}

// clonePtr returns a pointer to a copy of *p, or nil.
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	return new(*p)
}

// Campaign is the aggregate root: a draft and its place in the lifecycle. It copies what it is
// given and what it returns, so it changes only through its methods.
type Campaign struct {
	id          int64
	draft       Draft
	status      Status
	createdAt   time.Time
	updatedAt   time.Time // when the draft last changed
	scheduledAt time.Time // zero unless it was scheduled
	startedAt   time.Time // zero until its dispatch starts
	finishedAt  time.Time // zero until it is sent or fails
}

// New returns a draft campaign created at now. It fails with a *ValidationError listing every
// problem with d.
func New(d Draft, now time.Time) (*Campaign, error) {
	d, err := d.validate()
	if err != nil {
		return nil, err
	}
	return &Campaign{draft: d, status: StatusDraft, createdAt: now, updatedAt: now}, nil
}

// Update replaces the campaign's name, segment and content at now. Only a draft can change,
// and an invalid d leaves the campaign as it was.
func (c *Campaign) Update(d Draft, now time.Time) error {
	if err := c.ensureDraft(); err != nil {
		return err
	}
	d, err := d.validate()
	if err != nil {
		return err
	}
	c.draft, c.updatedAt = d, now
	return nil
}

// Dispatch approves the draft for sending: now, or at scheduledAt when it is set, which must be
// in the future and at most a year ahead. It fails with ErrNotDraft unless the campaign is a
// draft, and with a *ValidationError for a time it can't be scheduled at.
func (c *Campaign) Dispatch(scheduledAt *time.Time, now time.Time) error {
	if err := c.ensureDraft(); err != nil {
		return err
	}
	if scheduledAt == nil {
		c.status, c.startedAt = StatusSending, now
		return nil
	}
	at := scheduledAt.UTC().Truncate(time.Microsecond) // PostgreSQL's precision
	if !at.After(now) || at.Sub(now) > maxScheduleAhead {
		return &ValidationError{Problems: []Problem{{Field: "scheduled_at", Code: ProblemInvalid}}}
	}
	c.status, c.scheduledAt = StatusScheduled, at
	return nil
}

// Start begins sending a scheduled campaign whose time has come. A draft goes through Dispatch
// instead.
func (c *Campaign) Start(now time.Time) error {
	if c.status != StatusScheduled {
		return fmt.Errorf("%w: only a scheduled campaign starts, not a %s one", ErrStatusChange, c.status)
	}
	return c.moveTo(StatusSending, &c.startedAt, now)
}

// Finish marks a sending campaign sent: every one of its messages reached an outcome.
func (c *Campaign) Finish(now time.Time) error { return c.moveTo(StatusSent, &c.finishedAt, now) }

// Fail marks a scheduled or sending campaign failed: its dispatch couldn't go ahead.
func (c *Campaign) Fail(now time.Time) error { return c.moveTo(StatusFailed, &c.finishedAt, now) }

func (c *Campaign) moveTo(next Status, at *time.Time, now time.Time) error {
	if !c.status.CanBecome(next) {
		return fmt.Errorf("%w: %s to %s", ErrStatusChange, c.status, next)
	}
	c.status, *at = next, now
	return nil
}

func (c *Campaign) ensureDraft() error {
	if c.status != StatusDraft {
		return ErrNotDraft
	}
	return nil
}

// ID returns the campaign's ID, which the database assigns when the campaign is first saved.
func (c *Campaign) ID() int64 { return c.id }

// Draft returns a copy of the campaign's name, segment and content.
func (c *Campaign) Draft() Draft { return c.draft.clone() }

// Channels returns the channels the campaign uses: email before SMS.
func (c *Campaign) Channels() []customer.Channel { return c.draft.Content.Channels() }

// Status returns where the campaign is in its lifecycle.
func (c *Campaign) Status() Status { return c.status }

// CreatedAt returns when the campaign was created.
func (c *Campaign) CreatedAt() time.Time { return c.createdAt }

// UpdatedAt returns when the campaign's draft last changed.
func (c *Campaign) UpdatedAt() time.Time { return c.updatedAt }

// ScheduledAt returns when a scheduled campaign is due, or zero.
func (c *Campaign) ScheduledAt() time.Time { return c.scheduledAt }

// StartedAt returns when the campaign's dispatch started, or zero. Its segment's windows end
// then.
func (c *Campaign) StartedAt() time.Time { return c.startedAt }

// FinishedAt returns when the campaign was sent or failed, or zero.
func (c *Campaign) FinishedAt() time.Time { return c.finishedAt }
