package segmentation

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"gorm.io/gorm"

	"github.com/bengisupinar/campaign/internal/campaign"
	"github.com/bengisupinar/campaign/internal/commerce"
	"github.com/bengisupinar/campaign/internal/customer"
	"github.com/bengisupinar/campaign/internal/platform/database/databasetest"
	"github.com/bengisupinar/campaign/internal/seed"
)

// These tests need PostgreSQL and are skipped without it: run `make db`, then
// `make test-integration`.

const (
	email = customer.ChannelEmail
	sms   = customer.ChannelSMS
)

// shop is a hand-made dataset in which every audience is known. Times count back from asOf.
//
//	Ayşe    İstanbul  tr  phone     SMS, email           basket: headphones 5 days ago
//	Mehmet  İstanbul  tr  phone     SMS; email declined  basket: headphones 40 days ago, a novel 3 days
//	                                                     ago; bought a novel 10 days ago
//	Zeynep  Ankara    tr  no phone  email                bought headphones 20 and 200 days ago and a novel
//	                                                     25 days ago; an order of 5 days ago was cancelled
//	                                                     and one of 15 days ago refunded
//	Lukas   Berlin    de  phone     email; SMS revoked   bought headphones 60 days ago, a novel 5 days ago
//	Emma    Berlin    en  phone     none                 bought 5 novels for 999.90 TRY 12 days ago
//	Can     İstanbul  tr  phone     SMS, email           signed up 2 days ago; basket: headphones a day
//	                                                     after asOf
//
// Headphones (Electronics) cost 1,500 TRY and a novel (Books) 200 TRY. The others signed up 100
// to 500 days ago. Zeynep also has a granted SMS permission row, which the domain would refuse
// without a phone but the table accepts.
type shop struct {
	db        *gorm.DB
	svc       *Service
	customers map[string]*customer.Customer // by first name
	cities    map[int64]string              // names by ID
}

