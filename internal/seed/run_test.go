package seed

import (
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/platform/database/databasetest"
)

// These tests need PostgreSQL and are skipped without it: run `make db`, then
// `make test-integration`.

// istanbulAbandonersWithSMS is the Phase 2A example segment: customers in İstanbul who added
// to their basket since the cut-off, completed no order since then, and allow SMS.
const istanbulAbandonersWithSMS = `
SELECT count(*) FROM customers c
JOIN cities ci ON ci.id = c.city_id
WHERE ci.name = 'İstanbul'
  AND EXISTS (SELECT 1 FROM basket_items b WHERE b.customer_id = c.id AND b.added_at >= ?)
  AND NOT EXISTS (SELECT 1 FROM orders o
                  WHERE o.customer_id = c.id AND o.status = 'completed' AND o.ordered_at >= ?)
  AND EXISTS (SELECT 1 FROM notification_permissions p
              WHERE p.customer_id = c.id AND p.channel = 'sms' AND p.granted)`

func TestRunSeedsAnEmptyDatabaseOnce(t *testing.T) {
	db := databasetest.New(t)
	ctx := t.Context()
	opts := DefaultOptions()

	first, err := Run(ctx, db, opts, testNow)
	if err != nil {
		t.Fatalf("Run(): %v", err)
	}
	generated := generateInMemory(t, opts)
	want := summarize(generated)
	if first != want {
		t.Fatalf("Run() = %+v, want the generated dataset's counts %+v", first, want)
	}
	wantRows := map[string]int{
		"countries":                3,
		"cities":                   28,
		"categories":               8,
		"products":                 want.Products,
		"customers":                want.Customers,
		"notification_permissions": want.Permissions,
		"orders":                   want.Orders,
		"order_items":              want.OrderLines,
		"basket_items":             want.BasketLines,
	}
	assertRowCounts(t, db, wantRows)

	cutoff := testNow.Add(-30 * day)
	var segment int
	if err := db.Raw(istanbulAbandonersWithSMS, cutoff, cutoff).Scan(&segment).Error; err != nil {
		t.Fatalf("example segment: %v", err)
	}
	if wantSegment := analyze(generated, testNow).istanbulAbandonersWithSMS; segment != wantSegment || segment < 25 {
		t.Errorf("example segment has %d customers in SQL, want %d as generated, and at least 25", segment, wantSegment)
	}

	second, err := Run(ctx, db, opts, testNow.Add(time.Hour))
	if err != nil {
		t.Fatalf("second Run(): %v", err)
	}
	if !second.Skipped {
		t.Errorf("second Run() = %+v, want it skipped", second)
	}
	assertRowCounts(t, db, wantRows)
}

func TestConcurrentRunsSeedOnce(t *testing.T) {
	db := databasetest.New(t)
	opts := Options{Customers: 200, Seed: 7}

	var wg sync.WaitGroup
	results := make([]Result, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Go(func() { results[i], errs[i] = Run(t.Context(), db, opts, testNow) })
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Fatalf("Run(): %v", err)
		}
	}
	if results[0].Skipped == results[1].Skipped {
		t.Errorf("results = %+v, want exactly one run to seed", results)
	}
	assertRowCounts(t, db, map[string]int{"customers": 200})
}

func TestRunRejectsInvalidOptions(t *testing.T) {
	for _, customers := range []int{0, maxCustomers + 1} {
		if _, err := Run(t.Context(), nil, Options{Customers: customers}, testNow); err == nil {
			t.Errorf("Run() with %d customers succeeded, want an error", customers)
		}
	}
}

func assertRowCounts(t *testing.T, db *gorm.DB, want map[string]int) {
	t.Helper()
	for table, n := range want {
		var got int
		if err := db.Raw("SELECT count(*) FROM " + table).Scan(&got).Error; err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != n {
			t.Errorf("%s has %d rows, want %d", table, got, n)
		}
	}
}
