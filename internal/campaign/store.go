package campaign

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/bengisupinar/campaign/internal/platform/database"
)

// Store keeps campaigns in PostgreSQL.
type Store struct{ db *gorm.DB }

// NewStore returns a store that runs its statements on db, which may be a transaction: the
// delivery worker changes a campaign's status inside its own transactions.
func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// campaignRow maps the campaigns table. It stays unexported, so GORM never reaches the domain
// types. The segment is stored as JSON text in a jsonb column.
type campaignRow struct {
	ID           int64
	Name         string
	Status       string
	Segment      string  `gorm:"type:jsonb"`
	SMSBody      *string `gorm:"column:sms_body"` // NULL when the campaign doesn't use SMS
	EmailSubject *string // NULL when the campaign doesn't use email
	EmailBody    *string
	CreatedAt    time.Time `gorm:"autoCreateTime:false"`
	UpdatedAt    time.Time `gorm:"autoUpdateTime:false"`
	ScheduledAt  *time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
}

func (campaignRow) TableName() string { return "campaigns" }

// Create saves a new campaign and sets its ID.
func (s *Store) Create(ctx context.Context, c *Campaign) error {
	if c.id != 0 {
		return errors.New("create campaign: already saved")
	}
	row, err := toRow(c)
	if err != nil {
		return fmt.Errorf("create campaign: %w", err)
	}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("create campaign: %w", err)
	}
	c.id = row.ID
	return nil
}

// Update saves a draft's name, segment, content and update time. The write only applies while
// the stored campaign is a draft, so it can't race a status change.
func (s *Store) Update(ctx context.Context, c *Campaign) error {
	row, err := toRow(c)
	if err != nil {
		return fmt.Errorf("update campaign %d: %w", c.id, err)
	}
	result := s.db.WithContext(ctx).Model(&campaignRow{}).
		Where("id = ? AND status = ?", c.id, StatusDraft).
		Updates(map[string]any{
			"name":          row.Name,
			"segment":       row.Segment,
			"sms_body":      row.SMSBody,
			"email_subject": row.EmailSubject,
			"email_body":    row.EmailBody,
			"updated_at":    row.UpdatedAt,
		})
	if result.Error != nil {
		return fmt.Errorf("update campaign %d: %w", c.id, result.Error)
	}
	if result.RowsAffected == 0 {
		return s.missingOrNotDraft(ctx, c.id)
	}
	return nil
}

// Delete removes a campaign while it is a draft.
func (s *Store) Delete(ctx context.Context, id int64) error {
	result := s.db.WithContext(ctx).Where("id = ? AND status = ?", id, StatusDraft).Delete(&campaignRow{})
	if result.Error != nil {
		return fmt.Errorf("delete campaign %d: %w", id, result.Error)
	}
	if result.RowsAffected == 0 {
		return s.missingOrNotDraft(ctx, id)
	}
	return nil
}

// Get returns a campaign, or ErrNotFound.
func (s *Store) Get(ctx context.Context, id int64) (*Campaign, error) {
	var row campaignRow
	err := s.db.WithContext(ctx).Take(&row, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get campaign %d: %w", id, err)
	}
	return row.toCampaign()
}

// List returns every campaign, newest first.
func (s *Store) List(ctx context.Context) ([]*Campaign, error) {
	var rows []campaignRow
	if err := s.db.WithContext(ctx).Order("id DESC").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list campaigns: %w", err)
	}
	campaigns := make([]*Campaign, len(rows))
	for i, row := range rows {
		c, err := row.toCampaign()
		if err != nil {
			return nil, err
		}
		campaigns[i] = c
	}
	return campaigns, nil
}

// Dispatch saves a draft's move to scheduled or sending, which c already made, and lets
// enqueue queue its job in the same transaction: both happen or neither does. The write only
// applies while the stored campaign is a draft, so of several concurrent dispatches exactly
// one succeeds; the others get ErrNotDraft.
func (s *Store) Dispatch(ctx context.Context, c *Campaign, enqueue func(tx *sql.Tx) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&campaignRow{}).
			Where("id = ? AND status = ?", c.id, StatusDraft).
			Updates(statusColumns(c))
		if result.Error != nil {
			return fmt.Errorf("dispatch campaign %d: %w", c.id, result.Error)
		}
		if result.RowsAffected == 0 {
			return NewStore(tx).missingOrNotDraft(ctx, c.id)
		}
		sqlTx, err := database.SQLTx(tx)
		if err != nil {
			return fmt.Errorf("dispatch campaign %d: %w", c.id, err)
		}
		return enqueue(sqlTx)
	})
}