func newShop(t *testing.T) shop {
	t.Helper()
	db := databasetest.New(t)
	ctx := t.Context()
	people, sales := customer.NewStore(db), commerce.NewStore(db)

	check(t, people.CreateCountries(ctx, []customer.Country{{Code: "TR", Name: "Türkiye"}, {Code: "DE", Name: "Germany"}}))
	cities := []customer.City{{CountryCode: "TR", Name: "İstanbul"}, {CountryCode: "TR", Name: "Ankara"}, {CountryCode: "DE", Name: "Berlin"}}
	check(t, people.CreateCities(ctx, cities))
	istanbul, ankara, berlin := cities[0].ID, cities[1].ID, cities[2].ID

	categories := []commerce.Category{{Name: "Electronics"}, {Name: "Books"}}
	check(t, sales.CreateCategories(ctx, categories))
	product := func(p commerce.Product, err error) commerce.Product { t.Helper(); check(t, err); return p }
	products := []commerce.Product{
		product(commerce.NewProduct(categories[0].ID, "Headphones", 150_000)),
		product(commerce.NewProduct(categories[1].ID, "Novel", 20_000)),
	}
	check(t, sales.CreateProducts(ctx, products))
	headphones, novel := products[0], products[1]

	s := shop{db: db, svc: NewService(db), customers: make(map[string]*customer.Customer), cities: make(map[int64]string)}
	for _, c := range cities {
		s.cities[c.ID] = c.Name
	}
	var all []*customer.Customer
	for _, p := range []struct {
		first, last, email, phone string
		city                      int64
		language                  customer.Language
		signedUp                  int // days before asOf
	}{
		{"Ayşe", "Yılmaz", "ayse@example.com", "+905321000001", istanbul, customer.LanguageTurkish, 400},
		{"Mehmet", "Demir", "mehmet@example.com", "+905321000002", istanbul, customer.LanguageTurkish, 300},
		{"Zeynep", "Kaya", "zeynep@example.com", "", ankara, customer.LanguageTurkish, 500},
		{"Lukas", "Schmidt", "lukas@example.com", "+491511000004", berlin, customer.LanguageGerman, 250},
		{"Emma", "Brown", "emma@example.com", "+491511000005", berlin, customer.LanguageEnglish, 100},
		{"Can", "Öztürk", "can@example.com", "+905321000006", istanbul, customer.LanguageTurkish, 2},
	} {
		profile := customer.Profile{FirstName: p.first, LastName: p.last, CityID: p.city, Language: p.language}
		var err error
		profile.Email, err = customer.ParseEmail(p.email)
		check(t, err)
		if p.phone != "" {
			profile.Phone, err = customer.ParsePhone(p.phone)
			check(t, err)
		}
		c, err := customer.New(profile, daysAgo(p.signedUp))
		check(t, err)
		s.customers[p.first] = c
		all = append(all, c)
	}
	permit := func(name string, ch customer.Channel, granted bool, days int) {
		t.Helper()
		if granted {
			check(t, s.customers[name].Grant(ch, daysAgo(days)))
		} else {
			check(t, s.customers[name].Revoke(ch, daysAgo(days)))
		}
	}
	permit("Ayşe", sms, true, 400)
	permit("Ayşe", email, true, 400)
	permit("Mehmet", sms, true, 300)
	permit("Mehmet", email, false, 300)
	permit("Zeynep", email, true, 500)
	permit("Lukas", email, true, 250)
	permit("Lukas", sms, true, 250)
	permit("Lukas", sms, false, 30)
	permit("Can", sms, true, 2)
	permit("Can", email, true, 2)
	check(t, people.CreateCustomers(ctx, all))
	check(t, db.Exec("INSERT INTO notification_permissions (customer_id, channel, granted, updated_at) VALUES (?, ?, TRUE, ?)",
		s.id("Zeynep"), string(sms), daysAgo(1)).Error)

	order := func(name string, days int, lines ...commerce.OrderLine) *commerce.Order {
		t.Helper()
		o, err := commerce.NewOrder(s.id(name), daysAgo(days), lines)
		check(t, err)
		return o
	}
	oneHeadphones := commerce.OrderLine{ProductID: headphones.ID, Quantity: 1, UnitPrice: headphones.Price}
	oneNovel := commerce.OrderLine{ProductID: novel.ID, Quantity: 1, UnitPrice: novel.Price}
	cancelled, refunded := order("Zeynep", 5, oneHeadphones), order("Zeynep", 15, oneNovel)
	check(t, cancelled.Cancel(daysAgo(4)))
	check(t, refunded.Refund(daysAgo(14)))
	check(t, sales.CreateOrders(ctx, []*commerce.Order{
		order("Mehmet", 10, oneNovel),
		order("Zeynep", 20, oneHeadphones), order("Zeynep", 25, oneNovel), order("Zeynep", 200, oneHeadphones),
		cancelled, refunded,
		order("Lukas", 60, oneHeadphones), order("Lukas", 5, oneNovel),
		order("Emma", 12, commerce.OrderLine{ProductID: novel.ID, Quantity: 5, UnitPrice: 19_998}), // 999.90 TRY
	}))

	basket := func(name string) *commerce.Basket {
		t.Helper()
		b, err := commerce.NewBasket(s.id(name))
		check(t, err)
		return b
	}
	ayse, mehmet, can := basket("Ayşe"), basket("Mehmet"), basket("Can")
	check(t, ayse.Add(headphones.ID, 1, daysAgo(5)))
	check(t, mehmet.Add(headphones.ID, 1, daysAgo(40)))
	check(t, mehmet.Add(novel.ID, 2, daysAgo(3)))
	check(t, can.Add(headphones.ID, 1, daysAgo(-1)))
	check(t, sales.CreateBaskets(ctx, []*commerce.Basket{ayse, mehmet, can}))
	return s
}

func (s shop) id(name string) int64 { return s.customers[name].ID() }

