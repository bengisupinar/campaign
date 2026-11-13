package campaign

import (
	"context"
	"time"
)

// repository is what Service needs from storage. *Store implements it; the API tests use an
// in-memory fake.
type repository interface {
	// Create saves a new campaign and sets its ID.
	Create(ctx context.Context, c *Campaign) error
	// Update saves a draft's changes. It fails with ErrNotFound or ErrNotDraft when the stored
	// campaign is gone or no longer a draft.
	Update(ctx context.Context, c *Campaign) error
	// Delete removes a draft, failing like Update.
	Delete(ctx context.Context, id int64) error
	// Get returns a campaign, or ErrNotFound.
	Get(ctx context.Context, id int64) (*Campaign, error)
	// List returns every campaign, newest first.
	List(ctx context.Context) ([]*Campaign, error)
}

// Service runs the campaign use cases. The HTTP handlers use it now, and AI proposals will
// later, so manual and generated campaigns follow the same rules.
type Service struct {
	repo repository
	now  func() time.Time
}

// NewService returns a service that stores campaigns in repo and reads the time from now.
func NewService(repo repository, now func() time.Time) *Service {
	return &Service{repo: repo, now: now}
}

// Create validates d and saves it as a new draft.
func (s *Service) Create(ctx context.Context, d Draft) (*Campaign, error) {
	c, err := New(d, s.time())
	if err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// Update replaces a draft's name, segment and content with d.
func (s *Service) Update(ctx context.Context, id int64, d Draft) (*Campaign, error) {
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := c.Update(d, s.time()); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// Get returns a campaign by ID.
func (s *Service) Get(ctx context.Context, id int64) (*Campaign, error) {
	return s.repo.Get(ctx, id)
}

// List returns every campaign, newest first.
func (s *Service) List(ctx context.Context) ([]*Campaign, error) {
	return s.repo.List(ctx)
}

// Delete removes a draft. Campaigns past the draft stage are kept.
func (s *Service) Delete(ctx context.Context, id int64) error {
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := c.ensureDraft(); err != nil {
		return err
	}
	return s.repo.Delete(ctx, id)
}

// time returns the current time in UTC at PostgreSQL's microsecond precision, so a campaign
// reads back exactly as it was saved.
func (s *Service) time() time.Time {
	return s.now().UTC().Truncate(time.Microsecond)
}
