package seed

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bengisupinar/campaign/internal/commerce"
	"github.com/bengisupinar/campaign/internal/customer"
)

// testNow is the reference time of the generated test data.
var testNow = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

func TestBuildIsDeterministic(t *testing.T) {
	opts := Options{Customers: 500, Seed: 42}
	first, second := generateInMemory(t, opts), generateInMemory(t, opts)
	if !reflect.DeepEqual(first, second) {
		t.Fatal("the same seed and time generated different data")
	}
	if other := generateInMemory(t, Options{Customers: 500, Seed: 43}); reflect.DeepEqual(first, other) {
		t.Fatal("a different seed generated the same data")
	}
}

func TestGeneratedDataIsConsistent(t *testing.T) {
	d := generateInMemory(t, DefaultOptions())

	signedUp := make(map[int64]time.Time, len(d.customers))
	emails := make(map[string]bool, len(d.customers))
	phones := make(map[string]bool, len(d.customers))
	for i, c := range d.customers {
		p := c.Profile()
		signedUp[int64(i+1)] = c.CreatedAt()
		if c.CreatedAt().After(testNow) {
			t.Fatalf("customer %d signed up in the future", i+1)
		}
		email := p.Email.String()
		if emails[email] {
			t.Fatalf("email of customer %d is not unique", i+1)
		}
		emails[email] = true
		if !strings.HasSuffix(email, "@example.com") && !strings.HasSuffix(email, "@example.net") &&
			!strings.HasSuffix(email, "@example.org") {
			t.Fatalf("email of customer %d is not on a domain reserved for examples", i+1)
		}
		if !p.Phone.IsZero() {
			if phones[p.Phone.String()] {
				t.Fatalf("phone of customer %d is not unique", i+1)
			}
			phones[p.Phone.String()] = true
		}
		for _, perm := range c.Permissions() {
			if perm.UpdatedAt.Before(c.CreatedAt()) || perm.UpdatedAt.After(testNow) {
				t.Fatalf("permission of customer %d changed outside signup..now", i+1)
			}
		}
	}

	lastOrder := make(map[int64]time.Time)
	for _, o := range d.orders {
		if o.OrderedAt().Before(signedUp[o.CustomerID()]) || o.OrderedAt().After(testNow) {
			t.Fatalf("order of customer %d placed outside signup..now", o.CustomerID())
		}
		if o.StatusChangedAt().Before(o.OrderedAt()) || o.StatusChangedAt().After(testNow) {
			t.Fatalf("order of customer %d changed status outside order..now", o.CustomerID())
		}
		lastOrder[o.CustomerID()] = later(lastOrder[o.CustomerID()], o.OrderedAt())
	}
	for _, b := range d.baskets {
		for _, item := range b.Items() {
			if !item.AddedAt.After(lastOrder[b.CustomerID()]) || !item.AddedAt.After(signedUp[b.CustomerID()]) ||
				item.AddedAt.After(testNow) {
				t.Fatalf("basket of customer %d has a line outside last order..now", b.CustomerID())
			}
		}
	}
}

func TestDefaultDataCoversSegments(t *testing.T) {
	d := generateInMemory(t, DefaultOptions())
	f := analyze(d, testNow)
	customers, orders := len(d.customers), len(d.orders)

	tests := []struct {
		name     string
		got, min int
	}{
		{"İstanbul customers who abandoned a basket in the last 30 days and allow SMS", f.istanbulAbandonersWithSMS, 25},
		{"customers without a completed order", f.neverPurchased, customers * 15 / 100},
		{"lapsed customers, last completed order 180+ days ago", f.lapsed, customers * 10 / 100},
		{"loyal customers, 10+ completed orders", f.loyal, customers * 5 / 100},
		{"customers who signed up in the last 30 days", f.signedUpRecently, customers * 5 / 100},
		{"German customers who prefer Turkish", f.germanTurkishSpeakers, f.germanCustomers * 15 / 100},
		{"cancelled orders", f.cancelled, orders * 3 / 100},
		{"refunded orders", f.refunded, orders * 2 / 100},
		{"customers without a phone", f.withoutPhone, customers * 5 / 100},
		{"customers who withdrew a permission", f.withdrew, customers * 2 / 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("%d (at least %d)", tt.got, tt.min)
			if tt.got < tt.min {
				t.Errorf("%d, want at least %d", tt.got, tt.min)
			}
		})
	}

	for _, c := range d.cities {
		if f.customersByCity[c.ID] == 0 {
			t.Errorf("no customers in %s", c.Name)
		}
	}
	for _, c := range d.categories {
		if f.recentBuyersByCategory[c.ID] == 0 {
			t.Errorf("nobody bought from %s in the last 90 days", c.Name)
		}
	}
}

