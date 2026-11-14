// Package campaign is the Campaign bounded context: a marketer's message to a segment of
// customers, on SMS, email or both, from draft to delivery.
//
// Manual editing and, later, AI proposals both go through Service, so one set of rules applies
// to every campaign. For now campaigns are only drafted: nothing schedules or sends them yet.
package campaign

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bengisupinar/campaign/internal/customer"
)

const maxNameLength = 100

var (
	// ErrNotFound reports that no campaign has the given ID.
	ErrNotFound = errors.New("campaign not found")
	// ErrNotDraft reports an attempt to change or delete a campaign that is no longer a draft.
	ErrNotDraft = errors.New("campaign is not a draft")
)

// Status is where a campaign is in its lifecycle.
type Status string

// Statuses. Only the start of delivery is modelled: how a campaign finishes is decided when
// delivery is built, which will add statuses and transitions.
const (
	StatusDraft     Status = "draft"     // being written; the only status that can change or be deleted
	StatusScheduled Status = "scheduled" // approved, to be sent at a set time
	StatusSending   Status = "sending"   // approved and handed to delivery
)

// CanBecome reports whether a campaign may move from s to next:
//
//	draft     → scheduled  approved with a send time
//	draft     → sending    approved to send now
//	scheduled → draft      unscheduled, to be edited again
//	scheduled → sending    its send time arrived
func (s Status) CanBecome(next Status) bool {
	switch s {
	case StatusDraft:
		return next == StatusScheduled || next == StatusSending
	case StatusScheduled:
		return next == StatusDraft || next == StatusSending
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
	id        int64
	draft     Draft
	status    Status
	createdAt time.Time
	updatedAt time.Time
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

// UpdatedAt returns when the campaign last changed.
func (c *Campaign) UpdatedAt() time.Time { return c.updatedAt }
