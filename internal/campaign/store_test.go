package campaign

import (
	"database/sql"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/platform/database/databasetest"
)

// These tests need PostgreSQL and are skipped without it: run `make db`, then
// `make test-integration`.

func TestStoreRoundTrip(t *testing.T) {
	email := validDraft()
	email.Content = Content{Email: &Email{Subject: "{{first_name}}, your basket misses you", Body: "It's still waiting."}}
	nested := validDraft()
	nested.Content.Email = email.Content.Email
	nested.Segment.Conditions = append(nested.Segment.Conditions, Condition{
		Type: ConditionGroup, Match: MatchAny, Conditions: []Condition{
			{Type: ConditionOrderCount, WithinDays: new(365), Min: new(3)},
			{Type: ConditionTotalSpent, Min: new(100_000), Max: new(500_000), Negate: true},
		},
	})
	everyone := validDraft()
	everyone.Segment.Conditions = nil

	tests := []struct {
		name  string
		draft Draft
	}{
		{"SMS", validDraft()},
		{"email", email},
		{"both channels and a nested segment", nested},
		{"every customer", everyone},
	}
	store := NewStore(databasetest.New(t))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := mustCreate(t, store, tt.draft)

			got, err := store.Get(t.Context(), c.ID())
			if err != nil {
				t.Fatalf("Get(): %v", err)
			}
			if !reflect.DeepEqual(got, c) {
				t.Errorf("Get() = %+v, want the saved campaign %+v", got, c)
			}
		})
	}
}

func TestStoreChangesOnlyDrafts(t *testing.T) {
	db := databasetest.New(t)
	store := NewStore(db)
	ctx := t.Context()
	changed := validDraft()
	changed.Name = "Come back to your basket"

	draft := mustCreate(t, store, validDraft())
	scheduled := mustCreate(t, store, validDraft())
	schedule(t, db, scheduled.ID())

	// A draft changes and can be deleted.
	if err := draft.Update(changed, now.Add(time.Hour)); err != nil {
		t.Fatalf("Update(): %v", err)
	}
	if err := store.Update(ctx, draft); err != nil {
		t.Fatalf("store.Update(draft): %v", err)
	}
	if got, err := store.Get(ctx, draft.ID()); err != nil || !reflect.DeepEqual(got, draft) {
		t.Errorf("Get() after Update = %+v, %v; want %+v", got, err, draft)
	}

	// A campaign that left the draft stage keeps its content, even against a stale copy.
	if err := scheduled.Update(changed, now.Add(time.Hour)); err != nil { // the copy still thinks it's a draft
		t.Fatalf("Update(): %v", err)
	}
	if err := store.Update(ctx, scheduled); !errors.Is(err, ErrNotDraft) {
		t.Errorf("store.Update(scheduled) error = %v, want ErrNotDraft", err)
	}
	if err := store.Delete(ctx, scheduled.ID()); !errors.Is(err, ErrNotDraft) {
		t.Errorf("store.Delete(scheduled) error = %v, want ErrNotDraft", err)
	}
	if got, err := store.Get(ctx, scheduled.ID()); err != nil || got.Status() != StatusScheduled || got.Draft().Name != validDraft().Name {
		t.Errorf("scheduled campaign = %+v, %v; want it unchanged", got, err)
	}

	// Missing campaigns.
	missing := mustNewCampaign(t, validDraft())
	missing.id = 999
	if err := store.Update(ctx, missing); !errors.Is(err, ErrNotFound) {
		t.Errorf("store.Update(missing) error = %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("store.Delete(missing) error = %v, want ErrNotFound", err)
	}
	if err := store.Delete(ctx, draft.ID()); err != nil {
		t.Fatalf("store.Delete(draft): %v", err)
	}
	if _, err := store.Get(ctx, draft.ID()); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get() after Delete error = %v, want ErrNotFound", err)
	}
}

