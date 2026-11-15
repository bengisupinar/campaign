// Package segmentation evaluates campaign segments. It checks that the countries, cities and
// categories a segment names exist, compiles the segment into parameterised SQL over the
// customer and commerce tables, and counts or lists the customers each channel can reach.
//
// Audience estimates and dispatch use the same compiled query, so they apply the same rules.
// Delivery and the AI agent's tools call the service directly; the composer uses a read-only
// API (see NewHandler) that returns counts, never customers.
package segmentation

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
)

// maxBatchSize bounds how many recipients one call returns, and so the memory it needs.
const maxBatchSize = 1000

// reachable selects whether a campaign can reach customer c by email and by SMS. A channel
// needs the campaign to use it (the two ? parameters, email first), a contact point and a
// granted marketing permission. The domain guarantees formats, so only presence is tested.
// Permissions are read as they are now: delivery checks them again just before sending.
const reachable = `(? AND c.email <> '' AND EXISTS (SELECT 1 FROM notification_permissions np
		WHERE np.customer_id = c.id AND np.channel = 'email' AND np.granted)) AS email_ok,
	(? AND c.phone IS NOT NULL AND EXISTS (SELECT 1 FROM notification_permissions np
		WHERE np.customer_id = c.id AND np.channel = 'sms' AND np.granted)) AS sms_ok`

// Service evaluates segments against the customer and commerce tables.
type Service struct {
	db *gorm.DB
}

// NewService returns a service that runs its queries on db.
func NewService(db *gorm.DB) *Service { return &Service{db: db} }

// Query asks which customers a segment reaches on some channels.
type Query struct {
	Segment  campaign.Segment
	Channels []customer.Channel // at least one, without repeats
	// AsOf is where relative windows end: "the last 30 days" means the 30 days before AsOf.
	// It doesn't freeze the data, so the same query can find a different audience later.
	AsOf time.Time
}

// Audience counts the customers a query reaches. Its JSON form is the API's audience estimate.
type Audience struct {
	Matched   int                      `json:"matched"`    // customers the segment matches
	Reachable int                      `json:"reachable"`  // of those, customers who can receive at least one channel
	ByChannel map[customer.Channel]int `json:"by_channel"` // of those, customers who can receive each channel
}

// Recipient is a customer who can receive a campaign on at least one of its channels, with the
// details needed to personalise and address the messages.
type Recipient struct {
	CustomerID int64
	FirstName  string
	LastName   string
	City       string
	Language   customer.Language
	Email      customer.Email     // zero unless Channels includes email
	Phone      customer.Phone     // zero unless Channels includes SMS
	Channels   []customer.Channel // the query's channels this customer can receive, email first
}

// LogValue implements slog.LogValuer: a recipient is logged by ID and channels only, never
// with personal data.
func (r Recipient) LogValue() slog.Value {
	return slog.GroupValue(slog.Int64("customer_id", r.CustomerID), slog.Any("channels", r.Channels))
}

// CountAudience counts the customers the segment matches, those who can receive at least one
// of the channels, and those who can receive each one.
func (s *Service) CountAudience(ctx context.Context, q Query) (Audience, error) {
	segment, err := s.prepare(ctx, q)
	if err != nil {
		return Audience{}, err
	}
	// MATERIALIZED tests each customer's permissions once, not once per count (DECISIONS.md §23).
	query := "WITH audience AS MATERIALIZED (SELECT " + reachable + " FROM customers c WHERE " + segment.sql + ") " +
		"SELECT count(*) AS matched, count(*) FILTER (WHERE email_ok OR sms_ok) AS reachable, " +
		"count(*) FILTER (WHERE email_ok) AS email, count(*) FILTER (WHERE sms_ok) AS sms FROM audience"
	email, sms := q.uses(customer.ChannelEmail), q.uses(customer.ChannelSMS)
	var counts struct {
		Matched   int
		Reachable int
		Email     int `gorm:"column:email"`
		SMS       int `gorm:"column:sms"`
	}
	if err := s.db.WithContext(ctx).Raw(query, slices.Concat([]any{email, sms}, segment.args)...).Scan(&counts).Error; err != nil {
		return Audience{}, fmt.Errorf("count audience: %w", err)
	}

	audience := Audience{Matched: counts.Matched, Reachable: counts.Reachable, ByChannel: map[customer.Channel]int{}}
	if email {
		audience.ByChannel[customer.ChannelEmail] = counts.Email
	}
	if sms {
		audience.ByChannel[customer.ChannelSMS] = counts.SMS
	}
	return audience, nil
}