// Lock returns a campaign and locks its row until the transaction the store runs on ends.
// Changes to a campaign and its deliveries lock the campaign first, so they happen one at a
// time and never deadlock.
func (s *Store) Lock(ctx context.Context, id int64) (*Campaign, error) {
	var row campaignRow
	err := s.db.WithContext(ctx).Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).Take(&row, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("lock campaign %d: %w", id, err)
	}
	return row.toCampaign()
}

// Transition saves the status change c made from status from, and its times. It fails with
// ErrStatusChange if the stored campaign is no longer in status from.
func (s *Store) Transition(ctx context.Context, c *Campaign, from Status) error {
	result := s.db.WithContext(ctx).Model(&campaignRow{}).
		Where("id = ? AND status = ?", c.id, from).
		Updates(statusColumns(c))
	if result.Error != nil {
		return fmt.Errorf("change campaign %d to %s: %w", c.id, c.status, result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("%w: campaign %d is no longer %s", ErrStatusChange, c.id, from)
	}
	return nil
}

// statusColumns are the columns a lifecycle move changes.
func statusColumns(c *Campaign) map[string]any {
	return map[string]any{
		"status":       c.status,
		"scheduled_at": nullTime(c.scheduledAt),
		"started_at":   nullTime(c.startedAt),
		"finished_at":  nullTime(c.finishedAt),
	}
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func timeOf(p *time.Time) time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.UTC()
}

// missingOrNotDraft explains why a write that only applies to drafts matched no row.
func (s *Store) missingOrNotDraft(ctx context.Context, id int64) error {
	var status string
	if err := s.db.WithContext(ctx).Raw("SELECT status FROM campaigns WHERE id = ?", id).Scan(&status).Error; err != nil {
		return fmt.Errorf("campaign %d: %w", id, err)
	}
	if status == "" {
		return ErrNotFound
	}
	return ErrNotDraft
}

func toRow(c *Campaign) (campaignRow, error) {
	segment, err := json.Marshal(c.draft.Segment)
	if err != nil {
		return campaignRow{}, fmt.Errorf("encode segment: %w", err)
	}
	row := campaignRow{
		ID:          c.id,
		Name:        c.draft.Name,
		Status:      string(c.status),
		Segment:     string(segment),
		CreatedAt:   c.createdAt,
		UpdatedAt:   c.updatedAt,
		ScheduledAt: nullTime(c.scheduledAt),
		StartedAt:   nullTime(c.startedAt),
		FinishedAt:  nullTime(c.finishedAt),
	}
	if sms := c.draft.Content.SMS; sms != nil {
		row.SMSBody = &sms.Body
	}
	if email := c.draft.Content.Email; email != nil {
		row.EmailSubject, row.EmailBody = &email.Subject, &email.Body
	}
	return row, nil
}

func (r campaignRow) toCampaign() (*Campaign, error) {
	var segment Segment
	if err := json.Unmarshal([]byte(r.Segment), &segment); err != nil {
		return nil, fmt.Errorf("campaign %d: decode segment: %w", r.ID, err)
	}
	var content Content
	if r.SMSBody != nil {
		content.SMS = &SMS{Body: *r.SMSBody}
	}
	if r.EmailSubject != nil && r.EmailBody != nil {
		content.Email = &Email{Subject: *r.EmailSubject, Body: *r.EmailBody}
	}
	return &Campaign{
		id:          r.ID,
		draft:       Draft{Name: r.Name, Segment: segment.clone(), Content: content},
		status:      Status(r.Status),
		createdAt:   r.CreatedAt.UTC(),
		updatedAt:   r.UpdatedAt.UTC(),
		scheduledAt: timeOf(r.ScheduledAt),
		startedAt:   timeOf(r.StartedAt),
		finishedAt:  timeOf(r.FinishedAt),
	}, nil
}