func TestStoreListsNewestFirst(t *testing.T) {
	store := NewStore(databasetest.New(t))
	var want []int64
	for range 3 {
		want = append([]int64{mustCreate(t, store, validDraft()).ID()}, want...)
	}

	campaigns, err := store.List(t.Context())
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	var got []int64
	for _, c := range campaigns {
		got = append(got, c.ID())
	}
	if !slices.Equal(got, want) {
		t.Errorf("List() IDs = %v, want %v", got, want)
	}
}

func TestStoreDispatchAndTransitions(t *testing.T) {
	db := databasetest.New(t)
	store := NewStore(db)
	ctx := t.Context()
	c := mustCreate(t, store, validDraft())
	if err := c.Dispatch(nil, now); err != nil {
		t.Fatalf("Dispatch(): %v", err)
	}

	// If the job can't be queued, the dispatch isn't saved either.
	errQueue := errors.New("queue unavailable")
	if err := store.Dispatch(ctx, c, func(*sql.Tx) error { return errQueue }); !errors.Is(err, errQueue) {
		t.Fatalf("store.Dispatch() error = %v, want the queue's", err)
	}
	if got, err := store.Get(ctx, c.ID()); err != nil || got.Status() != StatusDraft {
		t.Fatalf("after the failed dispatch: %v, %v; want still a draft", got.Status(), err)
	}

	// Otherwise both are saved, in the transaction handed to enqueue.
	var queuedIn *sql.Tx
	if err := store.Dispatch(ctx, c, func(tx *sql.Tx) error { queuedIn = tx; return nil }); err != nil {
		t.Fatalf("store.Dispatch(): %v", err)
	}
	got, err := store.Get(ctx, c.ID())
	if err != nil || queuedIn == nil || got.Status() != StatusSending || !got.StartedAt().Equal(now) {
		t.Fatalf("after dispatch: %+v, %v (queued in %v); want sending since %v", got, err, queuedIn, now)
	}
	if err := store.Dispatch(ctx, c, func(*sql.Tx) error { return nil }); !errors.Is(err, ErrNotDraft) {
		t.Errorf("second store.Dispatch() error = %v, want ErrNotDraft", err)
	}

	// Lock and Transition save a lifecycle move, once.
	finished := now.Add(time.Minute)
	err = db.Transaction(func(tx *gorm.DB) error {
		s := NewStore(tx)
		locked, err := s.Lock(ctx, c.ID())
		if err != nil {
			return err
		}
		if err := locked.Finish(finished); err != nil {
			return err
		}
		return s.Transition(ctx, locked, StatusSending)
	})
	if err != nil {
		t.Fatalf("finish in a transaction: %v", err)
	}
	if got, err := store.Get(ctx, c.ID()); err != nil || got.Status() != StatusSent || !got.FinishedAt().Equal(finished) {
		t.Errorf("after Finish: %v, finished %v, %v; want sent at %v", got.Status(), got.FinishedAt(), err, finished)
	}
	if err := store.Transition(ctx, got, StatusSending); !errors.Is(err, ErrStatusChange) {
		t.Errorf("Transition from a status the campaign left: error = %v, want ErrStatusChange", err)
	}
	if _, err := store.Lock(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("Lock(999) error = %v, want ErrNotFound", err)
	}
}

func mustNewCampaign(t *testing.T, d Draft) *Campaign {
	t.Helper()
	c, err := New(d, now)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	return c
}

func mustCreate(t *testing.T, store *Store, d Draft) *Campaign {
	t.Helper()
	c := mustNewCampaign(t, d)
	if err := store.Create(t.Context(), c); err != nil {
		t.Fatalf("Create(): %v", err)
	}
	if c.ID() == 0 {
		t.Fatal("Create() left the ID unset")
	}
	return c
}

// setStatus moves a campaign along the lifecycle directly in the database, as a later phase
// would.
// schedule moves a draft to scheduled behind the store's back, as a stale copy would not know.
func schedule(t *testing.T, db *gorm.DB, id int64) {
	t.Helper()
	if err := db.Exec("UPDATE campaigns SET status = ?, scheduled_at = ? WHERE id = ?", StatusScheduled, now.Add(time.Hour), id).Error; err != nil {
		t.Fatalf("schedule: %v", err)
	}
}
