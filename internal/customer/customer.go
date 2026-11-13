// Package customer is the Customer bounded context: who can be contacted, where, in which
// language and on which channels.
//
// Customers are written by the seeder and, later, by opt-outs; segmentation reads their
// tables directly. The types here carry no persistence concerns.
package customer

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

var (
	// ErrInvalid wraps every validation error returned by this package. Messages never repeat
	// the rejected value, because it may be personal data.
	ErrInvalid = errors.New("invalid")
	// ErrUnreachable reports a permission granted on a channel the customer has no contact
	// point for, such as SMS without a phone number.
	ErrUnreachable = errors.New("customer has no contact point for the channel")
)

// Profile holds the details a customer signs up with.
type Profile struct {
	FirstName string
	LastName  string
	Email     Email
	Phone     Phone // optional: the zero value means no phone number
	CityID    int64
	Language  Language
}

// Customer is the aggregate root of the Customer context. Besides the profile, it guards the
// rule that a marketing permission can only be granted on a channel the customer can be
// reached on.
type Customer struct {
	id          int64
	profile     Profile
	createdAt   time.Time
	permissions map[Channel]Permission
}

// New returns a customer who signed up at createdAt, with trimmed names. The customer has no
// permissions yet, so every channel starts as not permitted.
func New(p Profile, createdAt time.Time) (*Customer, error) {
	p.FirstName = strings.TrimSpace(p.FirstName)
	p.LastName = strings.TrimSpace(p.LastName)
	switch {
	case p.FirstName == "":
		return nil, fmt.Errorf("%w first name: empty", ErrInvalid)
	case p.LastName == "":
		return nil, fmt.Errorf("%w last name: empty", ErrInvalid)
	case p.Email.IsZero():
		return nil, fmt.Errorf("%w email: missing", ErrInvalid)
	case p.CityID <= 0:
		return nil, fmt.Errorf("%w city ID %d", ErrInvalid, p.CityID)
	case !p.Language.Valid():
		return nil, fmt.Errorf("%w language %q: want tr, de or en", ErrInvalid, string(p.Language))
	case createdAt.IsZero():
		return nil, fmt.Errorf("%w signup time: missing", ErrInvalid)
	}
	return &Customer{profile: p, createdAt: createdAt, permissions: make(map[Channel]Permission)}, nil
}

// ID returns the customer's ID, which the database assigns when the customer is first saved.
func (c *Customer) ID() int64 { return c.id }

// Profile returns the details the customer signed up with.
func (c *Customer) Profile() Profile { return c.profile }

// CreatedAt returns when the customer signed up.
func (c *Customer) CreatedAt() time.Time { return c.createdAt }

// Grant records, as of at, that the customer allows marketing messages on ch. It fails with
// ErrUnreachable when the customer has no contact point for ch.
func (c *Customer) Grant(ch Channel, at time.Time) error {
	if err := c.checkPermissionChange(ch, at); err != nil {
		return err
	}
	if !c.reachableOn(ch) {
		return fmt.Errorf("grant %s permission: %w", ch, ErrUnreachable)
	}
	c.permissions[ch] = Permission{Channel: ch, Granted: true, UpdatedAt: at}
	return nil
}

// Revoke records, as of at, that the customer no longer allows marketing messages on ch.
// Revoking is always allowed, even on a channel the customer can't be reached on.
func (c *Customer) Revoke(ch Channel, at time.Time) error {
	if err := c.checkPermissionChange(ch, at); err != nil {
		return err
	}
	c.permissions[ch] = Permission{Channel: ch, Granted: false, UpdatedAt: at}
	return nil
}

// Permitted reports whether the customer allows marketing messages on ch. A channel without a
// recorded permission is not permitted: commercial messages need prior opt-in.
func (c *Customer) Permitted(ch Channel) bool { return c.permissions[ch].Granted }

// Permissions returns the recorded permissions, ordered by channel.
func (c *Customer) Permissions() []Permission {
	permissions := make([]Permission, 0, len(c.permissions))
	for _, ch := range slices.Sorted(maps.Keys(c.permissions)) {
		permissions = append(permissions, c.permissions[ch])
	}
	return permissions
}

func (c *Customer) checkPermissionChange(ch Channel, at time.Time) error {
	if !ch.Valid() {
		return fmt.Errorf("%w channel %q", ErrInvalid, string(ch))
	}
	if at.Before(c.createdAt) {
		return fmt.Errorf("%w permission time: before the customer signed up", ErrInvalid)
	}
	return nil
}

func (c *Customer) reachableOn(ch Channel) bool {
	switch ch {
	case ChannelEmail:
		return !c.profile.Email.IsZero()
	case ChannelSMS:
		return !c.profile.Phone.IsZero()
	default:
		return false
	}
}