// generateInMemory builds a dataset without a database, numbering the rows of every batch from
// 1 as PostgreSQL's identity columns would.
func generateInMemory(t *testing.T, opts Options) dataset {
	t.Helper()
	d, err := build(newGenerator(opts.Seed, testNow), opts.Customers, sink{
		countries:  func([]customer.Country) error { return nil },
		cities:     numbered(func(c *customer.City, id int64) { c.ID = id }),
		categories: numbered(func(c *commerce.Category, id int64) { c.ID = id }),
		products:   numbered(func(p *commerce.Product, id int64) { p.ID = id }),
		customers: func(cs []*customer.Customer) ([]int64, error) {
			ids := make([]int64, len(cs))
			for i := range ids {
				ids[i] = int64(i + 1)
			}
			return ids, nil
		},
		orders:  func([]*commerce.Order) error { return nil },
		baskets: func([]*commerce.Basket) error { return nil },
	})
	if err != nil {
		t.Fatalf("build(): %v", err)
	}
	return d
}

func numbered[T any](setID func(*T, int64)) func([]T) error {
	return func(items []T) error {
		for i := range items {
			setID(&items[i], int64(i+1))
		}
		return nil
	}
}

// segmentFacts are the counts the segment checks need, worked out from a dataset generated in
// memory, whose customer IDs are their positions plus one.
type segmentFacts struct {
	istanbulAbandonersWithSMS int
	neverPurchased            int
	lapsed                    int
	loyal                     int
	signedUpRecently          int
	germanCustomers           int
	germanTurkishSpeakers     int
	cancelled                 int
	refunded                  int
	withoutPhone              int
	withdrew                  int
	customersByCity           map[int64]int
	recentBuyersByCategory    map[int64]int
}

func analyze(d dataset, now time.Time) segmentFacts {
	f := segmentFacts{customersByCity: make(map[int64]int), recentBuyersByCategory: make(map[int64]int)}
	cities := make(map[int64]customer.City, len(d.cities))
	for _, c := range d.cities {
		cities[c.ID] = c
	}
	categoryOf := make(map[int64]int64, len(d.products))
	for _, p := range d.products {
		categoryOf[p.ID] = p.CategoryID
	}

	type purchases struct {
		completed int
		last      time.Time
		recently  bool // within the last 30 days
	}
	byCustomer := make(map[int64]purchases)
	recentBuyers := make(map[int64]map[int64]bool) // category ID → customer IDs
	for _, o := range d.orders {
		switch o.Status() {
		case commerce.StatusCancelled:
			f.cancelled++
		case commerce.StatusRefunded:
			f.refunded++
		case commerce.StatusCompleted:
			p := byCustomer[o.CustomerID()]
			p.completed++
			p.last = later(p.last, o.OrderedAt())
			p.recently = p.recently || !o.OrderedAt().Before(now.Add(-30*day))
			byCustomer[o.CustomerID()] = p
			if o.OrderedAt().Before(now.Add(-90 * day)) {
				continue
			}
			for _, l := range o.Lines() {
				category := categoryOf[l.ProductID]
				if recentBuyers[category] == nil {
					recentBuyers[category] = make(map[int64]bool)
				}
				recentBuyers[category][o.CustomerID()] = true
			}
		}
	}
	for category, buyers := range recentBuyers {
		f.recentBuyersByCategory[category] = len(buyers)
	}
	lastBasketAdd := make(map[int64]time.Time)
	for _, b := range d.baskets {
		for _, item := range b.Items() {
			lastBasketAdd[b.CustomerID()] = later(lastBasketAdd[b.CustomerID()], item.AddedAt)
		}
	}

	for i, c := range d.customers {
		id := int64(i + 1)
		profile := c.Profile()
		city := cities[profile.CityID]
		f.customersByCity[city.ID]++
		p := byCustomer[id]
		if p.completed == 0 {
			f.neverPurchased++
		}
		if p.completed > 0 && p.last.Before(now.Add(-180*day)) {
			f.lapsed++
		}
		if p.completed >= 10 {
			f.loyal++
		}
		if !c.CreatedAt().Before(now.Add(-30 * day)) {
			f.signedUpRecently++
		}
		if city.CountryCode == "DE" {
			f.germanCustomers++
			if profile.Language == customer.LanguageTurkish {
				f.germanTurkishSpeakers++
			}
		}
		if profile.Phone.IsZero() {
			f.withoutPhone++
		}
		for _, perm := range c.Permissions() {
			if !perm.Granted && perm.UpdatedAt.After(c.CreatedAt()) {
				f.withdrew++
				break
			}
		}
		abandoned := !lastBasketAdd[id].Before(now.Add(-30*day)) && !p.recently
		if city.Name == "İstanbul" && abandoned && c.Permitted(customer.ChannelSMS) {
			f.istanbulAbandonersWithSMS++
		}
	}
	return f
}