// ids returns the customers' IDs in ascending order.
func (s shop) ids(names ...string) []int64 {
	ids := make([]int64, len(names))
	for i, n := range names {
		ids[i] = s.id(n)
	}
	slices.Sort(ids)
	return ids
}

// recipient is what Recipients returns for a customer who can receive channels (email first).
func (s shop) recipient(name string, channels ...customer.Channel) Recipient {
	c := s.customers[name]
	p := c.Profile()
	r := Recipient{CustomerID: c.ID(), FirstName: p.FirstName, LastName: p.LastName, City: s.cities[p.CityID], Language: p.Language, Channels: channels}
	if slices.Contains(channels, email) {
		r.Email = p.Email
	}
	if slices.Contains(channels, sms) {
		r.Phone = p.Phone
	}
	return r
}

func TestSegmentsMatchTheirCustomers(t *testing.T) {
	s := newShop(t)
	everyone := []string{"Ayşe", "Mehmet", "Zeynep", "Lukas", "Emma", "Can"}
	tests := []struct {
		name    string
		segment campaign.Segment
		want    []string
	}{
		{"every customer", segment(campaign.MatchAll), everyone},
		{"in İstanbul", segment(campaign.MatchAll, cityIn("İstanbul")), []string{"Ayşe", "Mehmet", "Can"}},
		{"in Germany", segment(campaign.MatchAll, countryIn("DE")), []string{"Lukas", "Emma"}},
		{"English speakers", segment(campaign.MatchAll, languageIn("en")), []string{"Emma"}},
		{"English speakers or in Ankara", segment(campaign.MatchAny, languageIn("en"), cityIn("Ankara")), []string{"Zeynep", "Emma"}},
		{"signed up in the last 7 days", segment(campaign.MatchAll,
			campaign.Condition{Type: campaign.ConditionSignedUp, WithinDays: new(7)}), []string{"Can"}},
		{"a basket, at any time", segment(campaign.MatchAll, campaign.Condition{Type: campaign.ConditionBasket}),
			[]string{"Ayşe", "Mehmet", "Can"}},
		{"added to the basket in the last 30 days, which end at asOf", segment(campaign.MatchAll,
			campaign.Condition{Type: campaign.ConditionBasket, WithinDays: new(30)}), []string{"Ayşe", "Mehmet"}},
		{"electronics added to the basket in the last 30 days, on the same line", segment(campaign.MatchAll,
			campaign.Condition{Type: campaign.ConditionBasket, WithinDays: new(30), Categories: []string{"Electronics"}}),
			[]string{"Ayşe"}},
		{"a purchase, ever", segment(campaign.MatchAll, campaign.Condition{Type: campaign.ConditionPurchased}),
			[]string{"Mehmet", "Zeynep", "Lukas", "Emma"}},
		{"a purchase in the last 7 days, where cancelled orders don't count", segment(campaign.MatchAll,
			campaign.Condition{Type: campaign.ConditionPurchased, WithinDays: new(7)}), []string{"Lukas"}},
		{"electronics bought in the last 30 days, in the same order", segment(campaign.MatchAll,
			campaign.Condition{Type: campaign.ConditionPurchased, WithinDays: new(30), Categories: []string{"Electronics"}}),
			[]string{"Zeynep"}},
		{"no purchase in the last 30 days", segment(campaign.MatchAll,
			campaign.Condition{Type: campaign.ConditionPurchased, WithinDays: new(30), Negate: true}), []string{"Ayşe", "Can"}},
		{"at least 3 orders", segment(campaign.MatchAll,
			campaign.Condition{Type: campaign.ConditionOrderCount, Min: new(3)}), []string{"Zeynep"}},
		{"at most 2 orders in the last 30 days, not counting cancelled and refunded ones", segment(campaign.MatchAll,
			campaign.Condition{Type: campaign.ConditionOrderCount, WithinDays: new(30), Max: new(2)}), everyone},
		{"no orders", segment(campaign.MatchAll,
			campaign.Condition{Type: campaign.ConditionOrderCount, Max: new(0)}), []string{"Ayşe", "Can"}},
		// Spend bounds are in kuruş: 100,000 is 1,000 TRY.
		{"spent at least 1,000 TRY in the last 30 days", segment(campaign.MatchAll,
			campaign.Condition{Type: campaign.ConditionTotalSpent, WithinDays: new(30), Min: new(100_000)}), []string{"Zeynep"}},
		{"spent 999 to 1,000 TRY in the last 30 days", segment(campaign.MatchAll,
			campaign.Condition{Type: campaign.ConditionTotalSpent, WithinDays: new(30), Min: new(99_900), Max: new(100_000)}),
			[]string{"Emma"}},
		{"spent at most 1,700 TRY in the last 30 days, nothing included", segment(campaign.MatchAll,
			campaign.Condition{Type: campaign.ConditionTotalSpent, WithinDays: new(30), Max: new(170_000)}), everyone},
		{"abandoned a basket in İstanbul in the last 30 days", abandonedBasketsInIstanbul(), []string{"Ayşe"}},
		{"in Türkiye, with a recent basket or a recent electronics purchase", segment(campaign.MatchAll,
			countryIn("TR"),
			campaign.Condition{Type: campaign.ConditionGroup, Match: campaign.MatchAny, Conditions: []campaign.Condition{
				{Type: campaign.ConditionBasket, WithinDays: new(30)},
				{Type: campaign.ConditionPurchased, WithinDays: new(30), Categories: []string{"Electronics"}},
			}}), []string{"Ayşe", "Mehmet", "Zeynep"}},
		{"neither in İstanbul nor in Germany", segment(campaign.MatchAll,
			campaign.Condition{Type: campaign.ConditionGroup, Match: campaign.MatchAny, Negate: true, Conditions: []campaign.Condition{
				cityIn("İstanbul"), countryIn("DE"),
			}}), []string{"Zeynep"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			where, err := compileSegment(tt.segment, asOf)
			if err != nil {
				t.Fatalf("compileSegment(): %v", err)
			}
			var got []int64
			if err := s.db.Raw("SELECT c.id FROM customers c WHERE "+where.sql+" ORDER BY c.id", where.args...).Scan(&got).Error; err != nil {
				t.Fatalf("select the segment: %v", err)
			}
			if want := s.ids(tt.want...); !slices.Equal(got, want) {
				t.Errorf("customers %v, want %v (%v)", got, want, tt.want)
			}

			// The service checks the segment and counts the same customers.
			audience, err := s.svc.CountAudience(t.Context(), Query{Segment: tt.segment, Channels: []customer.Channel{email, sms}, AsOf: asOf})
			if err != nil {
				t.Fatalf("CountAudience(): %v", err)
			}
			if audience.Matched != len(tt.want) {
				t.Errorf("CountAudience().Matched = %d, want %d", audience.Matched, len(tt.want))
			}
		})
	}
}

