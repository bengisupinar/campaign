package delivery

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
	"github.com/bengisupinar/campaign/internal/segmentation"
)

// Message statuses. A message is pending until its provider accepts it, refuses it, or it is
// skipped; an accepted message becomes delivered or failed when its receipt arrives.
const (
	statusPending   = "pending"
	statusAccepted  = "accepted"
	statusDelivered = "delivered"
	statusFailed    = "failed"
	statusSkipped   = "skipped"
)

// store reads and writes deliveries. It runs on db, which may be a transaction. Every status
// change applies only from the status it expects, so repeating one is harmless.
type store struct{ db *gorm.DB }

func newStore(db *gorm.DB) *store { return &store{db: db} }

// created is a delivery that createPending inserted.
type created struct {
	ID      int64
	Channel customer.Channel
}

// createPending inserts a pending delivery for each recipient and channel and returns the new
// ones. A delivery that already exists is left alone, so preparing a campaign twice creates
// nothing twice.
func (s *store) createPending(ctx context.Context, campaignID int64, recipients []segmentation.Recipient, now time.Time) ([]created, error) {
	var values []string
	var args []any
	for _, r := range recipients {
		for _, ch := range r.Channels {
			values = append(values, "(?, ?, ?, ?, ?, ?)")
			args = append(args, campaignID, r.CustomerID, string(ch), statusPending, now, now)
		}
	}
	if len(values) == 0 {
		return nil, nil
	}
	var rows []created
	err := s.db.WithContext(ctx).Raw("INSERT INTO deliveries (campaign_id, customer_id, channel, status, created_at, updated_at) VALUES "+
		strings.Join(values, ", ")+" ON CONFLICT (campaign_id, customer_id, channel) DO NOTHING RETURNING id, channel", args...).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("create deliveries for campaign %d: %w", campaignID, err)
	}
	return rows, nil
}

// pendingMessage is what sending one message needs: its delivery, the recipient's current
// contact details and permission, and the campaign's content.
type pendingMessage struct {
	id         int64
	campaignID int64
	channel    customer.Channel
	status     string
	email      string
	phone      *string
	permitted  bool // the recipient's permission for the channel, read now
	values     campaign.Values
	content    campaign.Content
}

// message loads what sending a delivery needs, with the recipient's permission as it is now.
func (s *store) message(ctx context.Context, deliveryID int64) (pendingMessage, error) {
	var row struct {
		ID         int64
		CampaignID int64
		Channel    string
		Status     string
		FirstName  string
		LastName   string
		City       string
		Email      string
		Phone      *string
		Permitted  bool
	}
	err := s.db.WithContext(ctx).Raw(`
		SELECT d.id, d.campaign_id, d.channel, d.status, c.first_name, c.last_name, ci.name AS city, c.email, c.phone,
		       EXISTS (SELECT 1 FROM notification_permissions np
		               WHERE np.customer_id = c.id AND np.channel = d.channel AND np.granted) AS permitted
		FROM deliveries d
		JOIN customers c ON c.id = d.customer_id
		JOIN cities ci ON ci.id = c.city_id
		WHERE d.id = ?`, deliveryID).Scan(&row).Error
	if err != nil {
		return pendingMessage{}, fmt.Errorf("load delivery %d: %w", deliveryID, err)
	}
	if row.ID == 0 {
		return pendingMessage{}, fmt.Errorf("load delivery %d: not found", deliveryID)
	}
	c, err := campaign.NewStore(s.db).Get(ctx, row.CampaignID)
	if err != nil {
		return pendingMessage{}, fmt.Errorf("load delivery %d: %w", deliveryID, err)
	}
	return pendingMessage{
		id:         row.ID,
		campaignID: row.CampaignID,
		channel:    customer.Channel(row.Channel),
		status:     row.Status,
		email:      row.Email,
		phone:      row.Phone,
		permitted:  row.Permitted,
		values:     campaign.Values{FirstName: row.FirstName, LastName: row.LastName, City: row.City},
		content:    c.Draft().Content,
	}, nil
}