// Recipients returns up to limit (at most 1,000) customers the query reaches whose ID is above
// after, in ID order. Pass 0 for the first batch and the last ID received for the next; a batch
// shorter than limit is the last one. Only one batch is ever held in memory. Permissions are
// read as they are now, so delivery must check them again just before sending.
func (s *Service) Recipients(ctx context.Context, q Query, after int64, limit int) ([]Recipient, error) {
	if limit < 1 || limit > maxBatchSize {
		return nil, fmt.Errorf("recipients: batch size %d: want 1 to %d", limit, maxBatchSize)
	}
	if after < 0 {
		return nil, fmt.Errorf("recipients: after %d: want 0 or a customer ID", after)
	}
	segment, err := s.prepare(ctx, q)
	if err != nil {
		return nil, err
	}
	// Keyset pagination: each batch is a range scan of the primary key that LIMIT stops.
	query := "SELECT * FROM (SELECT c.id, c.first_name, c.last_name, ci.name AS city, c.preferred_language, " +
		"c.email, c.phone, " + reachable + " FROM customers c JOIN cities ci ON ci.id = c.city_id " +
		"WHERE c.id > ? AND " + segment.sql + ") a WHERE email_ok OR sms_ok ORDER BY id LIMIT ?"
	args := slices.Concat([]any{q.uses(customer.ChannelEmail), q.uses(customer.ChannelSMS), after}, segment.args, []any{limit})
	var rows []struct {
		ID                int64
		FirstName         string
		LastName          string
		City              string
		PreferredLanguage string
		Email             string
		Phone             *string
		EmailOK           bool `gorm:"column:email_ok"`
		SMSOK             bool `gorm:"column:sms_ok"`
	}
	if err := s.db.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("recipients: %w", err)
	}

	recipients := make([]Recipient, len(rows))
	for i, row := range rows {
		r := Recipient{
			CustomerID: row.ID,
			FirstName:  row.FirstName,
			LastName:   row.LastName,
			City:       row.City,
			Language:   customer.Language(row.PreferredLanguage),
			Channels:   []customer.Channel{},
		}
		if row.EmailOK {
			if r.Email, err = customer.ParseEmail(row.Email); err != nil {
				return nil, fmt.Errorf("recipients: customer %d: %w", row.ID, err)
			}
			r.Channels = append(r.Channels, customer.ChannelEmail)
		}
		if row.SMSOK && row.Phone != nil {
			if r.Phone, err = customer.ParsePhone(*row.Phone); err != nil {
				return nil, fmt.Errorf("recipients: customer %d: %w", row.ID, err)
			}
			r.Channels = append(r.Channels, customer.ChannelSMS)
		}
		recipients[i] = r
	}
	return recipients, nil
}

// prepare checks the query, its segment and the segment's references, then compiles the
// segment. Both operations go through it, so estimates and dispatch apply the same rules and
// the same SQL. Problems come back as a *campaign.ValidationError.
func (s *Service) prepare(ctx context.Context, q Query) (expr, error) {
	problems := q.problems()
	if len(problems) == 0 {
		var err error
		if problems, err = s.CheckReferences(ctx, q.Segment); err != nil {
			return expr{}, err
		}
	}
	if len(problems) > 0 {
		return expr{}, &campaign.ValidationError{Problems: problems}
	}
	return compileSegment(q.Segment, q.AsOf)
}

// problems lists what is wrong with the query: its segment's structure, its channels and its
// reference time.
func (q Query) problems() []campaign.Problem {
	problems := q.Segment.Problems()
	if len(q.Channels) == 0 {
		problems = append(problems, campaign.Problem{Field: "channels", Code: campaign.ProblemRequired})
	}
	for i, ch := range q.Channels {
		if !ch.Valid() || slices.Contains(q.Channels[:i], ch) {
			problems = append(problems, campaign.Problem{Field: fmt.Sprintf("channels[%d]", i), Code: campaign.ProblemInvalid})
		}
	}
	if q.AsOf.IsZero() {
		problems = append(problems, campaign.Problem{Field: "as_of", Code: campaign.ProblemRequired})
	}
	return problems
}

// uses reports whether the query's campaign uses ch.
func (q Query) uses(ch customer.Channel) bool { return slices.Contains(q.Channels, ch) }
