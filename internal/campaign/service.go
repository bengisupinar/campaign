package campaign

import (
	"context"
	"database/sql"
	"errors"
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
	// Dispatch saves a draft's dispatch and runs enqueue in the same transaction, failing like
	// Update.
	Dispatch(ctx context.Context, c *Campaign, enqueue func(tx *sql.Tx) error) error
	// Delete removes a draft, failing like Update.
	Delete(ctx context.Context, id int64) error
	// Get returns a campaign, or ErrNotFound.
	Get(ctx context.Context, id int64) (*Campaign, error)
	// List returns every campaign, newest first.
	List(ctx context.Context) ([]*Campaign, error)
}

// referenceChecker reports the countries, cities and categories a segment names that don't
// exist, and nothing for a segment with problems of its own. The segmentation engine
// implements it; declaring it here keeps campaign from importing the engine.
type referenceChecker interface {
	CheckReferences(ctx context.Context, s Segment) ([]Problem, error)
}

// dispatcher queues the job that sends a dispatched campaign, in tx, the transaction that
// records the dispatch. The delivery capability implements it.
type dispatcher interface {
	EnqueueDispatch(ctx context.Context, tx *sql.Tx, c *Campaign) error
}

// Service runs the campaign use cases. The HTTP handlers use it now, and AI proposals will
// later, so manual and generated campaigns follow the same rules.
type Service struct {
	repo       repository
	references referenceChecker
	dispatcher dispatcher
	now        func() time.Time
}

// NewService returns a service that stores campaigns in repo, checks segment references with
// references, queues dispatched campaigns with dispatcher, and reads the time from now.
func NewService(repo repository, references referenceChecker, dispatcher dispatcher, now func() time.Time) *Service {
	return &Service{repo: repo, references: references, dispatcher: dispatcher, now: now}
}

// Dispatch approves a draft for sending: now, or at scheduledAt. The names in its segment are
// checked again, since reference data may have changed after it was saved. The status change
// and the queued job are saved together, and only if the draft is still the one checked: an
// edit in between gets ErrChanged. A campaign dispatched before, including by a concurrent
// request, gets ErrAlreadyDispatched.
func (s *Service) Dispatch(ctx context.Context, id int64, scheduledAt *time.Time) (*Campaign, error) {
	return s.dispatch(ctx, id, nil, scheduledAt)
}

// DispatchApproved dispatches a draft like Dispatch, but only the version a person approved:
// the draft as it was when it was last updated at approved. A draft changed since gets
// ErrChanged, checked in the transaction that dispatches it.
func (s *Service) DispatchApproved(ctx context.Context, id int64, approved time.Time, scheduledAt *time.Time) (*Campaign, error) {
	return s.dispatch(ctx, id, &approved, scheduledAt)
}

func (s *Service) dispatch(ctx context.Context, id int64, approved *time.Time, scheduledAt *time.Time) (*Campaign, error) {
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if approved != nil && c.status == StatusDraft && !c.updatedAt.Equal(*approved) {
		return nil, ErrChanged
	}
	err = c.Dispatch(scheduledAt, s.time())
	if errors.Is(err, ErrNotDraft) {
		return nil, ErrAlreadyDispatched
	}
	if err = s.addUnknownReferences(ctx, c.draft.Segment, err); err != nil {
		return nil, err
	}
	err = s.repo.Dispatch(ctx, c, func(tx *sql.Tx) error {
		return s.dispatcher.EnqueueDispatch(ctx, tx, c)
	})
	if errors.Is(err, ErrNotDraft) {
		return nil, ErrAlreadyDispatched
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Create validates d, including the references in its segment, and saves it as a new draft.
func (s *Service) Create(ctx context.Context, d Draft) (*Campaign, error) {
	c, err := New(d, s.time())
	if err = s.addUnknownReferences(ctx, d.Segment, err); err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// Update replaces a draft's name, segment and content with d, whose references are checked
// like Create's.
func (s *Service) Update(ctx context.Context, id int64, d Draft) (*Campaign, error) {
	c, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.addUnknownReferences(ctx, d.Segment, c.Update(d, s.time())); err != nil {
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

// addUnknownReferences adds the unknown countries, cities and categories in seg to err, the
// outcome of validating a draft, so a draft's problems are all reported at once. An error
// that isn't a *ValidationError, such as ErrNotDraft, is returned as it is.
func (s *Service) addUnknownReferences(ctx context.Context, seg Segment, err error) error {
	var invalid *ValidationError
	if err != nil && !errors.As(err, &invalid) {
		return err
	}
	unknown, checkErr := s.references.CheckReferences(ctx, seg)
	if checkErr != nil {
		return checkErr // the checker describes what it was doing
	}
	var ps problems
	if invalid != nil {
		ps = invalid.Problems
	}
	return append(ps, unknown...).err()
}

// time returns the current time in UTC at PostgreSQL's microsecond precision, so a campaign
// reads back exactly as it was saved.
func (s *Service) time() time.Time {
	return s.now().UTC().Truncate(time.Microsecond)
}
