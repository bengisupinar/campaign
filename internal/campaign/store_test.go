package campaign

import (
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
			{Type: ConditionTotalSpent, MinTRY: new(1000), MaxTRY: new(5000), Negate: true},
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
	setStatus(t, db, scheduled.ID(), StatusScheduled)

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
func setStatus(t *testing.T, db *gorm.DB, id int64, status Status) {
	t.Helper()
	if err := db.Exec("UPDATE campaigns SET status = ? WHERE id = ?", status, id).Error; err != nil {
		t.Fatalf("set status: %v", err)
	}
}