func TestCountAudience(t *testing.T) {
	s := newShop(t)
	everyone := segment(campaign.MatchAll)
	tests := []struct {
		name     string
		segment  campaign.Segment
		channels []customer.Channel
		want     Audience
	}{
		{"each channel counts its own permission", everyone, []customer.Channel{email, sms},
			Audience{Matched: 6, Reachable: 5, ByChannel: map[customer.Channel]int{email: 4, sms: 3}}},
		{"SMS needs a phone and a granted permission", everyone, []customer.Channel{sms},
			Audience{Matched: 6, Reachable: 3, ByChannel: map[customer.Channel]int{sms: 3}}},
		{"email needs a granted permission", everyone, []customer.Channel{email},
			Audience{Matched: 6, Reachable: 4, ByChannel: map[customer.Channel]int{email: 4}}},
		{"in Germany", segment(campaign.MatchAll, countryIn("DE")), []customer.Channel{sms, email},
			Audience{Matched: 2, Reachable: 1, ByChannel: map[customer.Channel]int{email: 1, sms: 0}}},
		{"nobody", segment(campaign.MatchAll, cityIn("Ankara"), languageIn("en")), []customer.Channel{email, sms},
			Audience{Matched: 0, Reachable: 0, ByChannel: map[customer.Channel]int{email: 0, sms: 0}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.svc.CountAudience(t.Context(), Query{Segment: tt.segment, Channels: tt.channels, AsOf: asOf})
			if err != nil {
				t.Fatalf("CountAudience(): %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CountAudience() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRecipientsComeInBatches(t *testing.T) {
	s := newShop(t)
	ctx := t.Context()
	everyone := segment(campaign.MatchAll)

	// Emma allows no channel; Mehmet only SMS; Zeynep and Lukas only email, since Zeynep has no
	// phone and Lukas revoked SMS. Contact details come only for the channels a customer allows.
	q := Query{Segment: everyone, Channels: []customer.Channel{sms, email}, AsOf: asOf}
	want := [][]Recipient{
		{s.recipient("Ayşe", email, sms), s.recipient("Mehmet", sms)},
		{s.recipient("Zeynep", email), s.recipient("Lukas", email)},
		{s.recipient("Can", email, sms)},
		{},
	}
	var after int64
	for i, page := range want {
		got, err := s.svc.Recipients(ctx, q, after, 2)
		if err != nil {
			t.Fatalf("Recipients(after %d): %v", after, err)
		}
		if !reflect.DeepEqual(got, page) {
			t.Fatalf("batch %d = %+v, want %+v", i, got, page)
		}
		if len(got) > 0 {
			after = got[len(got)-1].CustomerID
		}
	}

	q.Channels = []customer.Channel{sms}
	got, err := s.svc.Recipients(ctx, q, 0, maxBatchSize)
	if err != nil {
		t.Fatalf("Recipients(SMS): %v", err)
	}
	if want := []Recipient{s.recipient("Ayşe", sms), s.recipient("Mehmet", sms), s.recipient("Can", sms)}; !reflect.DeepEqual(got, want) {
		t.Errorf("SMS recipients = %+v, want %+v", got, want)
	}
}

func TestUnknownReferencesAreProblems(t *testing.T) {
	s := newShop(t)
	ctx := t.Context()
	injection := "Ankara'); DROP TABLE customers; --"
	seg := segment(campaign.MatchAll,
		countryIn("TR", "FR"),
		campaign.Condition{Type: campaign.ConditionGroup, Match: campaign.MatchAny, Conditions: []campaign.Condition{
			cityIn("Berlin", "istanbul", injection),
			{Type: campaign.ConditionPurchased, Categories: []string{"Books", "Garden"}},
		}},
	)
	want := []campaign.Problem{
		{Field: "segment.conditions[0].in[1]", Code: campaign.ProblemUnknown, Detail: "FR"},
		{Field: "segment.conditions[1].conditions[0].in[1]", Code: campaign.ProblemUnknown, Detail: "istanbul"},
		{Field: "segment.conditions[1].conditions[0].in[2]", Code: campaign.ProblemUnknown, Detail: injection},
		{Field: "segment.conditions[1].conditions[1].categories[1]", Code: campaign.ProblemUnknown, Detail: "Garden"},
	}

	got, err := s.svc.CheckReferences(ctx, seg)
	if err != nil {
		t.Fatalf("CheckReferences(): %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("CheckReferences() = %+v, want %+v", got, want)
	}
	known := segment(campaign.MatchAll, countryIn("TR"), cityIn("Berlin", "İstanbul"),
		campaign.Condition{Type: campaign.ConditionBasket, Categories: []string{"Books"}})
	if got, err := s.svc.CheckReferences(ctx, known); err != nil || got != nil {
		t.Errorf("CheckReferences(known) = %+v, %v; want no problems", got, err)
	}
	// A malformed name is reported as invalid by Segment.Problems, never also as unknown.
	malformed := segment(campaign.MatchAll, cityIn(" Atlantis"))
	if got, err := s.svc.CheckReferences(ctx, malformed); err != nil || got != nil {
		t.Errorf("CheckReferences(malformed) = %+v, %v; want no report", got, err)
	}

	// Counting or listing the audience reports the same problems.
	q := Query{Segment: seg, Channels: []customer.Channel{email}, AsOf: asOf}
	_, countErr := s.svc.CountAudience(ctx, q)
	_, listErr := s.svc.Recipients(ctx, q, 0, 10)
	for name, err := range map[string]error{"CountAudience": countErr, "Recipients": listErr} {
		var invalid *campaign.ValidationError
		if !errors.As(err, &invalid) || !reflect.DeepEqual(invalid.Problems, want) {
			t.Errorf("%s() error = %v, want the unknown references", name, err)
		}
	}

	// The names only ever travelled as data.
	var customers int
	if err := s.db.Raw("SELECT count(*) FROM customers").Scan(&customers).Error; err != nil || customers != 6 {
		t.Errorf("customers = %d, %v; want the 6 of the fixture", customers, err)
	}
}

// istanbulAbandonersWithSMS counts, in hand-written SQL, the Phase 2A example segment:
// customers in İstanbul who added to their basket since the cut-off, completed no order since
// then, and allow SMS. The seed's tests check this query against the generator's own analysis.
const istanbulAbandonersWithSMS = `
SELECT count(*) FROM customers c
JOIN cities ci ON ci.id = c.city_id
WHERE ci.name = 'İstanbul'
  AND EXISTS (SELECT 1 FROM basket_items b WHERE b.customer_id = c.id AND b.added_at >= ?)
  AND NOT EXISTS (SELECT 1 FROM orders o
                  WHERE o.customer_id = c.id AND o.status = 'completed' AND o.ordered_at >= ?)
  AND EXISTS (SELECT 1 FROM notification_permissions p
              WHERE p.customer_id = c.id AND p.channel = 'sms' AND p.granted)`

func TestSeededDataset(t *testing.T) {
	db := seededDatabase(t)
	svc := NewService(db)
	ctx := t.Context()

	var want int
	cutoff := daysAgo(30)
	if err := db.Raw(istanbulAbandonersWithSMS, cutoff, cutoff).Scan(&want).Error; err != nil {
		t.Fatalf("count by hand: %v", err)
	}
	got, err := svc.CountAudience(ctx, Query{Segment: abandonedBasketsInIstanbul(), Channels: []customer.Channel{sms}, AsOf: asOf})
	if err != nil {
		t.Fatalf("CountAudience(): %v", err)
	}
	if got.ByChannel[sms] != want || want == 0 {
		t.Errorf("example segment reaches %d customers by SMS, want %d as counted by hand", got.ByChannel[sms], want)
	}

	// Estimates and dispatch agree: paging through Recipients finds, once each, exactly the
	// customers CountAudience counts.
	for _, tt := range seededSegments() {
		t.Run(tt.name, func(t *testing.T) {
			const limit = 250
			q := Query{Segment: tt.segment, Channels: []customer.Channel{email, sms}, AsOf: asOf}
			audience, err := svc.CountAudience(ctx, q)
			if err != nil {
				t.Fatalf("CountAudience(): %v", err)
			}
			if audience.Reachable == 0 {
				t.Fatal("nobody is reachable, so the comparison would prove nothing")
			}
			listed := map[customer.Channel]int{email: 0, sms: 0}
			var total int
			var after int64
			for {
				batch, err := svc.Recipients(ctx, q, after, limit)
				if err != nil {
					t.Fatalf("Recipients(after %d): %v", after, err)
				}
				for _, r := range batch {
					if r.CustomerID <= after {
						t.Fatalf("customer %d came after %d", r.CustomerID, after)
					}
					after = r.CustomerID
					for _, ch := range r.Channels {
						listed[ch]++
					}
				}
				total += len(batch)
				if len(batch) < limit {
					break
				}
			}
			if total != audience.Reachable || !maps.Equal(listed, audience.ByChannel) {
				t.Errorf("Recipients listed %d customers %v, CountAudience counted %d %v",
					total, listed, audience.Reachable, audience.ByChannel)
			}
		})
	}
}

// BenchmarkAudience times the audience queries on the seeded dataset of 5,000 customers:
//
//	TEST_DATABASE_URL=… go test -run '^$' -bench . ./internal/segmentation
func BenchmarkAudience(b *testing.B) {
	svc := NewService(seededDatabase(b))
	for _, s := range seededSegments() {
		q := Query{Segment: s.segment, Channels: []customer.Channel{email, sms}, AsOf: asOf}
		b.Run(s.name+"/count", func(b *testing.B) {
			for b.Loop() {
				if _, err := svc.CountAudience(b.Context(), q); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(s.name+"/first batch", func(b *testing.B) {
			for b.Loop() {
				if _, err := svc.Recipients(b.Context(), q, 0, maxBatchSize); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// seededDatabase returns a database holding the default seed, generated as of asOf. It gathers
// the statistics autovacuum would, so query plans are realistic from the start and don't
// change while a benchmark runs.
func seededDatabase(t testing.TB) *gorm.DB {
	t.Helper()
	db := databasetest.New(t)
	if _, err := seed.Run(t.Context(), db, seed.DefaultOptions(), asOf); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := db.WithContext(t.Context()).Exec("ANALYZE").Error; err != nil {
		t.Fatalf("analyze: %v", err)
	}
	return db
}

type namedSegment struct {
	name    string
	segment campaign.Segment
}

// seededSegments are segments over the seeded dataset, from the simplest to a nested one.
func seededSegments() []namedSegment {
	return []namedSegment{
		{"everyone", segment(campaign.MatchAll)},
		{"abandoned baskets in İstanbul", abandonedBasketsInIstanbul()},
		{"nested", segment(campaign.MatchAll,
			countryIn("TR", "DE"),
			campaign.Condition{Type: campaign.ConditionGroup, Match: campaign.MatchAny, Conditions: []campaign.Condition{
				{Type: campaign.ConditionBasket, WithinDays: new(14), Categories: []string{"Electronics", "Books & Stationery"}},
				{Type: campaign.ConditionGroup, Match: campaign.MatchAll, Conditions: []campaign.Condition{
					{Type: campaign.ConditionOrderCount, WithinDays: new(365), Min: new(3)},
					{Type: campaign.ConditionTotalSpent, WithinDays: new(365), Min: new(500_000)}, // 5,000 TRY
				}},
			}},
			campaign.Condition{Type: campaign.ConditionPurchased, WithinDays: new(7), Negate: true},
		)},
	}
}

// abandonedBasketsInIstanbul is the Phase 2A example segment: customers in İstanbul who added
// to their basket in the last 30 days and completed no order since.
func abandonedBasketsInIstanbul() campaign.Segment {
	return segment(campaign.MatchAll,
		cityIn("İstanbul"),
		campaign.Condition{Type: campaign.ConditionBasket, WithinDays: new(30)},
		campaign.Condition{Type: campaign.ConditionPurchased, WithinDays: new(30), Negate: true},
	)
}

func cityIn(names ...string) campaign.Condition {
	return campaign.Condition{Type: campaign.ConditionCity, In: names}
}

func countryIn(codes ...string) campaign.Condition {
	return campaign.Condition{Type: campaign.ConditionCountry, In: codes}
}

func languageIn(codes ...string) campaign.Condition {
	return campaign.Condition{Type: campaign.ConditionLanguage, In: codes}
}

func check(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
