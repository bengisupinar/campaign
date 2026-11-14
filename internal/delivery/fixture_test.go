package delivery

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/customer"
	"github.com/bengisupinar/campaign/internal/platform/database/databasetest"
	"github.com/bengisupinar/campaign/internal/segmentation"
)

// These tests need PostgreSQL and are skipped without it: run `make db`, then
// `make test-integration`.

var discard = slog.New(slog.DiscardHandler)

// fixture is a database with customers who allow both channels, and the services around it.
type fixture struct {
	db        *gorm.DB
	sqlDB     *sql.DB
	queue     *Queue
	campaigns *campaign.Service
}

// newFixture creates n customers in İstanbul, each with an email address and a phone number,
// and permission to receive both.
func newFixture(t testing.TB, n int) *fixture {
	t.Helper()
	db := databasetest.New(t)
	sqlDB, err := db.DB()
	check(t, err)
	if n > 0 {
		createCustomers(t, db, n)
	}
	queue, err := NewQueue(sqlDB, discard)
	check(t, err)
	return &fixture{
		db:        db,
		sqlDB:     sqlDB,
		queue:     queue,
		campaigns: campaign.NewService(campaign.NewStore(db), segmentation.NewService(db), queue, now),
	}
}

func createCustomers(t testing.TB, db *gorm.DB, n int) {
	t.Helper()
	ctx := t.Context()
	people := customer.NewStore(db)
	check(t, people.CreateCountries(ctx, []customer.Country{{Code: "TR", Name: "Türkiye"}}))
	cities := []customer.City{{CountryCode: "TR", Name: "İstanbul"}}
	check(t, people.CreateCities(ctx, cities))
	signedUp := now().Add(-24 * time.Hour)
	customers := make([]*customer.Customer, n)
	for i := range customers {
		email, err := customer.ParseEmail(fmt.Sprintf("customer%d@example.com", i))
		check(t, err)
		phone, err := customer.ParsePhone(fmt.Sprintf("+90532%07d", i))
		check(t, err)
		c, err := customer.New(customer.Profile{
			FirstName: "Ayşe", LastName: fmt.Sprintf("Test %d", i), Email: email, Phone: phone,
			CityID: cities[0].ID, Language: customer.LanguageTurkish,
		}, signedUp)
		check(t, err)
		check(t, c.Grant(customer.ChannelEmail, signedUp))
		check(t, c.Grant(customer.ChannelSMS, signedUp))
		customers[i] = c
	}
	check(t, people.CreateCustomers(ctx, customers))
}

// draft creates a campaign for every customer on both channels.
func (f *fixture) draft(t testing.TB) *campaign.Campaign {
	t.Helper()
	c, err := f.campaigns.Create(t.Context(), campaign.Draft{
		Name:    "Basket reminder",
		Segment: campaign.Segment{Version: campaign.SegmentVersion, Match: campaign.MatchAll, Conditions: []campaign.Condition{}},
		Content: campaign.Content{
			SMS:   &campaign.SMS{Body: "Merhaba {{first_name}}, sepetin seni bekliyor!"},
			Email: &campaign.Email{Subject: "{{first_name}}, your basket misses you", Body: "Greetings to {{city}}."},
		},
	})
	check(t, err)
	return c
}

// dispatch dispatches a new campaign now and returns its ID.
func (f *fixture) dispatch(t testing.TB) int64 {
	t.Helper()
	c, err := f.campaigns.Dispatch(t.Context(), f.draft(t).ID(), nil)
	check(t, err)
	return c.ID()
}

// startWorker works jobs with the given providers until the test ends.
func (f *fixture) startWorker(t testing.TB, providers map[customer.Channel]provider) {
	t.Helper()
	w, err := newWorker(f.db, f.sqlDB, providers, discard, now)
	check(t, err)
	check(t, w.Start(context.WithoutCancel(t.Context())))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := w.Stop(ctx); err != nil {
			t.Errorf("stop worker: %v", err)
		}
	})
}

func (f *fixture) status(t testing.TB, campaignID int64) campaign.Status {
	t.Helper()
	c, err := f.campaigns.Get(t.Context(), campaignID)
	check(t, err)
	return c.Status()
}

// count runs a count query.
func (f *fixture) count(t testing.TB, query string, args ...any) int {
	t.Helper()
	var n int
	check(t, f.db.Raw(query, args...).Scan(&n).Error)
	return n
}

// statuses counts a campaign's messages by status.
func (f *fixture) statuses(t testing.TB, campaignID int64) map[string]int {
	t.Helper()
	var rows []struct {
		Status string
		N      int
	}
	check(t, f.db.Raw("SELECT status, count(*) AS n FROM deliveries WHERE campaign_id = ? GROUP BY status", campaignID).Scan(&rows).Error)
	counts := make(map[string]int)
	for _, r := range rows {
		counts[r.Status] = r.N
	}
	return counts
}

// fakeProvider records its calls and answers them with answer, or accepts every message.
type fakeProvider struct {
	answer func(ctx context.Context, m message, attempt int) (string, error)

	mu       sync.Mutex
	calls    []time.Time
	attempts map[string]int
}

func (p *fakeProvider) Send(ctx context.Context, m message) (string, error) {
	p.mu.Lock()
	p.calls = append(p.calls, time.Now())
	if p.attempts == nil {
		p.attempts = make(map[string]int)
	}
	p.attempts[m.IdempotencyKey]++
	attempt := p.attempts[m.IdempotencyKey]
	p.mu.Unlock()
	if p.answer == nil {
		return "fake-" + m.IdempotencyKey, nil
	}
	return p.answer(ctx, m, attempt)
}

func (p *fakeProvider) callTimes() []time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]time.Time(nil), p.calls...)
}

// waitFor polls cond until it holds, failing the test after timeout.
func waitFor(t testing.TB, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func check(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