// outcome is how a message's send ended.
type outcome struct {
	status            string // accepted, failed or skipped
	providerMessageID string // when accepted
	reason            string // when failed or skipped: a short code, never personal data
}

// recordOutcome records a pending message's outcome. It reports false if the message wasn't
// pending any more: its outcome was recorded before.
func (s *store) recordOutcome(ctx context.Context, deliveryID int64, out outcome, now time.Time) (bool, error) {
	result := s.db.WithContext(ctx).Exec(`
		UPDATE deliveries SET status = ?, provider_message_id = ?, error = ?, updated_at = ?
		WHERE id = ? AND status = ?`,
		out.status, nullString(out.providerMessageID), nullString(out.reason), now, deliveryID, statusPending)
	if result.Error != nil {
		return false, fmt.Errorf("record outcome of delivery %d: %w", deliveryID, result.Error)
	}
	return result.RowsAffected == 1, nil
}

// recordReceipt records a provider's delivery report for an accepted message. It reports false
// if no accepted message has that provider ID.
func (s *store) recordReceipt(ctx context.Context, providerMessageID string, delivered bool, now time.Time) (bool, error) {
	status, reason := statusDelivered, ""
	if !delivered {
		status, reason = statusFailed, "undelivered"
	}
	result := s.db.WithContext(ctx).Exec(`
		UPDATE deliveries SET status = ?, error = ?, updated_at = ?
		WHERE provider_message_id = ? AND status = ?`,
		status, nullString(reason), now, providerMessageID, statusAccepted)
	if result.Error != nil {
		return false, fmt.Errorf("record receipt: %w", result.Error)
	}
	return result.RowsAffected == 1, nil
}

// exists runs an EXISTS query.
func (s *store) exists(ctx context.Context, query string, args ...any) (bool, error) {
	var found bool
	if err := s.db.WithContext(ctx).Raw("SELECT EXISTS ("+query+")", args...).Scan(&found).Error; err != nil {
		return false, fmt.Errorf("query deliveries: %w", err)
	}
	return found, nil
}

func (s *store) hasDeliveries(ctx context.Context, campaignID int64) (bool, error) {
	return s.exists(ctx, "SELECT 1 FROM deliveries WHERE campaign_id = ?", campaignID)
}

func (s *store) hasPending(ctx context.Context, campaignID int64) (bool, error) {
	return s.exists(ctx, "SELECT 1 FROM deliveries WHERE campaign_id = ? AND status = ?", campaignID, statusPending)
}

func (s *store) hasProviderMessage(ctx context.Context, providerMessageID string) (bool, error) {
	return s.exists(ctx, "SELECT 1 FROM deliveries WHERE provider_message_id = ?", providerMessageID)
}

// statusCount is how many of a campaign's messages on a channel have a status.
type statusCount struct {
	Channel customer.Channel
	Status  string
	N       int
}

// counts counts a campaign's messages by channel and status, and its recipients.
func (s *store) counts(ctx context.Context, campaignID int64) ([]statusCount, int, error) {
	var counts []statusCount
	err := s.db.WithContext(ctx).Raw(`
		SELECT channel, status, count(*) AS n FROM deliveries WHERE campaign_id = ? GROUP BY channel, status`,
		campaignID).Scan(&counts).Error
	if err != nil {
		return nil, 0, fmt.Errorf("count deliveries of campaign %d: %w", campaignID, err)
	}
	var recipients int
	err = s.db.WithContext(ctx).Raw("SELECT count(DISTINCT customer_id) FROM deliveries WHERE campaign_id = ?", campaignID).
		Scan(&recipients).Error
	if err != nil {
		return nil, 0, fmt.Errorf("count recipients of campaign %d: %w", campaignID, err)
	}
	return counts, recipients, nil
}

func nullString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
